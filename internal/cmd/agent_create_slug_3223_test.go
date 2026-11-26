package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// cli#3223 (hadron-server#5203 S1): agent create --slug sends createAgent(slug:)
// in the create call, in its own document variant; without --slug the slug-free
// documents are used unchanged, so they still validate on a server that lacks
// the argument.
// runAgentCreate3223 runs the CLI against a fake server and returns the request
// variables captured per operation.
func runAgentCreate3223(t *testing.T, responses map[string]string, argv ...string) (map[string]json.RawMessage, error) {
	t.Helper()
	gql, captured := captureGraphQL(t, responses)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(argv, "--server", gql.URL))
	err := root.Execute()
	return captured, err
}

func TestAgentCreateSlugSelectsTheVariantDocument3223(t *testing.T) {
	for _, tc := range []struct {
		name         string
		extra        []string
		plain, slugd string
	}{
		{"plain", nil, "CreateAgent", "CreateAgentSlug"},
		{"lifecycle", []string{"--lifecycle", "SHORT_LIVED"}, "CreateAgentWithLifecycle", "CreateAgentWithLifecycleSlug"},
		{"context cap", []string{"--context-cap", "50000"}, "CreateAgentWithContextCap", "CreateAgentWithContextCapSlug"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := `{"data":{"createAgent":` + strings.Replace(agentJSON, `"usesTasks":true,`, `"usesTasks":true,"lifecycle":"LONG_LIVED","contextCap":null,`, 1) + `}}`
			base := []string{"agent", "create", "--org", "acme.com", "--name", "Support Bot"}

			calls, err := runAgentCreate3223(t, map[string]string{tc.slugd: resp}, append(append(base, tc.extra...), "--slug", "support-bot")...)
			if err != nil {
				t.Fatalf("with --slug: %v", err)
			}
			raw, ok := calls[tc.slugd]
			if !ok || len(calls) != 1 {
				t.Fatalf("with --slug want exactly %s, got %v", tc.slugd, calls)
			}
			var vars map[string]any
			_ = json.Unmarshal(raw, &vars)
			if vars["slug"] != "support-bot" || vars["name"] != "Support Bot" {
				t.Errorf("slug must ride in the create: %v", vars)
			}

			calls, err = runAgentCreate3223(t, map[string]string{tc.plain: resp}, append(base, tc.extra...)...)
			if err != nil {
				t.Fatalf("without --slug: %v", err)
			}
			raw, ok = calls[tc.plain]
			if !ok || len(calls) != 1 {
				t.Fatalf("without --slug want exactly %s, got %v", tc.plain, calls)
			}
			vars = nil
			_ = json.Unmarshal(raw, &vars)
			if _, has := vars["slug"]; has {
				t.Errorf("no slug variable without --slug: %v", vars)
			}
		})
	}
}

func TestAgentCreateSlugRefusedAndInvalid3223(t *testing.T) {
	_, err := runAgentCreate3223(t, map[string]string{"CreateAgentSlug": `{"errors":[{"message":"agent urn taken","extensions":{"code":"AGENT_URN_TAKEN"}}]}`},
		"agent", "create", "--org", "acme.com", "--name", "Bot", "--slug", "bot")
	if err == nil || !strings.Contains(err.Error(), "agent urn taken") {
		t.Fatalf("the server's refusal must be shown, got %v", err)
	}
	for _, bad := range []string{"Bad Slug", ""} {
		_, err := runAgentCreate3223(t, map[string]string{}, "agent", "create", "--org", "acme.com", "--name", "Bot", "--slug", bad)
		if err == nil || !strings.Contains(err.Error(), "is not valid") {
			t.Errorf("--slug %q must be a usage error, got %v", bad, err)
		}
	}
}
