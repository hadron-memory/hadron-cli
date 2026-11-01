package cmd

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// #1490: a --value-file / --token-file / --api-key-file argument that cannot be
// read must not be repeated in the error. A user who pastes the secret itself
// into the flag would otherwise see it on stderr (and in the --json envelope),
// which lands in scrollback, CI logs and agent transcripts. Every caller of
// cmdutil.ReadSecretInput is pinned here: exit 2, the argument absent from
// every output, and nothing sent to the server.
func TestSecretInput1490FailedReadNeverEchoesArgument(t *testing.T) {
	const pasted = "sk-live-123"

	// A directory whose path carries the pasted value: os.ReadFile fails with
	// "read <path>: is a directory", a second failure class that also names
	// the argument.
	dir := filepath.Join(t.TempDir(), pasted+"-dir")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}

	sources := map[string]string{
		"bare not found": pasted,
		"@ not found":    "@" + pasted,
		"directory":      dir,
	}

	callers := map[string]func(src string) []string{
		"secret create generic": func(src string) []string {
			return []string{"secret", "create", "--name", "n1490", "--scope", "user", "--kind", "generic", "--value-file", src}
		},
		"secret create webfetch-auth bearer": func(src string) []string {
			return []string{"secret", "create", "--name", "n1490", "--scope", "user", "--kind", "webfetch-auth", "--type", "bearer", "--url-prefix", "https://api.example.test/", "--value-file", src}
		},
		"secret create webfetch-auth basic": func(src string) []string {
			return []string{"secret", "create", "--name", "n1490", "--scope", "user", "--kind", "webfetch-auth", "--type", "basic", "--username", "u", "--url-prefix", "https://api.example.test/", "--value-file", src}
		},
		"secret create webfetch-auth header": func(src string) []string {
			return []string{"secret", "create", "--name", "n1490", "--scope", "user", "--kind", "webfetch-auth", "--type", "header", "--header-name", "X-Key", "--url-prefix", "https://api.example.test/", "--value-file", src}
		},
		"secret update": func(src string) []string {
			return []string{"secret", "update", "sec_1", "--value-file", src}
		},
		"ha create": func(src string) []string {
			return []string{"ha", "create", "--org", "acme.com", "--slug", "home", "--name", "Home", "--url", "https://ha.example.test", "--token-file", src}
		},
		"ha update": func(src string) []string {
			return []string{"ha", "update", "ha_1", "--token-file", src}
		},
		"org resend-sender create": func(src string) []string {
			return []string{"org", "resend-sender", "create", "--org", "acme.com", "--from", "x@example.test", "--api-key-file", src}
		},
		"org resend-sender update": func(src string) []string {
			return []string{"org", "resend-sender", "update", "s1", "--api-key-file", src}
		},
	}

	for callerName, argv := range callers {
		for sourceName, src := range sources {
			for _, jsonMode := range []bool{false, true} {
				name := fmt.Sprintf("%s/%s/json=%v", callerName, sourceName, jsonMode)
				t.Run(name, func(t *testing.T) {
					var calls atomic.Int32
					srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls.Add(1)
						w.Header().Set("Content-Type", "application/json")
						_, _ = fmt.Fprint(w, `{"data":{}}`)
					}))
					defer srv.Close()

					f, out, errOut := testFactoryTTY(t, "")
					f.IOStreams.In = strings.NewReader("")
					args := argv(src)
					if jsonMode {
						args = append(args, "--json")
					}
					root := NewRootCmd(f)
					root.SetArgs(append(args, "--server", srv.URL))
					err := root.Execute()
					if err == nil {
						t.Fatal("expected a refusal for an unreadable file argument")
					}
					if got := exitCodeFor(err); got != exitcode.Usage {
						t.Fatalf("exit = %d, want %d (%v)", got, exitcode.Usage, err)
					}
					if n := calls.Load(); n != 0 {
						t.Fatalf("%d requests sent; a failed read must send nothing", n)
					}
					// Render the error exactly as the binary does (stderr, or the
					// --json envelope on stdout).
					renderError(f, err)
					for where, text := range map[string]string{
						"error":  err.Error(),
						"stdout": out.String(),
						"stderr": errOut.String(),
					} {
						if strings.Contains(text, pasted) {
							t.Fatalf("%s repeats the file argument: %q", where, text)
						}
					}
				})
			}
		}
	}
}
