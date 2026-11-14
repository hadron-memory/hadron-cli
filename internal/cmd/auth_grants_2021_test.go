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
