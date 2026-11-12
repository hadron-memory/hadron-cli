package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/pflag"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// hadron-cli#1879 / hadron-server#2973 PR 2: `hadron org signing`. These pins
// use an ORDERED request log (op, raw variables, X-Hadron-Session header) rather
// than captureGraphQL, which keeps only the last variables per operation, drops
// the headers and cannot show ordering (review:assert-the-query-not-the-capture).

type sigReq struct {
	Op      string
	Vars    string
	Session string
}

type sigFake struct {
	t   *testing.T
	srv *httptest.Server
	mu  sync.Mutex
	log []sigReq
	// respond answers one request. n is the 0-based count of earlier requests
	// of the same operation. It is called under the lock, so a queued page is
	// claimed before it is written.
	respond func(op string, n int, vars map[string]any) string
	// onMutation runs when SetOrgBootPromptSigning arrives, before it answers.
	onMutation func()
	counts     map[string]int
}

func newSigFake(t *testing.T, respond func(op string, n int, vars map[string]any) string) *sigFake {
	t.Helper()
	s := &sigFake{t: t, respond: respond, counts: map[string]int{}}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/capabilities" {
			_, _ = w.Write([]byte(`{"capabilities":[]}`))
			return
		}
		var body struct {
			OperationName string          `json:"operationName"`
			Variables     json.RawMessage `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		var vars map[string]any
		_ = json.Unmarshal(body.Variables, &vars)
		s.mu.Lock()
		s.log = append(s.log, sigReq{body.OperationName, string(body.Variables), r.Header.Get("X-Hadron-Session")})
		n := s.counts[body.OperationName]
		s.counts[body.OperationName]++
		hook := s.onMutation
		resp := s.respond(body.OperationName, n, vars)
		s.mu.Unlock()
		if body.OperationName == "SetOrgBootPromptSigning" && hook != nil {
			hook()
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *sigFake) ops() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, r := range s.log {
		out = append(out, r.Op)
	}
	return out
}

func (s *sigFake) requests(op string) []sigReq {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []sigReq
	for _, r := range s.log {
		if r.Op == op {
			out = append(out, r)
		}
	}
	return out
}

func (s *sigFake) assertNoSessionHeader(t *testing.T) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.log {
		if r.Session != "" {
			t.Errorf("%s sent X-Hadron-Session %q; a worker session is refused by the server (cor:agt:040:09)", r.Op, r.Session)
		}
	}
}

// runSigning executes the root command against the fake and returns the exit
// code a user would get (renderError, not exitcode.FromError).
func runSigning(t *testing.T, f *cmdutil.Factory, fake *sigFake, args ...string) int {
	t.Helper()
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{}, args...), "--server", fake.srv.URL))
	if err := root.Execute(); err != nil {
		return renderError(f, err)
	}
	return 0
}

const statusJSON = `{"data":{"orgBootPromptSigning":{"required":%t,"enforced":%t,"deploymentSigns":%t}}}`

func statusResp(required, enforced, deploys bool) string {
	return fmt.Sprintf(statusJSON, required, enforced, deploys)
}

func setResp(required, enforced, deploys bool) string {
	return fmt.Sprintf(`{"data":{"setOrgBootPromptSigning":{"required":%t,"enforced":%t,"deploymentSigns":%t}}}`, required, enforced, deploys)
}

func sigRow(n int) string {
	return fmt.Sprintf(`{"appId":"app-%d","appUrn":"hrn:app:acme.com:a%d","agentId":"agt-%d","agentName":"agent-%d","workerId":null,"workerName":null,"compiledNodeId":"cn-%d","compiledNodeUrn":null,"servedRevSeq":%d,"state":"UNSIGNED_STOPS","changesToRevSeq":null}`, n, n, n, n, n, n)
}

func sigPage(rowNums []int, total int, hasMore bool, cursor, token string) string {
	rows := make([]string, 0, len(rowNums))
	for _, n := range rowNums {
		rows = append(rows, sigRow(n))
	}
	cur := "null"
	if cursor != "" {
		cur = fmt.Sprintf("%q", cursor)
	}
	return fmt.Sprintf(`{"data":{"orgBootPromptSigningPreview":{"enforced":false,"deploymentSigns":true,"total":%d,"hasMore":%t,"endCursor":%s,"previewToken":%q,"rows":[%s]}}}`,
		total, hasMore, cur, token, strings.Join(rows, ","))
}

const forbiddenResp = `{"errors":[{"message":"Forbidden","extensions":{"code":"FORBIDDEN"}}],"data":null}`

func gqlErrorResp(code, msg string) string {
	return fmt.Sprintf(`{"errors":[{"message":%q,"extensions":{"code":%q}}],"data":null}`, msg, code)
}

// threePages serves 3 pages (sizes 2/2/1, total 5) with a DIFFERENT token each.
func threePages(op string, n int, _ map[string]any) string {
	switch op {
	case "OrgBootPromptSigningPreview":
		switch n {
		case 0:
			return sigPage([]int{1, 2}, 5, true, "c1", "tok-p1")
		case 1:
			return sigPage([]int{3, 4}, 5, true, "c2", "tok-p2")
		default:
			return sigPage([]int{5}, 5, false, "", "tok-p3")
		}
	case "SetOrgBootPromptSigning":
		return setResp(true, true, true)
	}
	return `{"data":null}`
}

func sigVars(t *testing.T, raw string) map[string]any {
	t.Helper()
	m := map[string]any{}
	if raw != "" && raw != "null" {
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			t.Fatalf("variables %q: %v", raw, err)
		}
	}
	return m
}

// Pin 1 + 2: get sends exactly its operation, as typed, with no session header.
func TestOrgSigningGet(t *testing.T) {
	fake := newSigFake(t, func(op string, _ int, _ map[string]any) string { return statusResp(true, false, false) })
	f, out := testFactory(t)
	if code := runSigning(t, f, fake, "org", "signing", "get", "acme.com", "--json"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if ops := fake.ops(); len(ops) != 1 || ops[0] != "OrgBootPromptSigning" {
		t.Fatalf("operations = %v", ops)
	}
	if v := sigVars(t, fake.requests("OrgBootPromptSigning")[0].Vars); len(v) != 1 || v["org"] != "acme.com" {
		t.Errorf("variables = %v", v)
	}
	fake.assertNoSessionHeader(t)
	var got map[string]any
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	want := map[string]any{"org": "acme.com", "required": true, "enforced": false, "deploymentSigns": false}
	if len(got) != len(want) {
		t.Errorf("json = %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("json[%s] = %v, want %v", k, got[k], v)
		}
	}
}

func TestOrgSigningGetRequiredButNotEnforcedWarns(t *testing.T) {
	cases := []struct {
		name                    string
		required, enforced, dep bool
		warn                    bool
	}{
		{"required but deployment does not sign", true, false, false, true},
		{"off while the deployment signs", false, false, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fake := newSigFake(t, func(string, int, map[string]any) string { return statusResp(c.required, c.enforced, c.dep) })
			f, out := testFactory(t)
			if code := runSigning(t, f, fake, "org", "signing", "get", "acme.com"); code != 0 {
				t.Fatalf("exit %d", code)
			}
			for _, label := range []string{"required:", "enforced:", "deploymentSigns:"} {
				if !strings.Contains(out.String(), label) {
					t.Errorf("table lacks %q:\n%s", label, out)
				}
			}
			if got := strings.Contains(out.String(), "does not ship boot-prompt signing"); got != c.warn {
				t.Errorf("warning shown = %v, want %v:\n%s", got, c.warn, out)
			}
		})
	}
}

// Pin 3: plain preview reads ONE page and sends no first/after.
func TestOrgSigningPreviewSinglePage(t *testing.T) {
	fake := newSigFake(t, func(op string, n int, _ map[string]any) string {
		return sigPage([]int{1, 2}, 5, true, "c1", "tok-p1")
	})
	f, out := testFactory(t)
	if code := runSigning(t, f, fake, "org", "signing", "preview", "acme.com"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	reqs := fake.requests("OrgBootPromptSigningPreview")
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want exactly 1 even though hasMore is true", len(reqs))
	}
	v := sigVars(t, reqs[0].Vars)
	if _, ok := v["first"]; ok {
		t.Errorf("plain preview sent first: %s", reqs[0].Vars)
	}
	if _, ok := v["after"]; ok {
		t.Errorf("plain preview sent after: %s", reqs[0].Vars)
	}
	for _, want := range []string{"agent-1", "agent-2", "--after c1", "--all"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	fake.assertNoSessionHeader(t)
}

func TestOrgSigningPreviewFirstAfterAndJSONShape(t *testing.T) {
	fake := newSigFake(t, func(string, int, map[string]any) string {
		return sigPage([]int{1}, 1, false, "", "tok")
	})
	f, out := testFactory(t)
	if code := runSigning(t, f, fake, "org", "signing", "preview", "acme.com", "--first", "2", "--after", "C", "--json"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	v := sigVars(t, fake.requests("OrgBootPromptSigningPreview")[0].Vars)
	if v["org"] != "acme.com" || v["first"] != float64(2) || v["after"] != "C" {
		t.Errorf("variables = %v", v)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if _, ok := got["previewToken"]; ok {
		t.Error("preview --json must not carry previewToken (R10)")
	}
	for _, k := range []string{"org", "enforced", "deploymentSigns", "total", "rows", "hasMore", "endCursor"} {
		if _, ok := got[k]; !ok {
			t.Errorf("json lacks %q", k)
		}
	}
	row := got["rows"].([]any)[0].(map[string]any)
	for _, k := range []string{"appId", "appUrn", "agentId", "agentName", "workerId", "workerName", "compiledNodeId", "compiledNodeUrn", "servedRevSeq", "state", "changesToRevSeq"} {
		if _, ok := row[k]; !ok {
			t.Errorf("row lacks %q (null must be present, not omitted)", k)
		}
	}
	if row["workerId"] != nil || row["compiledNodeUrn"] != nil || row["changesToRevSeq"] != nil {
		t.Errorf("absent fields must be null: %v", row)
	}
}

func TestOrgSigningPreviewEmptyRowsIsArray(t *testing.T) {
	fake := newSigFake(t, func(string, int, map[string]any) string { return sigPage(nil, 0, false, "", "tok") })
	f, out := testFactory(t)
	if code := runSigning(t, f, fake, "org", "signing", "preview", "acme.com", "--json"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out.String(), `"rows": []`) {
		t.Errorf("empty rows must render as [] (raw check):\n%s", out)
	}
}

// Pin 4: --all reads every page through endCursor/hasMore.
func TestOrgSigningPreviewAll(t *testing.T) {
	fake := newSigFake(t, threePages)
	f, out := testFactory(t)
	if code := runSigning(t, f, fake, "org", "signing", "preview", "acme.com", "--all", "--json"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	reqs := fake.requests("OrgBootPromptSigningPreview")
	if len(reqs) != 3 {
		t.Fatalf("requests = %d, want 3", len(reqs))
	}
	wantAfter := []any{nil, "c1", "c2"}
	for i, r := range reqs {
		v := sigVars(t, r.Vars)
		if v["after"] != wantAfter[i] {
			t.Errorf("page %d after = %v, want %v", i+1, v["after"], wantAfter[i])
		}
		if v["first"] != float64(200) {
			t.Errorf("page %d first = %v, want 200", i+1, v["first"])
		}
	}
	var got struct {
		Total   int
		Rows    []map[string]any
		HasMore bool
	}
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if len(got.Rows) != 5 || got.HasMore {
		t.Fatalf("rows=%d hasMore=%v", len(got.Rows), got.HasMore)
	}
	for i, r := range got.Rows {
		if r["agentName"] != fmt.Sprintf("agent-%d", i+1) {
			t.Errorf("row %d = %v (order)", i, r["agentName"])
		}
	}
}

func TestOrgSigningCollectorRefusesDrift(t *testing.T) {
	cases := map[string]func(op string, n int, _ map[string]any) string{
		"repeated cursor": func(op string, n int, _ map[string]any) string {
			return sigPage([]int{n*2 + 1, n*2 + 2}, 99, true, "same", "t")
		},
		"hasMore with a null cursor": func(op string, n int, _ map[string]any) string {
			return sigPage([]int{1, 2}, 5, true, "", "t")
		},
		"total changes on page 2": func(op string, n int, _ map[string]any) string {
			if n == 0 {
				return sigPage([]int{1, 2}, 3, true, "c1", "t")
			}
			// Rows still sum to the FIRST total (2+1=3), so only the total-drift check can refuse this.
			return sigPage([]int{3}, 4, false, "", "t")
		},
		"repeated row key": func(op string, n int, _ map[string]any) string {
			if n == 0 {
				return sigPage([]int{1, 2}, 3, true, "c1", "t")
			}
			return sigPage([]int{2}, 3, false, "", "t")
		},
		"row count differs from total": func(op string, n int, _ map[string]any) string {
			return sigPage([]int{1}, 4, false, "", "t")
		},
	}
	for name, respond := range cases {
		t.Run(name, func(t *testing.T) {
			fake := newSigFake(t, func(op string, n int, v map[string]any) string {
				if n > 6 {
					t.Fatalf("collector kept going: %d requests", n)
				}
				return respond(op, n, v)
			})
			f, out := testFactory(t)
			if code := runSigning(t, f, fake, "org", "signing", "preview", "acme.com", "--all"); code != exitcode.Error {
				t.Errorf("exit = %d, want 1", code)
			}
			if out.String() != "" {
				t.Errorf("no partial output; stdout:\n%s", out)
			}
		})
	}
}

// Pin 5: --first is the server's to judge; only a non-integer is the client's.
func TestOrgSigningPreviewFirstPassesThrough(t *testing.T) {
	fake := newSigFake(t, func(op string, n int, v map[string]any) string {
		if v["first"] == float64(201) {
			return sigPage([]int{1, 2}, 2, false, "", "t")
		}
		return gqlErrorResp("BAD_USER_INPUT", "first must be at least 1")
	})
	f, out := testFactory(t)
	if code := runSigning(t, f, fake, "org", "signing", "preview", "acme.com", "--first", "201"); code != 0 {
		t.Fatalf("--first 201 exit %d", code)
	}
	if v := sigVars(t, fake.requests("OrgBootPromptSigningPreview")[0].Vars); v["first"] != float64(201) {
		t.Errorf("first = %v, want 201 unchanged", v["first"])
	}
	if !strings.Contains(out.String(), "agent-2") {
		t.Errorf("server page not rendered:\n%s", out)
	}
	for _, n := range []string{"0", "-1"} {
		f, _ := testFactory(t)
		before := len(fake.requests("OrgBootPromptSigningPreview"))
		if code := runSigning(t, f, fake, "org", "signing", "preview", "acme.com", "--first", n); code != exitcode.Usage {
			t.Errorf("--first %s exit = %d, want 2 (server BAD_USER_INPUT)", n, code)
		}
		if got := len(fake.requests("OrgBootPromptSigningPreview")); got != before+1 {
			t.Errorf("--first %s was not sent to the server", n)
		}
	}
	for _, args := range [][]string{{"--first", "abc"}, {"--all", "--first", "5"}, {"--all", "--after", "c"}} {
		f, _ := testFactory(t)
		before := len(fake.requests("OrgBootPromptSigningPreview"))
		a := append([]string{"org", "signing", "preview", "acme.com"}, args...)
		if code := runSigning(t, f, fake, a...); code != exitcode.Usage {
			t.Errorf("%v exit = %d, want 2", args, code)
		}
		if got := len(fake.requests("OrgBootPromptSigningPreview")); got != before {
			t.Errorf("%v made a request; a client usage error must make none", args)
		}
	}
}

// Pin 6: every page is read and shown before the mutation, with page 1's token.
func TestOrgSigningOnFetchesAndShowsEveryPageFirst(t *testing.T) {
	fake := newSigFake(t, threePages)
	f, _, _ := testFactoryTTY(t, "y\n")
	// One buffer for both streams so ordering is observable
	// (review:separate-streams-cannot-test-order).
	var both strings.Builder
	f.IOStreams.Out, f.IOStreams.ErrOut = output.Tracked(&both), &both
	if code := runSigning(t, f, fake, "org", "signing", "on", "acme.com"); code != 0 {
		t.Fatalf("exit %d\n%s", code, both.String())
	}
	want := []string{"OrgBootPromptSigningPreview", "OrgBootPromptSigningPreview", "OrgBootPromptSigningPreview", "SetOrgBootPromptSigning"}
	if got := fake.ops(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("operations = %v, want %v", got, want)
	}
	v := sigVars(t, fake.requests("SetOrgBootPromptSigning")[0].Vars)
	if v["org"] != "acme.com" || v["required"] != true || v["previewToken"] != "tok-p1" {
		t.Errorf("mutation variables = %v, want the FIRST page's token", v)
	}
	text := both.String()
	prompt := strings.Index(text, "(y/N)")
	if prompt < 0 {
		t.Fatalf("no prompt:\n%s", text)
	}
	for i := 1; i <= 5; i++ {
		at := strings.Index(text, fmt.Sprintf("agent-%d", i))
		if at < 0 || at > prompt {
			t.Errorf("agent-%d not shown before the prompt (at %d, prompt at %d)", i, at, prompt)
		}
	}
	fake.assertNoSessionHeader(t)
}

// Pin 7: no combination of flags sends the mutation without the fetch + display.
func TestOrgSigningOnNoFlagCombinationSkipsFetchOrDisplay(t *testing.T) {
	f0, _ := testFactory(t)
	root := NewRootCmd(f0)
	on, _, err := root.Find([]string{"org", "signing", "on"})
	if err != nil {
		t.Fatal(err)
	}
	// Every flag `on` can see, local or inherited. A flag not classified here
	// FAILS the test, so a future --token or --skip-preview cannot be added
	// without being covered.
	boolFlags := map[string]bool{"yes": true, "json": true, "unlock-passphrase-stdin": false}
	valueFlags := map[string]string{"app": "hrn:app:acme.com:team"}
	skip := map[string]string{
		"help":                   "prints help and sends nothing",
		"server":                 "always passed by the harness",
		"unlock":                 "memory unlock for reads of memories; cannot reach the signing mutation",
		"unlock-passphrase-file": "paired with --unlock",
	}
	var toggled, valued []string
	classify := func(name string) {
		switch {
		case skip[name] != "":
		case valueFlags[name] != "":
			valued = append(valued, name)
		default:
			if _, ok := boolFlags[name]; ok && boolFlags[name] {
				toggled = append(toggled, name)
			} else if ok {
				// a bool flag we cannot drive safely (stdin passphrase): excluded explicitly
			} else {
				t.Fatalf("flag --%s of `org signing on` is not covered by the no-blind-switch pin", name)
			}
		}
	}
	on.Flags().VisitAll(func(fl *pflag.Flag) { classify(fl.Name) })
	on.InheritedFlags().VisitAll(func(fl *pflag.Flag) { classify(fl.Name) })

	for mask := 0; mask < 1<<(len(toggled)+len(valued)); mask++ {
		var args []string
		name := fmt.Sprintf("mask=%b", mask)
		for i, fl := range append(append([]string{}, toggled...), valued...) {
			if mask&(1<<i) == 0 {
				continue
			}
			if v, ok := valueFlags[fl]; ok {
				args = append(args, "--"+fl, v)
			} else {
				args = append(args, "--"+fl)
			}
		}
		t.Run(name, func(t *testing.T) {
			fake := newSigFake(t, threePages)
			f, _, errOut := testFactoryTTY(t, "y\n")
			var prefixAtMutation string
			fake.onMutation = func() { prefixAtMutation = errOut.String() }
			if code := runSigning(t, f, fake, append([]string{"org", "signing", "on", "acme.com"}, args...)...); code != 0 {
				t.Fatalf("exit %d args=%v stderr=%s", code, args, errOut)
			}
			ops := fake.ops()
			if len(ops) != 4 || ops[3] != "SetOrgBootPromptSigning" {
				t.Fatalf("ops = %v", ops) // (a) every page, then the mutation
			}
			reqs := fake.requests("OrgBootPromptSigningPreview")
			if v := sigVars(t, reqs[2].Vars); v["after"] != "c2" { // (b) read through the last page
				t.Errorf("final page request = %v", v)
			}
			for i := 1; i <= 5; i++ { // (c) every row written before the mutation arrived
				if !strings.Contains(prefixAtMutation, fmt.Sprintf("agent-%d", i)) {
					t.Errorf("agent-%d not on stderr when the mutation arrived (args %v)", i, args)
				}
			}
			if v := sigVars(t, fake.requests("SetOrgBootPromptSigning")[0].Vars); v["previewToken"] != "tok-p1" { // (d)
				t.Errorf("token = %v", v["previewToken"])
			}
		})
	}
}

func TestOrgSigningOnCompanionsNeverSendTheMutation(t *testing.T) {
	t.Run("a preview page fails", func(t *testing.T) {
		for failAt := 0; failAt < 2; failAt++ {
			fake := newSigFake(t, func(op string, n int, v map[string]any) string {
				if op == "OrgBootPromptSigningPreview" && n == failAt {
					return forbiddenResp
				}
				return threePages(op, n, v)
			})
			f, _ := testFactory(t)
			if code := runSigning(t, f, fake, "org", "signing", "on", "acme.com", "--yes"); code != exitcode.Forbidden {
				t.Errorf("fail at page %d: exit %d, want 8", failAt+1, code)
			}
			if len(fake.requests("SetOrgBootPromptSigning")) != 0 {
				t.Errorf("fail at page %d: mutation sent", failAt+1)
			}
		}
	})
	t.Run("non-TTY without --yes", func(t *testing.T) {
		fake := newSigFake(t, threePages)
		f, _ := testFactory(t)
		if code := runSigning(t, f, fake, "org", "signing", "on", "acme.com"); code != exitcode.Usage {
			t.Errorf("exit %d, want 2", code)
		}
		if len(fake.requests("SetOrgBootPromptSigning")) != 0 {
			t.Error("mutation sent without consent")
		}
	})
	t.Run("TTY answers n", func(t *testing.T) {
		fake := newSigFake(t, threePages)
		f, _, _ := testFactoryTTY(t, "n\n")
		if code := runSigning(t, f, fake, "org", "signing", "on", "acme.com"); code != exitcode.Cancelled {
			t.Errorf("exit %d, want 6", code)
		}
		if len(fake.requests("SetOrgBootPromptSigning")) != 0 {
			t.Error("mutation sent after n")
		}
	})
}

// R2: on --json stdout carries the status plus the preview that was shown.
func TestOrgSigningOnJSONCarriesThePreviewShown(t *testing.T) {
	fake := newSigFake(t, threePages)
	f, out := testFactory(t)
	if code := runSigning(t, f, fake, "org", "signing", "on", "acme.com", "--yes", "--json"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	var got struct {
		Org      string
		Required bool
		Preview  struct {
			Total int
			Rows  []map[string]any
		}
	}
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("stdout must be one JSON document: %v\n%s", err, out)
	}
	if got.Org != "acme.com" || !got.Required || got.Preview.Total != 5 || len(got.Preview.Rows) != 5 {
		t.Errorf("on --json = %+v", got)
	}
}

// Pin 8 (R5): zero rows still fetches, notes it, and still confirms.
func TestOrgSigningOnZeroRows(t *testing.T) {
	respond := func(op string, n int, _ map[string]any) string {
		if op == "SetOrgBootPromptSigning" {
			return setResp(true, true, true)
		}
		return sigPage(nil, 0, false, "", "tok-empty")
	}
	t.Run("prompts", func(t *testing.T) {
		fake := newSigFake(t, respond)
		f, _, errOut := testFactoryTTY(t, "y\n")
		if code := runSigning(t, f, fake, "org", "signing", "on", "acme.com"); code != 0 {
			t.Fatalf("exit %d", code)
		}
		if !strings.Contains(errOut.String(), "Nothing served today would change") || !strings.Contains(errOut.String(), "(y/N)") {
			t.Errorf("stderr:\n%s", errOut)
		}
	})
	t.Run("non-TTY without --yes still refuses", func(t *testing.T) {
		fake := newSigFake(t, respond)
		f, _ := testFactory(t)
		if code := runSigning(t, f, fake, "org", "signing", "on", "acme.com"); code != exitcode.Usage {
			t.Errorf("exit %d, want 2", code)
		}
	})
	t.Run("--yes skips the prompt only", func(t *testing.T) {
		fake := newSigFake(t, respond)
		f, _ := testFactory(t)
		if code := runSigning(t, f, fake, "org", "signing", "on", "acme.com", "--yes"); code != 0 {
			t.Fatalf("exit %d", code)
		}
		if got := len(fake.requests("OrgBootPromptSigningPreview")); got != 1 {
			t.Errorf("preview requests = %d, want 1 (the fetch still happens)", got)
		}
	})
}

// R8: switching on where the deployment does not sign is allowed, with a note.
func TestOrgSigningOnWhereDeploymentDoesNotSign(t *testing.T) {
	fake := newSigFake(t, func(op string, n int, _ map[string]any) string {
		if op == "SetOrgBootPromptSigning" {
			return setResp(true, false, false)
		}
		return strings.Replace(sigPage(nil, 0, false, "", "t"), `"deploymentSigns":true`, `"deploymentSigns":false`, 1)
	})
	f, _, errOut := testFactoryTTY(t, "y\n")
	if code := runSigning(t, f, fake, "org", "signing", "on", "acme.com"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(errOut.String(), "does not ship boot-prompt signing") {
		t.Errorf("no not-enforced note before the prompt:\n%s", errOut)
	}
}

// Pin 9: off sends no token, makes no preview request.
func TestOrgSigningOff(t *testing.T) {
	respond := func(op string, n int, _ map[string]any) string { return setResp(false, false, true) }
	t.Run("--yes", func(t *testing.T) {
		fake := newSigFake(t, respond)
		f, out := testFactory(t)
		if code := runSigning(t, f, fake, "org", "signing", "off", "acme.com", "--yes", "--json"); code != 0 {
			t.Fatalf("exit %d", code)
		}
		if ops := fake.ops(); len(ops) != 1 || ops[0] != "SetOrgBootPromptSigning" {
			t.Fatalf("operations = %v (off must not preview)", ops)
		}
		raw := fake.requests("SetOrgBootPromptSigning")[0].Vars
		if strings.Contains(raw, "previewToken") {
			t.Errorf("off must omit previewToken, sent %s", raw)
		}
		if v := sigVars(t, raw); v["required"] != false || v["org"] != "acme.com" {
			t.Errorf("variables = %s", raw)
		}
		var got map[string]any
		if err := json.Unmarshal([]byte(out.String()), &got); err != nil || got["required"] != false || got["org"] != "acme.com" {
			t.Errorf("status DTO = %s (%v)", out, err)
		}
		fake.assertNoSessionHeader(t)
	})
	t.Run("non-TTY without --yes", func(t *testing.T) {
		fake := newSigFake(t, respond)
		f, _ := testFactory(t)
		if code := runSigning(t, f, fake, "org", "signing", "off", "acme.com"); code != exitcode.Usage {
			t.Errorf("exit %d, want 2", code)
		}
		if len(fake.ops()) != 0 {
			t.Errorf("sent %v before consent", fake.ops())
		}
	})
	t.Run("TTY declines", func(t *testing.T) {
		fake := newSigFake(t, respond)
		f, _, _ := testFactoryTTY(t, "n\n")
		if code := runSigning(t, f, fake, "org", "signing", "off", "acme.com"); code != exitcode.Cancelled {
			t.Errorf("exit %d, want 6", code)
		}
	})
}

// Pin 10: STALE is exit 5, says to preview again, and rides serverCode.
func TestOrgSigningOnStale(t *testing.T) {
	fake := newSigFake(t, func(op string, n int, v map[string]any) string {
		if op == "SetOrgBootPromptSigning" {
			return gqlErrorResp("BOOT_PROMPT_SIGNING_PREVIEW_STALE", "stale")
		}
		return threePages(op, n, v)
	})
	f, out, errOut := testFactoryTTY(t, "")
	if code := runSigning(t, f, fake, "org", "signing", "on", "acme.com", "--yes", "--json"); code != exitcode.Conflict {
		t.Fatalf("exit %d, want 5", code)
	}
	all := out.String() + errOut.String()
	if !strings.Contains(all, "again") || !strings.Contains(all, "nothing changed") {
		t.Errorf("no preview-again message:\n%s", all)
	}
	if !strings.Contains(all, `"serverCode": "BOOT_PROMPT_SIGNING_PREVIEW_STALE"`) {
		t.Errorf("envelope lacks serverCode:\n%s", all)
	}
	if strings.Contains(out.String(), "agent-1") {
		t.Errorf("stdout must stay free of the preview (it went to stderr):\n%s", out)
	}
}

// Pin 11: FORBIDDEN is identical for an unknown org and a non-admin; no lookup;
// no session header on any request of any verb.
func TestOrgSigningForbiddenIsIdenticalAndNeverLooksUpTheOrg(t *testing.T) {
	verbs := map[string][]string{
		"get":     {"org", "signing", "get", "%s", "--json"},
		"preview": {"org", "signing", "preview", "%s", "--json"},
		"on":      {"org", "signing", "on", "%s", "--yes", "--json"},
		"off":     {"org", "signing", "off", "%s", "--yes", "--json"},
	}
	for verb, tmpl := range verbs {
		t.Run(verb, func(t *testing.T) {
			var codes []int
			var outs []string
			for _, org := range []string{"nonexistent.example", "acme.com"} {
				fake := newSigFake(t, func(string, int, map[string]any) string { return forbiddenResp })
				f, out, errOut := testFactoryTTY(t, "")
				args := append([]string{}, tmpl...)
				for i, a := range args {
					args[i] = strings.ReplaceAll(a, "%s", org)
				}
				codes = append(codes, runSigning(t, f, fake, args...))
				outs = append(outs, strings.ReplaceAll(out.String()+"|"+errOut.String(), org, "<org>"))
				for _, op := range fake.ops() {
					if !strings.Contains(op, "BootPromptSigning") {
						t.Errorf("%s made a lookup (%s); that would make an unknown org distinguishable", verb, op)
					}
				}
				fake.assertNoSessionHeader(t)
			}
			if codes[0] != exitcode.Forbidden || codes[1] != exitcode.Forbidden {
				t.Errorf("exit codes = %v, want 8", codes)
			}
			if outs[0] != outs[1] {
				t.Errorf("outputs differ:\n%s\n---\n%s", outs[0], outs[1])
			}
			if !strings.Contains(outs[0], "ADMIN") {
				t.Errorf("no constant remedy:\n%s", outs[0])
			}
		})
	}
}

// Pin 11 (cont.): on's session-header audit covers its preview pages and mutation.
func TestOrgSigningOnSendsNoSessionHeader(t *testing.T) {
	fake := newSigFake(t, threePages)
	f, _ := testFactory(t)
	if code := runSigning(t, f, fake, "org", "signing", "on", "acme.com", "--yes"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if len(fake.ops()) != 4 {
		t.Fatalf("ops = %v", fake.ops())
	}
	fake.assertNoSessionHeader(t)
}

// Pin 14: an older server rejects the document: schema skew, exit 2, no mutation.
func TestOrgSigningOldServer(t *testing.T) {
	fake := newSigFake(t, func(string, int, map[string]any) string {
		return gqlErrorResp("GRAPHQL_VALIDATION_FAILED", `Cannot query field "orgBootPromptSigningPreview" on type "Query".`)
	})
	f, _ := testFactory(t)
	if code := runSigning(t, f, fake, "org", "signing", "on", "acme.com", "--yes"); code != exitcode.Usage {
		t.Errorf("exit %d, want 2", code)
	}
	if len(fake.requests("SetOrgBootPromptSigning")) != 0 {
		t.Error("mutation sent after a failed preview")
	}
}

// Pin 12 (R9a): a bind the server refuses with BOOT_PROMPT_NOT_SIGNED (the org
// switched signing on) keeps exit 1 and the server's message. No remedy line is
// added in this change; if the alpha run shows the message reads poorly, that is
// a filed follow-up.
func TestSessionStartBootPromptNotSignedPassesThrough(t *testing.T) {
	teamGitDir(t)
	row := withLiveness(irisWorkerJSON, "false")
	const msg = "BOOT_PROMPT_NOT_SIGNED: the served revision is approved but not signed"
	gql, _ := captureGraphQL(t, map[string]string{
		"Workers":          `{"data":{"workers":{"total":1,"items":[` + row + `]}}}`,
		"TeamSessionsPage": `{"data":{"sessionsPage":{"total":0,"hasMore":false,"items":[]}}}`,
		"TeamMemoryApp":    `{"data":{"memory":{"id":"m1","appId":"capp100000000000000000000"}}}`,
		"StartTeamSession": gqlErrorResp("BOOT_PROMPT_NOT_SIGNED", msg),
	})
	f, _ := testFactory(t)
	errOut := &strings.Builder{}
	f.IOStreams.ErrOut = errOut
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "Iris", "--tool", "codex", "-m", "acme.com::eng-team", "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("bind must be refused")
	}
	if code := renderError(f, err); code != exitcode.Error {
		t.Errorf("exit %d, want 1 (a configuration state)", code)
	}
	if !strings.Contains(errOut.String(), "approved but not signed") {
		t.Errorf("server message lost:\n%s", errOut)
	}
}

type sigClosedWriter struct{}

func (sigClosedWriter) Write([]byte) (int, error) { return 0, fmt.Errorf("stderr closed") }

// Blind review P2 (cli#1942): a failed display must stop `on`, for a non-empty
// AND an empty preview, in text and --json: the preview is only "shown" if the
// write succeeded.
func TestOrgSigningOnFailedDisplayNeverSendsTheMutation(t *testing.T) {
	empty := func(op string, n int, _ map[string]any) string {
		if op == "SetOrgBootPromptSigning" {
			return setResp(true, true, true)
		}
		return sigPage(nil, 0, false, "", "tok")
	}
	for name, respond := range map[string]func(string, int, map[string]any) string{"rows": threePages, "empty": empty} {
		for _, asJSON := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/json=%v", name, asJSON), func(t *testing.T) {
				fake := newSigFake(t, respond)
				f, _ := testFactory(t)
				f.IOStreams.ErrOut = sigClosedWriter{}
				args := []string{"org", "signing", "on", "acme.com", "--yes"}
				if asJSON {
					args = append(args, "--json")
				}
				if code := runSigning(t, f, fake, args...); code == 0 {
					t.Error("a failed preview display must fail the command")
				}
				if len(fake.requests("SetOrgBootPromptSigning")) != 0 {
					t.Error("mutation sent although the preview could not be displayed")
				}
			})
		}
	}
}

// Blind review P2 (cli#1942): a successful response with a null result is a
// controlled error, never a panic, and a null preview never reaches the mutation.
func TestOrgSigningNullResultsAreControlledErrors(t *testing.T) {
	nullData := func(field string) string { return fmt.Sprintf(`{"data":{%q:null}}`, field) }
	cases := []struct {
		name    string
		args    []string
		respond func(op string, n int, v map[string]any) string
	}{
		{"get", []string{"get", "acme.com"}, func(string, int, map[string]any) string { return nullData("orgBootPromptSigning") }},
		{"preview", []string{"preview", "acme.com"}, func(string, int, map[string]any) string { return nullData("orgBootPromptSigningPreview") }},
		{"on: null preview", []string{"on", "acme.com", "--yes"}, func(string, int, map[string]any) string { return nullData("orgBootPromptSigningPreview") }},
		{"on: null mutation result", []string{"on", "acme.com", "--yes"}, func(op string, n int, v map[string]any) string {
			if op == "SetOrgBootPromptSigning" {
				return nullData("setOrgBootPromptSigning")
			}
			return threePages(op, n, v)
		}},
		{"off: null mutation result", []string{"off", "acme.com", "--yes"}, func(string, int, map[string]any) string { return nullData("setOrgBootPromptSigning") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fake := newSigFake(t, c.respond)
			f, _ := testFactory(t)
			if code := runSigning(t, f, fake, append([]string{"org", "signing"}, c.args...)...); code != exitcode.Error {
				t.Errorf("exit %d, want 1", code)
			}
			if c.name == "on: null preview" && len(fake.requests("SetOrgBootPromptSigning")) != 0 {
				t.Error("a null preview reached the mutation")
			}
		})
	}
}
