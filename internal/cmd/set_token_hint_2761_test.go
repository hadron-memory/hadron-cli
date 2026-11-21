package cmd

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// hadron-cli#2761: renderError appends storedCredentialHint to an exit-3
// failure AFTER setTokenError chose a message that provably carries no
// spelling of the source token. A token whose text sits inside that fixed hint
// (any substring) was re-echoed. Whatever is appended to an already-redacted
// refusal must pass the same containment test the message passed.
func TestSetTokenStoredHintNeverEchoesTheToken2761(t *testing.T) {
	for _, token := range []string{
		"credential was refused", // inside the hint
		"hadron auth login",      // the remedy itself
		"refused; run",           // spans a punctuation boundary of the hint
		"}]} — your stored",      // spans the seam between message and hint
		"stored",                 // a single word of the hint
	} {
		for _, asJSON := range []bool{false, true} {
			name := token
			if asJSON {
				name += "-json"
			}
			t.Run(name, func(t *testing.T) {
				srv, _ := refusingServer1624(t, nil)
				f, out := testFactory(t)
				withStoredLogin1624(t, f, srv.URL, nil)
				file := filepath.Join(t.TempDir(), "tok")
				if err := os.WriteFile(file, []byte(token), 0o600); err != nil {
					t.Fatal(err)
				}
				args := []string{"memory", "sync", "set-token", "acme.com:kb", "--token-file", file, "--server", srv.URL}
				if asJSON {
					args = append(args, "--json")
				}
				code, errText := run1624(t, f, args)
				if code != exitcode.AuthRequired {
					t.Fatalf("exit %d, want %d: %s", code, exitcode.AuthRequired, errText)
				}
				if all := outputs1624(out, errText); strings.Contains(all, token) {
					t.Fatalf("the token was re-echoed by the stored-credential hint: %q", all)
				}
			})
		}
	}
}

// The remedy is kept for every token it cannot carry, so the guard is not a
// blanket removal of the hint.
func TestSetTokenStoredHintKeptForAnUnrelatedToken2761(t *testing.T) {
	srv, _ := refusingServer1624(t, nil)
	f, out := testFactory(t)
	withStoredLogin1624(t, f, srv.URL, nil)
	file := filepath.Join(t.TempDir(), "tok")
	if err := os.WriteFile(file, []byte("zz-unrelated-2761"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, errText := run1624(t, f, []string{"memory", "sync", "set-token", "acme.com:kb", "--token-file", file, "--server", srv.URL})
	if all := outputs1624(out, errText); !strings.Contains(all, storedHint1624) {
		t.Fatalf("the remedy was dropped for a token it cannot carry: %q", all)
	}
}

// The render path for the --json envelope form (cli#2777 x cli#2761): the
// server's message ends in a quote, so the message alone is clean in both its
// plain and its encoded form, but under --json the encoder writes that quote as
// \" directly before the hint's separator, and a token spanning that seam
// appears in the stdout bytes. renderError must consult the guard before it
// appends the hint, so the hint is dropped under --json and kept in text.
func TestSetTokenStoredHintEnvelopeSeam2761(t *testing.T) {
	const token = `\" — your`
	for _, asJSON := range []bool{false, true} {
		name := "text"
		if asJSON {
			name = "json"
		}
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodGet {
					_, _ = w.Write([]byte(`{"capabilities":[]}`))
					return
				}
				_, _ = w.Write([]byte(`{"errors":[{"message":"bad\"","extensions":{"code":"UNAUTHENTICATED"}}],"data":null}`))
			}))
			t.Cleanup(srv.Close)
			f, out := testFactory(t)
			withStoredLogin1624(t, f, srv.URL, nil)
			file := filepath.Join(t.TempDir(), "tok")
			if err := os.WriteFile(file, []byte(token), 0o600); err != nil {
				t.Fatal(err)
			}
			args := []string{"memory", "sync", "set-token", "acme.com:kb", "--token-file", file, "--server", srv.URL}
			if asJSON {
				args = append(args, "--json")
			}
			code, errText := run1624(t, f, args)
			if code != exitcode.AuthRequired {
				t.Fatalf("exit %d, want %d: %s", code, exitcode.AuthRequired, errText)
			}
			all := outputs1624(out, errText)
			if !strings.Contains(all, `bad`) {
				t.Fatalf("the server's refusal did not reach the output: %q", all)
			}
			if asJSON {
				if strings.Contains(all, token) || strings.Contains(all, storedHint1624) {
					t.Fatalf("--json: the hint re-formed the token across the seam: %q", all)
				}
			} else if !strings.Contains(all, storedHint1624) {
				t.Fatalf("text: the remedy was dropped for a token its text cannot carry: %q", all)
			}
		})
	}
}
