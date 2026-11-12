package cmd

// cli#878 revoke-page handoff — `hadron auth passkey revoke-page [--open]
// [--json]` against ServerInfo.passkeyRevokeUrl (hadron-server#4063,
// 739ecd7d6). Author contract issuecomment-6835152900, gate 6835278968. The
// CLI prints, or on an explicit --open in a terminal opens, the URL the
// server returned; it never revokes and never builds the URL. The fake server
// counts capability reads and GraphQL POSTs; the fake opener counts calls.

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmd/auth"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// The fixture's host is NOT the --server host (a 127.0.0.1 httptest server):
// a printed URL equal to it proves nothing was built from --server.
const revokeURLFixture = "https://accounts.hadron.example/passkeys/revoke"

type revokePageServer struct {
	url      string
	caps     atomic.Int32
	posts    atomic.Int32
	mu       sync.Mutex
	ops      []string
	queries  []string
	authHdrs []string
}

func newRevokePageServer(t *testing.T, capsStatus int, capsBody string, gqlStatus int, gqlBody string) *revokePageServer {
	t.Helper()
	s := &revokePageServer{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/capabilities" {
			s.caps.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(capsStatus)
			_, _ = io.WriteString(w, capsBody)
			return
		}
		s.posts.Add(1)
		var body struct {
			OperationName string `json:"operationName"`
			Query         string `json:"query"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		s.mu.Lock()
		s.ops = append(s.ops, body.OperationName)
		s.queries = append(s.queries, body.Query)
		s.authHdrs = append(s.authHdrs, r.Header.Get("Authorization"))
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(gqlStatus)
		_, _ = io.WriteString(w, gqlBody)
	}))
	t.Cleanup(srv.Close)
	s.url = srv.URL
	// Pin 10, on every server this file starts: no operation but the one read.
	t.Cleanup(func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, op := range s.ops {
			if op != "PasskeyRevokeUrl" {
				t.Errorf("operation %q sent, want only PasskeyRevokeUrl", op)
			}
		}
	})
	return s
}

func revokeURLBody(u string) string {
	b, _ := json.Marshal(u)
	return `{"data":{"serverInfo":{"passkeyRevokeUrl":` + string(b) + `}}}`
}

type revokeRun struct {
	out, errOut string
	code        int
	opened      []string
}

// runRevokePage runs the command with a fake opener. tty makes stdout a
// terminal; openErr is what the opener returns.
func runRevokePage(t *testing.T, serverURL string, tty bool, openErr error, env map[string]string, extra ...string) revokeRun {
	t.Helper()
	f, out := testFactory(t)
	errOut := &strings.Builder{}
	f.IOStreams.ErrOut = errOut
	if tty {
		f, out, errOut = testFactoryTTY(t, "")
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
	var opened []string
	prev := auth.PasskeyOpenBrowser
	auth.PasskeyOpenBrowser = func(u string) error { opened = append(opened, u); return openErr }
	prevTTY := auth.PasskeyStdoutIsTerminal
	auth.PasskeyStdoutIsTerminal = func(*output.IOStreams) bool { return tty }
	t.Cleanup(func() { auth.PasskeyOpenBrowser, auth.PasskeyStdoutIsTerminal = prev, prevTTY })
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"auth", "passkey", "revoke-page", "--server", serverURL}, extra...))
	code := 0
	if err := root.Execute(); err != nil {
		code = renderError(f, err)
	}
	return revokeRun{out: out.String(), errOut: errOut.String(), code: code, opened: opened}
}

// Pin 1: no capability (or a failed discovery) is exit 2, zero GraphQL and
// zero opener calls, in text and --json, signed in or out.
func TestPasskeyRevokePageOffMakesNoGraphQLRequest(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		env    map[string]string
		want   string
	}{
		{"switch off", 200, `{"capabilities":["node-signing"]}`, nil, "this server does not offer passkeys"},
		{"older server 404", 404, `not found`, nil, "could not confirm that this server offers passkeys"},
		{"unparsable discovery", 200, `{`, nil, "could not confirm that this server offers passkeys"},
		{"off and no credential", 200, `{"capabilities":[]}`, map[string]string{"HADRON_TOKEN": ""}, "this server does not offer passkeys"},
	}
	for _, tc := range cases {
		for _, js := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{true: " json", false: ""}[js], func(t *testing.T) {
				s := newRevokePageServer(t, tc.status, tc.body, 200, revokeURLBody(revokeURLFixture))
				var extra []string
				if js {
					extra = []string{"--json"}
				}
				r := runRevokePage(t, s.url, true, nil, tc.env, extra...)
				if r.code != 2 || s.posts.Load() != 0 || len(r.opened) != 0 {
					t.Fatalf("exit %d posts %d opened %v, want 2, 0, none (out %q err %q)", r.code, s.posts.Load(), r.opened, r.out, r.errOut)
				}
				if !strings.Contains(r.out+r.errOut, tc.want) {
					t.Fatalf("want %q: out %q err %q", tc.want, r.out, r.errOut)
				}
				if strings.Contains(r.out+r.errOut, revokeURLFixture) {
					t.Fatalf("a URL was printed for a server without passkeys: %q", r.out)
				}
			})
		}
	}
}

// Pin 2: signed out, the one read answers; the URL is printed byte-identical
// (its host is not --server's), from exactly one POST selecting only
// serverInfo{passkeyRevokeUrl}, with no Authorization header.
func TestPasskeyRevokePageSignedOut(t *testing.T) {
	s := newRevokePageServer(t, 200, passkeysOn, 200, revokeURLBody(revokeURLFixture))
	r := runRevokePage(t, s.url, false, nil, map[string]string{"HADRON_TOKEN": ""})
	if r.code != 0 {
		t.Fatalf("exit %d: out %q err %q", r.code, r.out, r.errOut)
	}
	if r.out != revokeURLFixture+"\n" {
		t.Fatalf("stdout %q, want the URL alone, byte-identical", r.out)
	}
	if s.posts.Load() != 1 || s.ops[0] != "PasskeyRevokeUrl" {
		t.Fatalf("posts %d ops %v, want one PasskeyRevokeUrl", s.posts.Load(), s.ops)
	}
	if got := strings.Join(strings.Fields(s.queries[0]), ""); got != "queryPasskeyRevokeUrl{serverInfo{passkeyRevokeUrl}}" {
		t.Fatalf("selection %q, want serverInfo{passkeyRevokeUrl} only", got)
	}
	if s.authHdrs[0] != "" {
		t.Fatalf("signed-out read carried Authorization %q", s.authHdrs[0])
	}
	if len(r.opened) != 0 {
		t.Fatalf("opened %v without --open", r.opened)
	}
}

// Signed in, the existing PublicGraphQLClient transport attaches the
// credential unchanged (gate: "preserve that existing transport behavior"),
// and the URL is still printed verbatim.
func TestPasskeyRevokePageSignedInKeepsTransport(t *testing.T) {
	s := newRevokePageServer(t, 200, passkeysOn, 200, revokeURLBody(revokeURLFixture))
	r := runRevokePage(t, s.url, false, nil, nil)
	if r.code != 0 || r.out != revokeURLFixture+"\n" {
		t.Fatalf("exit %d out %q err %q", r.code, r.out, r.errOut)
	}
	if s.authHdrs[0] != "Bearer hdr_user_test" {
		t.Fatalf("authorization %q", s.authHdrs[0])
	}
}

// Pin 3: --json is exactly {"passkeyRevokeUrl": …}, and opens nothing even on
// a terminal.
func TestPasskeyRevokePageJSON(t *testing.T) {
	s := newRevokePageServer(t, 200, passkeysOn, 200, revokeURLBody(revokeURLFixture))
	r := runRevokePage(t, s.url, true, nil, nil, "--json")
	if r.code != 0 || len(r.opened) != 0 {
		t.Fatalf("exit %d opened %v: %q %q", r.code, r.opened, r.out, r.errOut)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(r.out), &got); err != nil {
		t.Fatalf("json: %v\n%s", err, r.out)
	}
	if len(got) != 1 || got["passkeyRevokeUrl"] != revokeURLFixture {
		t.Fatalf("json %v, want exactly {passkeyRevokeUrl: %q}", got, revokeURLFixture)
	}
}

// Pin 4: no auto-open — a terminal without --open only prints.
func TestPasskeyRevokePageNoAutoOpen(t *testing.T) {
	s := newRevokePageServer(t, 200, passkeysOn, 200, revokeURLBody(revokeURLFixture))
	r := runRevokePage(t, s.url, true, nil, nil)
	if r.code != 0 || len(r.opened) != 0 || r.out != revokeURLFixture+"\n" {
		t.Fatalf("exit %d opened %v out %q", r.code, r.opened, r.out)
	}
}

// Pin 5: --open on a terminal makes exactly one launcher call with exactly
// the URL, and prints it.
func TestPasskeyRevokePageOpen(t *testing.T) {
	s := newRevokePageServer(t, 200, passkeysOn, 200, revokeURLBody(revokeURLFixture))
	r := runRevokePage(t, s.url, true, nil, nil, "--open")
	if r.code != 0 {
		t.Fatalf("exit %d: %q %q", r.code, r.out, r.errOut)
	}
	if len(r.opened) != 1 || r.opened[0] != revokeURLFixture {
		t.Fatalf("opened %v, want exactly [%q]", r.opened, revokeURLFixture)
	}
	if r.out != revokeURLFixture+"\n" {
		t.Fatalf("stdout %q", r.out)
	}
}

// Pin 6: a launcher failure is exit 1 and the message carries the URL.
func TestPasskeyRevokePageLauncherFailure(t *testing.T) {
	s := newRevokePageServer(t, 200, passkeysOn, 200, revokeURLBody(revokeURLFixture))
	r := runRevokePage(t, s.url, true, errors.New("xdg-open: not found"), nil, "--open")
	if r.code != 1 {
		t.Fatalf("exit %d, want 1", r.code)
	}
	all := r.out + r.errOut
	if !strings.Contains(all, "browser launcher failed: xdg-open: not found") || !strings.Contains(all, revokeURLFixture) {
		t.Fatalf("message %q lacks the failure or the URL", all)
	}
}

// Pin 7: --open with --json, or with stdout not a terminal, is refused before
// ANY network: zero capability reads, zero POSTs, zero opener calls.
func TestPasskeyRevokePageOpenMisuse(t *testing.T) {
	cases := []struct {
		name  string
		tty   bool
		extra []string
		want  string
	}{
		{"open with json", true, []string{"--open", "--json"}, "--open cannot be used with --json"},
		{"open not a terminal", false, []string{"--open"}, "--open needs a terminal"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newRevokePageServer(t, 200, passkeysOn, 200, revokeURLBody(revokeURLFixture))
			r := runRevokePage(t, s.url, tc.tty, nil, nil, tc.extra...)
			if r.code != 2 || s.caps.Load() != 0 || s.posts.Load() != 0 || len(r.opened) != 0 {
				t.Fatalf("exit %d caps %d posts %d opened %v, want 2/0/0/none (%q %q)",
					r.code, s.caps.Load(), s.posts.Load(), r.opened, r.out, r.errOut)
			}
			if !strings.Contains(r.out+r.errOut, tc.want) {
				t.Fatalf("want %q: %q %q", tc.want, r.out, r.errOut)
			}
		})
	}
}

// Pin 8: a validation envelope naming only passkeyRevokeUrl (an older server,
// or the switch went off after discovery) is exit 2 with its own sentence, not
// the skew advice; a validation error on another name keeps the skew advice.
func TestPasskeyRevokePageFieldMissing(t *testing.T) {
	missing := `{"errors":[{"message":"Cannot query field \"passkeyRevokeUrl\" on type \"ServerInfo\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`
	s := newRevokePageServer(t, 200, passkeysOn, 400, missing)
	r := runRevokePage(t, s.url, true, nil, nil, "--open")
	all := r.out + r.errOut
	if r.code != 2 || s.posts.Load() != 1 || len(r.opened) != 0 {
		t.Fatalf("exit %d posts %d opened %v, want 2, 1, none", r.code, s.posts.Load(), r.opened)
	}
	want := "this server does not advertise its passkey revoke page (an older server, or passkeys were switched off since discovery); no URL was constructed"
	if !strings.Contains(all, want) || strings.Contains(all, "out of sync") {
		t.Fatalf("got %q, want %q and no skew advice", all, want)
	}

	other := `{"errors":[{"message":"Cannot query field \"serverInfo\" on type \"Query\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`
	s2 := newRevokePageServer(t, 200, passkeysOn, 400, other)
	r2 := runRevokePage(t, s2.url, false, nil, nil)
	if all := r2.out + r2.errOut; r2.code != 2 || !strings.Contains(all, "out of sync") || strings.Contains(all, "revoke page") {
		t.Fatalf("exit %d %q: another name must keep the skew mapping", r2.code, all)
	}

	mixed := `{"errors":[{"message":"Cannot query field \"passkeyRevokeUrl\" on type \"ServerInfo\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}},{"message":"forbidden","extensions":{"code":"FORBIDDEN"}}]}`
	s3 := newRevokePageServer(t, 200, passkeysOn, 200, mixed)
	r3 := runRevokePage(t, s3.url, false, nil, nil)
	if strings.Contains(r3.out+r3.errOut, "does not advertise") {
		t.Fatalf("a mixed envelope was read as the missing field: %q", r3.out+r3.errOut)
	}
}

// Transport failure keeps the existing mapping: exit 7, no body quoted.
func TestPasskeyRevokePageTransportFailure(t *testing.T) {
	s := newRevokePageServer(t, 200, passkeysOn, 500, "<html><body>Internal Server Error</body></html>")
	r := runRevokePage(t, s.url, true, nil, nil, "--open")
	if r.code != 7 || len(r.opened) != 0 || strings.Contains(r.out+r.errOut, "<html") {
		t.Fatalf("exit %d opened %v %q", r.code, r.opened, r.out+r.errOut)
	}
}

// Pin 9: a URL that is not an absolute http(s) URL with a host and no
// userinfo, starts with '-', or fails the CLI's own transport rule is exit 1
// and never reaches the opener; loopback http and HADRON_ALLOW_HTTP=1 pass.
func TestPasskeyRevokePageInvalidURL(t *testing.T) {
	for _, bad := range []string{
		"",
		"javascript:alert(1)",
		"/passkeys/revoke",
		"https://u:p@accounts.hadron.example/passkeys/revoke",
		"-h",
		"http://evil.example/x",
		"https:opaque",
		"https:///passkeys/revoke",
		"ftp://localhost/passkeys/revoke",
		"https://accounts.hadron.example/passkeys/revoke\u0085",
		"https://accounts.hadron.example/\u202epasskeys/revoke",
	} {
		t.Run(bad, func(t *testing.T) {
			s := newRevokePageServer(t, 200, passkeysOn, 200, revokeURLBody(bad))
			for _, extra := range [][]string{{"--open"}, {"--json"}, nil} {
				r := runRevokePage(t, s.url, true, nil, map[string]string{"HADRON_ALLOW_HTTP": ""}, extra...)
				if r.code != 1 || len(r.opened) != 0 {
					t.Fatalf("%v: exit %d opened %v, want 1 and none", extra, r.code, r.opened)
				}
				if !strings.Contains(r.out+r.errOut, "the server returned an invalid passkey revoke URL") {
					t.Fatalf("%v: %q", extra, r.out+r.errOut)
				}
				if bad != "" && strings.Contains(r.out, bad) {
					t.Fatalf("%v: the rejected URL reached stdout: %q", extra, r.out)
				}
			}
		})
	}
	for _, tc := range []struct {
		url   string
		allow string
	}{
		{"http://127.0.0.1:8080/passkeys/revoke", ""},
		{"http://localhost/passkeys/revoke", ""},
		{"http://hadron.internal/passkeys/revoke", "1"},
	} {
		t.Run("ok "+tc.url, func(t *testing.T) {
			s := newRevokePageServer(t, 200, passkeysOn, 200, revokeURLBody(tc.url))
			r := runRevokePage(t, s.url, true, nil, map[string]string{"HADRON_ALLOW_HTTP": tc.allow}, "--open")
			if r.code != 0 || len(r.opened) != 1 || r.opened[0] != tc.url || r.out != tc.url+"\n" {
				t.Fatalf("exit %d opened %v out %q err %q", r.code, r.opened, r.out, r.errOut)
			}
		})
	}
}

// The help says what the page is for, that the CLI removes nothing, and
// that --open is explicit; it names no URL (the server supplies it).
func TestPasskeyRevokePageHelp(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"auth", "passkey", "revoke-page", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	flat := strings.Join(strings.Fields(out.String()), " ")
	for _, want := range []string{
		"Print the address of the server's passkey revoke page.",
		"with another passkey or a linked identity provider",
		"It names no passkey and grants nothing, and this CLI removes nothing itself.",
		"Without --open, nothing is launched.",
	} {
		if !strings.Contains(flat, want) {
			t.Fatalf("help lacks %q:\n%s", want, out.String())
		}
	}
	for _, bad := range []string{"http", "/passkeys/"} {
		if strings.Contains(out.String(), bad) {
			t.Fatalf("help names %q", bad)
		}
	}
}
