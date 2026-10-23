package cmd

import (
	"encoding/json"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
	"strings"
	"testing"
)

func TestEmailConnections198(t *testing.T) {
	for _, ref := range []string{"org-id", "hrn:org:example.com:team", "example.com::team"} {
		t.Run(ref, func(t *testing.T) {
			row := `{"id":"c","organizationId":"o","userId":"u","mailboxEmail":"synthetic@example.test","provider":"gmail","displayName":null,"syncEnabled":false,"syncStatus":"ERROR","lastSyncAt":null,"createdAt":"created","updatedAt":"updated","lastError":"PRIVATE_ERROR","accessToken":"PRIVATE_TOKEN"}`
			srv, ops := captureGraphQL(t, map[string]string{"EmailConnections": `{"data":{"exchangeConnections":[` + row + `]}}`})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"email", "connections", "--org", ref, "--json", "--server", srv.URL})
			if e := root.Execute(); e != nil {
				t.Fatal(e)
			}
			var vars map[string]any
			_ = json.Unmarshal(ops["EmailConnections"], &vars)
			if len(ops) != 1 || vars["orgId"] != ref {
				t.Fatal(ops)
			}
			var rows []map[string]any
			if e := json.Unmarshal([]byte(out.String()), &rows); e != nil {
				t.Fatal(e)
			}
			if len(rows) != 1 || len(rows[0]) != 11 || rows[0]["displayName"] != nil || rows[0]["lastSyncAt"] != nil || rows[0]["syncEnabled"] != false || rows[0]["syncStatus"] != "ERROR" {
				t.Fatal(out.String())
			}
			if strings.Contains(out.String(), "PRIVATE") || strings.Contains(out.String(), "lastError") {
				t.Fatal(out.String())
			}
		})
	}
}

func TestEmailConnections198EmptyAndFailure(t *testing.T) {
	for _, tc := range []struct {
		name, wire string
		want       int
	}{
		{"empty", `{"data":{"exchangeConnections":[]}}`, 0},
		{"null", `{"data":{"exchangeConnections":null}}`, 7},
		{"null-row", `{"data":{"exchangeConnections":[null]}}`, 7},
		{"admin-refused", `{"errors":[{"message":"Forbidden","extensions":{"code":"FORBIDDEN"}}]}`, 8},
		{"unknown-tool", `{"errors":[{"message":"synthetic failure","extensions":{"code":"EMAIL_TOOL_ERROR","emailErrorCode":"unknown"}}]}`, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := captureGraphQL(t, map[string]string{"EmailConnections": tc.wire})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"email", "connections", "--org", "o", "--json", "--server", srv.URL})
			e := root.Execute()
			if exitCodeFor(e) != tc.want {
				t.Fatal(e, out.String(), exitCodeFor(e))
			}
			if tc.want == 0 && strings.TrimSpace(out.String()) != "[]" {
				t.Fatal(out.String())
			}
			if tc.want != 0 && strings.Contains(out.String(), "mailboxEmail") {
				t.Fatal(out.String())
			}
		})
	}
}

func TestEmailConnections198InvalidScope(t *testing.T) {
	for _, args := range [][]string{{}, {"--org", ""}, {"--org", "   "}, {"--org", "o", "extra"}} {
		srv, ops := captureGraphQL(t, map[string]string{})
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		a := append([]string{"email", "connections", "--server", srv.URL}, args...)
		root.SetArgs(a)
		if e := root.Execute(); exitCodeFor(e) != 2 {
			t.Fatal(e)
		}
		if len(ops) != 0 {
			t.Fatal(ops)
		}
	}
}

func TestEmailConnections198Document(t *testing.T) {
	doc, e := parser.ParseQuery(&ast.Source{Input: gen.EmailConnections_Operation})
	if e != nil {
		t.Fatal(e)
	}
	op := doc.Operations.ForName("EmailConnections")
	if len(op.SelectionSet) != 1 {
		t.Fatal("extra root selection")
	}
	field := op.SelectionSet[0].(*ast.Field)
	if field.Name != "exchangeConnections" || len(field.Arguments) != 1 || field.Arguments.ForName("orgId").Value.Raw != "orgId" {
		t.Fatal(field)
	}
	want := []string{"id", "organizationId", "userId", "mailboxEmail", "provider", "displayName", "syncEnabled", "syncStatus", "lastSyncAt", "createdAt", "updatedAt"}
	if len(field.SelectionSet) != len(want) {
		t.Fatal("expanded connection projection")
	}
	for i, s := range field.SelectionSet {
		f := s.(*ast.Field)
		if f.Name != want[i] || len(f.SelectionSet) != 0 {
			t.Fatal(f)
		}
	}
}

func TestEmailConnections198Human(t *testing.T) {
	for _, rows := range []string{`[]`, `[{"id":"c","organizationId":"o","userId":"u","mailboxEmail":"synthetic@example.test","provider":"gmail","displayName":"line\nescape\u001b[31m","syncEnabled":true,"syncStatus":"IDLE","lastSyncAt":null,"createdAt":"created","updatedAt":"updated","lastError":"PRIVATE"}]`} {
		srv, _ := captureGraphQL(t, map[string]string{"EmailConnections": `{"data":{"exchangeConnections":` + rows + `}}`})
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"email", "connections", "--org", " o ", "--server", srv.URL})
		if e := root.Execute(); e != nil {
			t.Fatal(e)
		}
		if !strings.Contains(out.String(), `organization: "o" (from --org`) || strings.Contains(out.String(), "PRIVATE") || strings.ContainsRune(out.String(), '\x1b') {
			t.Fatal(out.String())
		}
		if rows == "[]" && !strings.Contains(out.String(), "No email connections returned to this caller.") {
			t.Fatal(out.String())
		}
		if rows != "[]" && (!strings.Contains(out.String(), `line\nescape\x1b[31m`) || !strings.Contains(out.String(), "null")) {
			t.Fatal(out.String())
		}
	}
}
