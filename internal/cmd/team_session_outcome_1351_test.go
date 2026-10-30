package cmd

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// cli#1351: `team session outcome` reports a session's outcome through the
// hadron-server#2896 mutation. One operation; the range check, the owner gate
// and the MCP-only OAuth refusal are the server's, so the client sends any
// finite number and maps the answer.

const (
	session1351  = "01234567-89ab-4cde-8f01-23456789abcd"
	recorded1351 = `{"data":{"recordSessionOutcome":{"id":"` + session1351 + `","outcome":0.8,"outcomeRef":"T-1","outcomeMeta":{"a":1}}}}`
)

func runOutcome1351(t *testing.T, srv *httptest.Server, args ...string) keyRun {
	t.Helper()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{"team", "session", "outcome"}, args...), "--server", srv.URL))
	err := root.Execute()
	return keyRun{out: out.String(), errOut: f.IOStreams.ErrOut.(*strings.Builder).String(), err: err}
}

// Without --ref and --meta neither is sent, so the server keeps what it has.
func TestSessionOutcomeSendsOneOperationAndOmitsUnsetFields(t *testing.T) {
	srv, _, reqs := keyServerCapture(t, 200, `{"data":{"recordSessionOutcome":{"id":"`+session1351+`","outcome":0.8,"outcomeRef":null,"outcomeMeta":null}}}`)
	r := runOutcome1351(t, srv, session1351, "0.8", "--json")
	if r.err != nil {
		t.Fatalf("execute: %v", r.err)
	}
	vars := onlyOp(t, reqs(), "RecordSessionOutcome")
	if len(vars) != 2 || vars["sessionId"] != session1351 || vars["outcome"] != 0.8 {
		t.Errorf("variables = %v, want exactly sessionId and outcome 0.8", vars)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(r.out), &m); err != nil {
		t.Fatalf("json: %v %q", err, r.out)
	}
	if m["sessionId"] != session1351 || m["outcome"] != 0.8 {
		t.Errorf("json = %v", m)
	}
	for _, k := range []string{"outcomeRef", "outcomeMeta"} {
		if v, ok := m[k]; !ok || v != nil {
			t.Errorf("%s = %v (present %v), want null", k, v, ok)
		}
	}
}

// An integer outcome still travels as a number, and --ref and --meta are sent
// as given: the ref as a string, the meta as JSON, not as a string of JSON.
func TestSessionOutcomeSendsRefAndMeta(t *testing.T) {
	srv, _, reqs := keyServerCapture(t, 200, recorded1351)
	r := runOutcome1351(t, srv, session1351, "1", "--ref", "T-1", "--meta", `{"a":1}`, "--json")
	if r.err != nil {
		t.Fatalf("execute: %v", r.err)
	}
	vars := onlyOp(t, reqs(), "RecordSessionOutcome")
	if vars["outcome"] != 1.0 || vars["outcomeRef"] != "T-1" {
		t.Errorf("variables = %v", vars)
	}
	if meta, ok := vars["outcomeMeta"].(map[string]any); !ok || meta["a"] != 1.0 {
		t.Errorf("outcomeMeta = %#v, want the object {\"a\":1}", vars["outcomeMeta"])
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(r.out), &m); err != nil || m["outcomeRef"] != "T-1" {
		t.Fatalf("json = %v %q", err, r.out)
	}
	if meta, ok := m["outcomeMeta"].(map[string]any); !ok || meta["a"] != 1.0 {
		t.Errorf("--json outcomeMeta = %#v", m["outcomeMeta"])
	}
}

func TestSessionOutcomeTextIsOneLine(t *testing.T) {
	srv, _ := keyServer(t, 200, recorded1351)
	r := runOutcome1351(t, srv, session1351, "0.8")
	if r.err != nil {
		t.Fatalf("execute: %v", r.err)
	}
	if lines := strings.Split(strings.TrimRight(r.out, "\n"), "\n"); len(lines) != 1 || !strings.Contains(lines[0], "0.8") || !strings.Contains(lines[0], session1351) {
		t.Errorf("text = %q, want one line naming the outcome and the session", r.out)
	}
}

// What the client can tell without the server is refused locally, with
// nothing sent: a malformed --meta, an outcome that is not a finite number,
// an empty session id.
func TestSessionOutcomeRefusesMalformedInputLocally(t *testing.T) {
	srv, n := keyServer(t, 200, recorded1351)
	for name, args := range map[string][]string{
		"malformed meta": {session1351, "0.5", "--meta", `{"a":`},
		"word outcome":   {session1351, "high"},
		"NaN outcome":    {session1351, "NaN"},
		"Inf outcome":    {session1351, "Inf"},
		"empty session":  {" ", "0.5"},
	} {
		t.Run(name, func(t *testing.T) {
			r := runOutcome1351(t, srv, args...)
			if r.err == nil || exitCodeFor(r.err) != 2 {
				t.Fatalf("want exit 2, got %v (%d)", r.err, exitCodeFor(r.err))
			}
		})
	}
	if n.Load() != 0 {
		t.Errorf("%d request(s) sent, want none", n.Load())
	}
}

// The range is the server's rule: an outcome outside 0..1 is sent, and the
// server's refusal is what the caller sees.
func TestSessionOutcomeLeavesTheRangeToTheServer(t *testing.T) {
	srv, n := keyServer(t, 200, `{"errors":[{"message":"outcome must be a number between 0 and 1","extensions":{"code":"BAD_USER_INPUT"}}],"data":null}`)
	r := runOutcome1351(t, srv, session1351, "1.5")
	if n.Load() != 1 {
		t.Fatalf("%d request(s) sent, want the one the server judges", n.Load())
	}
	if r.err == nil || exitCodeFor(r.err) != 2 || !strings.Contains(r.err.Error(), "between 0 and 1") {
		t.Errorf("want the server's refusal at exit 2, got %v (%d)", r.err, exitCodeFor(r.err))
	}
}

// Each server refusal keeps its typed exit code and its serverCode in --json.
func TestSessionOutcomeRefusalsKeepServerCode(t *testing.T) {
	for _, tc := range []struct {
		name, body, code string
		exit             int
	}{
		{"out of range", `{"message":"outcome must be a number between 0 and 1","extensions":{"code":"BAD_USER_INPUT"}}`, "BAD_USER_INPUT", 2},
		{"missing session", `{"message":"Session not found: x","extensions":{"code":"BAD_USER_INPUT"}}`, "BAD_USER_INPUT", 2},
		{"not the owner", `{"message":"Not authorized","extensions":{"code":"FORBIDDEN"}}`, "FORBIDDEN", 8},
		{"no credential", `{"message":"A credential is required","extensions":{"code":"UNAUTHENTICATED"}}`, "UNAUTHENTICATED", 3},
		{"MCP-only OAuth", `{"message":"This OAuth credential is limited to the MCP surface.","extensions":{"code":"FORBIDDEN","reason":"OAUTH_SCOPE_INSUFFICIENT"}}`, "FORBIDDEN", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := keyServer(t, 200, `{"errors":[`+tc.body+`],"data":null}`)
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"team", "session", "outcome", session1351, "0.5", "--json", "--server", srv.URL})
			err := root.Execute()
			if err == nil {
				t.Fatal("refusal succeeded")
			}
			if code := renderError(f, err); code != tc.exit {
				t.Errorf("exit %d want %d", code, tc.exit)
			}
			var env struct {
				Error struct {
					Code       int    `json:"code"`
					ServerCode string `json:"serverCode"`
				} `json:"error"`
			}
			if e := json.Unmarshal([]byte(out.String()), &env); e != nil {
				t.Fatal(e, out.String())
			}
			if env.Error.ServerCode != tc.code || env.Error.Code != tc.exit {
				t.Errorf("envelope %s", out.String())
			}
		})
	}
}

// The help says who may run it: the session's owner, for any session they
// own, not only the worktree's bound one.
func TestSessionOutcomeHelpSaysWhoMayRunIt(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "outcome", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	help := out.String()
	for _, want := range []string{"owner", "App key", "not only", "exit 8"} {
		if !strings.Contains(help, want) {
			t.Errorf("help lacks %q:\n%s", want, help)
		}
	}
}
