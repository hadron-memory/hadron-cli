package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

const personalGrant2021 = `{"id":"g1","appId":"app-other","action":"node.read","values":{"node":"n1"},"holderKind":"USER","holderId":"u1","startsAt":null,"expiresAt":"2026-11-01T00:00:00Z","maxUses":2,"useCount":2,"reason":"Past terms","createdAt":"2026-10-01T00:00:00Z","activatedAt":"2026-10-02T00:00:00Z","revokedAt":null,"allows":false,"signatureStatus":{"state":"EARLIER","integrityProblems":0}}`

func TestAuthGrantsPersonNotWorker2021(t *testing.T) {
	calls, out, err := runTeamTask(t, map[string]string{"MyGrants": `{"data":{"myGrants":{"items":[` + personalGrant2021 + `],"total":3}}}`}, "auth", "grants", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || (*calls)[0].Op != "MyGrants" || (*calls)[0].Session != "" {
		t.Fatalf("person read became worker/preflight: %+v", *calls)
	}
	if (*calls)[0].Vars["limit"] != float64(20) || (*calls)[0].Vars["offset"] != float64(0) {
		t.Fatalf("paging: %+v", (*calls)[0].Vars)
	}
	if strings.Contains(out, `"state":"expired"`) || strings.Contains(out, `"state":"revoked"`) {
		t.Fatalf("derived lifecycle: %s", out)
	}
	var dto struct {
		Items []struct {
			ID, AppID       string
			Allows          bool
			ExpiresAt       *string
			SignatureStatus struct{ State *string }
		}
		Total int
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatal(err)
	}
	if dto.Total != 3 || len(dto.Items) != 1 || dto.Items[0].AppID != "app-other" || dto.Items[0].Allows || dto.Items[0].SignatureStatus.State == nil || *dto.Items[0].SignatureStatus.State != "EARLIER" || dto.Items[0].ExpiresAt == nil {
		t.Fatalf("server facts changed: %s", out)
	}
}

func TestAuthGrantsEmptyDoesNotProveNone2021(t *testing.T) {
	for _, jsonOut := range []bool{false, true} {
		args := []string{"auth", "grants"}
		if jsonOut {
			args = append(args, "--json")
		}
		calls, out, err := runTeamTask(t, map[string]string{"MyGrants": `{"data":{"myGrants":{"items":[],"total":0}}}`}, args...)
		if err != nil {
			t.Fatal(err)
		}
		if len(*calls) != 1 || (*calls)[0].Session != "" {
			t.Fatalf("wrong identity: %+v", *calls)
		}
		if jsonOut {
			assertEmptyGrantItems2028(t, out)
		} else if strings.Contains(strings.ToLower(out), "you have no grants") || !strings.Contains(out, "outside the person audience") {
			t.Fatalf("overclaimed empty: %s", out)
		}
	}
}

func TestAuthGrantsPagingAndCount2021(t *testing.T) {
	calls, out, err := runTeamTask(t, map[string]string{"MyGrants": `{"data":{"myGrants":{"items":[],"total":37}}}`}, "auth", "grants", "--limit", "0", "--offset", "17", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || (*calls)[0].Vars["limit"] != float64(0) || (*calls)[0].Vars["offset"] != float64(17) {
		t.Fatalf("count request: %+v", *calls)
	}
	var dto map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatal(err)
	}
	if len(dto) != 1 || string(dto["total"]) != "37" {
		t.Fatalf("count-only shape: %s", out)
	}
}

func TestAuthGrantsBoundsBeforeRequest2021(t *testing.T) {
	for _, args := range [][]string{{"--limit", "-1"}, {"--limit", "2147483648"}, {"--offset", "-1"}, {"--offset", "2147483648"}} {
		calls, _, err := runTeamTask(t, map[string]string{}, append([]string{"auth", "grants"}, args...)...)
		if exitCodeFor(err) != 2 || len(*calls) != 0 || !strings.Contains(err.Error(), "--limit and --offset take") {
			t.Fatalf("bounds: args=%v err=%v calls=%+v", args, err, *calls)
		}
	}
}

func TestAuthGrantsServerCapAndContinuation2021(t *testing.T) {
	rows := []string{}
	for i := 0; i < 200; i++ {
		rows = append(rows, grantReplace2028(t, personalGrant2021, `"id":"g1"`, fmt.Sprintf(`"id":"g%d"`, i)))
	}
	page := `{"data":{"myGrants":{"items":[` + strings.Join(rows, ",") + `],"total":201}}}`
	calls, out, err := runTeamTask(t, map[string]string{"MyGrants": page}, "auth", "grants", "--limit", "201", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || (*calls)[0].Vars["limit"] != float64(201) {
		t.Fatalf("larger user request was refused/rewritten: %+v", *calls)
	}
	var dto struct {
		Items      []json.RawMessage
		Total      int
		HasMore    bool
		NextOffset *int
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatal(err)
	}
	if len(dto.Items) != 200 || dto.Total != 201 || !dto.HasMore || dto.NextOffset == nil || *dto.NextOffset != 200 {
		t.Fatalf("continuation lost: %s", out)
	}
}

func TestAuthGrantsFailureIsNotEmpty2021(t *testing.T) {
	for _, tc := range []struct {
		name, page string
		code       int
	}{
		{"forbidden", `{"errors":[{"message":"refused","extensions":{"code":"FORBIDDEN"}}]}`, 8},
		{"unsupported", `{"errors":[{"message":"Cannot query field \"myGrants\" on type \"Query\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`, 2},
		{"missing-page", `{"data":{"myGrants":null}}`, 1},
		{"missing-row", `{"data":{"myGrants":{"items":[null],"total":1}}}`, 1},
		{"non-progress", `{"data":{"myGrants":{"items":[],"total":1}}}`, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls, out, err := runTeamTask(t, map[string]string{"MyGrants": tc.page}, "auth", "grants", "--json")
			if exitCodeFor(err) != tc.code || len(*calls) != 1 || strings.Contains(out, `"items"`) {
				t.Fatalf("failure became empty/success/retry: code=%d err=%v calls=%+v out=%s", exitCodeFor(err), err, *calls, out)
			}
		})
	}
}

func TestAuthGrantsProjectionAndRawSignatureState2021(t *testing.T) {
	for _, state := range []string{`null`, `"PENDING"`, `"CURRENT"`, `"EARLIER"`, `"CANCELLED"`} {
		t.Run(state, func(t *testing.T) {
			row := grantReplace2028(t, personalGrant2021, `"state":"EARLIER"`, `"state":`+state)
			calls, out, err := runTeamTask(t, map[string]string{"MyGrants": `{"data":{"myGrants":{"items":[` + row + `],"total":1}}}`}, "auth", "grants", "--json")
			if err != nil {
				t.Fatal(err)
			}
			var dto struct {
				Items []struct {
					SignatureStatus struct{ State json.RawMessage }
				}
				HasMore    bool
				NextOffset *int
			}
			if err := json.Unmarshal([]byte(out), &dto); err != nil {
				t.Fatal(err)
			}
			if len(dto.Items) != 1 || string(dto.Items[0].SignatureStatus.State) != state || dto.HasMore || dto.NextOffset != nil {
				t.Fatalf("server signature/paging fact changed: %s", out)
			}
			if (*calls)[0].Query != gen.MyGrants_Operation {
				t.Fatal("wire query differs from generated operation")
			}
			doc, err := parser.ParseQuery(&ast.Source{Input: gen.MyGrants_Operation})
			if err != nil {
				t.Fatal(err)
			}
			op := doc.Operations.ForName("MyGrants")
			if op == nil || op.Operation != ast.Query || len(op.SelectionSet) != 1 || op.SelectionSet[0].(*ast.Field).Name != "myGrants" {
				t.Fatalf("not a personal query: %s", (*calls)[0].Query)
			}
			forbidden := map[string]bool{"signatures": true, "signer": true, "verifiedBy": true, "issuerUserId": true, "signatureId": true, "holderDepartedAt": true, "revokedByUserId": true}
			var walk func(ast.SelectionSet)
			walk = func(ss ast.SelectionSet) {
				for _, s := range ss {
					f, ok := s.(*ast.Field)
					if !ok {
						t.Fatal("unexamined fragment")
					}
					if forbidden[f.Name] {
						t.Fatalf("private/manager projection %s", f.Name)
					}
					walk(f.SelectionSet)
				}
			}
			walk(op.SelectionSet)
		})
	}
}

func TestAuthGrantsHumanCellsSafe2021(t *testing.T) {
	row := grantReplace2028(t, personalGrant2021, "Past terms", `unsafe\u001b[31mline\nnext`)
	_, out, err := runTeamTask(t, map[string]string{"MyGrants": `{"data":{"myGrants":{"items":[` + row + `],"total":1}}}`}, "auth", "grants")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "\x1b") || strings.Contains(out, "line\nnext") ||
		!strings.Contains(out, "unsafe") || !strings.Contains(out, "line") || !strings.Contains(out, "next") {
		t.Fatalf("unsafe cell: %q", out)
	}
}

// Both boolean directions and nonzero counts must survive JSON and human
// columns. A CURRENT signature can still have spent terms; signature state is
// not the server's allows fact. All fixture dates are raw data, not a CLI clock.
func TestAuthGrantsRawDecisionFactsJSONAndTable2021(t *testing.T) {
	for _, tc := range []struct {
		name, state    string
		allows         bool
		problems, uses int
		futureStart    bool
	}{
		{"current-allowed", "CURRENT", true, 7, 0, false},
		{"current-spent", "CURRENT", false, 0, 2, false},
		{"current-before-start", "CURRENT", false, 0, 0, true},
		{"earlier-integrity", "EARLIER", false, 3, 2, false},
		{"pending", "PENDING", false, 0, 2, false},
		{"cancelled", "CANCELLED", false, 0, 2, false},
		{"null", "", false, 0, 2, false},
	} {
		stateJSON, stateCell := "null", "—"
		if tc.state != "" {
			b, err := json.Marshal(tc.state)
			if err != nil {
				t.Fatal(err)
			}
			stateJSON, stateCell = string(b), tc.state
		}
		row := grantReplace2028(t, personalGrant2021, `"state":"EARLIER"`, `"state":`+stateJSON)
		row = grantReplace2028(t, row, `"allows":false`, fmt.Sprintf(`"allows":%t`, tc.allows))
		row = grantReplace2028(t, row, `"integrityProblems":0`, fmt.Sprintf(`"integrityProblems":%d`, tc.problems))
		row = grantReplace2028(t, row, `"useCount":2`, fmt.Sprintf(`"useCount":%d`, tc.uses))
		// Keep the positive / pre-start stubs plausible without computing allows.
		if tc.allows || tc.futureStart {
			row = grantReplace2028(t, row, `"expiresAt":"2026-11-01T00:00:00Z"`, `"expiresAt":"2035-11-01T00:00:00Z"`)
		}
		if tc.futureStart {
			row = grantReplace2028(t, row, `"startsAt":null`, `"startsAt":"2030-11-01T00:00:00Z"`)
		}
		for _, jsonOut := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/json=%v", tc.name, jsonOut), func(t *testing.T) {
				args := []string{"auth", "grants"}
				if jsonOut {
					args = append(args, "--json")
				}
				calls, out, err := runTeamTask(t, map[string]string{"MyGrants": `{"data":{"myGrants":{"items":[` + row + `],"total":1}}}`}, args...)
				if err != nil {
					t.Fatal(err)
				}
				if len(*calls) != 1 {
					t.Fatalf("unexpected requests: %+v", *calls)
				}
				if jsonOut {
					var page map[string]json.RawMessage
					decodeGrantJSON2028(t, []byte(out), &page)
					var items []map[string]json.RawMessage
					decodeGrantJSON2028(t, requireGrantKey2028(t, page, "items"), &items)
					if len(items) != 1 {
						t.Fatalf("missing row: %s", out)
					}
					var allows *bool
					decodeGrantJSON2028(t, requireGrantKey2028(t, items[0], "allows"), &allows)
					var uses *int
					decodeGrantJSON2028(t, requireGrantKey2028(t, items[0], "useCount"), &uses)
					var sig map[string]json.RawMessage
					decodeGrantJSON2028(t, requireGrantKey2028(t, items[0], "signatureStatus"), &sig)
					var problems *int
					decodeGrantJSON2028(t, requireGrantKey2028(t, sig, "integrityProblems"), &problems)
					if allows == nil || *allows != tc.allows || uses == nil || *uses != tc.uses ||
						problems == nil || *problems != tc.problems || string(requireGrantKey2028(t, sig, "state")) != stateJSON {
						t.Fatalf("raw decision facts changed: %s", out)
					}
					return
				}
				if !strings.Contains(out, "ALLOWS") || !strings.Contains(out, "SIGNATURE STATE") || !strings.Contains(out, "INTEGRITY PROBLEMS") || !strings.Contains(out, "USE COUNT") {
					t.Fatalf("missing public columns: %s", out)
				}
				cells := grantHumanRow2028(t, out, "g1")
				// Controlled short fixture: USE COUNT is column 10; the final three
				// columns are ALLOWS / SIGNATURE STATE / INTEGRITY PROBLEMS. A reason
				// may contain spaces, so it is deliberately not a split-column anchor.
				if len(cells) < 13 || cells[9] != fmt.Sprint(tc.uses) ||
					cells[len(cells)-3] != fmt.Sprint(tc.allows) ||
					cells[len(cells)-2] != stateCell || cells[len(cells)-1] != fmt.Sprint(tc.problems) {
					t.Fatalf("raw human decision cells changed: %v; output=%s", cells, out)
				}
			})
		}
	}
}

func decodeGrantJSON2028(t *testing.T, raw []byte, dst any) {
	t.Helper()
	if err := json.Unmarshal(raw, dst); err != nil {
		t.Fatalf("invalid JSON %s: %v", raw, err)
	}
}
func requireGrantKey2028(t *testing.T, object map[string]json.RawMessage, key string) json.RawMessage {
	t.Helper()
	raw, ok := object[key]
	if !ok {
		t.Fatalf("required exact key %q missing: %s", key, object)
	}
	return raw
}
func assertEmptyGrantItems2028(t *testing.T, out string) {
	t.Helper()
	var page map[string]json.RawMessage
	decodeGrantJSON2028(t, []byte(out), &page)
	var items []json.RawMessage
	decodeGrantJSON2028(t, requireGrantKey2028(t, page, "items"), &items)
	if items == nil || len(items) != 0 {
		t.Fatalf("expected a present empty list, not null/missing: %s", out)
	}
}
func grantHumanRow2028(t *testing.T, out, id string) []string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] == id {
			return fields
		}
	}
	t.Fatalf("missing human row %q: %s", id, out)
	return nil
}

// Fixture edits must fail at setup if the wire spelling changes, rather than
// quietly exercising a different server row.
func grantReplace2028(t *testing.T, row, old, replacement string) string {
	t.Helper()
	if n := strings.Count(row, old); n != 1 {
		t.Fatalf("fixture fragment %q: expected one match, got %d", old, n)
	}
	return strings.Replace(row, old, replacement, 1)
}

func TestAuthGrantsCountWording2028(t *testing.T) {
	calls, out, err := runTeamTask(t, map[string]string{"MyGrants": `{"data":{"myGrants":{"items":[],"total":41}}}`},
		"auth", "grants", "--limit", "0", "--offset", "17")
	if err != nil {
		t.Fatal(err)
	}
	if out != "total matching: 41\n" || len(*calls) != 1 ||
		(*calls)[0].Vars["limit"] != float64(0) || (*calls)[0].Vars["offset"] != float64(17) {
		t.Fatalf("count wording or forwarded offset: %q calls=%+v", out, *calls)
	}
}

func TestAuthGrantsContinuationText2028(t *testing.T) {
	second := grantReplace2028(t, personalGrant2021, `"id":"g1"`, `"id":"g2"`)
	page := `{"data":{"myGrants":{"items":[` + personalGrant2021 + "," + second + `],"total":10}}}`
	for _, jsonOut := range []bool{false, true} {
		t.Run(fmt.Sprintf("json=%v", jsonOut), func(t *testing.T) {
			args := []string{"auth", "grants", "--limit", "2", "--offset", "7"}
			if jsonOut {
				args = append(args, "--json")
			}
			calls, out, err := runTeamTask(t, map[string]string{"MyGrants": page}, args...)
			if err != nil {
				t.Fatal(err)
			}
			if len(*calls) != 1 || (*calls)[0].Vars["limit"] != float64(2) || (*calls)[0].Vars["offset"] != float64(7) {
				t.Fatalf("paging request changed: %+v", *calls)
			}
			if jsonOut {
				var result struct {
					Items      []json.RawMessage
					Total      int
					HasMore    bool
					NextOffset *int
				}
				if err := json.Unmarshal([]byte(out), &result); err != nil {
					t.Fatal(err)
				}
				if len(result.Items) != 2 || result.Total != 10 || !result.HasMore || result.NextOffset == nil || *result.NextOffset != 9 {
					t.Fatalf("continuation changed: %s", out)
				}
			} else if !strings.Contains(out, "g1") || !strings.Contains(out, "g2") ||
				!strings.Contains(out, "next offset: 9\n") ||
				!strings.HasSuffix(out, "total matching: 10; rows on this page: 2; offset: 7; requested limit: 2; has more: true\n") {
				t.Fatalf("row/continuation/footer wording: %s", out)
			}
		})
	}
}

func TestAuthGrantsHelpPaging2028(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"auth", "grants", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, sentence := range []string{
		"Limit 0 returns the total matching and no rows.",
		"Offset pages are not a snapshot; grants added or removed between calls can shift later pages.",
	} {
		if !strings.Contains(strings.Join(strings.Fields(out.String()), " "), sentence) {
			t.Fatalf("missing help sentence %q: %s", sentence, out)
		}
	}
}

func TestAuthGrantsMissingConfiguredCredential2028(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"myGrants":{"items":[],"total":0}}}`))
	}))
	t.Cleanup(srv.Close)
	// testFactory supplies an empty in-memory store; clearing its test env
	// credential cannot read a host keychain or a developer's auth.json.
	f, _ := testFactory(t)
	t.Setenv("HADRON_TOKEN", "")
	root := NewRootCmd(f)
	root.SetArgs([]string{"--server", srv.URL, "auth", "grants"})
	err := root.Execute()
	mu.Lock()
	gotCalls := calls
	mu.Unlock()
	if exitCodeFor(err) != 3 || err == nil || !strings.Contains(err.Error(), "not signed in") || gotCalls != 0 {
		t.Fatalf("missing local credential: code=%d err=%v requests=%d", exitCodeFor(err), err, gotCalls)
	}
}

func TestAuthGrantsPastEndIsEmptyPage2028(t *testing.T) {
	calls, out, err := runTeamTask(t, map[string]string{"MyGrants": `{"data":{"myGrants":{"items":[],"total":41}}}`},
		"auth", "grants", "--offset", "99", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || (*calls)[0].Vars["offset"] != float64(99) {
		t.Fatalf("past-end request: %+v", *calls)
	}
	assertEmptyGrantItems2028(t, out)
	var page map[string]json.RawMessage
	decodeGrantJSON2028(t, []byte(out), &page)
	if string(requireGrantKey2028(t, page, "total")) != "41" ||
		string(requireGrantKey2028(t, page, "hasMore")) != "false" ||
		string(requireGrantKey2028(t, page, "nextOffset")) != "null" {
		t.Fatalf("past-end page metadata: %s", out)
	}
}

func TestAuthGrantsExplicitAppNeverScopesPersonalRead2028(t *testing.T) {
	for _, bound := range []bool{true, false} {
		t.Run(fmt.Sprintf("bound=%v", bound), func(t *testing.T) {
			type wire struct {
				header http.Header
				op     string
				vars   map[string]any
			}
			var mu sync.Mutex
			requests := []wire{}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					OperationName string
					Variables     map[string]any
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("invalid request body: %v", err)
					http.Error(w, "bad fixture request", http.StatusBadRequest)
					return
				}
				mu.Lock()
				requests = append(requests, wire{r.Header.Clone(), body.OperationName, body.Variables})
				mu.Unlock()
				if body.OperationName != "MyGrants" {
					t.Errorf("unexpected preflight/operation %s", body.OperationName)
					http.Error(w, "unexpected operation", http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"data":{"myGrants":{"items":[],"total":0}}}`))
			}))
			t.Cleanup(srv.Close)
			f, out := testFactory(t)
			if bound {
				writeTeamBinding(t)
				setTeamBindingServer(t, srv.URL)
			} else {
				teamGitDir(t) // a private, explicitly empty binding cache
				t.Chdir(t.TempDir())
			}
			root := NewRootCmd(f)
			root.SetArgs([]string{"--server", srv.URL, "--app", "app-not-the-binding-app", "auth", "grants", "--json"})
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			got := append([]wire{}, requests...)
			mu.Unlock()
			if len(got) != 1 {
				t.Fatalf("expected one personal query, got %d requests", len(got))
			}
			if got[0].op != "MyGrants" || len(got[0].vars) != 2 ||
				got[0].vars["limit"] != float64(20) || got[0].vars["offset"] != float64(0) {
				t.Fatalf("App flag added a variable/resolution: op=%s vars=%+v", got[0].op, got[0].vars)
			}
			if got[0].header.Get("Authorization") == "" {
				t.Fatal("personal credential not sent")
			}
			for name := range got[0].header {
				lower := strings.ToLower(name)
				if strings.HasPrefix(lower, "x-hadron-") && (strings.Contains(lower, "session") || strings.Contains(lower, "app")) {
					t.Fatalf("personal query acquired a scope header %q", name)
				}
			}
			assertEmptyGrantItems2028(t, out.String())
		})
	}
}
