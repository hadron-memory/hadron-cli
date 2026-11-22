package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2738 (hadron-server#3999 S4, cor:sec:070:02): a change of a protected
// secret is STAGED, never applied. The CLI hands the person the request id and
// the ceremony URL, exactly as sent, and never completes the ceremony. Fake HTTP
// only; the server surface is hadron-server#4177 at 71997f19.

const (
	req2738 = "01a1fcd0aaaa4bbbcccc000000000001"
	// The ceremony URL is on the fake server itself ({{BASE}} is replaced by its
	// address), so a request to it is seen: the CLI must never make one.
	url2738          = "{{BASE}}/secret-changes/01a1fcd0aaaa4bbbcccc000000000001"
	value2738        = "CANARY-secret-value-2738"
	ceremonyPath2738 = "/secret-changes/" + req2738
)

// gqlErr2738 is a GraphQL error envelope the way the server sends one.
func gqlErr2738(code string, ext map[string]any) string {
	e := map[string]any{"code": code}
	for k, v := range ext {
		e[k] = v
	}
	b, _ := json.Marshal(map[string]any{"errors": []any{map[string]any{"message": "refused " + value2738, "extensions": e}}, "data": nil})
	return string(b)
}

func staged2738() string { return stagedURL2738(url2738) }

func stagedURL2738(url string) string {
	return gqlErr2738("SECRET_PROTECTED_NEEDS_PASSKEY", map[string]any{"requestId": req2738, "ceremonyUrl": url})
}

type call2738 struct {
	Op   string
	Vars map[string]any
	Path string
	// Query is the raw request query text, so a test can pin WHICH document
	// was sent (cli#2815 round 6: the flag-omitted documents must carry no
	// `protected` at all, not just omit the variable).
	Query string
}

// fake2738 answers each GraphQL operation from respond (called with its
// 1-based count) and records every request, REST paths included, so a test can
// prove no ceremony page was ever requested.
func fake2738(t *testing.T, respond func(op string, n int) (int, string)) (*httptest.Server, *[]call2738) {
	t.Helper()
	var mu sync.Mutex
	calls := &[]call2738{}
	counts := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/capabilities" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"capabilities":[]}`))
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var b struct {
			OperationName string         `json:"operationName"`
			Query         string         `json:"query"`
			Variables     map[string]any `json:"variables"`
		}
		_ = json.Unmarshal(raw, &b)
		mu.Lock()
		*calls = append(*calls, call2738{Op: b.OperationName, Vars: b.Variables, Path: r.URL.Path, Query: b.Query})
		counts[b.OperationName]++
		n := counts[b.OperationName]
		mu.Unlock()
		status, body := respond(b.OperationName, n)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(strings.ReplaceAll(body, "{{BASE}}", "http://"+r.Host)))
	}))
	t.Cleanup(srv.Close)
	return srv, calls
}

func only2738(status int, body string) func(string, int) (int, string) {
	return func(string, int) (int, string) { return status, body }
}

// run2738 runs `hadron secret <args>` and renders its error the way main does,
// so stderr, the JSON envelope and the exit code are what a script sees.
func run2738(t *testing.T, srvURL string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	f, out := testFactory(t)
	var errOut strings.Builder
	f.IOStreams.ErrOut = &errOut
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{"secret"}, args...), "--server", srvURL))
	err := root.Execute()
	if err != nil {
		code = renderError(f, err)
	}
	return out.String(), errOut.String(), code
}

func ops2738(calls *[]call2738) []string {
	var ops []string
	for _, c := range *calls {
		if c.Op != "" {
			ops = append(ops, c.Op)
		}
	}
	return ops
}

func noCeremonyRequest2738(t *testing.T, calls *[]call2738) {
	t.Helper()
	for _, c := range *calls {
		if strings.Contains(c.Path, "secret-changes") {
			t.Errorf("the ceremony must never be requested, saw %s", c.Path)
		}
	}
}

func valueFile2738(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "v")
	if err := os.WriteFile(p, []byte(value2738), 0o600); err != nil {
		t.Fatal(err)
	}
	return "@" + p
}

func envelope2738(t *testing.T, stdout, stderr string) map[string]any {
	t.Helper()
	for _, s := range []string{stdout, stderr} {
		var m map[string]any
		if json.Unmarshal([]byte(strings.TrimSpace(s)), &m) == nil && m != nil {
			if e, ok := m["error"].(map[string]any); ok {
				return e
			}
			return m
		}
	}
	t.Fatalf("no JSON envelope\nstdout=%q\nstderr=%q", stdout, stderr)
	return nil
}

// Acceptance 1: every door that can reach a protected secret prints the request
// id and the ceremony URL byte for byte, says the change is staged, sends the
// mutation once, never requests the ceremony, shows no value, and exits 5.
// Red on main: the code is unmapped, so the exit is 1 and no id is printed.
func TestProtectedChangeIsStagedNotApplied2738(t *testing.T) {
	doors := []struct {
		name string
		op   string
		args func(t *testing.T) []string
	}{
		{"update", "UpdateSecret", func(t *testing.T) []string {
			return []string{"update", "sec-1", "--value-file", valueFile2738(t)}
		}},
		{"set", "UpdateSecret", func(t *testing.T) []string {
			return []string{"set", "sec-1", "--value-file", valueFile2738(t)}
		}},
		{"rm", "DeleteSecret", func(*testing.T) []string { return []string{"rm", "sec-1", "--yes"} }},
		{"create", "CreateSecret", func(t *testing.T) []string {
			return []string{"create", "--name", "tok", "--scope", "app", "--owner", "hrn:app:acme.com:ops", "--kind", "generic", "--value-file", valueFile2738(t)}
		}},
		{"protect-on", "SetSecretProtected", func(*testing.T) []string { return []string{"protect", "sec-1", "--on"} }},
		{"protect-off", "SetSecretProtected", func(*testing.T) []string { return []string{"protect", "sec-1", "--off"} }},
	}
	for _, d := range doors {
		for _, asJSON := range []bool{false, true} {
			name := d.name + "/text"
			if asJSON {
				name = d.name + "/json"
			}
			t.Run(name, func(t *testing.T) {
				srv, calls := fake2738(t, only2738(200, staged2738()))
				args := d.args(t)
				if asJSON {
					args = append(args, "--json")
				}
				stdout, stderr, code := run2738(t, srv.URL, args...)
				if code != exitcode.Conflict {
					t.Fatalf("exit = %d, want 5 (staged)\n%s%s", code, stdout, stderr)
				}
				if got := ops2738(calls); len(got) != 1 || got[0] != d.op {
					t.Fatalf("want exactly one %s, got %v", d.op, got)
				}
				noCeremonyRequest2738(t, calls)
				all := stdout + stderr
				if strings.Contains(all, value2738) {
					t.Errorf("the value must never appear in output:\n%s", all)
				}
				if asJSON {
					e := envelope2738(t, stdout, stderr)
					if e["requestId"] != req2738 || e["ceremonyUrl"] != srv.URL+ceremonyPath2738 || e["serverCode"] != "SECRET_PROTECTED_NEEDS_PASSKEY" || e["code"] != float64(5) {
						t.Errorf("envelope must carry the id, the URL and the server code: %v", e)
					}
					return
				}
				for _, want := range []string{"staged, not applied", "request id: " + req2738, "ceremony URL: " + srv.URL + ceremonyPath2738, "never completes the ceremony", "secret change get", "secret change withdraw"} {
					if !strings.Contains(stderr, want) {
						t.Errorf("stderr lacks %q:\n%s", want, stderr)
					}
				}
			})
		}
	}
}

// A ceremony URL that could act on a terminal is not printed as text, and
// --json still carries it exactly. The id is neutralised in text only.
func TestStagedChangeWithholdsAnUnsafeURLInText2738(t *testing.T) {
	bad := "https://hadron.example/secret-changes/x\x1b[2J"
	srv, _ := fake2738(t, only2738(200, stagedURL2738(bad)))
	_, stderr, code := run2738(t, srv.URL, "protect", "sec-1", "--on")
	if code != exitcode.Conflict {
		t.Fatalf("exit = %d, want 5", code)
	}
	if strings.ContainsRune(stderr, 0x1b) || strings.Contains(stderr, "secret-changes/x") {
		t.Errorf("the unsafe URL must not reach the terminal: %q", stderr)
	}
	if !strings.Contains(stderr, "use --json to read it exactly") || !strings.Contains(stderr, "request id: "+req2738) {
		t.Errorf("text must still name the request and say how to read the URL: %q", stderr)
	}
	stdout, errOut, _ := run2738(t, srv.URL, "protect", "sec-1", "--on", "--json")
	if e := envelope2738(t, stdout, errOut); e["ceremonyUrl"] != bad {
		t.Errorf("--json must carry the URL exactly: %q", e["ceremonyUrl"])
	}
}

// The bare code (no request id) is NOT "a request is open" (cor:sec:070:02):
// signing is off, or the caller is not a person. Nothing is staged, exit 2.
func TestProtectedRefusalWithoutARequestIsNotAStagedChange2738(t *testing.T) {
	srv, calls := fake2738(t, only2738(200, gqlErr2738("SECRET_PROTECTED_NEEDS_PASSKEY", nil)))
	for _, args := range [][]string{{"update", "sec-1", "--meta", "k=v"}, {"rm", "sec-1", "--yes"}, {"protect", "sec-1", "--on"}} {
		_, stderr, code := run2738(t, srv.URL, args...)
		if code != exitcode.Usage {
			t.Fatalf("%v: exit = %d, want 2\n%s", args, code, stderr)
		}
		if !strings.Contains(stderr, "does not offer protected-secret changes") || !strings.Contains(stderr, "nothing was staged") {
			t.Errorf("%v: must say nothing was staged: %q", args, stderr)
		}
		if strings.Contains(stderr, "request id:") || strings.Contains(stderr, "staged, not applied") {
			t.Errorf("%v: must not claim a request is open: %q", args, stderr)
		}
	}
	if len(*calls) != 3 {
		t.Errorf("one request per command, never retried: %d", len(*calls))
	}
}

// Too many open requests on one secret is the secret's state: exit 5, not 1.
func TestChangeRequestLimitExitsConflict2738(t *testing.T) {
	srv, _ := fake2738(t, only2738(200, gqlErr2738("SECRET_CHANGE_REQUEST_LIMIT", nil)))
	if _, stderr, code := run2738(t, srv.URL, "update", "sec-1", "--meta", "k=v"); code != exitcode.Conflict {
		t.Fatalf("exit = %d, want 5\n%s", code, stderr)
	}
}

func TestProtectNeedsExactlyOneDirection2738(t *testing.T) {
	srv, calls := fake2738(t, only2738(200, `{"data":{}}`))
	for _, args := range [][]string{{"protect", "sec-1"}, {"protect", "sec-1", "--on", "--off"}} {
		if _, stderr, code := run2738(t, srv.URL, args...); code != exitcode.Usage || !strings.Contains(stderr, "exactly one of --on or --off") {
			t.Errorf("%v: exit %d %q", args, code, stderr)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("a usage refusal sends nothing: %v", ops2738(calls))
	}
}

// protect sends the direction it was given, including false for --off (the
// variable is non-null, so false must go on the wire, not be omitted).
func TestProtectSendsTheDirection2738(t *testing.T) {
	for _, tc := range []struct {
		flag string
		want bool
	}{{"--on", true}, {"--off", false}} {
		srv, calls := fake2738(t, only2738(200, staged2738()))
		run2738(t, srv.URL, "protect", "sec-9", tc.flag)
		if len(*calls) != 1 || (*calls)[0].Vars["ref"] != "sec-9" || (*calls)[0].Vars["protected"] != tc.want {
			t.Errorf("%s: %v", tc.flag, *calls)
		}
	}
}

// setSecretProtected always stages a request (hadron-server#4177), so a returned
// secret is NOT reported as a change that happened: it is an unconfirmed outcome
// (exit 7) that names the protection the server reported and how to check.
func TestProtectSecretAnswerIsNotReportedAsAChange2738(t *testing.T) {
	for _, flag := range []string{"--on", "--off"} {
		srv, calls := fake2738(t, only2738(200, `{"data":{"setSecretProtected":{"id":"sec-1","ownerType":"app","ownerId":"a1","name":"tok","kind":"generic","metadata":null,"currentVersion":1,"protected":false,"createdAt":"t","createdBy":null,"updatedAt":"t","updatedBy":null}}}`))
		stdout, stderr, code := run2738(t, srv.URL, "protect", "sec-1", flag)
		if code != exitcode.Unavailable {
			t.Fatalf("%s: exit %d, want 7\n%s%s", flag, code, stdout, stderr)
		}
		if strings.Contains(stdout+stderr, "✓") || !strings.Contains(stderr, "not confirmed") || !strings.Contains(stderr, "protected: false") || !strings.Contains(stderr, "hadron secret get sec-1") {
			t.Errorf("%s: must say it is unconfirmed and how to check: %q %q", flag, stdout, stderr)
		}
		if len(*calls) != 1 {
			t.Errorf("%s: sent once: %d", flag, len(*calls))
		}
	}
}

// A server without signing has no setSecretProtected, secretChangeRequest or
// withdrawSecretChangeRequest: the framework rejects the field. Say so, exit 2.
func TestServerWithoutSigningOffersNoProtectedChanges2738(t *testing.T) {
	for field, args := range map[string][]string{
		"setSecretProtected":          {"protect", "sec-1", "--on"},
		"secretChangeRequest":         {"change", "get", req2738},
		"withdrawSecretChangeRequest": {"change", "withdraw", req2738},
	} {
		body := `{"errors":[{"message":"Cannot query field \"` + field + `\" on type \"Query\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`
		srv, calls := fake2738(t, only2738(400, body))
		_, stderr, code := run2738(t, srv.URL, args...)
		if code != exitcode.Usage || !strings.Contains(stderr, "this server does not offer protected-secret changes") {
			t.Errorf("%s: exit %d %q", field, code, stderr)
		}
		if len(*calls) != 1 {
			t.Errorf("%s: one request, not retried: %d", field, len(*calls))
		}
	}
}

func changeRow2738(state string) string {
	return `{"data":{"secretChangeRequest":{"id":"` + req2738 + `","state":"` + state + `","operation":"update","createdAt":"2026-11-21T01:00:00Z","expiresAt":"2026-11-21T02:00:00Z"}}}`
}

// change get is one read: the stable DTO, any state exits 0, and a null answer
// (unknown, or someone else's) is not found.
func TestChangeGetReadsOnceAndShowsAnyState2738(t *testing.T) {
	for _, state := range []string{"open", "completed", "declined", "expired", "withdrawn", "stale"} {
		srv, calls := fake2738(t, only2738(200, changeRow2738(state)))
		stdout, _, code := run2738(t, srv.URL, "change", "get", req2738, "--json")
		var d map[string]any
		if code != 0 || json.Unmarshal([]byte(stdout), &d) != nil {
			t.Fatalf("%s: exit %d %q", state, code, stdout)
		}
		if d["id"] != req2738 || d["state"] != state || d["operation"] != "update" || d["createdAt"] != "2026-11-21T01:00:00Z" || d["expiresAt"] != "2026-11-21T02:00:00Z" {
			t.Errorf("%s: DTO %v", state, d)
		}
		if len(*calls) != 1 || (*calls)[0].Vars["id"] != req2738 {
			t.Errorf("%s: want one read of the id: %v", state, *calls)
		}
	}
	srv, _ := fake2738(t, only2738(200, changeRow2738("open")))
	text, _, _ := run2738(t, srv.URL, "change", "get", req2738)
	if !strings.Contains(text, "state: open") || !strings.Contains(text, "request: "+req2738) {
		t.Errorf("text: %q", text)
	}
	null, _ := fake2738(t, only2738(200, `{"data":{"secretChangeRequest":null}}`))
	if _, stderr, code := run2738(t, null.URL, "change", "get", "nope"); code != exitcode.NotFound || !strings.Contains(stderr, "not found (or not yours)") {
		t.Errorf("null answer: exit %d %q", code, stderr)
	}
}

// --wait ends on each terminal state with the exit that says how it ended, and
// never withdraws anything.
func TestChangeGetWaitEndsOnEachTerminalState2738(t *testing.T) {
	for state, want := range map[string]int{"completed": 0, "declined": 5, "expired": 5, "withdrawn": 5, "stale": 5} {
		srv, calls := fake2738(t, only2738(200, changeRow2738(state)))
		stdout, stderr, code := run2738(t, srv.URL, "change", "get", req2738, "--wait", "--json")
		if code != want {
			t.Errorf("%s: exit %d, want %d\n%s%s", state, code, want, stdout, stderr)
		}
		if !strings.Contains(stdout, `"state":"`+state+`"`) && !strings.Contains(stdout, `"state": "`+state+`"`) {
			t.Errorf("%s: the ended state is still printed: %q", state, stdout)
		}
		for _, op := range ops2738(calls) {
			if op != "SecretChangeRequest" {
				t.Errorf("%s: --wait only reads, saw %s", state, op)
			}
		}
	}
	srv, _ := fake2738(t, only2738(200, changeRow2738("pending-review")))
	if _, stderr, code := run2738(t, srv.URL, "change", "get", req2738, "--wait"); code != exitcode.Error || !strings.Contains(stderr, "does not know") {
		t.Errorf("an unknown state is not waited on forever: exit %d %q", code, stderr)
	}
}

func TestChangeGetWaitTimeoutMustBePositive2738(t *testing.T) {
	srv, calls := fake2738(t, only2738(200, changeRow2738("open")))
	if _, _, code := run2738(t, srv.URL, "change", "get", req2738, "--wait", "--wait-timeout", "0s"); code != exitcode.Usage || len(*calls) != 0 {
		t.Errorf("exit %d, %d calls", code, len(*calls))
	}
}

// withdraw sends once; true is withdrawn, false is "not open" (5), an unknown
// or foreign request is 4, and a 5xx is never retried.
func TestChangeWithdraw2738(t *testing.T) {
	srv, calls := fake2738(t, only2738(200, `{"data":{"withdrawSecretChangeRequest":true}}`))
	stdout, _, code := run2738(t, srv.URL, "change", "withdraw", req2738, "--json")
	var d map[string]string
	if code != 0 || json.Unmarshal([]byte(stdout), &d) != nil || d["id"] != req2738 || d["status"] != "withdrawn" {
		t.Errorf("exit %d %q", code, stdout)
	}
	if len(*calls) != 1 || (*calls)[0].Vars["id"] != req2738 {
		t.Errorf("one withdraw of the id: %v", *calls)
	}
	text, _, _ := run2738(t, srv.URL, "change", "withdraw", req2738)
	if !strings.Contains(text, "✓ withdrawn change request "+req2738) {
		t.Errorf("text: %q", text)
	}
	notOpen, _ := fake2738(t, only2738(200, `{"data":{"withdrawSecretChangeRequest":false}}`))
	if _, stderr, code := run2738(t, notOpen.URL, "change", "withdraw", req2738); code != exitcode.Conflict || !strings.Contains(stderr, "is not open") || !strings.Contains(stderr, "secret change get") {
		t.Errorf("explicit false: exit %d %q", code, stderr)
	}
	// A missing or null result is a malformed answer, not an explicit "not open":
	// the outcome is unknown (7), and the way to check is named.
	for name, body := range map[string]string{"missing": `{"data":{}}`, "null": `{"data":{"withdrawSecretChangeRequest":null}}`} {
		bad, _ := fake2738(t, only2738(200, body))
		_, stderr, code := run2738(t, bad.URL, "change", "withdraw", req2738)
		if code != exitcode.Unavailable || !strings.Contains(stderr, "did not confirm") || !strings.Contains(stderr, "hadron secret change get "+req2738) {
			t.Errorf("%s result: exit %d %q", name, code, stderr)
		}
	}
	// A definitive refusal keeps its own class and is not called an unknown outcome.
	for name, c := range map[string]struct {
		body string
		want int
	}{"not found": {gqlErr2738("NOT_FOUND", nil), exitcode.NotFound}, "forbidden": {gqlErr2738("FORBIDDEN", nil), exitcode.Forbidden}} {
		refused, _ := fake2738(t, only2738(200, c.body))
		_, stderr, code := run2738(t, refused.URL, "change", "withdraw", req2738)
		if code != c.want || strings.Contains(stderr, "outcome unknown") {
			t.Errorf("%s: exit %d, want %d, no unknown-outcome wrap: %q", name, code, c.want, stderr)
		}
	}
	// A lost answer after the write (a 5xx) is sent once, never retried, and says
	// the request may have been withdrawn and how to check.
	down, dcalls := fake2738(t, only2738(503, `unavailable`))
	_, stderr, code := run2738(t, down.URL, "change", "withdraw", req2738)
	if code == 0 || len(*dcalls) != 1 || !strings.Contains(stderr, "withdraw outcome unknown (sent once, not retried)") || !strings.Contains(stderr, "hadron secret change get "+req2738) {
		t.Errorf("a 5xx: exit %d, %d calls, %q", code, len(*dcalls), stderr)
	}
}

// A refusal that is not the WHOLE answer is not claimed as staged: an internal
// error riding beside the code (either order), keeps its own request id and gets
// no ceremony URL, so the change request id never overwrites a fault's id.
func TestStagedClaimNeedsTheRefusalToBeTheWholeAnswer2738(t *testing.T) {
	staged := map[string]any{"message": "refused", "extensions": map[string]any{"code": "SECRET_PROTECTED_NEEDS_PASSKEY", "requestId": req2738, "ceremonyUrl": url2738}}
	fault := map[string]any{"message": "boom", "extensions": map[string]any{"code": "INTERNAL_SERVER_ERROR", "requestId": "fault-req-1"}}
	for name, errs := range map[string][]any{"staged first": {staged, fault}, "fault first": {fault, staged}} {
		b, _ := json.Marshal(map[string]any{"errors": errs, "data": nil})
		srv, _ := fake2738(t, only2738(200, string(b)))
		stdout, stderr, code := run2738(t, srv.URL, "protect", "sec-1", "--on", "--json")
		if code == 0 {
			t.Fatalf("%s: must fail", name)
		}
		e := envelope2738(t, stdout, stderr)
		if _, has := e["ceremonyUrl"]; has {
			t.Errorf("%s: a mixed answer is not a staged change, no ceremony URL: %v", name, e)
		}
		if e["requestId"] == req2738 {
			t.Errorf("%s: the change request id must not overwrite the fault's request id: %v", name, e)
		}
	}
}

// The same code from a gateway's 5xx envelope is not proof of what the server
// did, so it is not claimed as a staged change either.
func TestStagedClaimIsNotMadeFromAGateway5xx2738(t *testing.T) {
	srv, _ := fake2738(t, only2738(502, staged2738()))
	stdout, stderr, code := run2738(t, srv.URL, "protect", "sec-1", "--on", "--json")
	if code == 0 {
		t.Fatal("must fail")
	}
	if e := envelope2738(t, stdout, stderr); e["ceremonyUrl"] != nil {
		t.Errorf("a 5xx envelope is not a staged claim: %v", e)
	}
}

// A staged request whose ceremony URL is missing, null or blank cannot be signed
// by anyone: the request id is kept, the text says so and names withdrawal as the
// way out, and --json carries the id with an empty URL. Exit stays 5.
func TestStagedRequestWithoutAUsableURL2738(t *testing.T) {
	for name, ext := range map[string]map[string]any{
		"missing": {"requestId": req2738},
		"null":    {"requestId": req2738, "ceremonyUrl": nil},
		"blank":   {"requestId": req2738, "ceremonyUrl": "   "},
	} {
		srv, _ := fake2738(t, only2738(200, gqlErr2738("SECRET_PROTECTED_NEEDS_PASSKEY", ext)))
		_, stderr, code := run2738(t, srv.URL, "protect", "sec-1", "--on")
		if code != exitcode.Conflict {
			t.Fatalf("%s: exit %d, want 5", name, code)
		}
		for _, want := range []string{"request id: " + req2738, "no person can sign this request", "hadron secret change withdraw " + req2738} {
			if !strings.Contains(stderr, want) {
				t.Errorf("%s: text lacks %q: %q", name, want, stderr)
			}
		}
		if strings.Contains(stderr, "Give the URL") {
			t.Errorf("%s: must not tell the caller to hand over a URL that does not exist: %q", name, stderr)
		}
		stdout, errOut, _ := run2738(t, srv.URL, "protect", "sec-1", "--on", "--json")
		e := envelope2738(t, stdout, errOut)
		if e["requestId"] != req2738 || e["ceremonyUrl"] == nil && name == "missing" {
			t.Errorf("%s: --json keeps the request id: %v", name, e)
		}
		if u, _ := e["ceremonyUrl"].(string); strings.TrimSpace(u) != "" {
			t.Errorf("%s: no usable URL must not be invented: %q", name, u)
		}
	}
}

// SEC10 P3-2/P3-3: the staged code beneath the mutation's own level (a result
// field failing after the write may have committed) is not claimed as staged, so
// no ceremony URL is invented; it keeps its code, which maps to 5.
func TestStagedCodeBelowTheRootIsNotClaimed2738(t *testing.T) {
	nested := map[string]any{"message": "refused", "path": []any{"setSecretProtected", "name"},
		"extensions": map[string]any{"code": "SECRET_PROTECTED_NEEDS_PASSKEY", "requestId": req2738, "ceremonyUrl": url2738}}
	b, _ := json.Marshal(map[string]any{"errors": []any{nested}, "data": nil})
	srv, _ := fake2738(t, only2738(200, string(b)))
	stdout, stderr, code := run2738(t, srv.URL, "protect", "sec-1", "--on", "--json")
	e := envelope2738(t, stdout, stderr)
	if _, has := e["ceremonyUrl"]; has {
		t.Errorf("a nested refusal is not a staged claim: %v", e)
	}
	if code != exitcode.Conflict {
		t.Errorf("the code still maps to 5 (codeForExtension): exit %d", code)
	}
}

// SEC10 P3-1: a server-supplied request id is neutralised in text and quoted in
// the command hints, so it can neither act on a terminal nor inject into a shell
// line copied from the output; --json carries it exactly.
func TestStagedRequestIDIsNeutralisedAndQuoted2738(t *testing.T) {
	const quotedID = "r1; touch pwned"
	srv, _ := fake2738(t, only2738(200, gqlErr2738("SECRET_PROTECTED_NEEDS_PASSKEY", map[string]any{"requestId": quotedID, "ceremonyUrl": url2738})))
	_, stderr, _ := run2738(t, srv.URL, "protect", "sec-1", "--on")
	for _, want := range []string{"hadron secret change get 'r1; touch pwned' --wait", "hadron secret change withdraw 'r1; touch pwned'"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("hint lacks the quoted id %q:\n%s", want, stderr)
		}
	}
	const escID = "r\x1b[2Jz"
	srv2, _ := fake2738(t, only2738(200, gqlErr2738("SECRET_PROTECTED_NEEDS_PASSKEY", map[string]any{"requestId": escID, "ceremonyUrl": url2738})))
	_, stderr2, _ := run2738(t, srv2.URL, "protect", "sec-1", "--on")
	if strings.ContainsRune(stderr2, 0x1b) {
		t.Errorf("an id with a control character must be neutralised in text: %q", stderr2)
	}
	stdout, errOut, _ := run2738(t, srv2.URL, "protect", "sec-1", "--on", "--json")
	if e := envelope2738(t, stdout, errOut); e["requestId"] != escID {
		t.Errorf("--json carries the id exactly: %q", e["requestId"])
	}
}

// SEC10 P3-1: change get neutralises server-supplied fields in text rows.
func TestChangeGetNeutralisesServerFieldsInText2738(t *testing.T) {
	row := `{"data":{"secretChangeRequest":{"id":"r\u001b[2J","state":"open\u001b[2J","operation":"op\u001b[2J","createdAt":"c\u001b[2J","expiresAt":"e\u001b[2J"}}}`
	srv, _ := fake2738(t, only2738(200, row))
	stdout, _, code := run2738(t, srv.URL, "change", "get", "r1")
	if code != 0 || strings.ContainsRune(stdout, 0x1b) {
		t.Errorf("text rows must be neutralised: exit %d %q", code, stdout)
	}
}

// Jane's LOCAL F1 (P2) on cli#2738: the fallback and non-200 paths of the
// protected-change errors repeated the server's message, which can carry a
// submitted value. The server message here CONTAINS the canary (gqlErr2738), so a
// path that echoes it fails. Exit and server code are kept; text and --json are
// both checked, through the real renderer.
func TestProtectedChangeErrorsNeverEchoTheServerMessage2738(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		serv   string
	}{
		{"limit 200", 200, gqlErr2738("SECRET_CHANGE_REQUEST_LIMIT", nil), "SECRET_CHANGE_REQUEST_LIMIT"},
		{"limit 502", 502, gqlErr2738("SECRET_CHANGE_REQUEST_LIMIT", nil), "SECRET_CHANGE_REQUEST_LIMIT"},
		{"staged code 502", 502, staged2738(), "SECRET_PROTECTED_NEEDS_PASSKEY"},
		{"limit 502 beside staged", 502, gqlErr2738("SECRET_PROTECTED_NEEDS_PASSKEY", map[string]any{"requestId": req2738}), "SECRET_PROTECTED_NEEDS_PASSKEY"},
	}
	for _, c := range cases {
		for _, door := range []string{"update", "set"} {
			for _, asJSON := range []bool{false, true} {
				t.Run(c.name+"/"+door+map[bool]string{false: "/text", true: "/json"}[asJSON], func(t *testing.T) {
					srv, calls := fake2738(t, only2738(c.status, c.body))
					args := []string{door, "sec-1", "--value-file", valueFile2738(t)}
					if asJSON {
						args = append(args, "--json")
					}
					stdout, stderr, code := run2738(t, srv.URL, args...)
					if code == 0 {
						t.Fatalf("must fail\n%s%s", stdout, stderr)
					}
					if strings.Contains(stdout+stderr, value2738) {
						t.Errorf("the server's message (which carries the value) must never be echoed:\n%s%s", stdout, stderr)
					}
					if got := ops2738(calls); len(got) != 1 || got[0] != "UpdateSecret" {
						t.Errorf("one mutation, never retried: %v", got)
					}
					noCeremonyRequest2738(t, calls)
					if asJSON {
						if e := envelope2738(t, stdout, stderr); e["serverCode"] != c.serv {
							t.Errorf("the server code is kept for scripts: %v", e)
						}
					}
				})
			}
		}
	}
	// The exit class of a definite limit refusal is kept: 5.
	srv, _ := fake2738(t, only2738(200, gqlErr2738("SECRET_CHANGE_REQUEST_LIMIT", nil)))
	if _, _, code := run2738(t, srv.URL, "update", "sec-1", "--meta", "k=v"); code != exitcode.Conflict {
		t.Errorf("limit refusal exit = %d, want 5", code)
	}
}
