package cmd

// hadron-cli#2656 — test hardening for the `org github` verbs, collected from
// the #2641 BLIND delta review (Gemma, 7 P3, none blocking). Items 1 and 2
// strengthen existing pins in org_github_2512_test.go in place (ServerCodeOf
// on the key-bearing code; the masked-prompt guard's own refusal text); items
// 3-5 live here:
//
//   3. a gateway 502/503 (a non-GraphQL body) is a transport failure, not a
//      refusal: exit 7 (Unavailable — the retry-safe, outcome-unknown class)
//      with the verify-before-retry caveat surfaced, exactly ONE request per
//      verb (the no-retry contract), and the edge's body never quoted on a
//      stream. For the key-bearing verb that last guarantee is the withheld
//      diagnostic doing its job: the private key is NOT among requestSecrets'
//      operation-key/unlock secrets, so bearerDoer's body scrub does not run
//      for it — nothing may quote the body at all.
//   4. a key reflected as a FRAGMENT of a returned Organization field — inside
//      a longer string, not the field's whole value — is redacted in the
//      --json DTO and in the human renderer alike.
//   5. stream discipline and exit codes across the three verbs: a human-mode
//      failure renders on stderr only (stdout stays empty), a --json failure's
//      envelope is stdout's single document (stderr stays clean), and every
//      failure class keeps its documented exit code (2 usage, 7 transport,
//      8/4/3 server refusal).

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
)

// orgGithubVerbs2656 is the org github group's three verbs, for the
// cross-verb pins below.
var orgGithubVerbs2656 = []struct {
	name, op, field string
	args            []string
}{
	{"link", "LinkGithubApp", "linkGithubApp", []string{"github", "link", "acme.com", "--installation-id", "1"}},
	{"unlink", "UnlinkGithubApp", "unlinkGithubApp", []string{"github", "unlink", "acme.com", "--yes"}},
	{"set-app", "UpdateOrgGithubApp", "updateOrgGithubApp", []string{"github", "set-app", "acme.com", "--app-id", "1", "--private-key-file", "-"}},
}

// runOrgGithub2656 executes one org github verb against srv and returns the
// factory alongside its captured streams and the command error: the key sits
// on stdin (only set-app reads it) so every pin runs with a key in play, and
// stderr is captured because that is where a human-mode failure renders.
func runOrgGithub2656(t *testing.T, args []string, srvURL string, asJSON bool) (*cmdutil.Factory, *strings.Builder, *strings.Builder, error) {
	t.Helper()
	f, out := testFactory(t)
	stderr := &strings.Builder{}
	f.IOStreams.ErrOut = stderr
	f.IOStreams.In = strings.NewReader(keySentinel2512 + "\n")
	root := NewRootCmd(f)
	argv := append([]string{"org"}, args...)
	if asJSON {
		argv = append(argv, "--json")
	}
	root.SetArgs(append(argv, "--server", srvURL))
	return f, out, stderr, root.Execute()
}

// TestOrgGithub2656ServerCodeEnvelope (#2656 item 1): under --json the exit
// envelope's serverCode is the surface an agent parses. A clean
// extensions.code rides along for every verb — including set-app, whose
// diagnostic message is itself withheld — while a code that embeds the
// private key never renders at all: the envelope carries no serverCode and no
// key spelling anywhere, on either stream. The clean-code half is what keeps
// the key-bearing half from being vacuous: serverCode demonstrably renders
// for these errors, so its absence in the poisoned case is the guard, not a
// dead path.
func TestOrgGithub2656ServerCodeEnvelope(t *testing.T) {
	for _, tc := range []struct {
		code string
		exit int
	}{
		{"FORBIDDEN", 8},
		{"NOT_FOUND", 4},
		{"UNAUTHENTICATED", 3},
	} {
		for _, verb := range orgGithubVerbs2656 {
			t.Run(verb.name+" "+tc.code, func(t *testing.T) {
				srv, _ := captureGraphQL(t, map[string]string{verb.op: fmt.Sprintf(`{"errors":[{"message":"refused","extensions":{"code":%q}}]}`, tc.code)})
				f, out, stderr, err := runOrgGithub2656(t, verb.args, srv.URL, true)
				if err == nil {
					t.Fatal("expected refusal")
				}
				if code := renderError(f, err); code != tc.exit {
					t.Fatalf("rendered exit %d want %d", code, tc.exit)
				}
				var env struct {
					Error struct {
						Code       int    `json:"code"`
						Message    string `json:"message"`
						ServerCode string `json:"serverCode"`
					} `json:"error"`
				}
				if jerr := json.Unmarshal([]byte(out.String()), &env); jerr != nil {
					t.Fatalf("no --json envelope on stdout: %v (%s)", jerr, out.String())
				}
				if env.Error.Code != tc.exit {
					t.Fatalf("envelope code %d want %d (the envelope must agree with the process exit)", env.Error.Code, tc.exit)
				}
				if env.Error.ServerCode != tc.code {
					t.Fatalf("envelope serverCode %q want %q", env.Error.ServerCode, tc.code)
				}
				if verb.name == "set-app" {
					if !strings.Contains(env.Error.Message, "withheld") {
						t.Fatalf("set-app envelope quotes the diagnostic: %q", env.Error.Message)
					}
				} else if !strings.Contains(env.Error.Message, "refused") {
					t.Fatalf("envelope lost the server's sentence: %q", env.Error.Message)
				}
				if stderr.String() != "" {
					t.Fatalf("--json refusal wrote to stderr: %s", stderr.String())
				}
			})
		}
	}
	// The poisoned code: dropped, not filtered — and nothing key-bearing on
	// either stream.
	t.Run("set-app code embedding key", func(t *testing.T) {
		codeBody := fmt.Sprintf(`{"errors":[{"message":"refused","extensions":{"code":"E-%s"}}]}`, keySentinel2512)
		srv, _ := captureGraphQL(t, map[string]string{"UpdateOrgGithubApp": codeBody})
		f, out, stderr, err := runOrgGithub2656(t, orgGithubVerbs2656[2].args, srv.URL, true)
		if err == nil {
			t.Fatal("expected refusal")
		}
		code := renderError(f, err)
		var env struct {
			Error struct {
				Code       int    `json:"code"`
				ServerCode string `json:"serverCode"`
			} `json:"error"`
		}
		if jerr := json.Unmarshal([]byte(out.String()), &env); jerr != nil {
			t.Fatalf("no --json envelope on stdout: %v (%s)", jerr, out.String())
		}
		if env.Error.ServerCode != "" {
			t.Fatalf("serverCode %q rendered while embedding the key", env.Error.ServerCode)
		}
		if env.Error.Code != code {
			t.Fatalf("envelope code %d disagrees with the rendered exit %d", env.Error.Code, code)
		}
		if strings.Contains(out.String()+stderr.String(), keySentinel2512) {
			t.Fatal("key leaked into the exit envelope")
		}
	})
}

// TestOrgGithub2656GatewayNoRetry (#2656 item 3): a 502/503 whose body is not
// a GraphQL envelope is the edge, not the API. Every verb surfaces it as
// exit 7 with the verify-before-retry caveat (for set-app, as the withheld
// diagnostic's availability pointer), sends exactly one request — a gateway
// failure must not be retried; the mutation may have committed while its
// answer was lost — and quotes none of the edge's body, which for the
// key-bearing verb may echo the key (the private key does not ride
// requestSecrets, so no scrub stands between that body and a stream: the
// only defense is that nothing quotes it).
func TestOrgGithub2656GatewayNoRetry(t *testing.T) {
	for _, status := range []int{502, 503} {
		for _, verb := range orgGithubVerbs2656 {
			t.Run(fmt.Sprintf("%s %d", verb.name, status), func(t *testing.T) {
				var mu sync.Mutex
				posts := 0
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodPost {
						return
					}
					mu.Lock()
					posts++
					mu.Unlock()
					body := "<html>Bad Gateway</html>"
					if verb.name == "set-app" {
						// The edge echoing back what rode the request: hostile
						// or buggy, and untrusted diagnostics at every status.
						body = "<html>Bad Gateway: " + keySentinel2512 + "</html>"
					}
					w.Header().Set("Content-Type", "text/html")
					w.WriteHeader(status)
					_, _ = w.Write([]byte(body))
				}))
				defer srv.Close()
				f, out, stderr, err := runOrgGithub2656(t, verb.args, srv.URL, false)
				if err == nil {
					t.Fatal("expected transport failure")
				}
				mu.Lock()
				defer mu.Unlock()
				if posts != 1 {
					t.Fatalf("%d requests; a gateway failure must not be retried", posts)
				}
				if code := exitCodeFor(err); code != 7 {
					t.Fatalf("exit %d (%v); a gateway 5xx is Unavailable(7), the retry-safe outcome-unknown class", code, err)
				}
				if code := renderError(f, err); code != 7 {
					t.Fatalf("rendered exit %d want 7", code)
				}
				if strings.Contains(out.String()+stderr.String(), keySentinel2512) {
					t.Fatal("the edge's key-bearing echo reached a stream")
				}
				if verb.name == "set-app" {
					if !strings.Contains(stderr.String(), "server availability") {
						t.Fatalf("withheld diagnostic lost its availability pointer: %s", stderr.String())
					}
				} else if !strings.Contains(stderr.String(), "verify the current state before retrying") {
					t.Fatalf("write caveat missing: %s", stderr.String())
				}
			})
		}
	}
}

// TestOrgGithub2656ReflectedKeyFragment (#2656 item 4): the 2512 pins cover a
// server reflecting the key as a field's WHOLE value; a fragment of a longer
// value must be scrubbed just the same — ReplaceAll semantics, not
// exact-match — in the --json DTO and in the human renderer.
func TestOrgGithub2656ReflectedKeyFragment(t *testing.T) {
	for _, field := range []string{"id", "urn", "githubAppId"} {
		for _, asJSON := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s json=%v", field, asJSON), func(t *testing.T) {
				var row map[string]any
				if err := json.Unmarshal([]byte(orgGithubLinked2512), &row); err != nil {
					t.Fatal(err)
				}
				row[field] = "acme.com#" + keySentinel2512 + "#tail"
				body, err := json.Marshal(map[string]any{"data": map[string]any{"updateOrgGithubApp": row}})
				if err != nil {
					t.Fatal(err)
				}
				srv, _ := captureGraphQL(t, map[string]string{"UpdateOrgGithubApp": string(body)})
				_, out, _, err := runOrgGithub2656(t, orgGithubVerbs2656[2].args, srv.URL, asJSON)
				if err != nil {
					t.Fatal(err)
				}
				want := "acme.com#[REDACTED]#tail"
				if !strings.Contains(out.String(), want) {
					t.Fatalf("reflected fragment not redacted (want %q): %s", want, out.String())
				}
				if strings.Contains(out.String(), keySentinel2512) {
					t.Fatal("key reached the output inside a larger value")
				}
			})
		}
	}
}

// TestOrgGithub2656StreamSeparation (#2656 item 5): a human-mode failure —
// a usage refusal before any request, a server refusal, a gateway 5xx —
// renders on stderr only, leaving stdout empty; under --json the same
// failure puts the envelope on stdout as its single document, leaving stderr
// clean. Exit codes per class: 2, 8, 7.
func TestOrgGithub2656StreamSeparation(t *testing.T) {
	human := []struct {
		name string
		args []string
		exit int
		srv  func(t *testing.T) string
	}{
		{"usage refusal", []string{"github", "unlink", "acme.com"}, 2, func(t *testing.T) string {
			srv, _, _ := countingGraphQL(t, nil)
			return srv.URL
		}},
		{"server refusal", []string{"github", "link", "acme.com", "--installation-id", "1"}, 8, func(t *testing.T) string {
			srv, _ := captureGraphQL(t, map[string]string{"LinkGithubApp": `{"errors":[{"message":"refused","extensions":{"code":"FORBIDDEN"}}]}`})
			return srv.URL
		}},
		{"gateway 502", []string{"github", "link", "acme.com", "--installation-id", "1"}, 7, func(t *testing.T) string {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte("<html>Bad Gateway</html>"))
			}))
			t.Cleanup(srv.Close)
			return srv.URL
		}},
	}
	for _, tc := range human {
		t.Run("human "+tc.name, func(t *testing.T) {
			f, out, stderr, err := runOrgGithub2656(t, tc.args, tc.srv(t), false)
			if err == nil {
				t.Fatal("expected failure")
			}
			if code := renderError(f, err); code != tc.exit {
				t.Fatalf("exit %d want %d", code, tc.exit)
			}
			if out.String() != "" {
				t.Fatalf("human-mode failure wrote to stdout: %s", out.String())
			}
			if !strings.HasPrefix(stderr.String(), "hadron: ") {
				t.Fatalf("failure did not render on stderr: %q", stderr.String())
			}
		})
	}
	t.Run("json envelope is stdout's only document", func(t *testing.T) {
		srv, _ := captureGraphQL(t, map[string]string{"LinkGithubApp": `{"errors":[{"message":"refused","extensions":{"code":"FORBIDDEN"}}]}`})
		f, out, stderr, err := runOrgGithub2656(t, []string{"github", "link", "acme.com", "--installation-id", "1"}, srv.URL, true)
		if err == nil {
			t.Fatal("expected failure")
		}
		if code := renderError(f, err); code != 8 {
			t.Fatalf("exit %d want 8", code)
		}
		var env map[string]any
		if jerr := json.Unmarshal([]byte(out.String()), &env); jerr != nil {
			t.Fatalf("stdout is not the envelope: %v (%s)", jerr, out.String())
		}
		if stderr.String() != "" {
			t.Fatalf("--json refusal wrote to stderr: %s", stderr.String())
		}
	})
}
