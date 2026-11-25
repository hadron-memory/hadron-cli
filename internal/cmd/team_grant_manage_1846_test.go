package cmd

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

// cli#1846: the person-manager grant verbs under `hadron team grant`: create,
// app-list, revoke, withdraw. Every pin is red on a main without them. None of
// the verbs refuses a caller locally (acceptance 19): the tests run with no
// worker binding and no org, and the server answers.

const managerGrant1846 = `{"id":"g1","appId":"capp1","action":"node.read","values":["one"],"holderKind":"WORKER","holderId":"wkr1","issuerUserId":"u-issuer","requestedByUserId":"u-req","requestedByWorkerId":null,"requestedBySessionId":null,"startsAt":null,"expiresAt":"exp","maxUses":2,"useCount":1,"reason":"why","createdAt":"created","activatedAt":"act","revokedAt":"first-end","revokedByUserId":"u-rev","holderDepartedAt":"gone-at","allows":false,"state":"HOLDER_GONE","futureServerField":"must not leak"}`

var managerFields1846 = []string{"id", "appId", "action", "values", "holderKind", "holderId", "issuerUserId", "requestedByUserId", "requestedByWorkerId", "requestedBySessionId",
	"startsAt", "expiresAt", "maxUses", "useCount", "reason", "createdAt", "activatedAt", "revokedAt", "revokedByUserId", "holderDepartedAt", "allows", "state"}

func gqlRefusal1846(code string) string {
	return `{"errors":[{"message":"refused","extensions":{"code":"` + code + `"}}],"data":null}`
}

// runManage1846 runs one verb with no binding: the manager arm is a person's.
func runManage1846(t *testing.T, responses map[string]string, argv ...string) (*[]attnCall, string, string, error) {
	t.Helper()
	srv, calls := attnServer(t, responses)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"--server", srv.URL}, argv...))
	err := root.Execute()
	return calls, out.String(), f.IOStreams.ErrOut.(*strings.Builder).String(), err
}

// sent1846 fails the test when the verb sent nothing (a command that does not
// exist prints help and exits 0, which must not read as a pass).
func sent1846(t *testing.T, calls *[]attnCall) attnCall {
	t.Helper()
	if len(*calls) == 0 {
		t.Fatal("no request was sent")
	}
	return (*calls)[0]
}

func selectionFields1846(t *testing.T, query, root string) []string {
	t.Helper()
	doc, err := parser.ParseQuery(&ast.Source{Input: query})
	if err != nil {
		t.Fatal(err)
	}
	var walk func(set ast.SelectionSet) []string
	walk = func(set ast.SelectionSet) []string {
		var out []string
		for _, s := range set {
			switch s := s.(type) {
			case *ast.Field:
				out = append(out, s.Name)
			case *ast.FragmentSpread:
				out = append(out, walk(doc.Fragments.ForName(s.Name).SelectionSet)...)
			}
		}
		return out
	}
	top := doc.Operations[0].SelectionSet
	if len(top) != 1 || top[0].(*ast.Field).Name != root {
		t.Fatalf("root field: %v", top)
	}
	inner := top[0].(*ast.Field).SelectionSet
	if root == "appGrants" { // items { ... } under the page
		for _, s := range inner {
			if f, ok := s.(*ast.Field); ok && f.Name == "items" {
				inner = f.SelectionSet
			}
		}
	}
	got := walk(inner)
	sort.Strings(got)
	return got
}

func createArgs1846(extra ...string) []string {
	return append([]string{"team", "grant", "create", "--app", "acme.com:eng", "--action", "node.read", "--values", `{"node":["n1"]}`,
		"--reason", "need it", "--holder", "worker", "--holder-id", "wkr1"}, extra...)
}

// Acceptance 1, 17: create sends createGrant with input.appRef and only the flags
// given, prints the grant with state and allows, and the selection is pinned.
func TestGrantManageCreate1846(t *testing.T) {
	calls, out, _, err := runManage1846(t, map[string]string{"TeamCreateGrant": `{"data":{"createGrant":` + managerGrant1846 + `}}`}, createArgs1846("--json")...)
	if err != nil {
		t.Fatal(err)
	}
	if c0 := sent1846(t, calls); len(*calls) != 1 || c0.Op != "TeamCreateGrant" || c0.Session != "" {
		t.Fatalf("calls %+v", *calls)
	}
	var vars map[string]json.RawMessage
	_ = json.Unmarshal((*calls)[0].RawVars, &vars)
	var in map[string]json.RawMessage
	_ = json.Unmarshal(vars["input"], &in)
	keys := []string{}
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if !reflect.DeepEqual(keys, []string{"action", "appRef", "holderKind", "holderRef", "reason", "values"}) {
		t.Fatalf("input keys %v (unset optionals must be omitted, not null)", keys)
	}
	if string(in["appRef"]) != `"acme.com:eng"` || string(in["holderRef"]) != `"wkr1"` || string(in["holderKind"]) != `"WORKER"` {
		t.Fatalf("input %v", in)
	}
	if got := selectionFields1846(t, (*calls)[0].Query, "createGrant"); !reflect.DeepEqual(got, sortedCopy1846(managerFields1846)) {
		t.Fatalf("createGrant selection %v", got)
	}
	var dto map[string]any
	if e := json.Unmarshal([]byte(out), &dto); e != nil {
		t.Fatal(e, out)
	}
	if dto["state"] != "HOLDER_GONE" || dto["allows"] != false || dto["holderDepartedAt"] != "gone-at" || dto["revokedByUserId"] != "u-rev" {
		t.Fatalf("DTO %s", out)
	}
	if _, leaked := dto["futureServerField"]; leaked {
		t.Fatal("an unknown server field leaked into the DTO")
	}
}

func sortedCopy1846(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func TestGrantManageCreateOptionalFlagsAreSent1846(t *testing.T) {
	calls, _, _, err := runManage1846(t, map[string]string{"TeamCreateGrant": `{"data":{"createGrant":` + managerGrant1846 + `}}`},
		createArgs1846("--starts-at", "2026-01-01T00:00:00Z", "--expires-at", "2026-01-02T00:00:00Z", "--max-uses", "3")...)
	if err != nil {
		t.Fatal(err)
	}
	sent1846(t, calls)
	for _, want := range []string{`"startsAt":"2026-01-01T00:00:00Z"`, `"expiresAt":"2026-01-02T00:00:00Z"`, `"maxUses":3`} {
		if !strings.Contains(string((*calls)[0].RawVars), want) {
			t.Errorf("request lacks %s: %s", want, (*calls)[0].RawVars)
		}
	}
}

// Acceptance 4: a missing --app or --holder-id, or an empty optional flag, is
// refused before any request.
func TestGrantManageCreateUsageRefusals1846(t *testing.T) {
	base := func(drop string) []string {
		out := []string{"team", "grant", "create"}
		all := map[string]string{"--app": "acme.com:eng", "--action": "node.read", "--values": `{"a":1}`, "--reason": "r", "--holder": "worker", "--holder-id": "wkr1"}
		for k, v := range all {
			if k != drop {
				out = append(out, k, v)
			}
		}
		return out
	}
	cases := map[string][]string{
		"no --app":             base("--app"),
		"no --holder-id":       base("--holder-id"),
		"empty --starts-at":    append(base(""), "--starts-at", " "),
		"empty --expires-at":   append(base(""), "--expires-at", ""),
		"unknown --holder":     append(base("--holder"), "--holder", "person"),
		"values not an object": append(base("--values"), "--values", "{"),
	}
	for name, argv := range cases {
		t.Run(name, func(t *testing.T) {
			calls, _, _, err := runManage1846(t, map[string]string{}, argv...)
			if exitCodeFor(err) != exitcode.Usage || len(*calls) != 0 {
				t.Fatalf("exit %d calls %v err %v", exitCodeFor(err), *calls, err)
			}
			if strings.Contains(err.Error(), "unknown") {
				t.Fatalf("the verb does not exist yet (the refusal is the parser's): %v", err)
			}
		})
	}
}

// Acceptance 2, 3, 5, 14, 16: typed refusals keep their codes and exits; none
// echoes a submitted value.
func TestGrantManageRefusalCodes1846(t *testing.T) {
	cases := []struct {
		name, op, code string
		argv           []string
		exit           int
	}{
		{"create not manager", "TeamCreateGrant", "GRANT_NOT_MANAGER", createArgs1846(), exitcode.Forbidden},
		{"create holder not allowed", "TeamCreateGrant", "GRANT_HOLDER_NOT_ALLOWED", createArgs1846(), exitcode.Forbidden},
		{"create action unknown", "TeamCreateGrant", "GRANT_ACTION_UNKNOWN", createArgs1846(), exitcode.Usage},
		{"create values invalid", "TeamCreateGrant", "GRANT_VALUES_INVALID", createArgs1846(), exitcode.Usage},
		{"create terms invalid", "TeamCreateGrant", "GRANT_TERMS_INVALID", createArgs1846(), exitcode.Usage},
		{"revoke not manager", "TeamRevokeGrant", "GRANT_NOT_MANAGER", []string{"team", "grant", "revoke", "g1"}, exitcode.Forbidden},
		{"withdraw not found", "TeamWithdrawGrant", "GRANT_NOT_FOUND", []string{"team", "grant", "withdraw", "g1"}, exitcode.NotFound},
		{"withdraw not requester", "TeamWithdrawGrant", "GRANT_NOT_REQUESTER", []string{"team", "grant", "withdraw", "g1"}, exitcode.Forbidden},
		{"withdraw not pending", "TeamWithdrawGrant", "GRANT_NOT_PENDING", []string{"team", "grant", "withdraw", "g1"}, exitcode.Conflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, out, _, err := runManage1846(t, map[string]string{tc.op: gqlRefusal1846(tc.code)}, tc.argv...)
			if exitCodeFor(err) != tc.exit {
				t.Fatalf("exit %d, want %d (%v)", exitCodeFor(err), tc.exit, err)
			}
			if strings.TrimSpace(out) != "" {
				t.Fatalf("a refusal printed a grant: %q", out)
			}
			if strings.Contains(err.Error(), "node.read") || strings.Contains(err.Error(), "need it") {
				t.Fatalf("the refusal echoes a submitted value: %v", err)
			}
		})
	}
}

// Acceptance 5: a server without signing (field absent) is the existing refusal.
func TestGrantManageCreateSigningOff1846(t *testing.T) {
	_, _, _, err := runManage1846(t, map[string]string{"TeamCreateGrant": `{"errors":[{"message":"Cannot query field \"createGrant\" on type \"Mutation\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`}, createArgs1846()...)
	if exitCodeFor(err) != exitcode.Usage || !strings.Contains(err.Error(), "does not offer grant requests and grant signing") {
		t.Fatalf("exit %d: %v", exitCodeFor(err), err)
	}
}

// Acceptance 6, 7, 8, 17: app-list sends appGrants with the full selection,
// prints state and allows, and an empty page never says "no grants".
func TestGrantManageAppList1846(t *testing.T) {
	page := `{"data":{"appGrants":{"total":1,"items":[` + managerGrant1846 + `]}}}`
	calls, out, _, err := runManage1846(t, map[string]string{"TeamAppGrants": page}, "team", "grant", "app-list", "acme.com:eng", "--limit", "5", "--offset", "10")
	if err != nil {
		t.Fatal(err)
	}
	c := sent1846(t, calls)
	if c.Vars["appRef"] != "acme.com:eng" || c.Vars["limit"] != float64(5) || c.Vars["offset"] != float64(10) {
		t.Fatalf("vars %v", c.Vars)
	}
	if got := selectionFields1846(t, c.Query, "appGrants"); !reflect.DeepEqual(got, sortedCopy1846(managerFields1846)) {
		t.Fatalf("appGrants selection %v", got)
	}
	for _, want := range []string{"HOLDER_GONE", "gone-at", "u-rev", "u-issuer"} {
		if !strings.Contains(out, want) {
			t.Errorf("text lacks %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "total: 1") {
		t.Errorf("no total line:\n%s", out)
	}
}

func TestGrantManageAppListLimitZeroCountsOnly1846(t *testing.T) {
	_, out, _, err := runManage1846(t, map[string]string{"TeamAppGrants": `{"data":{"appGrants":{"total":7,"items":[]}}}`}, "team", "grant", "app-list", "acme.com:eng", "--limit", "0", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	_ = json.Unmarshal([]byte(out), &got)
	if got["total"] != float64(7) || len(got) != 1 {
		t.Fatalf("count-only JSON %s", out)
	}
}

func TestGrantManageAppListEmptyPageIsNotNoGrants1846(t *testing.T) {
	empty := map[string]string{"TeamAppGrants": `{"data":{"appGrants":{"total":0,"items":[]}}}`}
	_, out, _, err := runManage1846(t, empty, "team", "grant", "app-list", "acme.com:eng")
	if err != nil {
		t.Fatal(err)
	}
	low := strings.ToLower(out)
	if strings.Contains(low, "no grants") || !strings.Contains(low, "not be authorised") && !strings.Contains(low, "not be authorized") && !strings.Contains(low, "may not be authori") {
		t.Fatalf("an empty page must say the caller may not be authorised, never 'no grants': %q", out)
	}
	_, js, _, err := runManage1846(t, empty, "team", "grant", "app-list", "acme.com:eng", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	_ = json.Unmarshal([]byte(js), &got)
	if got["total"] != float64(0) || !reflect.DeepEqual(got["items"], []any{}) || len(got) != 2 {
		t.Fatalf("--json must be the bare page: %s", js)
	}
}

// Acceptance 9, 10, 12, 17: revoke sends revokeGrant(ref) with no instruction key;
// --instruction sends the claim; a bad claim is refused before any call; an
// already-ended grant is printed as returned, not as a new act.
func TestGrantManageRevoke1846(t *testing.T) {
	resp := map[string]string{"TeamRevokeGrant": `{"data":{"revokeGrant":` + managerGrant1846 + `}}`, "TeamRevokeGrantInstructed": `{"data":{"revokeGrant":` + managerGrant1846 + `}}`}
	calls, out, _, err := runManage1846(t, resp, "team", "grant", "revoke", "g1")
	if err != nil {
		t.Fatal(err)
	}
	c := sent1846(t, calls)
	if c.Op != "TeamRevokeGrant" || c.Vars["ref"] != "g1" || strings.Contains(string(c.RawVars), "instruction") {
		t.Fatalf("call %+v", c)
	}
	if got := selectionFields1846(t, c.Query, "revokeGrant"); !reflect.DeepEqual(got, sortedCopy1846(managerFields1846)) {
		t.Fatalf("revokeGrant selection %v", got)
	}
	// Acceptance 12: the grant is shown as returned, with its first end; no line
	// announces a new revocation.
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		name, _, ok := strings.Cut(line, ": ")
		if !ok || !slicesContains1846(managerFields1846, name) {
			t.Errorf("output line %q is not a returned grant field", line)
		}
	}
	if !strings.Contains(out, "revokedAt: first-end") {
		t.Errorf("the returned revokedAt is not printed:\n%s", out)
	}
	calls, _, _, err = runManage1846(t, resp, "team", "grant", "revoke", "g1", "--instruction", "task:hrn:agenttask:acme.com:eng:at1")
	if err != nil {
		t.Fatal(err)
	}
	c = sent1846(t, calls)
	claim, _ := c.Vars["instruction"].(map[string]any)
	if c.Op != "TeamRevokeGrantInstructed" || claim["ref"] != "hrn:agenttask:acme.com:eng:at1" || claim["kind"] == nil {
		t.Fatalf("instructed call %+v", c)
	}
	for _, bad := range []string{"task", "unknown:ref", "task: "} {
		calls, _, _, err = runManage1846(t, resp, "team", "grant", "revoke", "g1", "--instruction", bad)
		if exitCodeFor(err) != exitcode.Usage || len(*calls) != 0 || err == nil || strings.Contains(err.Error(), "unknown") {
			t.Errorf("--instruction %q: exit %d calls %d err %v", bad, exitCodeFor(err), len(*calls), err)
		}
	}
}

func slicesContains1846(in []string, s string) bool {
	for _, v := range in {
		if v == s {
			return true
		}
	}
	return false
}

// Acceptance 13, 15, 17: withdraw sends withdrawGrant(ref) and prints the grant
// as returned; the help says it withdraws the GRANT, not a signing request.
func TestGrantManageWithdraw1846(t *testing.T) {
	calls, out, _, err := runManage1846(t, map[string]string{"TeamWithdrawGrant": `{"data":{"withdrawGrant":` + managerGrant1846 + `}}`}, "team", "grant", "withdraw", "g1")
	if err != nil {
		t.Fatal(err)
	}
	c := sent1846(t, calls)
	if c.Op != "TeamWithdrawGrant" || c.Vars["ref"] != "g1" || len(c.Vars) != 1 {
		t.Fatalf("call %+v", c)
	}
	if got := selectionFields1846(t, c.Query, "withdrawGrant"); !reflect.DeepEqual(got, sortedCopy1846(managerFields1846)) {
		t.Fatalf("withdrawGrant selection %v", got)
	}
	if !strings.Contains(out, "state: HOLDER_GONE") || !strings.Contains(out, "allows: false") {
		t.Errorf("state and allows are printed as returned:\n%s", out)
	}
	f, helpOut := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "grant", "withdraw", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	help := strings.Join(strings.Fields(helpOut.String()), " ")
	if !strings.Contains(help, "withdraws the GRANT") || !strings.Contains(help, "signing-request withdraw") {
		t.Errorf("withdraw help must say it withdraws the GRANT, not a signing request:\n%s", help)
	}
}

// Acceptance 17: no manager verb asks for a signature, and nothing activates.
func TestGrantManageSendsNoSigningOperation1846(t *testing.T) {
	g := `{"data":{"createGrant":` + managerGrant1846 + `}}`
	calls, _, _, err := runManage1846(t, map[string]string{"TeamCreateGrant": g}, createArgs1846()...)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range *calls {
		if strings.Contains(c.Op, "Signature") || strings.Contains(c.Query, "requestGrantSignature") {
			t.Fatalf("a signing operation was sent: %s", c.Op)
		}
	}
}

// Worker reads and the manager's agree: list and get select and print state.
func TestGrantManageWorkerReadsCarryState1846(t *testing.T) {
	item := strings.Replace(workerGrant1832, `"allows":true`, `"allows":true,"state":"ACTIVE"`, 1)
	for _, tc := range []struct {
		op   string
		resp string
		argv []string
		root string
	}{
		{"TeamGrants", `{"data":{"grants":{"items":[` + item + `],"total":1}}}`, []string{"team", "grant", "list"}, "grants"},
		{"TeamGrant", `{"data":{"grant":` + item + `}}`, []string{"team", "grant", "get", "g1"}, "grant"},
	} {
		t.Run(tc.op, func(t *testing.T) {
			calls, out, err := runTeamTask(t, map[string]string{tc.op: tc.resp}, tc.argv...)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains((*calls)[len(*calls)-1].Query, "state") {
				t.Errorf("%s does not select state", tc.op)
			}
			if !strings.Contains(out, "ACTIVE") || !strings.Contains(out, "yes") {
				t.Errorf("state and allows are printed side by side:\n%s", out)
			}
		})
	}
}

// Wolfgang's note: the worker read lists a role agent's grants too.
func TestGrantListHelpSaysRoleAgentGrantsAreListed1846(t *testing.T) {
	for _, verb := range []string{"list", "get"} {
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"team", "grant", verb, "--help"})
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		help := strings.Join(strings.Fields(out.String()), " ")
		if strings.Contains(help, "Grants held by a role agent, another worker, a person or an App are not listed") {
			t.Errorf("%s: the stale 'role agent grants are not listed' sentence is still there", verb)
		}
		if !strings.Contains(help, "role agent") || !strings.Contains(help, "another worker's, a person's or an App's are not") {
			t.Errorf("%s: help must say role-agent grants are listed and another worker's, a person's or an App's are not:\n%s", verb, help)
		}
	}
}
