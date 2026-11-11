package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

// hadron-cli#1875 over hadron-server#3853 (accountUsageAccounts, cor:agt:140:05):
// `team usage accounts` prints the App's usage accounts exactly as the server
// returns them. Through the real command with a stub server.

const acctRupaliOrca1 = `{"account":"team-rupali","host":"orca1","family":"claude","newestSnapshotAt":"2026-11-11T08:00:00Z","workers":["Ruth","Rafa"],"workersConfigured":true}`
const acctRupaliOrca2 = `{"account":"team-rupali","host":"orca2","family":"claude","newestSnapshotAt":"2026-11-11T07:30:00Z","workers":[],"workersConfigured":true}`
const acctHolgerCodex = `{"account":"team-holger","host":"orca1","family":"codex","newestSnapshotAt":"2026-11-11T06:00:00Z","workers":[],"workersConfigured":false}`

func acctsResponse(complete bool, entries ...string) string {
	c := "true"
	if !complete {
		c = "false"
	}
	return `{"data":{"accountUsageAccounts":{"asOf":"2026-11-11T08:01:00Z","accounts":[` + strings.Join(entries, ",") + `],"complete":` + c + `}}}`
}

// runAccounts runs the command and returns the calls, stdout, stderr and error.
func runAccounts(t *testing.T, resp map[string]string, argv ...string) (*[]attnCall, string, string, error) {
	t.Helper()
	writeTeamBinding(t)
	srv, calls := attnServer(t, resp)
	setTeamBindingServer(t, srv.URL)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"--server", srv.URL, "team", "usage", "accounts"}, argv...))
	err := root.Execute()
	return calls, out.String(), f.IOStreams.ErrOut.(*strings.Builder).String(), err
}

func acctCalls(calls *[]attnCall) []attnCall {
	var got []attnCall
	for _, c := range *calls {
		if c.Op == "AccountUsageAccounts" {
			got = append(got, c)
		}
	}
	return got
}

// Acceptance 1: every field, the stub's order unchanged, one label from two
// hosts is two entries.
func TestUsageAccounts1875ListsInServerOrder(t *testing.T) {
	// Deliberately NOT sorted by label: the CLI must not re-order.
	resp := map[string]string{"AccountUsageAccounts": acctsResponse(true, acctRupaliOrca1, acctHolgerCodex, acctRupaliOrca2)}
	calls, out, errOut, err := runAccounts(t, resp)
	if err != nil {
		t.Fatal(err)
	}
	got := acctCalls(calls)
	if len(got) != 1 || got[0].Vars["appRef"] == "" || got[0].Vars["appRef"] == nil {
		t.Fatalf("calls %+v", got)
	}
	var rows []string
	for _, l := range strings.Split(out, "\n") {
		if f := strings.Fields(l); len(f) > 0 && strings.HasPrefix(f[0], "team-") {
			rows = append(rows, l)
		}
	}
	if len(rows) != 3 {
		t.Fatalf("want 3 rows, got %d:\n%s", len(rows), out)
	}
	for i, want := range [][]string{
		{"team-rupali", "orca1", "claude", "2026-11-11T08:00:00Z", "Ruth, Rafa"},
		{"team-holger", "orca1", "codex", "2026-11-11T06:00:00Z"},
		{"team-rupali", "orca2", "claude", "2026-11-11T07:30:00Z"},
	} {
		for _, w := range want {
			if !strings.Contains(rows[i], w) {
				t.Errorf("row %d %q lacks %q", i, rows[i], w)
			}
		}
	}
	if strings.Contains(errOut, "incomplete") {
		t.Fatalf("complete list warned: %q", errOut)
	}

	_, js, _, err := runAccounts(t, resp, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		AsOf     string           `json:"asOf"`
		Accounts []map[string]any `json:"accounts"`
		Complete *bool            `json:"complete"`
	}
	if err := json.Unmarshal([]byte(js), &env); err != nil {
		t.Fatalf("%v: %s", err, js)
	}
	if env.AsOf != "2026-11-11T08:01:00Z" || env.Complete == nil || !*env.Complete || len(env.Accounts) != 3 {
		t.Fatalf("envelope %s", js)
	}
	first := env.Accounts[0]
	if first["account"] != "team-rupali" || first["host"] != "orca1" || first["family"] != "claude" ||
		first["newestSnapshotAt"] != "2026-11-11T08:00:00Z" || first["workersConfigured"] != true {
		t.Fatalf("entry 0 %v", first)
	}
	if ws, _ := first["workers"].([]any); len(ws) != 2 || ws[0] != "Ruth" || ws[1] != "Rafa" {
		t.Fatalf("workers %v", first["workers"])
	}
	if env.Accounts[1]["account"] != "team-holger" || env.Accounts[2]["host"] != "orca2" {
		t.Fatalf("order changed: %s", js)
	}
	var top map[string]json.RawMessage
	_ = json.Unmarshal([]byte(js), &top)
	keys := []string{}
	for k := range top {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if strings.Join(keys, ",") != "accounts,asOf,complete" {
		t.Fatalf("envelope keys %v, want exactly asOf, accounts, complete", keys)
	}
}

// Acceptance 2: no history is the envelope with accounts [] and complete true,
// never a bare [], exit 0; text prints an empty-list line.
func TestUsageAccounts1875EmptyHistory(t *testing.T) {
	resp := map[string]string{"AccountUsageAccounts": acctsResponse(true)}
	_, js, errOut, err := runAccounts(t, resp, "--json")
	if err != nil {
		t.Fatal(err)
	}
	if sq := squash1837(js); !strings.Contains(sq, `"accounts":[]`) || !strings.Contains(sq, `"complete":true`) || strings.HasPrefix(sq, "[") {
		t.Fatalf("json %s", js)
	}
	if strings.Contains(errOut, "incomplete") {
		t.Fatalf("stderr %q", errOut)
	}
	_, out, _, err := runAccounts(t, resp)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no usage accounts") {
		t.Fatalf("text %q", out)
	}
}

// Acceptance 3: complete false, with and without entries, exit 0; stderr note
// in text mode, the envelope in --json; stdout carries only the rows.
func TestUsageAccounts1875IncompleteIsNotAnError(t *testing.T) {
	for name, resp := range map[string]string{
		"with entries": acctsResponse(false, acctRupaliOrca1),
		"empty":        acctsResponse(false),
	} {
		t.Run(name, func(t *testing.T) {
			r := map[string]string{"AccountUsageAccounts": resp}
			_, out, errOut, err := runAccounts(t, r)
			if err != nil {
				t.Fatalf("exit 0 expected: %v", err)
			}
			if !strings.Contains(errOut, "incomplete") {
				t.Fatalf("stderr lacks the incomplete note: %q", errOut)
			}
			if strings.Contains(out, "incomplete") {
				t.Fatalf("the note leaked to stdout: %q", out)
			}
			_, js, _, err := runAccounts(t, r, "--json")
			if err != nil {
				t.Fatalf("exit 0 expected: %v", err)
			}
			if !strings.Contains(squash1837(js), `"complete":false`) {
				t.Fatalf("json %s", js)
			}
			if name == "empty" && !strings.Contains(squash1837(js), `"accounts":[]`) {
				t.Fatalf("json %s", js)
			}
		})
	}
}

// Acceptance 4: workersConfigured false is shown as such, distinct from a
// configured empty list.
func TestUsageAccounts1875WorkersNotConfiguredIsDistinct(t *testing.T) {
	resp := map[string]string{"AccountUsageAccounts": acctsResponse(true, acctHolgerCodex, acctRupaliOrca2)}
	_, out, _, err := runAccounts(t, resp)
	if err != nil {
		t.Fatal(err)
	}
	var notConf, empty string
	for _, l := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(l, "team-holger"):
			notConf = l
		case strings.HasPrefix(l, "team-rupali"):
			empty = l
		}
	}
	if !strings.Contains(notConf, "not configured") {
		t.Fatalf("unconfigured row %q", notConf)
	}
	if strings.Contains(empty, "not configured") || !strings.Contains(empty, "none") {
		t.Fatalf("configured-empty row %q", empty)
	}
	_, js, _, _ := runAccounts(t, resp, "--json")
	sq := squash1837(js)
	if !strings.Contains(sq, `"workers":[],"workersConfigured":false`) || !strings.Contains(sq, `"workers":[],"workersConfigured":true`) {
		t.Fatalf("json %s", js)
	}
}

// Acceptance 5: APP_NOT_FOUND is an error with no added hint, distinct from
// empty history; a server without the field says "too old".
func TestUsageAccounts1875AppNotFoundAndOldServer(t *testing.T) {
	_, out, _, err := runAccounts(t, map[string]string{"AccountUsageAccounts": gqlErrorJSON("APP_NOT_FOUND")})
	if err == nil || exitOf(err) == exitcode.OK || !strings.Contains(err.Error(), "APP_NOT_FOUND") {
		t.Fatalf("got %v (%d)", err, exitOf(err))
	}
	if strings.Contains(out, "no usage accounts") || strings.Contains(out, `"accounts"`) {
		t.Fatalf("not-found printed an empty result: %q", out)
	}
	low := strings.ToLower(err.Error())
	for _, hint := range []string{"may not", "permission", "does not exist", "not a member"} {
		if strings.Contains(low, hint) {
			t.Fatalf("the CLI added a hint (%q) telling missing from unreadable apart: %v", hint, err)
		}
	}
	old := `{"errors":[{"message":"Cannot query field \"accountUsageAccounts\" on type \"Query\"."}]}`
	_, _, _, err = runAccounts(t, map[string]string{"AccountUsageAccounts": old})
	if err == nil || !strings.Contains(err.Error(), "too old") {
		t.Fatalf("got %v", err)
	}
}

// Acceptance 5a: an App key as the caller (no worker binding, --app names the
// App) lists exactly as a participant does; the CLI adds no gate of its own.
func TestUsageAccounts1875AppKeyCallerIsAdmitted(t *testing.T) {
	var mu sync.Mutex
	var auths, ops []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string `json:"operationName"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		mu.Lock()
		auths = append(auths, r.Header.Get("Authorization"))
		ops = append(ops, body.OperationName)
		mu.Unlock()
		if body.OperationName == "AccountUsageAccounts" {
			_, _ = io.WriteString(w, acctsResponse(true, acctRupaliOrca1))
			return
		}
		_, _ = io.WriteString(w, `{"errors":[{"message":"unexpected operation `+body.OperationName+`"}]}`)
	}))
	t.Cleanup(srv.Close)
	t.Chdir(t.TempDir()) // no worktree binding
	f, out := testFactory(t)
	t.Setenv("HADRON_TOKEN", "hdr_app_key_1875")
	root := NewRootCmd(f)
	root.SetArgs([]string{"--server", srv.URL, "--app", "hrn:app:acme.com:eng-team", "team", "usage", "accounts", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("App-key caller refused by the CLI: %v (ops %v)", err, ops)
	}
	if len(ops) != 1 || ops[0] != "AccountUsageAccounts" {
		t.Fatalf("ops %v: the command must make exactly the one list call", ops)
	}
	if auths[0] != "Bearer hdr_app_key_1875" {
		t.Fatalf("authorization %q: the App key must be the caller", auths[0])
	}
	if !strings.Contains(squash1837(out.String()), `"account":"team-rupali"`) {
		t.Fatalf("out %s", out.String())
	}
}

// Acceptance 6: the document selects exactly the envelope and entry fields.
func TestUsageAccounts1875SelectsExactlyTheContractFields(t *testing.T) {
	src, err := os.ReadFile("../api/queries/usage_accounts.graphql")
	if err != nil {
		t.Fatal(err)
	}
	doc, perr := parser.ParseQuery(&ast.Source{Name: "usage_accounts.graphql", Input: string(src)})
	if perr != nil {
		t.Fatal(perr)
	}
	var op *ast.OperationDefinition
	for _, o := range doc.Operations {
		if o.Name == "AccountUsageAccounts" {
			op = o
		}
	}
	if op == nil || len(doc.Operations) != 1 || len(doc.Fragments) != 0 {
		t.Fatalf("want exactly one operation AccountUsageAccounts and no fragments")
	}
	var paths []string
	var walk func(prefix string, ss ast.SelectionSet)
	walk = func(prefix string, ss ast.SelectionSet) {
		for _, s := range ss {
			fld, ok := s.(*ast.Field)
			if !ok {
				t.Fatalf("non-field selection %T", s)
			}
			p := prefix + fld.Name
			paths = append(paths, p)
			walk(p+".", fld.SelectionSet)
		}
	}
	walk("", op.SelectionSet)
	sort.Strings(paths)
	want := []string{
		"accountUsageAccounts",
		"accountUsageAccounts.accounts",
		"accountUsageAccounts.accounts.account",
		"accountUsageAccounts.accounts.family",
		"accountUsageAccounts.accounts.host",
		"accountUsageAccounts.accounts.newestSnapshotAt",
		"accountUsageAccounts.accounts.workers",
		"accountUsageAccounts.accounts.workersConfigured",
		"accountUsageAccounts.asOf",
		"accountUsageAccounts.complete",
	}
	if strings.Join(paths, "\n") != strings.Join(want, "\n") {
		t.Fatalf("selection:\n%s\nwant:\n%s", strings.Join(paths, "\n"), strings.Join(want, "\n"))
	}
}

// Acceptance 7: the coverage matrix classifies the query as covered by this
// command.
func TestUsageAccounts1875CoverageMatrix(t *testing.T) {
	b, err := os.ReadFile("../api/coverage-matrix.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range strings.Split(string(b), "\n") {
		if f := strings.Fields(l); len(f) > 0 && f[0] == "accountUsageAccounts" {
			if strings.Join(f[1:], " ") != "command team usage accounts" {
				t.Fatalf("matrix line %q", l)
			}
			return
		}
	}
	t.Fatal("accountUsageAccounts missing from the coverage matrix")
}
