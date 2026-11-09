package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

const workerGrant1832 = `{"id":"g1","action":"node.read","values":["one","two"],"holderKind":"WORKER","holderId":"wkr1","startsAt":"start","expiresAt":null,"maxUses":3,"useCount":1,"reason":"because\u001b[31m","createdAt":"created","activatedAt":"activated-time","revokedAt":null,"allows":true,"futureServerField":"must not leak"}`

var grantFields1832 = []string{"id", "action", "values", "holderKind", "holderId", "startsAt", "expiresAt", "maxUses", "useCount", "reason", "createdAt", "activatedAt", "revokedAt", "allows"}

func TestTeamGrant1832List(t *testing.T) {
	for _, jsonMode := range []bool{false, true} {
		t.Run(map[bool]string{false: "text", true: "json"}[jsonMode], func(t *testing.T) {
			args := []string{"team", "grant", "list", "--limit", "7", "--offset", "2"}
			if jsonMode {
				args = append(args, "--json")
			}
			calls, out, err := runTeamTask(t, map[string]string{"TeamGrants": `{"data":{"grants":{"items":[` + workerGrant1832 + `],"total":9}}}`}, args...)
			if err != nil {
				t.Fatal(err)
			}
			var call *attnCall
			for i := range *calls {
				if (*calls)[i].Op == "TeamGrants" {
					call = &(*calls)[i]
				}
			}
			if call == nil || call.Session != "s-new" || call.Vars["limit"] != float64(7) || call.Vars["offset"] != float64(2) {
				t.Fatalf("wrong bound call: %+v", calls)
			}
			checkGrantSelection1832(t, call.Query, "grants")
			if jsonMode {
				var dto struct {
					Items []map[string]any `json:"items"`
					Total int              `json:"total"`
				}
				if e := json.Unmarshal([]byte(out), &dto); e != nil {
					t.Fatal(e)
				}
				if dto.Total != 9 || len(dto.Items) != 1 {
					t.Fatal(out)
				}
				var want map[string]any
				_ = json.Unmarshal([]byte(workerGrant1832), &want)
				delete(want, "futureServerField")
				if !reflect.DeepEqual(dto.Items[0], want) {
					t.Fatalf("DTO %s", out)
				}
			} else {
				for _, v := range []string{"g1", "node.read", "WORKER", "wkr1", "start", "created", "activated-time", "yes", "one", "because"} {
					if !strings.Contains(out, v) {
						t.Fatalf("missing %q: %s", v, out)
					}
				}
				if strings.Contains(out, "\x1b") {
					t.Fatal("raw terminal control")
				}
			}
		})
	}
}
func checkGrantSelection1832(t *testing.T, query, rootName string) {
	t.Helper()
	doc, e := parser.ParseQuery(&ast.Source{Input: query})
	if e != nil {
		t.Fatal(e)
	}
	op := doc.Operations[0]
	if len(op.SelectionSet) != 1 {
		t.Fatal("extra root")
	}
	root := op.SelectionSet[0].(*ast.Field)
	if root.Name != rootName {
		t.Fatal(root.Name)
	}
	set := root.SelectionSet
	if rootName == "grants" {
		if len(set) != 2 {
			t.Fatal("page shape")
		}
		var items *ast.Field
		for _, s := range set {
			f := s.(*ast.Field)
			if f.Name == "items" {
				items = f
			} else if f.Name != "total" {
				t.Fatal(f.Name)
			}
		}
		if items == nil {
			t.Fatal("no items")
		}
		set = items.SelectionSet
	}
	var got []string
	for _, s := range set {
		f, ok := s.(*ast.Field)
		if !ok || len(f.SelectionSet) != 0 {
			t.Fatal("unexpected fragment/nesting")
		}
		got = append(got, f.Name)
	}
	want := append([]string{}, grantFields1832...)
	sort.Strings(want)
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fields %v", got)
	}
}
func TestTeamGrant1832CountAndEmpty(t *testing.T) {
	for _, flag := range []string{"text", "json"} {
		t.Run(flag, func(t *testing.T) {
			args := []string{"team", "grant", "list", "--limit", "0"}
			if flag == "json" {
				args = append(args, "--json")
			}
			calls, out, e := runTeamTask(t, map[string]string{"TeamGrants": `{"data":{"grants":{"items":[],"total":8}}}`}, args...)
			if e != nil {
				t.Fatal(e)
			}
			if len(*calls) != 1 || (*calls)[0].Vars["limit"] != float64(0) {
				t.Fatal(calls)
			}
			if flag == "json" {
				var got map[string]any
				if err := json.Unmarshal([]byte(out), &got); err != nil || !reflect.DeepEqual(got, map[string]any{"total": float64(8)}) {
					t.Fatal(out)
				}
			} else if strings.TrimSpace(out) != "total: 8" {
				t.Fatal(out)
			}
		})
	}
	_, out, e := runTeamTask(t, map[string]string{"TeamGrants": `{"data":{"grants":{"items":[],"total":0}}}`}, "team", "grant", "list")
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(out, "worker-bound") || strings.Contains(out, "you have no grants") {
		t.Fatal(out)
	}
}
func TestTeamGrant1832GetAndNull(t *testing.T) {
	calls, out, e := runTeamTask(t, map[string]string{"TeamGrant": `{"data":{"grant":` + workerGrant1832 + `}}`}, "team", "grant", "get", "g1", "--json")
	if e != nil {
		t.Fatal(e)
	}
	if len(*calls) != 1 || (*calls)[0].Session != "s-new" || (*calls)[0].Vars["ref"] != "g1" {
		t.Fatal(calls)
	}
	checkGrantSelection1832(t, (*calls)[0].Query, "grant")
	if strings.Contains(out, "futureServerField") {
		t.Fatal(out)
	}
	var refusal string
	for _, label := range []string{"missing", "hidden"} {
		t.Run(label, func(t *testing.T) {
			_, out, e := runTeamTask(t, map[string]string{"TeamGrant": `{"data":{"grant":null}}`}, "team", "grant", "get", "same-id")
			if exitCodeFor(e) != 4 || out != "" {
				t.Fatalf("%q %v", out, e)
			}
			if refusal != "" && e.Error() != refusal {
				t.Fatal("oracle")
			}
			refusal = e.Error()
		})
	}
}
func TestTeamGrant1832UnboundAndInvalid(t *testing.T) {
	for _, args := range [][]string{{"list"}, {"get", "g1"}, {"list", "--limit", "-1"}, {"list", "--offset", "-1"}} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			dir := teamGitDir(t)
			_ = os.Remove(filepath.Join(dir, "hadron-team-session.json"))
			f, _ := testFactory(t)
			r := NewRootCmd(f)
			r.SetArgs(append([]string{"team", "grant"}, args...))
			if e := r.Execute(); exitCodeFor(e) != 2 {
				t.Fatalf("want usage, got %v", e)
			}
		})
	}
}
func TestTeamGrant1832HelpAndLegacy(t *testing.T) {
	for _, args := range [][]string{{"team", "grant", "list", "--help"}, {"team", "grant", "get", "--help"}} {
		f, out := testFactory(t)
		r := NewRootCmd(f)
		r.SetArgs(args)
		if e := r.Execute(); e != nil {
			t.Fatal(e)
		}
		for _, s := range []string{"worker-bound", "person-driven", "role agent", "neither activated nor revoked"} {
			if !strings.Contains(out.String(), s) {
				t.Fatalf("missing %s: %s", s, out.String())
			}
		}
	}
	for _, verb := range []string{"list", "get"} {
		responses := map[string]string{"PrincipalGrants": `{"data":{"principalGrants":{"items":[],"total":0}}}`, "PrincipalGrant": `{"data":{"principalGrant":` + grantJSON + `}}`}
		srv, got := attnServer(t, responses)
		f, _ := testFactory(t)
		r := NewRootCmd(f)
		args := []string{"grant", verb, "--server", srv.URL}
		if verb == "get" {
			args = append(args, "pg1")
		}
		r.SetArgs(args)
		if e := r.Execute(); e != nil {
			t.Fatal(e)
		}
		if len(*got) != 1 {
			t.Fatal(got)
		}
		doc, err := parser.ParseQuery(&ast.Source{Input: (*got)[0].Query})
		if err != nil {
			t.Fatal(err)
		}
		field := doc.Operations[0].SelectionSet[0].(*ast.Field).Name
		want := map[string]string{"list": "principalGrants", "get": "principalGrant"}[verb]
		if field != want {
			t.Fatalf("legacy field %s", field)
		}
	}
}

func TestTeamGrant1832EmptyJSONAndBounds(t *testing.T) {
	_, out, e := runTeamTask(t, map[string]string{"TeamGrants": `{"data":{"grants":{"items":[],"total":0}}}`}, "team", "grant", "list", "--json")
	if e != nil {
		t.Fatal(e)
	}
	var got map[string]json.RawMessage
	if e = json.Unmarshal([]byte(out), &got); e != nil || string(got["items"]) != "[]" {
		t.Fatal(out, e)
	}
	for _, args := range [][]string{{"--limit", "201"}, {"--limit", "-1"}, {"--offset", "-1"}, {"--offset", "2147483648"}} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			calls, _, e := runTeamTask(t, map[string]string{}, append([]string{"team", "grant", "list"}, args...)...)
			if exitCodeFor(e) != 2 || len(*calls) != 0 {
				t.Fatal(e, calls)
			}
		})
	}
}
func TestTeamGrant1832GetTextAndFalse(t *testing.T) {
	_, out, e := runTeamTask(t, map[string]string{"TeamGrant": `{"data":{"grant":` + strings.Replace(workerGrant1832, `"allows":true`, `"allows":false`, 1) + `}}`}, "team", "grant", "get", "g1")
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range grantFields1832 {
		if !strings.Contains(out, name+":") {
			t.Fatal(name, out)
		}
	}
	if !strings.Contains(out, "allows: no") || strings.Contains(out, "\x1b") {
		t.Fatal(out)
	}
}

func TestTeamGrant1832BindingBoundary(t *testing.T) {
	for _, kind := range []string{"server", "incomplete", "app"} {
		t.Run(kind, func(t *testing.T) {
			writeTeamBinding(t)
			srv, calls := attnServer(t, map[string]string{"TeamAppIdentity": strings.ReplaceAll(teamAppIdentityJSON, "capp100000000000000000000", "capp999999999999999999999")})
			setTeamBindingServer(t, srv.URL)
			f, _ := testFactory(t)
			r := NewRootCmd(f)
			args := []string{"team", "grant", "list", "--server", srv.URL}
			if kind == "server" {
				args = []string{"team", "grant", "list", "--server", "http://127.0.0.1:1"}
			}
			if kind == "incomplete" {
				dir := teamGitDir(t)
				if e := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(`{"sessionId":"s"}`), 0600); e != nil {
					t.Fatal(e)
				}
			}
			if kind == "app" {
				args = append(args, "--app", "some-other-app")
			}
			r.SetArgs(args)
			if e := r.Execute(); exitCodeFor(e) != 2 {
				t.Fatalf("%v", e)
			}
			for _, c := range *calls {
				if c.Op == "TeamGrants" {
					t.Fatal("read across binding boundary")
				}
			}
		})
	}
}

func TestTeamGrant1832OutsideWorktree(t *testing.T) {
	t.Setenv(team.GitDirEnv, "")
	t.Chdir(t.TempDir())
	f, _ := testFactory(t)
	r := NewRootCmd(f)
	r.SetArgs([]string{"team", "grant", "list"})
	if e := r.Execute(); exitCodeFor(e) != 2 {
		t.Fatal(e)
	}
}
