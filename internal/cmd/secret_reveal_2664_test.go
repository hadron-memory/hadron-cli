package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/hadron-memory/hadron-cli/internal/secretfile"
)

// cli#2664 slice 2: `secret exec` and `secret reveal`. Fake HTTP only; the exec
// tests run a real child process (sh), so they skip on windows.

const sentinel2664 = "S3CRET-SENTINEL-2664"

func revealJSON2664(valueJSONText string, readsLeft string) string {
	return `{"data":{"revealSecret":{"secretName":"deploy-token","value":` + mustJSON2631(valueJSONText) + `,"readsLeft":` + readsLeft + `}}}`
}

// sentinelReveal2664 reveals the sentinel as a JSON string value (the value text is
// a quoted JSON string).
var sentinelReveal2664 = revealJSON2664(mustJSON2631(sentinel2664), "1")

type revealRun2664 struct {
	stdout, stderr string
	err            error
}

func runSecretReveal2664(t *testing.T, srvURL string, tty bool, args ...string) revealRun2664 {
	t.Helper()
	f, out := testFactory(t)
	var errOut strings.Builder
	if tty {
		io, _, _ := output.TestOutputTerminal()
		io.Out, io.ErrOut = output.Tracked(out), &errOut
		// A terminal stdout cannot be a plain builder for the exec child, but reveal
		// writes through the io stream directly.
		f.IOStreams = io
		f.IOStreams.Out = out
	} else {
		f.IOStreams.ErrOut = &errOut
	}
	root := NewRootCmd(f)
	// --server goes BEFORE the subcommand: after `--` it would be the child's argument.
	root.SetArgs(append([]string{"--server", srvURL, "secret"}, args...))
	err := root.Execute()
	return revealRun2664{out.String(), errOut.String(), err}
}

func requireNoValue2664(t *testing.T, where string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if strings.Contains(p, sentinel2664) {
			t.Errorf("the revealed value must not appear in %s: %q", where, p)
		}
	}
}

func skipWindows2664(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("runs a real sh child")
	}
}

func revealCalls2664(calls *[]attnCall) int {
	n := 0
	for _, c := range *calls {
		if c.Op == "RevealSecret" {
			n++
		}
	}
	return n
}

// Acceptance 10: the value reaches the child and appears in no output or error
// of the CLI; exactly one RevealSecret is sent with the client id; text exec
// prints nothing of its own.
func TestSecretExecDeliversTheValueToTheChildOnly2664(t *testing.T) {
	skipWindows2664(t)
	srv, calls := attnServer(t, map[string]string{"RevealSecret": sentinelReveal2664})
	file := filepath.Join(t.TempDir(), "f")
	r := runSecretReveal2664(t, srv.URL, false, "exec", "--client", "cl-1", "--env", "TOKEN", "--", "sh", "-c", `printf %s "$TOKEN" > "$1"`, "sh", file)
	if r.err != nil {
		t.Fatal(r.err)
	}
	if b, _ := os.ReadFile(file); string(b) != sentinel2664 {
		t.Fatalf("the child must get the value in its environment: %q", b)
	}
	requireNoValue2664(t, "stdout/stderr", r.stdout, r.stderr)
	if r.stdout != "" {
		t.Errorf("exec prints nothing of its own: %q", r.stdout)
	}
	if revealCalls2664(calls) != 1 || (*calls)[0].Vars["clientId"] != "cl-1" {
		t.Fatalf("want exactly one RevealSecret for cl-1: %+v", *calls)
	}
}

// The child's own status is hadron's exit code once it has started; a signal
// gives 128+n; nothing of the value leaks on the way.
func TestSecretExecExitsWithTheChildsStatus2664(t *testing.T) {
	skipWindows2664(t)
	for name, tc := range map[string]struct {
		script string
		want   int
	}{
		"zero":                     {`exit 0`, 0},
		"seven":                    {`exit 7`, 7},
		"signalled":                {`kill -TERM $$`, 143},
		"looks like a hadron code": {`exit 2`, 2},
	} {
		t.Run(name, func(t *testing.T) {
			srv, calls := attnServer(t, map[string]string{"RevealSecret": sentinelReveal2664})
			r := runSecretReveal2664(t, srv.URL, false, "exec", "--client", "cl-1", "--env", "TOKEN", "--", "sh", "-c", tc.script)
			if exitCodeFor(r.err) != tc.want {
				t.Fatalf("exit=%d want=%d err=%v", exitCodeFor(r.err), tc.want, r.err)
			}
			errText := ""
			if r.err != nil {
				errText = r.err.Error()
			}
			requireNoValue2664(t, "output or error", r.stdout, r.stderr, errText)
			if revealCalls2664(calls) != 1 {
				t.Errorf("one reveal per process: %d", revealCalls2664(calls))
			}
		})
	}
}

// A child that cannot start after the read was spent: exit 1, says one read was
// spent and the value was not delivered, and never includes the value.
func TestSecretExecChildThatCannotStartAfterTheRead2664(t *testing.T) {
	skipWindows2664(t)
	bad := filepath.Join(t.TempDir(), "broken")
	if err := os.WriteFile(bad, []byte("#!/nonexistent/interpreter\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	srv, calls := attnServer(t, map[string]string{"RevealSecret": sentinelReveal2664})
	r := runSecretReveal2664(t, srv.URL, false, "exec", "--client", "cl-1", "--env", "TOKEN", "--", bad)
	if exitCodeFor(r.err) != 1 || r.err == nil || !strings.Contains(r.err.Error(), "one read was spent") || !strings.Contains(r.err.Error(), "not delivered") {
		t.Fatalf("exit=%d err=%v", exitCodeFor(r.err), r.err)
	}
	requireNoValue2664(t, "output or error", r.stdout, r.stderr, r.err.Error())
	if revealCalls2664(calls) != 1 {
		t.Errorf("one reveal: %d", revealCalls2664(calls))
	}
}

// Acceptance 11: every local check runs before the call (a successful reveal
// spends a read): exit 2, no request.
func TestSecretExecValidatesBeforeTheCall2664(t *testing.T) {
	skipWindows2664(t)
	for name, args := range map[string][]string{
		"bad env name":      {"exec", "--client", "c", "--env", "1BAD", "--", "true"},
		"env with dash":     {"exec", "--client", "c", "--env", "A-B", "--", "true"},
		"no command":        {"exec", "--client", "c", "--env", "T", "--"},
		"no dash":           {"exec", "--client", "c", "--env", "T"},
		"blank client":      {"exec", "--client", "  ", "--env", "T", "--", "true"},
		"arg before dash":   {"exec", "stray", "--client", "c", "--env", "T", "--", "true"},
		"command not found": {"exec", "--client", "c", "--env", "T", "--", "no-such-command-2664"},
		"json refused":      {"exec", "--client", "c", "--env", "T", "--json", "--", "true"},
	} {
		t.Run(name, func(t *testing.T) {
			srv, calls := attnServer(t, map[string]string{"RevealSecret": sentinelReveal2664})
			r := runSecretReveal2664(t, srv.URL, false, args...)
			if exitCodeFor(r.err) != 2 || len(*calls) != 0 {
				t.Fatalf("exit=%d calls=%d err=%v", exitCodeFor(r.err), len(*calls), r.err)
			}
		})
	}
}

// Acceptance 12: a second reveal of a one-use client is SPENT: exit 5 with the
// server code, no child, no value.
func TestSecretExecRefusalStartsNoChild2664(t *testing.T) {
	skipWindows2664(t)
	for code, want := range map[string]int{
		"SECRET_READ_CLIENT_SPENT": 5, "SECRET_READ_CLIENT_EXPIRED": 5, "SECRET_READ_CLIENT_PENDING": 5,
		"SECRET_READ_CLIENT_NOT_STARTED": 5, "SECRET_READ_CLIENT_REVOKED": 5, "SECRET_READ_CLIENT_ISSUER_GONE": 5,
		"SECRET_DELETED": 5, "FORBIDDEN": 8, "SECRET_REVEAL_ONE_PER_OPERATION": 2,
	} {
		t.Run(code, func(t *testing.T) {
			srv, calls := attnServer(t, map[string]string{"RevealSecret": gqlErrorJSON(code)})
			file := filepath.Join(t.TempDir(), "ran")
			r := runSecretReveal2664(t, srv.URL, false, "exec", "--client", "cl-1", "--env", "TOKEN", "--", "sh", "-c", `echo ran > "$1"`, "sh", file)
			if exitCodeFor(r.err) != want || revealCalls2664(calls) != 1 {
				t.Fatalf("exit=%d want=%d calls=%d err=%v", exitCodeFor(r.err), want, revealCalls2664(calls), r.err)
			}
			if _, err := os.Stat(file); err == nil {
				t.Errorf("no child may start after a refusal")
			}
			if strings.Contains(r.err.Error(), "may have been spent") {
				t.Errorf("a refusal with a server code is not a lost answer: %v", r.err)
			}
		})
	}
}

// Acceptance 14: with no bound session the server's NOT_FOUND is exit 4 and one
// uniform sentence, which says nothing of the cause. Bound: the session header is
// sent; unbound: no header and a stderr note.
func TestSecretExecPersonGetsTheUniformAnswer2664(t *testing.T) {
	skipWindows2664(t)
	srv, calls := attnServer(t, map[string]string{"RevealSecret": gqlErrorJSON("SECRET_READ_CLIENT_NOT_FOUND")})
	r := runSecretReveal2664(t, srv.URL, false, "exec", "--client", "cl-1", "--env", "TOKEN", "--", "true")
	if exitCodeFor(r.err) != 4 || revealCalls2664(calls) != 1 || (*calls)[0].Session != "" {
		t.Fatalf("exit=%d calls=%d err=%v", exitCodeFor(r.err), revealCalls2664(calls), r.err)
	}
	if !strings.Contains(r.err.Error(), "revealed by the worker it names") || !strings.Contains(r.err.Error(), "passkey page") {
		t.Errorf("the uniform sentence is missing: %v", r.err)
	}
	for _, hint := range []string{"you are a person", "you are not", "does not exist", "unknown client"} {
		if strings.Contains(strings.ToLower(r.err.Error()), hint) {
			t.Errorf("the answer must not say which cause applied (%q): %v", hint, r.err)
		}
	}
	if !strings.Contains(r.stderr, "no worker binding") {
		t.Errorf("unbound must say it sent without a session: %q", r.stderr)
	}
}

func TestSecretExecSendsTheBoundSession2664(t *testing.T) {
	skipWindows2664(t)
	writeTeamBinding(t)
	srv, calls := attnServer(t, map[string]string{"RevealSecret": sentinelReveal2664, "TeamAppIdentity": teamAppIdentityJSON})
	setTeamBindingServer(t, srv.URL)
	r := runSecretReveal2664(t, srv.URL, false, "exec", "--client", "cl-1", "--env", "TOKEN", "--", "true")
	if r.err != nil {
		t.Fatalf("%v", r.err)
	}
	var session string
	for _, c := range *calls {
		if c.Op == "RevealSecret" {
			session = c.Session
		}
	}
	if session == "" {
		t.Fatalf("a bound worktree must send its worker session: %+v", *calls)
	}
	if strings.Contains(r.stderr, "no worker binding") {
		t.Errorf("a bound worktree must not say it is unbound: %q", r.stderr)
	}
}

// Acceptance 15: a lost answer is sent once, never resent, says the read may be
// spent and names `client list`; a refusal that carries a server code (RATE_LIMITED)
// is not a lost answer.
func TestSecretRevealLostAnswerIsSentOnce2664(t *testing.T) {
	var n int
	var mu sync.Mutex
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		n++
		mu.Unlock()
		w.WriteHeader(502)
		_, _ = w.Write([]byte("bad gateway"))
	}))
	defer bad.Close()
	r := runSecretReveal2664(t, bad.URL, false, "reveal", "cl-1", "--allow-non-terminal")
	mu.Lock()
	defer mu.Unlock()
	if n != 1 || r.err == nil || !strings.Contains(r.err.Error(), "may have been spent") || !strings.Contains(r.err.Error(), "secret client list") {
		t.Fatalf("sends=%d err=%v", n, r.err)
	}
	srv, calls := attnServer(t, map[string]string{"RevealSecret": gqlErrorJSON("RATE_LIMITED")})
	r = runSecretReveal2664(t, srv.URL, false, "reveal", "cl-1", "--allow-non-terminal")
	if revealCalls2664(calls) != 1 || r.err == nil || strings.Contains(r.err.Error(), "may have been spent") {
		t.Fatalf("a rate limit spends no read and must not claim it did: %v", r.err)
	}
}

// Q8: a JSON string is its content; any other JSON is its compact text.
func TestSecretRevealValueForms2664(t *testing.T) {
	for name, tc := range map[string]struct{ valueJSONText, want string }{
		"string":        {`"plain"`, "plain"},
		"string escape": {`"a\"b\nc"`, "a\"b\nc"},
		"object":        {`{ "a": 1,  "b": [1, 2] }`, `{"a":1,"b":[1,2]}`},
		"number":        {`42`, "42"},
		"array":         {`[ "x" , 2 ]`, `["x",2]`},
	} {
		t.Run(name, func(t *testing.T) {
			srv, _ := attnServer(t, map[string]string{"RevealSecret": revealJSON2664(tc.valueJSONText, "1")})
			r := runSecretReveal2664(t, srv.URL, false, "reveal", "cl-1", "--allow-non-terminal")
			if r.err != nil || r.stdout != tc.want+"\n" {
				t.Fatalf("err=%v stdout=%q want=%q", r.err, r.stdout, tc.want+"\n")
			}
		})
	}
}

// Acceptance 13: reveal on a non-terminal exits 2 and sends no call; on a terminal
// it prints the value alone; readsLeft goes to stderr; --json carries the value.
func TestSecretRevealTerminalRule2664(t *testing.T) {
	srv, calls := attnServer(t, map[string]string{"RevealSecret": sentinelReveal2664})
	r := runSecretReveal2664(t, srv.URL, false, "reveal", "cl-1")
	if exitCodeFor(r.err) != 2 || len(*calls) != 0 || r.stdout != "" {
		t.Fatalf("non-terminal: exit=%d calls=%d out=%q err=%v", exitCodeFor(r.err), len(*calls), r.stdout, r.err)
	}
	r = runSecretReveal2664(t, srv.URL, false, "reveal", "cl-1", "--json")
	if exitCodeFor(r.err) != 2 || len(*calls) != 0 {
		t.Fatalf("--json on a non-terminal is refused too: exit=%d calls=%d", exitCodeFor(r.err), len(*calls))
	}
	r = runSecretReveal2664(t, srv.URL, true, "reveal", "cl-1")
	if r.err != nil || r.stdout != sentinel2664+"\n" || !strings.Contains(r.stderr, "reads left: 1") {
		t.Fatalf("terminal: err=%v out=%q err=%q", r.err, r.stdout, r.stderr)
	}
	srvU, _ := attnServer(t, map[string]string{"RevealSecret": revealJSON2664(`"v"`, "null")})
	r = runSecretReveal2664(t, srvU.URL, true, "reveal", "cl-1")
	if !strings.Contains(r.stderr, "reads left: unlimited") {
		t.Errorf("no budget reads as unlimited: %q", r.stderr)
	}
	r = runSecretReveal2664(t, srv.URL, false, "reveal", "cl-1", "--allow-non-terminal", "--json")
	var dto struct {
		SecretName string `json:"secretName"`
		Value      string `json:"value"`
		ReadsLeft  *int   `json:"readsLeft"`
	}
	if r.err != nil || json.Unmarshal([]byte(r.stdout), &dto) != nil || dto.Value != `"`+sentinel2664+`"` || dto.SecretName != "deploy-token" || dto.ReadsLeft == nil || *dto.ReadsLeft != 1 {
		t.Fatalf("--json: err=%v out=%q", r.err, r.stdout)
	}
}

// --out: a new 0600 file written before output, exact bytes, nothing printed; an
// existing path exits 2 and sends nothing; a refusal removes the empty file; --json
// with --out is refused.
func TestSecretRevealOutFile2664(t *testing.T) {
	dir := t.TempDir()
	srv, calls := attnServer(t, map[string]string{"RevealSecret": sentinelReveal2664})
	path := filepath.Join(dir, "token")
	r := runSecretReveal2664(t, srv.URL, false, "reveal", "cl-1", "--out", path)
	if r.err != nil {
		t.Fatal(r.err)
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != sentinel2664 {
		t.Fatalf("the file must hold the value exactly: %q %v", b, err)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
			t.Errorf("mode = %v, want 0600", fi.Mode().Perm())
		}
	}
	requireNoValue2664(t, "stdout/stderr", r.stdout, r.stderr)
	if r.stdout != "" {
		t.Errorf("--out prints nothing: %q", r.stdout)
	}
	before := len(*calls)
	r = runSecretReveal2664(t, srv.URL, false, "reveal", "cl-1", "--out", path)
	if exitCodeFor(r.err) != 2 || len(*calls) != before {
		t.Fatalf("an existing path: exit=%d new calls=%d err=%v", exitCodeFor(r.err), len(*calls)-before, r.err)
	}
	refused, _ := attnServer(t, map[string]string{"RevealSecret": gqlErrorJSON("SECRET_READ_CLIENT_SPENT")})
	p2 := filepath.Join(dir, "refused")
	r = runSecretReveal2664(t, refused.URL, false, "reveal", "cl-1", "--out", p2)
	if exitCodeFor(r.err) != 5 {
		t.Fatalf("exit=%d err=%v", exitCodeFor(r.err), r.err)
	}
	if _, err := os.Stat(p2); err == nil {
		t.Errorf("a refused reveal must not leave a file behind")
	}
	r = runSecretReveal2664(t, srv.URL, false, "reveal", "cl-1", "--out", filepath.Join(dir, "j"), "--json")
	if exitCodeFor(r.err) != 2 {
		t.Errorf("--json with --out is refused: exit=%d", exitCodeFor(r.err))
	}
}

// A blank client id and a NUL value.
func TestSecretRevealBlankClientAndNULValue2664(t *testing.T) {
	skipWindows2664(t)
	srv, calls := attnServer(t, map[string]string{"RevealSecret": sentinelReveal2664})
	r := runSecretReveal2664(t, srv.URL, false, "reveal", " ", "--allow-non-terminal")
	if exitCodeFor(r.err) != 2 || len(*calls) != 0 {
		t.Fatalf("blank client: exit=%d calls=%d", exitCodeFor(r.err), len(*calls))
	}
	srvN, callsN := attnServer(t, map[string]string{"RevealSecret": revealJSON2664(`"a\u0000b`+sentinel2664+`"`, "1")})
	file := filepath.Join(t.TempDir(), "ran")
	r = runSecretReveal2664(t, srvN.URL, false, "exec", "--client", "cl-1", "--env", "T", "--", "sh", "-c", `echo ran > "$1"`, "sh", file)
	if exitCodeFor(r.err) != 1 || revealCalls2664(callsN) != 1 || !strings.Contains(r.err.Error(), "NUL") {
		t.Fatalf("exit=%d err=%v", exitCodeFor(r.err), r.err)
	}
	requireNoValue2664(t, "error", r.err.Error(), r.stdout, r.stderr)
	if _, err := os.Stat(file); err == nil {
		t.Errorf("nothing may be run when the value cannot be delivered")
	}
}

// The sentinel never appears in an error envelope either, on a refusal after a
// successful-looking request path: a JSON error render of a failed exec.
func TestSecretExecErrorEnvelopeCarriesNoValue2664(t *testing.T) {
	srv, _ := attnServer(t, map[string]string{"RevealSecret": gqlErrorJSON("SECRET_READ_CLIENT_SPENT")})
	f, _ := testFactory(t)
	var errOut strings.Builder
	f.IOStreams.ErrOut = &errOut
	root := NewRootCmd(f)
	root.SetArgs([]string{"--server", srv.URL, "secret", "reveal", "cl-1", "--allow-non-terminal", "--json"})
	err := root.Execute()
	if err == nil || exitCodeFor(err) != exitcode.Conflict {
		t.Fatalf("exit=%d err=%v", exitCodeFor(err), err)
	}
	renderError(f, err)
	requireNoValue2664(t, "the error envelope", errOut.String(), err.Error(), fmt.Sprintf("%v %+v", err, err))
}

// Joaquin's P2: a revealed JSON null is the compact text "null", not an empty
// string (json.Unmarshal of null into a string succeeds and leaves it empty).
// Pinned for reveal on stdout, the --out file and the exec child's environment.
func TestSecretRevealJSONNullValue2664(t *testing.T) {
	skipWindows2664(t)
	srv, _ := attnServer(t, map[string]string{"RevealSecret": revealJSON2664(`null`, "1")})
	r := runSecretReveal2664(t, srv.URL, false, "reveal", "cl-1", "--allow-non-terminal")
	if r.err != nil || r.stdout != "null\n" {
		t.Fatalf("stdout: err=%v out=%q", r.err, r.stdout)
	}
	path := filepath.Join(t.TempDir(), "v")
	r = runSecretReveal2664(t, srv.URL, false, "reveal", "cl-1", "--out", path)
	if b, _ := os.ReadFile(path); r.err != nil || string(b) != "null" {
		t.Fatalf("--out: err=%v file=%q", r.err, b)
	}
	file := filepath.Join(t.TempDir(), "e")
	r = runSecretReveal2664(t, srv.URL, false, "exec", "--client", "cl-1", "--env", "T", "--", "sh", "-c", `printf %s "$T" > "$1"`, "sh", file)
	if b, _ := os.ReadFile(file); r.err != nil || string(b) != "null" {
		t.Fatalf("exec env: err=%v value=%q", r.err, b)
	}
}

// Joaquin's P3: error responses that carry the secret itself never put it in an
// error, a stream or a child: data plus errors in one answer, and a 502 whose body
// echoes it. (A server SENTENCE that contains a value is the server's own text and
// the CLI holds no value to redact on a refusal, so that is not pinned here.)
func TestSecretRevealErrorResponsesCarryingTheValue2664(t *testing.T) {
	skipWindows2664(t)
	for name, handler := range map[string]func(w http.ResponseWriter){
		"data and errors": func(w http.ResponseWriter) {
			_, _ = w.Write([]byte(`{"data":{"revealSecret":{"secretName":"n","value":` + mustJSON2631(mustJSON2631(sentinel2664)) + `,"readsLeft":1}},"errors":[{"message":"partial failure","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`))
		},
		"502 echoing the value": func(w http.ResponseWriter) {
			w.WriteHeader(502)
			_, _ = w.Write([]byte("upstream said: " + sentinel2664))
		},
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				handler(w)
			}))
			defer srv.Close()
			ran := filepath.Join(t.TempDir(), "ran")
			r := runSecretReveal2664(t, srv.URL, false, "exec", "--client", "cl-1", "--env", "T", "--", "sh", "-c", `echo ran > "$1"`, "sh", ran)
			if r.err == nil {
				t.Fatalf("a failed answer must be an error")
			}
			requireNoValue2664(t, "error, stdout or stderr", r.err.Error(), r.stdout, r.stderr)
			if _, err := os.Stat(ran); err == nil {
				t.Errorf("no child may start after a failed answer")
			}
		})
	}
}

// Jibril's P2: a non-200 whose BODY is success-shaped (data carrying the value,
// no errors[].message) must not put the value in the error or the real --json
// error envelope. The reveal error carries fixed classes and the server's own
// sentences only, never raw response data.
func TestSecretRevealNon200BodyCarryingTheValue2664(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 409, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = w.Write([]byte(sentinelReveal2664))
			}))
			defer srv.Close()
			f, out := testFactory(t)
			var errOut strings.Builder
			f.IOStreams.ErrOut = &errOut
			root := NewRootCmd(f)
			root.SetArgs([]string{"--server", srv.URL, "secret", "reveal", "cl-1", "--allow-non-terminal", "--json"})
			err := root.Execute()
			if err == nil {
				t.Fatal("a non-200 must be an error")
			}
			renderError(f, err)
			requireNoValue2664(t, "the error, the real --json envelope or the streams", err.Error(), errOut.String(), out.String(), fmt.Sprintf("%+v", err))
		})
	}
}

// Jibril's P2: a partial write whose cleanup also fails leaves a file that may
// hold part of the secret. The error says so in fixed words, names the file and
// carries neither the value nor the underlying write error.
func TestSecretRevealOutPartialWriteLeftBehind2664(t *testing.T) {
	oldWrite, oldRemove := secretfile.Write, secretfile.Remove
	secretfile.Write = func(f *os.File, b []byte) error {
		_, _ = f.Write(b[:6])
		return errors.New("injected write failure " + sentinel2664)
	}
	secretfile.Remove = func(string) error { return errors.New("injected remove failure") }
	t.Cleanup(func() { secretfile.Write, secretfile.Remove = oldWrite, oldRemove })
	srv, _ := attnServer(t, map[string]string{"RevealSecret": sentinelReveal2664})
	path := filepath.Join(t.TempDir(), "token")
	r := runSecretReveal2664(t, srv.URL, false, "reveal", "cl-1", "--out", path)
	if r.err == nil || exitCodeFor(r.err) != 1 {
		t.Fatalf("exit=%d err=%v", exitCodeFor(r.err), r.err)
	}
	for _, want := range []string{path, "may hold", "delete it"} {
		if !strings.Contains(r.err.Error(), want) {
			t.Errorf("the error must name the leftover file and say what to do (%q): %v", want, r.err)
		}
	}
	requireNoValue2664(t, "the error", r.err.Error(), r.stdout, r.stderr)
	if strings.Contains(r.err.Error(), "injected") {
		t.Errorf("the underlying write error must not be printed: %v", r.err)
	}
	if strings.Contains(r.err.Error(), "not delivered") {
		t.Errorf("it must not claim nothing was delivered while a partial file remains: %v", r.err)
	}
}
