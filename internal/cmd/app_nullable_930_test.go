package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const agent930 = `{"id":"agt9","urn":"hrn:agent:acme.com:support-bot","name":"Support Bot","description":null,"visibility":"ORGANIZATION","organizationId":"o1","personaRole":"backend-engineer","createdAt":"2026-08-11T00:00:00Z"}`
const join930 = `{"agent":{"id":"agt9"},"bootPromptRef":null,"bootPromptTargetDeleted":null,"installMethod":"ROW","importer":null}`
const maskedJoin930 = `{"agent":null,"bootPromptRef":null,"bootPromptTargetDeleted":null,"installMethod":null,"importer":null}`

func roster930(agents, joins string) string {
	return `{"data":{"app":{"id":"capp100000000000000000000","urn":"hrn:app:acme.com:support","name":"Support App","agents":` + agents + `,"appAgents":` + joins + `}}}`
}

// Strict recording fake: nil lists and [] are distinct, requests are not
// deduplicated by operation name, and unknown operations fail rather than fall back.
func runNullable930(t *testing.T, responses map[string]string, args ...string) (string, int, []string) {
	t.Helper()
	var mu sync.Mutex
	calls := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Operation string `json:"operationName"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		mu.Lock()
		calls = append(calls, req.Operation)
		mu.Unlock()
		body, ok := responses[req.Operation]
		if !ok {
			t.Errorf("unexpected operation %s", req.Operation)
			http.Error(w, "unstubbed", 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	}))
	defer srv.Close()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{}, args...), "--server", srv.URL))
	err := root.Execute()
	mu.Lock()
	recorded := append([]string{}, calls...)
	mu.Unlock()
	return out.String(), exitCodeFor(err), recorded
}

func TestAppNullable930MemberCount(t *testing.T) {
	for _, count := range []string{"null", "0", "3"} {
		for _, op := range []string{"Apps", "CreateApp", "CreateAppSettings"} {
			t.Run(op+count, func(t *testing.T) {
				app := `{"id":"capp100000000000000000000","urn":"hrn:app:acme.com:support","name":"Support","appType":"AGENT","agentId":null,"memberCount":` + count + `,"createdAt":"2026-01-01T00:00:00Z"}`
				args := []string{"app", "list", "--owned-by-me", "--json"}
				response := `{"data":{"apps":{"items":[` + app + `],"total":1}}}`
				if op != "Apps" {
					args = []string{"app", "install", "--owner-me", "--agent", "hrn:agent:acme.com:support-bot", "--name", "Support", "--json"}
					response = `{"data":{"createApp":` + app + `}}}`
				}
				if op == "CreateAppSettings" {
					args = append(args, "--session-timeout-seconds", "90", "--yes")
				}
				out, code, calls := runNullable930(t, map[string]string{op: response}, args...)
				if code != 0 || len(calls) != 1 || calls[0] != op {
					t.Fatalf("exit%d calls%v output%s", code, calls, out)
				}
				var row map[string]json.RawMessage
				if op == "Apps" {
					var rows []map[string]json.RawMessage
					if err := json.Unmarshal([]byte(out), &rows); err != nil || len(rows) != 1 {
						t.Fatalf("bad listing %s %v", out, err)
					}
					row = rows[0]
				} else if err := json.Unmarshal([]byte(out), &row); err != nil {
					t.Fatal(err)
				}
				if got, present := row["memberCount"]; !present || string(got) != count {
					t.Fatalf("memberCount got%s present%v want%s", got, present, count)
				}
			})
		}
	}
}

func TestAppNullable930Roster(t *testing.T) {
	for _, tc := range []struct {
		name, agents, joins, text string
		nullJSON                  bool
		rows                      int
	}{
		{"both-null", "null", "null", "roster withheld (not visible to you)", true, 0},
		{"install-null", "[" + agent930 + "]", "null", "roster withheld (not visible to you)", true, 0},
		{"agents-null", "null", "[" + join930 + "]", "1 installed", true, 0},
		{"empty", "[]", "[]", "0 readable agents", false, 0},
		{"complete", "[" + agent930 + "]", "[" + join930 + "]", "1 readable agents", false, 1},
		{"partial", "[" + agent930 + "]", "[" + strings.Replace(join930, `"ROW"`, `null`, 1) + "," + maskedJoin930 + "]", "2 installed (1 not visible to you)", false, 1},
		{"all-masked", "[]", "[" + maskedJoin930 + "]", "1 installed (1 not visible to you)", false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := map[string]string{"AppAgentInstallationRoster": roster930(tc.agents, tc.joins)}
			args := []string{"app", "agent", "list", "hrn:app:acme.com:support"}
			out, code, calls := runNullable930(t, resp, append(append([]string{}, args...), "--json")...)
			if code != 0 || len(calls) != 1 {
				t.Fatalf("exit%d calls%v %s", code, calls, out)
			}
			if tc.nullJSON {
				if strings.TrimSpace(out) != "null" {
					t.Fatalf("withheld became %s", out)
				}
			} else {
				var rows []map[string]json.RawMessage
				if err := json.Unmarshal([]byte(out), &rows); err != nil || rows == nil || len(rows) != tc.rows {
					t.Fatalf("rows %s %v", out, err)
				}
				if tc.name == "partial" {
					for _, key := range []string{"installMethod", "importer"} {
						if string(rows[0][key]) != "null" {
							t.Fatalf("invented %s: %s", key, out)
						}
					}
				}
			}
			human, code, _ := runNullable930(t, resp, args...)
			if code != 0 || !strings.Contains(human, tc.text) {
				t.Fatalf("want %q got exit%d %s", tc.text, code, human)
			}
			if tc.nullJSON && strings.Contains(human, "0 readable agents") {
				t.Fatalf("withheld became empty: %s", human)
			}
		})
	}
}

func TestAppNullable930RoleWarnings(t *testing.T) {
	for _, tc := range []struct {
		name, agents, joins string
		warn                bool
	}{
		{"both-null", "null", "null", false},
		{"install-null", "[" + agent930 + "]", "null", false},
		{"agents-null", "null", "[" + join930 + "]", false},
		{"partial", "[" + agent930 + "]", "[" + join930 + "," + maskedJoin930 + "]", false},
		{"complete", "[" + agent930 + "]", "[" + join930 + "]", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, code, _ := runNullable930(t, map[string]string{"AppAgentRoster": roleRoster930(t, tc.agents, tc.joins), "TeamRoles": teamRolesJSON, "TeamAppIdentity": teamAppIdentityJSON}, "team", "role", "list", "--app", "hrn:app:acme.com:eng-team")
			if code != 0 {
				t.Fatalf("exit%d %s", code, out)
			}
			if got := strings.Contains(out, "no installed agent carries"); got != tc.warn {
				t.Fatalf("warning=%v want%v: %s", got, tc.warn, out)
			}
		})
	}
}

// The role-warning document does not select installation provenance.
func roleRoster930(t *testing.T, agents, joins string) string {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal([]byte(roster930(agents, joins)), &payload); err != nil {
		t.Fatal(err)
	}
	app := payload["data"].(map[string]any)["app"].(map[string]any)
	if rows, ok := app["appAgents"].([]any); ok {
		for _, row := range rows {
			if m, ok := row.(map[string]any); ok {
				delete(m, "installMethod")
				delete(m, "importer")
			}
		}
	}
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
