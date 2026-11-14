package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
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
			if !strings.Contains(out, `"items": []`) || strings.Contains(out, `"items": null`) {
				t.Fatalf("empty JSON shape: %s", out)
			}
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
		rows = append(rows, strings.Replace(personalGrant2021, `"g1"`, fmt.Sprintf(`"g%d"`, i), 1))
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
			row := strings.Replace(personalGrant2021, `"EARLIER"`, state, 1)
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
	row := strings.Replace(personalGrant2021, "Past terms", `unsafe\u001b[31mline\nnext`, 1)
	_, out, err := runTeamTask(t, map[string]string{"MyGrants": `{"data":{"myGrants":{"items":[` + row + `],"total":1}}}`}, "auth", "grants")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "\x1b") || strings.Contains(out, "line\nnext") {
		t.Fatalf("unsafe cell: %q", out)
	}
}

// Both boolean directions and a nonzero count must survive the public JSON and
// human columns. Zero-only fixtures cannot distinguish a dropped server fact.
func TestAuthGrantsRawDecisionFactsJSONAndTable2021(t *testing.T) {
	for _, state := range []string{"CURRENT", "PENDING", "EARLIER", "CANCELLED", ""} {
		allows, problems := false, 0
		if state == "CURRENT" {
			allows, problems = true, 7
		}
		stateJSON, stateCell := "null", "—"
		if state != "" {
			b, err := json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
			stateJSON, stateCell = string(b), state
		}
		row := strings.Replace(personalGrant2021, `"EARLIER"`, stateJSON, 1)
		if allows {
			row = strings.Replace(row, `"allows":false`, `"allows":true`, 1)
			row = strings.Replace(row, `"integrityProblems":0`, `"integrityProblems":7`, 1)
			row = strings.Replace(row, `"useCount":2`, `"useCount":0`, 1)
			row = strings.Replace(row, "2026-11-01T00:00:00Z", "2030-11-01T00:00:00Z", 1)
		}
		for _, jsonOut := range []bool{true, false} {
			t.Run(fmt.Sprintf("state=%s/json=%v", stateCell, jsonOut), func(t *testing.T) {
				args := []string{"auth", "grants"}
				if jsonOut {
					args = append(args, "--json")
				}
				calls, out, err := runTeamTask(t, map[string]string{"MyGrants": `{"data":{"myGrants":{"items":[` + row + `],"total":1}}}`}, args...)
				if err != nil || len(*calls) != 1 {
					t.Fatalf("err=%v calls=%+v output=%s", err, *calls, out)
				}
				if jsonOut {
					var dto struct {
						Items []struct {
							Allows          bool
							SignatureStatus struct {
								State             json.RawMessage
								IntegrityProblems int
							}
						}
					}
					if err := json.Unmarshal([]byte(out), &dto); err != nil {
						t.Fatal(err)
					}
					if len(dto.Items) != 1 || dto.Items[0].Allows != allows ||
						dto.Items[0].SignatureStatus.IntegrityProblems != problems ||
						string(dto.Items[0].SignatureStatus.State) != stateJSON {
						t.Fatalf("raw decision facts changed: %s", out)
					}
					return
				}
				if !strings.Contains(out, "ALLOWS") || !strings.Contains(out, "SIGNATURE STATE") || !strings.Contains(out, "INTEGRITY PROBLEMS") {
					t.Fatalf("missing public columns: %s", out)
				}
				var cells []string
				for _, line := range strings.Split(out, "\n") {
					fields := strings.Fields(line)
					if len(fields) > 0 && fields[0] == "g1" {
						cells = fields
						break
					}
				}
				// The final three columns are ALLOWS, SIGNATURE STATE and
				// INTEGRITY PROBLEMS; a reason can itself contain spaces.
				if len(cells) < 3 || cells[len(cells)-3] != fmt.Sprint(allows) ||
					cells[len(cells)-2] != stateCell || cells[len(cells)-1] != fmt.Sprint(problems) {
					t.Fatalf("raw human decision cells changed: %v; output=%s", cells, out)
				}
			})
		}
	}
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
