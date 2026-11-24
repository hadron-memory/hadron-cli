package cmd

import (
	"encoding/json"
	"testing"
)

// cli#3098 (hadron-server#4368 / #5046): `app asset-upload enable|disable <agent>` sends the Agent as
// input.agentRef, not the retired input.agentUrn, and the user-facing --agent value is unchanged: the same
// ID or URN is passed through as typed. A hard rename with no alias (ruling 0274).
func TestAppAssetUploadSendsAgentRef_3098(t *testing.T) {
	for _, tc := range []struct {
		verb    string
		ref     string
		tools   string
		enabled bool
	}{
		{"enable", "hrn:agent:acme.com:bot", `["asset:upload"]`, true},
		{"disable", "hrn:agent:acme.com:bot", `[]`, false},
		{"enable", "agent-id-1", `["asset:upload"]`, true},
	} {
		t.Run(tc.verb+" "+tc.ref, func(t *testing.T) {
			resp := `{"data":{"setAgentAssetUploadEnabled":{"id":"a1","urn":"hrn:app:acme.com:team","agentTools":` + tc.tools + `}}}`
			srv, captured := captureGraphQL(t, map[string]string{"SetAgentAssetUploadEnabled": resp})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"app", "asset-upload", tc.verb, tc.ref, "--yes", "--server", srv.URL})
			if err := root.Execute(); err != nil {
				t.Fatalf("the command failed: %v", err)
			}
			raw, ok := captured["SetAgentAssetUploadEnabled"]
			if !ok {
				t.Fatal("the mutation was not sent")
			}
			var vars struct {
				Input map[string]any `json:"input"`
			}
			if err := json.Unmarshal(raw, &vars); err != nil {
				t.Fatalf("variables: %v: %s", err, raw)
			}
			if got := vars.Input["agentRef"]; got != tc.ref {
				t.Errorf("input.agentRef = %v, want %q (as typed): %s", got, tc.ref, raw)
			}
			if _, has := vars.Input["agentUrn"]; has {
				t.Errorf("the retired input.agentUrn is still sent: %s", raw)
			}
			if got := vars.Input["enabled"]; got != tc.enabled {
				t.Errorf("input.enabled = %v, want %v: %s", got, tc.enabled, raw)
			}
		})
	}
}
