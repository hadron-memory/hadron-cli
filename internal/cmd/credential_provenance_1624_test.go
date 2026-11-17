package cmd

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/auth"
	"github.com/hadron-memory/hadron-cli/internal/auth/store"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// hadron-cli#1624 (#941 §10 P3-1, Riana): the stored-credential remedy must
// follow the credential the failing request actually sent, never a second
// render-time token lookup. Every case below has a stored USER login on the
// machine, which is exactly what fooled the render-time check.

const storedHint1624 = "your stored credential was refused"

func impToken1624(t *testing.T, exp time.Time) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"exp": exp.Unix(), "sub": "target",
		"imp": map[string]string{"sid": "sess1", "act": "admin1", "org": "org1"}})
	if err != nil {
		t.Fatal(err)
	}
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"HS256"}`)) + "." + enc(payload) + "." + enc([]byte("sig"))
}

// refusingServer1624 answers every POST 401 and records the bearer of each.
// onPost, when set, runs before the answer (to simulate state changing between
// the request and the rendering).
func refusingServer1624(t *testing.T, onPost func()) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var bearers []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"capabilities":[]}`))
			return
		}
		mu.Lock()
		bearers = append(bearers, r.Header.Get("Authorization"))
		mu.Unlock()
		if onPost != nil {
			onPost()
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`unauthorized`))
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string{}, bearers...) }
}

// withStoredLogin1624 drops HADRON_TOKEN and files a stored user token (plus
// any extra entries) for srv.
func withStoredLogin1624(t *testing.T, f *cmdutil.Factory, srvURL string, extra map[string]string) memStore {
	t.Helper()
	t.Setenv(store.EnvToken, "")
	st := memStore{auth.Host(srvURL): "hdr_user_stored"}
	for k, v := range extra {
		st[k] = v
	}
	f.TokenStoreFn = func() store.Store { return st }
	return st
}

// run1624 executes args (both renderings) and returns the exit code and
// everything printed.
func run1624(t *testing.T, f *cmdutil.Factory, args []string) (int, string) {
	t.Helper()
	errOut := captureErrOut(f)
	root := NewRootCmd(f)
	root.SetArgs(args)
	err := root.Execute()
	if err == nil {
		t.Fatalf("%v: want a failure", args)
	}
	code := renderError(f, err)
	return code, errOut()
}

func outputs1624(out *strings.Builder, errText string) string { return out.String() + errText }

func TestCredentialProvenance1624HostKeyRefusalGetsNoStoredHint(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		// `orca terminal status --config` probes Orca before it reads, so its
		// host-key client is pinned in its own package without Orca
		// (TestDefaultHostReadsRecordsTheHostKey1624).
		for _, args := range [][]string{
			{"team", "host", "run", "--once"},
		} {
			t.Run(strings.Join(args, "-")+map[bool]string{true: "-json"}[asJSON], func(t *testing.T) {
				srv, bearers := refusingServer1624(t, nil)
				cfg := writeHostConfig(t, "")
				f, out := testFactory(t)
				withStoredLogin1624(t, f, srv.URL, nil)
				a := append(append([]string{}, args...), "--config", cfg, "--server", srv.URL)
				if asJSON {
					a = append(a, "--json")
				}
				code, errText := run1624(t, f, a)
				all := outputs1624(out, errText)
				sent := bearers()
				if len(sent) == 0 {
					t.Fatalf("no request was sent (exit %d): %s", code, all)
				}
				for _, b := range sent {
					if b != "Bearer hk_host_key" {
						t.Fatalf("a host command sent %q, want only the host key", b)
					}
				}
				if strings.Contains(all, storedHint1624) {
					t.Errorf("exit %d: a refused HOST KEY got the stored-user remedy:\n%s", code, all)
				}
			})
		}
	}
}

// A missing host key fails before any request: nothing was sent, so nothing
// may be claimed about a stored credential.
func TestCredentialProvenance1624NoRequestGetsNoStoredHint(t *testing.T) {
	srv, bearers := refusingServer1624(t, nil)
	cfg := writeHostConfig(t, "")
	// Point key_file at nothing.
	f, out := testFactory(t)
	withStoredLogin1624(t, f, srv.URL, nil)
	t.Setenv("HADRON_HOST_KEY", "")
	missing := strings.Replace(mustRead1624(t, cfg), "host.key", "absent.key", 1)
	writeFile1624(t, cfg, missing)
	code, errText := run1624(t, f, []string{"team", "host", "run", "--once", "--config", cfg, "--server", srv.URL})
	if code != exitcode.AuthRequired {
		t.Fatalf("exit = %d, want AuthRequired: %s", code, errText)
	}
	if n := len(bearers()); n != 0 {
		t.Fatalf("sent %d requests, want none", n)
	}
	if all := outputs1624(out, errText); strings.Contains(all, storedHint1624) {
		t.Errorf("a failure before any request got the stored-user remedy:\n%s", all)
	}
}

// The impersonation token is live when the request is sent; by the time the
// error renders it has expired and been purged, so a render-time lookup would
// see the admin's stored login and blame it.
func TestCredentialProvenance1624ExpiredImpersonationGetsNoStoredHint(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		t.Run(map[bool]string{true: "json", false: "human"}[asJSON], func(t *testing.T) {
			var st memStore
			var srvURL string
			srv, bearers := refusingServer1624(t, func() {
				delete(st, auth.ImpersonationHostKey(srvURL)) // expired and purged mid-command
			})
			srvURL = srv.URL
			imp := impToken1624(t, time.Now().Add(time.Hour))
			f, out := testFactory(t)
			st = withStoredLogin1624(t, f, srv.URL, map[string]string{auth.ImpersonationHostKey(srv.URL): imp})
			a := []string{"memory", "ls", "--server", srv.URL}
			if asJSON {
				a = append(a, "--json")
			}
			code, errText := run1624(t, f, a)
			if got := bearers(); len(got) != 1 || got[0] != "Bearer "+imp {
				t.Fatalf("want exactly one request with the impersonation token, got %d", len(got))
			}
			if code != exitcode.AuthRequired {
				t.Fatalf("exit = %d, want AuthRequired", code)
			}
			if all := outputs1624(out, errText); strings.Contains(all, storedHint1624) {
				t.Errorf("an expired IMPERSONATION token was blamed on the stored login:\n%s", all)
			}
		})
	}
}

// `auth token validate` sends the token read from stdin, never the stored one.
func TestCredentialProvenance1624ExplicitTokenGetsNoStoredHint(t *testing.T) {
	srv, bearers := refusingServer1624(t, nil)
	f, out := testFactory(t)
	withStoredLogin1624(t, f, srv.URL, nil)
	f.IOStreams.In = strings.NewReader("hdr_explicit_token\n")
	code, errText := run1624(t, f, []string{"auth", "token", "validate", "--server", srv.URL})
	if got := bearers(); len(got) != 1 || got[0] != "Bearer hdr_explicit_token" {
		t.Fatalf("want one request with the explicit token, got %v", got)
	}
	if all := outputs1624(out, errText); strings.Contains(all, storedHint1624) {
		t.Errorf("exit %d: an EXPLICIT token's refusal got the stored-user remedy:\n%s", code, all)
	}
}

// The control: the stored login really was the credential sent. Exactly one
// remedy, in both renderings, with the exit code unchanged.
func TestCredentialProvenance1624StoredRefusalKeepsOneHint(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		t.Run(map[bool]string{true: "json", false: "human"}[asJSON], func(t *testing.T) {
			srv, bearers := refusingServer1624(t, nil)
			f, out := testFactory(t)
			withStoredLogin1624(t, f, srv.URL, nil)
			a := []string{"memory", "ls", "--server", srv.URL}
			if asJSON {
				a = append(a, "--json")
			}
			code, errText := run1624(t, f, a)
			if got := bearers(); len(got) != 1 || got[0] != "Bearer hdr_user_stored" {
				t.Fatalf("want one request with the stored token, got %v", got)
			}
			if code != exitcode.AuthRequired {
				t.Fatalf("exit = %d, want AuthRequired", code)
			}
			all := outputs1624(out, errText)
			if n := strings.Count(all, storedHint1624); n != 1 {
				t.Errorf("want the stored-user remedy exactly once, got %d:\n%s", n, all)
			}
		})
	}
}

func mustRead1624(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func writeFile1624(t *testing.T, p, body string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// #941 §10 P3-2 mutant 1: the impersonation exclusion. A LIVE impersonation
// token (nothing purged) that the server refuses is the target's session, not
// the admin's stored login, so it never gets the stored-login remedy.
func TestCredentialProvenance1624LiveImpersonationGetsNoStoredHint(t *testing.T) {
	srv, bearers := refusingServer1624(t, nil)
	imp := impToken1624(t, time.Now().Add(time.Hour))
	f, out := testFactory(t)
	withStoredLogin1624(t, f, srv.URL, map[string]string{auth.ImpersonationHostKey(srv.URL): imp})
	code, errText := run1624(t, f, []string{"memory", "ls", "--server", srv.URL})
	if got := bearers(); len(got) != 1 || got[0] != "Bearer "+imp {
		t.Fatalf("want one request with the impersonation token, got %d", len(got))
	}
	if code != exitcode.AuthRequired {
		t.Fatalf("exit = %d, want AuthRequired", code)
	}
	if all := outputs1624(out, errText); strings.Contains(all, storedHint1624) {
		t.Errorf("a refused impersonation token got the stored-login remedy:\n%s", all)
	}
}

// #941 §10 P3-2 mutant 4: the dedupe. A stored login refused for its OAuth
// scope already carries a remedy naming `hadron auth login`; a second, generic
// remedy is not appended.
func TestCredentialProvenance1624ExistingRemedyIsNotDuplicated(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		t.Run(map[bool]string{true: "json", false: "human"}[asJSON], func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodGet {
					_, _ = w.Write([]byte(`{"capabilities":[]}`))
					return
				}
				_, _ = w.Write([]byte(`{"data":null,"errors":[{"message":"scope refused","extensions":{"code":"FORBIDDEN","reason":"OAUTH_SCOPE_UNSUPPORTED"}}]}`))
			}))
			t.Cleanup(srv.Close)
			f, out := testFactory(t)
			withStoredLogin1624(t, f, srv.URL, nil)
			a := []string{"memory", "ls", "--server", srv.URL}
			if asJSON {
				a = append(a, "--json")
			}
			code, errText := run1624(t, f, a)
			if code != exitcode.AuthRequired {
				t.Fatalf("exit = %d, want AuthRequired", code)
			}
			all := outputs1624(out, errText)
			if !strings.Contains(all, "hadron auth logout && hadron auth login") {
				t.Fatalf("the scope remedy is missing:\n%s", all)
			}
			if strings.Contains(all, storedHint1624) {
				t.Errorf("a second, generic remedy was appended to one that already names it:\n%s", all)
			}
		})
	}
}

// `hadron api` sends its token through RawGraphQL, not the typed client; it
// records what it sent the same way, so a refused stored login keeps its one
// remedy there too.
func TestCredentialProvenance1624RawAPIStoredRefusalKeepsOneHint(t *testing.T) {
	srv, bearers := refusingServer1624(t, nil)
	f, out := testFactory(t)
	withStoredLogin1624(t, f, srv.URL, nil)
	code, errText := run1624(t, f, []string{"api", "query { __typename }", "--server", srv.URL})
	if got := bearers(); len(got) != 1 || got[0] != "Bearer hdr_user_stored" {
		t.Fatalf("want one request with the stored token, got %v", got)
	}
	if code != exitcode.AuthRequired {
		t.Fatalf("exit = %d, want AuthRequired", code)
	}
	if n := strings.Count(outputs1624(out, errText), storedHint1624); n != 1 {
		t.Errorf("want the stored-login remedy exactly once, got %d:\n%s", n, outputs1624(out, errText))
	}
}
