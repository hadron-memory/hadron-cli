package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

// cli#1868: the bound worker's grant request verbs (request,
// request-signature, signing-request get|withdraw). Every pin here is red on a
// main without them.

const ceremony1868 = "https://hadronmemory.com/grant-signing/sr1?a=b&c=%2Fd/é" + "/long" // verbatim, unsafe-looking but legal

func signingRequest1868(url string) string {
	u, _ := json.Marshal(url)
	return `{"id":"sr1","grantId":"g1","version":2,"termsHash":"h1","payloadVersion":1,"state":"OPEN",` +
		`"requestedBy":{"userId":"u1","appId":"capp100000000000000000000","workerId":"wkr1","sessionId":"s-new"},` +
		`"createdAt":"c","expiresAt":"e","endedAt":null,"ceremonyUrl":` + string(u) + `,"futureServerField":"x"}`
}

var signingRequestFields1868 = []string{"id", "grantId", "version", "termsHash", "payloadVersion", "state", "requestedBy", "createdAt", "expiresAt", "endedAt", "ceremonyUrl"}

type grantVerb1868 struct {
	name string
	op   string // operation name
	root string // schema root field
	args []string
	resp func(url string) string
}

func grantVerbs1868() []grantVerb1868 {
	sr := func(root string) func(string) string {
		return func(u string) string { return `{"data":{"` + root + `":` + signingRequest1868(u) + `}}` }
	}
	return []grantVerb1868{
		{"request", "TeamCreateGrant", "createGrant", []string{"team", "grant", "request", "--action", "node.read", "--values", `{"node":["n1"]}`, "--reason", "need it"},
			func(string) string { return `{"data":{"createGrant":` + workerGrant1832 + `}}` }},
		{"request-signature", "TeamRequestGrantSignature", "requestGrantSignature", []string{"team", "grant", "request-signature", "g1"}, sr("requestGrantSignature")},
		{"withdraw", "TeamWithdrawGrantSigningRequest", "withdrawGrantSigningRequest", []string{"team", "grant", "signing-request", "withdraw", "sr1"}, sr("withdrawGrantSigningRequest")},
		{"get", "TeamGrantSigningRequest", "grantSigningRequest", []string{"team", "grant", "signing-request", "get", "sr1"}, sr("grantSigningRequest")},
	}
}

func findCall1868(calls *[]attnCall, op string) *attnCall {
	for i := range *calls {
		if (*calls)[i].Op == op {
			return &(*calls)[i]
		}
	}
	return nil
}

// grantOps1868 counts only the verbs' own operations (an identity read is not one).
func grantOps1868(calls *[]attnCall) int {
	n := 0
	for _, c := range *calls {
		switch c.Op {
		case "TeamCreateGrant", "TeamRequestGrantSignature", "TeamWithdrawGrantSigningRequest", "TeamGrantSigningRequest":
			n++
		}
	}
	return n
}

// selection1868 returns the sorted field names selected under the root field,
// following fragment spreads, and the nested requestedBy names.
func selection1868(t *testing.T, query, root string) (fields, requestedBy []string) {
	t.Helper()
	doc, err := parser.ParseQuery(&ast.Source{Input: query})
	if err != nil {
		t.Fatal(err)
	}
	var walk func(set ast.SelectionSet) []*ast.Field
	walk = func(set ast.SelectionSet) []*ast.Field {
		var out []*ast.Field
		for _, s := range set {
			switch s := s.(type) {
			case *ast.Field:
				out = append(out, s)
			case *ast.FragmentSpread:
				out = append(out, walk(doc.Fragments.ForName(s.Name).SelectionSet)...)
			case *ast.InlineFragment:
				out = append(out, walk(s.SelectionSet)...)
			}
		}
		return out
	}
	rootField := walk(doc.Operations[0].SelectionSet)
	if len(rootField) != 1 || rootField[0].Name != root {
		t.Fatalf("root fields: %v", rootField)
	}
	for _, f := range walk(rootField[0].SelectionSet) {
		fields = append(fields, f.Name)
		if f.Name == "requestedBy" {
			for _, n := range walk(f.SelectionSet) {
				requestedBy = append(requestedBy, n.Name)
			}
		}
	}
	sort.Strings(fields)
	sort.Strings(requestedBy)
	return
}

func sorted1868(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func inputKeys1868(t *testing.T, c *attnCall) map[string]json.RawMessage {
	t.Helper()
	if c == nil {
		t.Fatal("operation was never sent")
	}
	var vars map[string]json.RawMessage
	if err := json.Unmarshal(c.RawVars, &vars); err != nil {
		t.Fatal(err)
	}
	var in map[string]json.RawMessage
	if err := json.Unmarshal(vars["input"], &in); err != nil {
		t.Fatal(err)
	}
	return in
}

// P1/P3/P4: one createGrant, bound session, optional keys omitted, 14-field
// selection, DTO not a generated struct.
func TestGrantRequest1868CreateSendsOnlyWhatIsSet(t *testing.T) {
	v := grantVerbs1868()[0]
	calls, out, err := runTeamTask(t, map[string]string{v.op: v.resp("")}, append(v.args, "--json")...)
	if err != nil {
		t.Fatal(err)
	}
	c := findCall1868(calls, v.op)
	if c == nil || len(*calls) != 1 || c.Session != "s-new" {
		t.Fatalf("calls %+v", *calls)
	}
	in := inputKeys1868(t, c)
	keys := []string{}
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if !reflect.DeepEqual(keys, []string{"action", "holderKind", "holderRef", "reason", "values"}) {
		t.Fatalf("input keys %v (unset optionals must be omitted, not null)", keys)
	}
	if string(in["holderKind"]) != `"WORKER"` || string(in["holderRef"]) != `"wkr1"` || string(in["values"]) != `{"node":["n1"]}` {
		t.Fatalf("input %v", in)
	}
	checkGrantSelection1832(t, c.Query, "createGrant")
	var got, want map[string]any
	if e := json.Unmarshal([]byte(out), &got); e != nil {
		t.Fatal(e, out)
	}
	_ = json.Unmarshal([]byte(workerGrant1832), &want)
	delete(want, "futureServerField")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DTO %s", out)
	}
}

// P2
func TestGrantRequest1868CreateOptionalFlagsAreSent(t *testing.T) {
	v := grantVerbs1868()[0]
	args := append(append([]string{}, v.args...), "--starts-at", "2026-01-01T00:00:00Z", "--expires-at", "2026-01-02T00:00:00Z", "--max-uses", "3")
	calls, _, err := runTeamTask(t, map[string]string{v.op: v.resp("")}, args...)
	if err != nil {
		t.Fatal(err)
	}
	in := inputKeys1868(t, findCall1868(calls, v.op))
	if string(in["startsAt"]) != `"2026-01-01T00:00:00Z"` || string(in["expiresAt"]) != `"2026-01-02T00:00:00Z"` || string(in["maxUses"]) != `3` {
		t.Fatalf("input %v", in)
	}
}

// P15
func TestGrantRequest1868HolderResolution(t *testing.T) {
	v := grantVerbs1868()[0]
	cases := []struct {
		name     string
		extra    []string
		kind, id string
	}{
		{"default worker", nil, `"WORKER"`, `"wkr1"`},
		{"agent from binding", []string{"--holder", "agent"}, `"AGENT"`, `"agt1"`},
		{"override id", []string{"--holder-id", "other"}, `"WORKER"`, `"other"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls, _, err := runTeamTask(t, map[string]string{v.op: v.resp("")}, append(append([]string{}, v.args...), tc.extra...)...)
			if err != nil {
				t.Fatal(err)
			}
			in := inputKeys1868(t, findCall1868(calls, v.op))
			if string(in["holderKind"]) != tc.kind || string(in["holderRef"]) != tc.id {
				t.Fatalf("input %v", in)
			}
		})
	}
	t.Run("empty AgentID without --holder-id", func(t *testing.T) {
		writeTeamBinding(t)
		rewriteBinding1592(t, func(b map[string]any) { delete(b, "agentId") })
		srv, calls := attnServer(t, map[string]string{v.op: v.resp("")})
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append(append([]string{"--server", srv.URL}, v.args...), "--holder", "agent"))
		if code := exitCodeFor(root.Execute()); code != exitcode.Usage || len(*calls) != 0 {
			t.Fatalf("exit %d calls %v", code, *calls)
		}
	})
}

// P3: the signing-request operations select exactly the fragment's fields.
func TestGrantRequest1868SigningRequestSelection(t *testing.T) {
	for _, v := range grantVerbs1868()[1:] {
		t.Run(v.name, func(t *testing.T) {
			calls, _, err := runTeamTask(t, map[string]string{v.op: v.resp(ceremony1868)}, v.args...)
			if err != nil {
				t.Fatal(err)
			}
			c := findCall1868(calls, v.op)
			if c == nil {
				t.Fatal("operation was never sent")
			}
			fields, rb := selection1868(t, c.Query, v.root)
			if !reflect.DeepEqual(fields, sorted1868(signingRequestFields1868)) || !reflect.DeepEqual(rb, sorted1868([]string{"userId", "appId", "workerId", "sessionId"})) {
				t.Fatalf("selection %v / %v", fields, rb)
			}
			if c.Session != "s-new" {
				t.Fatalf("session %q", c.Session)
			}
		})
	}
}

// P5(i): the URL is verbatim in JSON and text; the DTO drops unknown fields and keeps nulls.
func TestGrantRequest1868CeremonyURLVerbatim(t *testing.T) {
	long := ceremony1868 + "/" + strings.Repeat("x", 300)
	for _, v := range grantVerbs1868()[1:] {
		t.Run(v.name, func(t *testing.T) {
			_, out, err := runTeamTask(t, map[string]string{v.op: v.resp(long)}, append(append([]string{}, v.args...), "--json")...)
			if err != nil {
				t.Fatal(err)
			}
			var dto map[string]any
			if e := json.Unmarshal([]byte(out), &dto); e != nil {
				t.Fatal(e, out)
			}
			if dto["ceremonyUrl"] != long || dto["state"] != "OPEN" || dto["endedAt"] != nil {
				t.Fatalf("json %s", out)
			}
			if _, leaked := dto["futureServerField"]; leaked {
				t.Fatal("generated/unknown field leaked into the DTO")
			}
			_, text, err := runTeamTask(t, map[string]string{v.op: v.resp(long)}, v.args...)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(text, "\n"+long+"\n") && !strings.HasSuffix(strings.TrimRight(text, "\n"), long) && !strings.Contains(text, ": "+long+"\n") {
				t.Fatalf("text did not carry the URL byte-for-byte on one line: %q", text)
			}
		})
	}
}

// P5(ii): each refused class refuses the text render and leaves JSON alone.
func TestGrantRequest1868UnsafeURLRefusesTextOnly(t *testing.T) {
	classes := map[string]string{
		"C0 ESC": "https://h/\x1b[31m", "C0 LF": "https://h/a\nb", "DEL": "https://h/\x7f",
		"C1 NEL": "https://h/\u0085", "C1 CSI": "https://h/\u009b", "Cf LRM": "https://h/\u200e",
		"Cf RLO": "https://h/\u202e", "Zl": "https://h/\u2028", "Zp": "https://h/\u2029",
	}
	v := grantVerbs1868()[1]
	for name, url := range classes {
		t.Run(name, func(t *testing.T) {
			resp := map[string]string{v.op: v.resp(url)}
			_, out, err := runTeamTask(t, resp, v.args...)
			if exitCodeFor(err) != exitcode.Error || strings.TrimSpace(out) != "" || !strings.Contains(err.Error(), "--json") {
				t.Fatalf("text: exit %d out %q err %v", exitCodeFor(err), out, err)
			}
			_, out, err = runTeamTask(t, resp, append(append([]string{}, v.args...), "--json")...)
			if err != nil {
				t.Fatal(err)
			}
			var dto map[string]any
			if e := json.Unmarshal([]byte(out), &dto); e != nil || dto["ceremonyUrl"] != url {
				t.Fatalf("json altered: %v %s", e, out)
			}
		})
	}
}

// P6: no CLI-derived grant state word; the request's own state is verbatim.
func TestGrantRequest1868NoDerivedGrantState(t *testing.T) {
	v := grantVerbs1868()[0]
	_, out, err := runTeamTask(t, map[string]string{v.op: v.resp("")}, v.args...)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"status:", "state:", "pending", "withdrawn", "revoked:  "} {
		if strings.Contains(out, w) {
			t.Fatalf("derived state word %q in %q", w, out)
		}
	}
	for _, v := range grantVerbs1868()[1:] {
		_, out, err := runTeamTask(t, map[string]string{v.op: v.resp(ceremony1868)}, v.args...)
		if err != nil || !strings.Contains(out, "OPEN") {
			t.Fatalf("%s: %v %q", v.name, err, out)
		}
	}
}

// P7: typed refusals exit per the issue's table and carry serverCode.
func TestGrantRequest1868RefusalExits(t *testing.T) {
	cases := []struct {
		verb int
		code string
		want int
	}{
		{0, "GRANT_REQUESTER_NOT_WORKER", exitcode.Forbidden}, {0, "GRANT_HOLDER_NOT_ALLOWED", exitcode.Forbidden},
		{0, "GRANT_ACTION_UNKNOWN", exitcode.Usage}, {0, "GRANT_VALUES_INVALID", exitcode.Usage}, {0, "GRANT_TERMS_INVALID", exitcode.Usage},
		{1, "GRANT_NOT_PENDING", exitcode.Conflict}, {1, "GRANT_EXPIRED", exitcode.Conflict}, {1, "GRANT_TERMS_UNREPRESENTABLE", exitcode.Conflict},
		{1, "GRANT_NOT_FOUND", exitcode.NotFound}, {1, "FORBIDDEN", exitcode.Forbidden},
		{2, "SIGNING_REQUEST_NOT_FOUND", exitcode.NotFound}, {2, "SIGNING_REQUEST_NOT_REQUESTER", exitcode.Forbidden}, {2, "SIGNING_REQUEST_ENDED", exitcode.Conflict},
		{3, "SIGNING_REQUEST_NOT_FOUND", exitcode.NotFound},
	}
	for _, tc := range cases {
		v := grantVerbs1868()[tc.verb]
		t.Run(v.name+"/"+tc.code, func(t *testing.T) {
			_, _, err := runTeamTask(t, map[string]string{v.op: gqlErrorJSON(tc.code)}, v.args...)
			if exitCodeFor(err) != tc.want || err == nil || !strings.Contains(err.Error(), "refused: "+tc.code) {
				t.Fatalf("exit %d err %v, want %d", exitCodeFor(err), err, tc.want)
			}
			_, out, err := runTeamTask(t, map[string]string{v.op: gqlErrorJSON(tc.code)}, append(append([]string{}, v.args...), "--json")...)
			if exitCodeFor(err) != tc.want || !strings.Contains(out+errString1868(err), tc.code) {
				t.Fatalf("json: exit %d out %q err %v", exitCodeFor(err), out, err)
			}
		})
	}
}

func errString1868(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// P9/P10: a server without signing says so (exit 2, no "upgrade"); a mixed envelope is not swallowed.
func TestGrantRequest1868SigningOffServer(t *testing.T) {
	envelope := func(extra string) string {
		return `{"errors":[{"message":"Unknown type \"CreateGrantInput\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}},` +
			`{"message":"Cannot query field \"createGrant\" on type \"Mutation\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}` + extra + `]}`
	}
	names := map[string]string{"TeamCreateGrant": "createGrant", "TeamRequestGrantSignature": "requestGrantSignature",
		"TeamWithdrawGrantSigningRequest": "withdrawGrantSigningRequest", "TeamGrantSigningRequest": "grantSigningRequest"}
	for _, v := range grantVerbs1868() {
		t.Run(v.name, func(t *testing.T) {
			resp := envelope("")
			if v.op != "TeamCreateGrant" {
				resp = missingFieldJSON(names[v.op])
			}
			_, _, err := runTeamTask(t, map[string]string{v.op: resp}, v.args...)
			if exitCodeFor(err) != exitcode.Usage || err == nil || !strings.Contains(err.Error(), "does not offer") || strings.Contains(err.Error(), "upgrade") {
				t.Fatalf("exit %d err %v", exitCodeFor(err), err)
			}
		})
	}
	t.Run("mixed envelope", func(t *testing.T) {
		v := grantVerbs1868()[0]
		mixed := envelope(`,{"message":"refused","extensions":{"code":"FORBIDDEN"}}`)
		_, _, err := runTeamTask(t, map[string]string{v.op: mixed}, v.args...)
		if err == nil || strings.Contains(err.Error(), "does not offer") {
			t.Fatalf("mixed envelope swallowed: %v", err)
		}
	})
}

func unboundRun1868(t *testing.T, responses map[string]string, argv ...string) (*[]attnCall, string, string, error) {
	t.Helper()
	dir := teamGitDir(t)
	_ = os.Remove(filepath.Join(dir, "hadron-team-session.json"))
	srv, calls := attnServer(t, responses)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	var errOut strings.Builder
	root.SetErr(&errOut)
	root.SetArgs(append([]string{"--server", srv.URL}, argv...))
	err := root.Execute()
	return calls, out.String(), errOut.String(), err
}

// P11: no binding is not a local refusal: one call, no session, a stderr note,
// and the server's own answer.
func TestGrantRequest1868UnboundPassesThrough(t *testing.T) {
	answers := map[string]struct {
		code string
		want int
	}{"TeamCreateGrant": {"GRANT_REQUESTER_NOT_WORKER", exitcode.Forbidden}, "TeamRequestGrantSignature": {"GRANT_NOT_FOUND", exitcode.NotFound},
		"TeamWithdrawGrantSigningRequest": {"SIGNING_REQUEST_NOT_FOUND", exitcode.NotFound}, "TeamGrantSigningRequest": {"SIGNING_REQUEST_NOT_FOUND", exitcode.NotFound}}
	for _, v := range grantVerbs1868() {
		t.Run(v.name, func(t *testing.T) {
			a := answers[v.op]
			args := append([]string{}, v.args...)
			if v.op == "TeamCreateGrant" {
				args = append(args, "--holder-id", "wkr1")
			}
			calls, _, stderr, err := unboundRun1868(t, map[string]string{v.op: gqlErrorJSON(a.code)}, args...)
			if exitCodeFor(err) != a.want || len(*calls) != 1 || (*calls)[0].Op != v.op || (*calls)[0].Session != "" {
				t.Fatalf("exit %d calls %+v err %v", exitCodeFor(err), *calls, err)
			}
			if !strings.Contains(stderr+errString1868(err), "no worker binding in this worktree; sent without a worker session") {
				t.Fatalf("no stderr note: %q", stderr)
			}
		})
	}
	t.Run("request unbound without a holder id is an input refusal", func(t *testing.T) {
		v := grantVerbs1868()[0]
		calls, _, _, err := unboundRun1868(t, map[string]string{v.op: v.resp("")}, v.args...)
		if exitCodeFor(err) != exitcode.Usage || len(*calls) != 0 {
			t.Fatalf("exit %d calls %v", exitCodeFor(err), *calls)
		}
	})
}

// P12: a binding never degrades to the unbound pass-through.
func TestGrantRequest1868BindingGuards(t *testing.T) {
	for _, v := range grantVerbs1868() {
		t.Run(v.name+"/other server", func(t *testing.T) {
			writeTeamBinding(t)
			setTeamBindingServer(t, "http://elsewhere.invalid")
			srv, calls := attnServer(t, map[string]string{v.op: v.resp(ceremony1868)})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append([]string{"--server", srv.URL}, v.args...))
			if code := exitCodeFor(root.Execute()); code != exitcode.Usage || len(*calls) != 0 {
				t.Fatalf("exit %d calls %v", code, *calls)
			}
		})
		t.Run(v.name+"/other app", func(t *testing.T) {
			resp := map[string]string{v.op: v.resp(ceremony1868), "TeamAppIdentity": `{"data":{"app":{"id":"capp999999999999999999999","urn":"hrn:app:acme.com:other","name":"Other"}}}`}
			calls, _, err := runTeamTask(t, resp, append([]string{"--app", "some-other-app"}, v.args...)...)
			if exitCodeFor(err) != exitcode.Usage || grantOps1868(calls) != 0 {
				t.Fatalf("exit %d calls %v", exitCodeFor(err), *calls)
			}
		})
	}
}

// P13/P14: empty is not absent, and bad input refuses before any call.
func TestGrantRequest1868InputRefusalsSendNothing(t *testing.T) {
	base := []string{"team", "grant", "request", "--action", "node.read", "--values", `{"a":"b"}`, "--reason", "r"}
	with := func(extra ...string) []string { return append(append([]string{}, base...), extra...) }
	cases := map[string][]string{
		"empty starts-at":  with("--starts-at="),
		"empty expires-at": with("--expires-at="),
		"empty holder-id":  with("--holder-id="),
		"max-uses nonint":  with("--max-uses", "many"),
		"bad holder":       with("--holder", "person"),
		"empty action":     {"team", "grant", "request", "--action=", "--values", `{}`, "--reason", "r"},
		"empty reason":     {"team", "grant", "request", "--action", "a", "--values", `{}`, "--reason="},
		"missing reason":   {"team", "grant", "request", "--action", "a", "--values", `{}`},
		"bad values":       with("--values", "{bad"),
		"blank sig id":     {"team", "grant", "request-signature", " "},
		"blank withdraw":   {"team", "grant", "signing-request", "withdraw", " "},
		"blank get":        {"team", "grant", "signing-request", "get", " "},
	}
	for name, argv := range cases {
		t.Run(name, func(t *testing.T) {
			calls, _, err := runTeamTask(t, map[string]string{}, argv...)
			if exitCodeFor(err) != exitcode.Usage || grantOps1868(calls) != 0 {
				t.Fatalf("exit %d calls %v err %v", exitCodeFor(err), *calls, err)
			}
		})
	}
	t.Run("values from file", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "v.json")
		_ = os.WriteFile(p, []byte(`{"node":["f"]}`), 0o600)
		v := grantVerbs1868()[0]
		calls, _, err := runTeamTask(t, map[string]string{v.op: v.resp("")}, "team", "grant", "request", "--action", "a", "--values", "@"+p, "--reason", "r")
		if err != nil {
			t.Fatal(err)
		}
		if got := string(inputKeys1868(t, findCall1868(calls, v.op))["values"]); got != `{"node":["f"]}` {
			t.Fatalf("values %s", got)
		}
	})
}

// P16: the existing reads keep their signing-free documents.
func TestGrantRequest1868ReadsStaySigningFree(t *testing.T) {
	calls, _, err := runTeamTask(t, map[string]string{"TeamGrant": `{"data":{"grant":` + workerGrant1832 + `}}`}, "team", "grant", "get", "g1")
	if err != nil {
		t.Fatal(err)
	}
	if q := (*calls)[0].Query; strings.Contains(q, "signatureStatus") || strings.Contains(q, "SigningRequest") {
		t.Fatalf("read document selects a signing type: %s", q)
	}
}

// P17: help states the rules the verbs enforce.
func TestGrantRequest1868Help(t *testing.T) {
	for _, argv := range [][]string{{"team", "grant", "request", "--help"}, {"team", "grant", "request-signature", "--help"}, {"team", "grant", "signing-request", "withdraw", "--help"}, {"team", "grant", "signing-request", "get", "--help"}} {
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(argv)
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		for _, s := range []string{"never signs", "person", "without a worker session", "does not offer"} {
			if !strings.Contains(out.String(), s) {
				t.Errorf("%v help lacks %q:\n%s", argv, s, out.String())
			}
		}
	}
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "grant", "request-signature", "--help"})
	_ = root.Execute()
	for _, s := range []string{"pending", "as printed"} {
		if !strings.Contains(out.String(), s) {
			t.Errorf("request-signature help lacks %q", s)
		}
	}
	f, out = testFactory(t)
	root = NewRootCmd(f)
	root.SetArgs([]string{"team", "grant", "--help"})
	_ = root.Execute()
	if strings.Contains(out.String(), "only reads") {
		t.Errorf("group help still says it only reads:\n%s", out.String())
	}
}
