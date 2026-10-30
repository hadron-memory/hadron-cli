package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#231 (registry writes): `hadron ha create|update|rm`. The access token is
// write-only: it is read from stdin or a file, never argv, and must not appear
// in any output, success or failure. Each command sends exactly one write.

const (
	haToken231   = "hat_SENTINEL_9f3c71"
	haCreated231 = `{"data":{"createHomeAssistantInstance":` + haInstanceJSON + `}}`
	haUpdated231 = `{"data":{"updateHomeAssistantInstance":` + haInstanceJSON + `}}`
)

// runHAWrite drives `hadron ha <args>` with stdin set to in.
func runHAWrite(t *testing.T, srv *httptest.Server, in string, args ...string) keyRun {
	t.Helper()
	f, out := testFactory(t)
	f.IOStreams.In = strings.NewReader(in)
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{"ha"}, args...), "--server", srv.URL))
	err := root.Execute()
	return keyRun{out: out.String(), errOut: f.IOStreams.ErrOut.(*strings.Builder).String(), err: err}
}

func assertNoHAToken(t *testing.T, r keyRun) {
	t.Helper()
	if strings.Contains(r.all(), haToken231) {
		t.Fatalf("access token leaked: out=%q err=%q error=%v", r.out, r.errOut, r.err)
	}
}

var haCreateArgs231 = []string{"create", "--org", "acme.com", "--slug", "home", "--name", "Home", "--url", "https://ha.example.com"}

// The token comes from stdin; only the given fields are sent, so the server's
// defaults (every op, enabled) apply. The answer is the sanitized registry row.
func TestHaCreateSendsOneWriteWithTheTokenFromStdin(t *testing.T) {
	srv, _, reqs := keyServerCapture(t, 200, haCreated231)
	r := runHAWrite(t, srv, haToken231+"\n", append(haCreateArgs231, "--token-file", "-", "--json")...)
	if r.err != nil {
		t.Fatalf("execute: %v", r.err)
	}
	vars := onlyOp(t, reqs(), "CreateHomeAssistantInstance")
	want := map[string]any{"orgRef": "acme.com", "slug": "home", "name": "Home", "url": "https://ha.example.com", "accessToken": haToken231}
	if len(vars) != len(want) {
		t.Errorf("variables = %v, want exactly %v", vars, want)
	}
	for k, v := range want {
		if vars[k] != v {
			t.Errorf("%s = %v, want %v", k, vars[k], v)
		}
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(r.out), &m); err != nil {
		t.Fatalf("json: %v %q", err, r.out)
	}
	if m["id"] != "ha1" || m["slug"] != "home" {
		t.Errorf("json = %v", m)
	}
	assertNoHAToken(t, r)
}

// Piped stdin is read without --token-file, and a file works as @path.
func TestHaCreateReadsTheTokenFromPipeOrFile(t *testing.T) {
	srv, _, reqs := keyServerCapture(t, 200, haCreated231)
	r := runHAWrite(t, srv, haToken231, haCreateArgs231...)
	if r.err != nil {
		t.Fatalf("piped: %v", r.err)
	}
	if v := onlyOp(t, reqs(), "CreateHomeAssistantInstance"); v["accessToken"] != haToken231 {
		t.Errorf("piped token not sent: %v", v["accessToken"])
	}
	assertNoHAToken(t, r)

	p := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(p, []byte(haToken231+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv2, _, reqs2 := keyServerCapture(t, 200, haCreated231)
	r = runHAWrite(t, srv2, "", append(haCreateArgs231, "--token-file", "@"+p)...)
	if r.err != nil {
		t.Fatalf("file: %v", r.err)
	}
	if v := onlyOp(t, reqs2(), "CreateHomeAssistantInstance"); v["accessToken"] != haToken231 {
		t.Errorf("file token not sent: %v", v["accessToken"])
	}
	assertNoHAToken(t, r)
}

// --allow-op restricts the ops; --disabled sends a real false.
func TestHaCreateSendsAllowOpsAndDisabled(t *testing.T) {
	srv, _, reqs := keyServerCapture(t, 200, haCreated231)
	r := runHAWrite(t, srv, haToken231, append(haCreateArgs231, "--allow-op", "get_state", "--allow-op", "list_states", "--disabled")...)
	if r.err != nil {
		t.Fatalf("execute: %v", r.err)
	}
	vars := onlyOp(t, reqs(), "CreateHomeAssistantInstance")
	ops, _ := vars["opAllowlist"].([]any)
	if len(ops) != 2 || ops[0] != "get_state" || ops[1] != "list_states" {
		t.Errorf("opAllowlist = %v", vars["opAllowlist"])
	}
	if v, ok := vars["enabled"]; !ok || v != false {
		t.Errorf("enabled = %v (present %v), want false", v, ok)
	}
}

// No token, no request. There is no argv flag for the token at all.
func TestHaCreateRefusesAMissingOrArgvTokenBeforeAnyRequest(t *testing.T) {
	for name, args := range map[string][]string{
		"empty stdin":             haCreateArgs231,
		"argv token":              append(append([]string{}, haCreateArgs231...), "--token", haToken231),
		"no org":                  {"create", "--slug", "home", "--name", "Home", "--url", "https://ha.example.com"},
		"empty token-file source": append(append([]string{}, haCreateArgs231...), "--token-file="),
	} {
		t.Run(name, func(t *testing.T) {
			srv, n := keyServer(t, 200, haCreated231)
			stdin := ""
			if name == "no org" || name == "empty token-file source" {
				stdin = haToken231
			}
			r := runHAWrite(t, srv, stdin, args...)
			if r.err == nil || exitCodeFor(r.err) != exitcode.Usage {
				t.Errorf("want exit 2, got %v", r.err)
			}
			if n.Load() != 0 {
				t.Errorf("%d request(s) sent", n.Load())
			}
			assertNoHAToken(t, r)
		})
	}
}

// A server refusal keeps its exit code and serverCode, and still never shows
// the token.
func TestHaWriteRefusalsKeepServerCodeAndLeakNoToken(t *testing.T) {
	for _, tc := range []struct {
		name, body, code string
		exit             int
		args             []string
	}{
		{"create not admin", `{"message":"Forbidden","extensions":{"code":"FORBIDDEN"}}`, "FORBIDDEN", 8, haCreateArgs231},
		{"create bad url", `{"message":"url must be https","extensions":{"code":"BAD_USER_INPUT"}}`, "BAD_USER_INPUT", 2, haCreateArgs231},
		{"replace token on a foreign row", `{"message":"Home Assistant instance not found","extensions":{"code":"NOT_FOUND"}}`, "NOT_FOUND", 4, []string{"update", "ha1", "--token-file", "-"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, n := keyServer(t, 200, `{"errors":[`+tc.body+`],"data":null}`)
			f, out := testFactory(t)
			f.IOStreams.In = strings.NewReader(haToken231)
			root := NewRootCmd(f)
			root.SetArgs(append(append([]string{"ha"}, tc.args...), "--json", "--server", srv.URL))
			err := root.Execute()
			if err == nil {
				t.Fatal("refusal succeeded")
			}
			if code := renderError(f, err); code != tc.exit {
				t.Errorf("exit %d want %d", code, tc.exit)
			}
			if n.Load() != 1 {
				t.Errorf("%d request(s), want 1", n.Load())
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
			assertNoHAToken(t, keyRun{out: out.String(), errOut: f.IOStreams.ErrOut.(*strings.Builder).String(), err: err})
		})
	}
}

// A null payload is not a success.
func TestHaCreateAndUpdateNullAnswerIsAnError(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		args []string
	}{
		"create": {`{"data":{"createHomeAssistantInstance":null}}`, haCreateArgs231},
		"update": {`{"data":{"updateHomeAssistantInstance":null}}`, []string{"update", "ha1", "--name", "x"}},
	} {
		t.Run(name, func(t *testing.T) {
			srv, _ := keyServer(t, 200, tc.body)
			r := runHAWrite(t, srv, haToken231, tc.args...)
			if r.err == nil || exitCodeFor(r.err) == 0 {
				t.Fatalf("a null answer must fail, got %v", r.err)
			}
			if strings.Contains(r.out, "✓") {
				t.Errorf("no success claim: %q", r.out)
			}
		})
	}
}

// A request that never gets an answer leaves the outcome unknown: sent once,
// not retried, no success, and the guidance says to read the registry back.
func TestHaWriteLostResponseIsOutcomeUnknownAndNotRetried(t *testing.T) {
	for name, args := range map[string][]string{
		"create": haCreateArgs231,
		"update": {"update", "ha1", "--disabled"},
		"rm":     {"rm", "ha1", "--yes"},
	} {
		t.Run(name, func(t *testing.T) {
			var posts atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					_, _ = w.Write([]byte(`{"capabilities":[]}`))
					return
				}
				posts.Add(1)
				if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
					_ = conn.Close()
				}
			}))
			t.Cleanup(srv.Close)
			r := runHAWrite(t, srv, haToken231, args...)
			if exitCodeFor(r.err) != exitcode.Unavailable {
				t.Fatalf("want Unavailable, got %v", r.err)
			}
			if !strings.Contains(r.err.Error(), "outcome unknown") || !strings.Contains(r.err.Error(), "hadron ha ") {
				t.Errorf("must say the outcome is unknown and how to check: %v", r.err)
			}
			if posts.Load() != 1 {
				t.Errorf("sent %d times, want once", posts.Load())
			}
			if strings.Contains(r.out, "✓") {
				t.Errorf("no success claim: %q", r.out)
			}
			assertNoHAToken(t, r)
		})
	}
}

// Update sends only what was named; an omitted field is not sent, so the
// server keeps it.
func TestHaUpdateSendsOnlyTheNamedFields(t *testing.T) {
	srv, _, reqs := keyServerCapture(t, 200, haUpdated231)
	r := runHAWrite(t, srv, "", "update", "ha1", "--name", "Cabin", "--json")
	if r.err != nil {
		t.Fatalf("execute: %v", r.err)
	}
	vars := onlyOp(t, reqs(), "UpdateHomeAssistantInstance")
	if len(vars) != 2 || vars["ref"] != "ha1" || vars["name"] != "Cabin" {
		t.Errorf("variables = %v, want exactly ref and name", vars)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(r.out), &m); err != nil || m["id"] != "ha1" {
		t.Errorf("json = %v %q", err, r.out)
	}
}

// --enabled and --disabled send real booleans; --clear-allow-ops sends an
// explicit [] (every catalog op); --allow-op replaces the list.
func TestHaUpdateSendsBooleansAndTheAllowlist(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		key  string
		want string
	}{
		{"enabled", []string{"--enabled"}, "enabled", `true`},
		{"disabled", []string{"--disabled"}, "enabled", `false`},
		{"clear allowlist", []string{"--clear-allow-ops"}, "opAllowlist", `[]`},
		{"replace allowlist", []string{"--allow-op", "get_state"}, "opAllowlist", `["get_state"]`},
		{"url", []string{"--url", "https://ha2.example.com"}, "url", `"https://ha2.example.com"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _, reqs := keyServerCapture(t, 200, haUpdated231)
			r := runHAWrite(t, srv, "", append([]string{"update", "ha1"}, tc.args...)...)
			if r.err != nil {
				t.Fatalf("execute: %v", r.err)
			}
			vars := onlyOp(t, reqs(), "UpdateHomeAssistantInstance")
			if len(vars) != 2 {
				t.Errorf("variables = %v, want ref and %s only", vars, tc.key)
			}
			got, _ := json.Marshal(vars[tc.key])
			if _, ok := vars[tc.key]; !ok || string(got) != tc.want {
				t.Errorf("%s = %s (present %v), want %s", tc.key, got, ok, tc.want)
			}
		})
	}
}

// --token-file replaces the token in the same single write; nothing reads the
// old one, and the new one is never printed.
func TestHaUpdateReplacesTheTokenWithoutPrintingIt(t *testing.T) {
	srv, _, reqs := keyServerCapture(t, 200, haUpdated231)
	r := runHAWrite(t, srv, haToken231+"\n", "update", "ha1", "--token-file", "-", "--json")
	if r.err != nil {
		t.Fatalf("execute: %v", r.err)
	}
	vars := onlyOp(t, reqs(), "UpdateHomeAssistantInstance")
	if len(vars) != 2 || vars["accessToken"] != haToken231 {
		t.Errorf("variables = %v", vars)
	}
	assertNoHAToken(t, r)
}

// Without --token-file, update never reads stdin for a token: an agent's piped
// stdin must not silently replace the stored one.
func TestHaUpdateWithoutTokenFileLeavesTheTokenAlone(t *testing.T) {
	srv, _, reqs := keyServerCapture(t, 200, haUpdated231)
	r := runHAWrite(t, srv, haToken231, "update", "ha1", "--disabled")
	if r.err != nil {
		t.Fatalf("execute: %v", r.err)
	}
	if _, ok := onlyOp(t, reqs(), "UpdateHomeAssistantInstance")["accessToken"]; ok {
		t.Error("accessToken sent without --token-file")
	}
}

// Usage refusals send nothing: an empty patch, contradictory flags, a slug
// change, a token on argv and an empty token.
func TestHaUpdateUsageRefusalsSendNothing(t *testing.T) {
	for name, args := range map[string][]string{
		"empty patch":           {"update", "ha1"},
		"enabled and disabled":  {"update", "ha1", "--enabled", "--disabled"},
		"allow and clear":       {"update", "ha1", "--allow-op", "get_state", "--clear-allow-ops"},
		"slug is immutable":     {"update", "ha1", "--slug", "x"},
		"argv token":            {"update", "ha1", "--token", haToken231},
		"empty token from file": {"update", "ha1", "--token-file", "-"},
	} {
		t.Run(name, func(t *testing.T) {
			srv, n := keyServer(t, 200, haUpdated231)
			r := runHAWrite(t, srv, "", args...)
			if r.err == nil || exitCodeFor(r.err) != exitcode.Usage {
				t.Errorf("want exit 2, got %v", r.err)
			}
			if n.Load() != 0 {
				t.Errorf("%d request(s) sent", n.Load())
			}
			assertNoHAToken(t, r)
		})
	}
}

func TestHaRmSendsOneDeleteWithYes(t *testing.T) {
	srv, _, reqs := keyServerCapture(t, 200, `{"data":{"deleteHomeAssistantInstance":true}}`)
	r := runHAWrite(t, srv, "", "rm", "ha1", "--yes", "--json")
	if r.err != nil {
		t.Fatalf("execute: %v", r.err)
	}
	vars := onlyOp(t, reqs(), "DeleteHomeAssistantInstance")
	if len(vars) != 1 || vars["ref"] != "ha1" {
		t.Errorf("variables = %v", vars)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(r.out), &m); err != nil || m["id"] != "ha1" || m["status"] != "deleted" {
		t.Errorf("json = %v %q", err, r.out)
	}
}

// Non-interactive without --yes, or a declined prompt, sends no mutation.
func TestHaRmWithoutConfirmationSendsNothing(t *testing.T) {
	srv, n := keyServer(t, 200, `{"data":{"deleteHomeAssistantInstance":true}}`)
	r := runHAWrite(t, srv, "", "rm", "ha1")
	if r.err == nil {
		t.Error("non-interactive rm without --yes succeeded")
	}
	f, out, _ := testFactoryTTY(t, "n\n")
	root := NewRootCmd(f)
	root.SetArgs([]string{"ha", "rm", "ha1", "--server", srv.URL})
	if err := root.Execute(); err == nil {
		t.Error("declined rm succeeded")
	}
	if n.Load() != 0 {
		t.Errorf("%d request(s) sent", n.Load())
	}
	if strings.Contains(out.String(), "✓") {
		t.Errorf("no success claim: %q", out.String())
	}
}

// false is not a delete; NOT_FOUND keeps its code (missing and foreign rows
// are concealed alike).
func TestHaRmFalseOrNotFoundIsNotSuccess(t *testing.T) {
	srv, _ := keyServer(t, 200, `{"data":{"deleteHomeAssistantInstance":false}}`)
	r := runHAWrite(t, srv, "", "rm", "ha1", "--yes")
	if r.err == nil || exitCodeFor(r.err) == 0 || strings.Contains(r.out, "✓") {
		t.Errorf("false must not read as a delete: %v %q", r.err, r.out)
	}
	srv2, _ := keyServer(t, 200, `{"errors":[{"message":"Home Assistant instance not found","extensions":{"code":"NOT_FOUND"}}],"data":null}`)
	r = runHAWrite(t, srv2, "", "rm", "ha1", "--yes")
	if exitCodeFor(r.err) != exitcode.NotFound {
		t.Errorf("want exit 4, got %v", r.err)
	}
}

// The help says what the dangerous choices mean.
func TestHaWriteHelpStatesTheSemantics(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"update", "--help"}, []string{"--clear-allow-ops", "every op", "--token-file"}},
		{[]string{"rm", "--help"}, []string{"ha__<slug>__<op>", "fail"}},
		{[]string{"create", "--help"}, []string{"--token-file", "never"}},
	} {
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append([]string{"ha"}, tc.args...))
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		for _, w := range tc.want {
			if !strings.Contains(out.String(), w) {
				t.Errorf("ha %v help lacks %q", tc.args, w)
			}
		}
	}
}

// --token-file="$F" with F unset is an explicitly empty source, not "read
// piped stdin": that would replace the token with whatever an agent happened
// to pipe in. Refused with nothing sent, even with a token on stdin.
func TestHaUpdateEmptyTokenFileSourceIsRefusedWithPipedStdin(t *testing.T) {
	srv, n := keyServer(t, 200, haUpdated231)
	r := runHAWrite(t, srv, haToken231, "update", "ha1", "--token-file=")
	if r.err == nil || exitCodeFor(r.err) != exitcode.Usage {
		t.Errorf("want exit 2, got %v", r.err)
	}
	if n.Load() != 0 {
		t.Errorf("%d request(s) sent", n.Load())
	}
	assertNoHAToken(t, r)
}
