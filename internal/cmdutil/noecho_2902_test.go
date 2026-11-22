package cmdutil

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2902: the #2898 no-echo policy (cli#2876) applied to the validators of cmdutil. A refusal names the
// flag or argument, what is wrong and the accepted forms, says the value is not shown, and never echoes it:
// at validation no secret is known to redact, so a credential pasted in the wrong place would print to the
// terminal, the --json envelope and CI logs.

// leaks returns the parts of in that must not appear in a message: the whole value and the runs at its
// ends, which are the usual leak by truncation.
func leaks2902(in string) []string {
	out := []string{in}
	if r := []rune(in); len(r) >= 10 {
		out = append(out, string(r[:10]), string(r[len(r)-10:]))
	}
	return out
}

func assertNoEcho2902(t *testing.T, what, in string, err error, wantCode int) {
	t.Helper()
	if err == nil {
		t.Errorf("%s(%q): want a refusal", what, in)
		return
	}
	if code := exitcode.FromError(err); code != wantCode {
		t.Errorf("%s: exit = %d, want %d: %v", what, code, wantCode, err)
	}
	msg := err.Error()
	for _, leak := range leaks2902(in) {
		if strings.Contains(msg, leak) {
			t.Errorf("%s leaks %q of the value:\n%s", what, leak, msg)
		}
	}
	if strings.ContainsRune(msg, '\x1b') {
		t.Errorf("%s: a control character reached the message: %q", what, msg)
	}
	if !strings.Contains(msg, "not shown") {
		t.Errorf("%s: the refusal says the value is not shown:\n%s", what, msg)
	}
}

// Assembled at runtime: a token-shaped literal in the source trips push protection.
func tokens2902() []string {
	body := "AbCdEfGhIjKlMnOpQrStUvWx"
	return []string{
		"xo" + "xb-1234567890-0987654321-" + body,
		"gh" + "p_" + body + "0123456789",
		"sk:" + body + "\x1b[31mred",
		"xo" + "xb:1234567890:" + body + ":extra",
	}
}

func TestURNValidatorsNeverEchoTheValue2902(t *testing.T) {
	for _, in := range tokens2902() {
		for name, fn := range map[string]func(string, string) error{
			"ValidateURNSlug":      ValidateURNSlug,
			"ValidateOrgSlug":      ValidateOrgSlug,
			"ValidateUserHandle":   ValidateUserHandle,
			"ValidateURNPath":      ValidateURNPath,
			"ValidateAgentURNPath": ValidateAgentURNPath,
		} {
			err := fn("--flag", in)
			assertNoEcho2902(t, name, in, err, exitcode.Usage)
			if err != nil && !strings.Contains(err.Error(), "--flag") {
				t.Errorf("%s: the refusal names the flag: %v", name, err)
			}
		}
		_, err := CanonicalizeURN("<agent>", in)
		assertNoEcho2902(t, "CanonicalizeURN", in, err, exitcode.Usage)
		// a scheme-prefixed value that fails the grammar takes the other branch
		_, err = CanonicalizeURN("<agent>", "hrn:node:"+in)
		assertNoEcho2902(t, "CanonicalizeURN(prefixed)", in, err, exitcode.Usage)
	}
}

// A URN path still says which segment is wrong (by position) and the rule it breaks.
func TestValidateURNPathNamesTheSegmentByPositionNotValue2902(t *testing.T) {
	err := ValidateURNPath("--loc", "good:b@d:also")
	if err == nil || !strings.Contains(err.Error(), "segment 2") || !strings.Contains(err.Error(), slugRule) || strings.Contains(err.Error(), "b@d") {
		t.Errorf("name the segment by position and the rule, never its text: %v", err)
	}
}

func TestTimeAndKeyValueFlagsNeverEchoTheValue2902(t *testing.T) {
	for _, in := range tokens2902() {
		_, err := ParseTimeFlag("since", in)
		assertNoEcho2902(t, "ParseTimeFlag", in, err, exitcode.Usage)
		if err != nil && !strings.Contains(err.Error(), "--since") {
			t.Errorf("ParseTimeFlag names the flag: %v", err)
		}
		_, err = KeyValsToJSON([]string{in}, "set")
		assertNoEcho2902(t, "KeyValsToJSON", in, err, exitcode.Usage)
		if err != nil && !strings.Contains(err.Error(), "--set") {
			t.Errorf("KeyValsToJSON names the flag: %v", err)
		}
	}
}
