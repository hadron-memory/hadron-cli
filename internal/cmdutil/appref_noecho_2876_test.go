package cmdutil

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2876: a usage refusal never echoes the value the operator typed. At flag
// validation no secret is known to redact, so a token pasted into --app (an easy
// slip beside --bot-token-file) would otherwise print to the terminal, the
// --json envelope and any CI log. The refusal still names the flag, what was
// wrong and the accepted forms, so it stays actionable.

// The fixtures are assembled at runtime so no token-shaped literal sits in the
// source: a secret scanner (GitHub push protection) cannot tell a fixture from a
// real token, and a push blocked on one is worse than a split string.
func tokenShaped2876() []string {
	body := "AbCdEfGhIjKlMnOpQrStUvWx"
	return []string{
		"xo" + "xb-1234567890-0987654321-" + body,                        // a bot token: no colon, the "not org-qualified" branch
		"xa" + "pp-1-A0123456789-1234567890123-0a1b2c3d4e5f6a7b8c9d0e1f", // an app-level token
		"gh" + "p_" + body + "0123456789",                                // a GitHub token
		"xo" + "xb:1234567890:" + body + ":extra",                        // colons, more than two atoms: the "does not name an App" branch
		"sk:" + body + "\x1b[31mred",                                     // control characters ride along
	}
}

func TestCanonicalAppRefNeverEchoesTheValue2876(t *testing.T) {
	for _, in := range tokenShaped2876() {
		for _, what := range []string{"--app", "--org-app", "<app>"} {
			got, err := CanonicalAppRef(what, in)
			if err == nil {
				t.Errorf("CanonicalAppRef(%q, %q) = %q, want a refusal", what, in, got)
				continue
			}
			if code := exitcode.FromError(err); code != exitcode.Usage {
				t.Errorf("exit = %d, want Usage", code)
			}
			msg := err.Error()
			// Not the whole value, and not any run of it long enough to be a credential's
			// body (the first and last ten characters are the usual leak by truncation).
			head, tail := []rune(in)[:10], []rune(in)[len([]rune(in))-10:]
			for _, leak := range []string{in, string(head), string(tail)} {
				if strings.Contains(msg, leak) {
					t.Errorf("CanonicalAppRef(%q, %q) leaks %q:\n%s", what, in, leak, msg)
				}
			}
			if strings.ContainsRune(msg, '\x1b') {
				t.Errorf("a control character reached the message: %q", msg)
			}
			// Still actionable: the flag, the problem and the accepted forms.
			for _, want := range []string{what, "hrn:app:<root>:<slug>", "<root>:<slug>", "an App id"} {
				if !strings.Contains(msg, want) {
					t.Errorf("CanonicalAppRef(%q, %q) message lacks %q:\n%s", what, in, want, msg)
				}
			}
		}
	}
}

// The two refusals keep their different readings: a bare slug lacks its org, any
// other malformed value does not name an App.
func TestCanonicalAppRefKeepsBothReadingsWithoutTheValue2876(t *testing.T) {
	_, e1 := CanonicalAppRef("--app", tokenShaped2876()[0])
	if e1 == nil || !strings.Contains(e1.Error(), "not org-qualified") {
		t.Errorf("a colon-less value is told it lacks its org: %v", e1)
	}
	_, e2 := CanonicalAppRef("--app", tokenShaped2876()[3])
	if e2 == nil || !strings.Contains(e2.Error(), "does not name an App") || strings.Contains(e2.Error(), "not org-qualified") {
		t.Errorf("a malformed colon value does not name an App: %v", e2)
	}
}
