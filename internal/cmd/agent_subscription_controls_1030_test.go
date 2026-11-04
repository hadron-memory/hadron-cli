package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

func TestOrgGrantControls1030(t *testing.T) {
	const row = `{"agentId":"active","orgId":"org","isActive":true,"createdAt":"now","activatedAt":null,"expiresAt":null,"revokedAt":null}`
	const revoked = `{"agentId":"revoked","orgId":"org","isActive":false,"createdAt":"then","revokedAt":"ended"}`
	for _, tc := range []struct {
		name, body   string
		status, code int
		org          []string
		success      bool
	}{
		{"missing parent", `{"data":{"organization":null}}`, 200, 4, []string{"--org", "exact-ref"}, false},
		{"denied", `{"errors":[{"message":"denied","extensions":{"code":"FORBIDDEN"}}]}`, 200, 8, []string{"--org", "exact-ref"}, false},
		{"transport502", `gateway`, 502, 7, []string{"--org", "exact-ref"}, false},
		{"partial null row", `{"data":{"organization":{"agentOrgGrants":[` + row + `,null]}}}`, 200, 1, []string{"--org", "exact-ref"}, false},
		{"missing org", "", 200, 2, nil, false},
		{"blank org", "", 200, 2, []string{"--org="}, false},
		{"space org", "", 200, 2, []string{"--org", "  "}, false},
		{"lifecycle rows", `{"data":{"organization":{"agentOrgGrants":[` + row + `,` + revoked + `]}}}`, 200, 0, []string{"--org", "exact-ref"}, true},
		{"empty", `{"data":{"organization":{"agentOrgGrants":[]}}}`, 200, 0, []string{"--org", "exact-ref"}, true},
	} {
		for _, asJSON := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{true: " JSON", false: " text"}[asJSON], func(t *testing.T) {
				var mu sync.Mutex
				calls := 0
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					calls++
					mu.Unlock()
					var req struct {
						OperationName, Query string
						Variables            map[string]any
					}
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Error(err)
					}
					if req.OperationName != "OrgAgentGrants" || req.Query != gen.OrgAgentGrants_Operation || req.Variables["ref"] != "exact-ref" {
						t.Errorf("unexpected read/write/ref: %+v", req)
					}
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(tc.body))
				}))
				defer srv.Close()
				f, out := testFactory(t)
				root := NewRootCmd(f)
				args := append([]string{"agent", "subscription", "list"}, tc.org...)
				args = append(args, "--server", srv.URL)
				if asJSON {
					args = append(args, "--json")
				}
				root.SetArgs(args)
				code := 0
				if err := root.Execute(); err != nil {
					code = renderError(f, err)
				}
				if code != tc.code {
					t.Errorf("exit%d want%d: %s", code, tc.code, out.String())
				}
				mu.Lock()
				n := calls
				mu.Unlock()
				wantCalls := 1
				if tc.code == 2 {
					wantCalls = 0
				}
				if n != wantCalls {
					t.Fatalf("actual HTTP requests%d want%d", n, wantCalls)
				}
				if !tc.success {
					if strings.Contains(out.String(), `"agentId"`) || strings.Contains(out.String(), "AGENT ID") {
						t.Fatalf("partial success: %s", out.String())
					}
					return
				}
				if asJSON {
					if tc.name == "empty" {
						if strings.TrimSpace(out.String()) != "[]" {
							t.Fatal(out.String())
						}
						return
					}
					var rows []map[string]any
					if err := json.Unmarshal([]byte(out.String()), &rows); err != nil {
						t.Fatal(err)
					}
					if len(rows) != 2 || rows[0]["isActive"] != true || rows[1]["isActive"] != false || rows[1]["revokedAt"] != "ended" {
						t.Fatal(rows)
					}
					if v, ok := rows[0]["revokedAt"]; !ok || v != nil {
						t.Fatalf("null revokedAt lost: %s", out.String())
					}
					for _, i := range []int{0, 1} {
						for _, key := range []string{"activatedAt", "expiresAt"} {
							v, ok := rows[i][key]
							if !ok || v != nil {
								t.Fatalf("null/omitted lifecycle %s: %s", key, out.String())
							}
						}
					}
				} else if tc.name == "empty" {
					if !strings.Contains(out.String(), "visible to this caller") {
						t.Fatal(out.String())
					}
				} else if !strings.Contains(out.String(), "active") || !strings.Contains(out.String(), "revoked") {
					t.Fatal(out.String())
				}
			})
		}
	}
}
