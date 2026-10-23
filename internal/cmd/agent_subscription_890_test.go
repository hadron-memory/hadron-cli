package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAgentSubscription890(t *testing.T) {
	for _, tc := range []struct{ name, rows string }{
		{"rows", `[{"agentId":"a1","orgId":"o1","isActive":true,"createdAt":"created","activatedAt":"activated","expiresAt":null,"revokedAt":null},{"agentId":"a2","orgId":"o1","isActive":false,"createdAt":"old","activatedAt":null,"expiresAt":null,"revokedAt":"revoked"}]`},
		{"empty", `[]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, ops := captureGraphQL(t, map[string]string{"OrgAgentGrants": `{"data":{"organization":{"agentOrgGrants":` + tc.rows + `}}}`})
			f, out := testFactory(t)
			r := NewRootCmd(f)
			r.SetArgs([]string{"agent", "subscription", "ls", "--org", "unchanged-org-ref", "--json", "--server", srv.URL})
			if err := r.Execute(); err != nil {
				t.Fatal(err)
			}
			var v map[string]any
			_ = json.Unmarshal(ops["OrgAgentGrants"], &v)
			if v["ref"] != "unchanged-org-ref" || len(ops) != 1 {
				t.Fatal(ops)
			}
			var got []map[string]any
			if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
				t.Fatal(err)
			}
			if tc.name == "empty" {
				if strings.TrimSpace(out.String()) != "[]" {
					t.Fatal(out.String())
				}
			} else if len(got) != 2 || got[0]["agentId"] != "a1" || got[0]["isActive"] != true || got[1]["isActive"] != false || got[1]["revokedAt"] != "revoked" || got[0]["expiresAt"] != nil {
				t.Fatal(out.String())
			}
		})
	}
}
