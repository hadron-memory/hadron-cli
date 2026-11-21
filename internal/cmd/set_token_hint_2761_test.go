package cmd

import (
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
