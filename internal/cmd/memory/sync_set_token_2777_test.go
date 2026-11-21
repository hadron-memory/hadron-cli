package memory

import (
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2/gqlerror"
)

// cli#2777: the --json envelope JSON-encodes the drop path's message and server
// code, which can re-form the token's spelling. The predicate has its own pin
// (the #2714 family convention), and setTokenError takes the form only under
// --json, so the human path stays as it was.
func TestEnvelopeReformsToken2777(t *testing.T) {
	for _, c := range []struct {
		name, s, token string
		want           bool
	}{
		{"decoded quote re-escapes into the token", `bad a"b here`, `a\"b`, true},
		{"decoded backslash re-escapes into the token", `x a\b y`, `a\\b`, true},
		{"decoded newline re-escapes into the token", "a\nb", `a\nb`, true},
		{"the token itself encodes into its own escaped spelling", `a\"b`, `a\"b`, true},
		{"unrelated text", `request denied`, `a\"b`, false},
		{"plain token needs no encoder", `bad secret here`, `secret`, true},
		{"empty token", `anything`, ``, false},
		{"empty text", ``, `a\"b`, false},
	} {
		if got := envelopeReformsToken(c.s, c.token); got != c.want {
			t.Errorf("%s: envelopeReformsToken(%q, %q) = %v, want %v", c.name, c.s, c.token, got, c.want)
		}
	}
}

func TestSetTokenErrorJSONModeJudgesTheEncodedForm2777(t *testing.T) {
	const token = `a\"b`
	mk := func(msg, code string) error {
		return &gqlerror.Error{Message: msg, Extensions: map[string]any{"code": code}}
	}
	// --json: the decoded echo is dropped for the fixed refusal.
	if got := setTokenError(mk(`bad a"b here`, "FORBIDDEN"), token, true).Error(); got != fullTokenRefusalForTest {
		t.Errorf("--json must drop a message that re-forms the token, got %q", got)
	}
	// Human: unchanged, the message is shown as the server wrote it.
	if got := setTokenError(mk(`bad a"b here`, "FORBIDDEN"), token, false).Error(); !strings.Contains(got, `bad a"b here`) {
		t.Errorf("the human path must be unchanged, got %q", got)
	}
	// Both dropped at once: the message AND a code that re-forms the token go.
	if err := setTokenError(mk(`bad a"b here`, `X a"b`), token, true); err.Error() != fullTokenRefusalForTest || carriedCode(err) != "" {
		t.Errorf("--json must drop the message and the re-forming code together, got %q / %q", err.Error(), carriedCode(err))
	}
	// A server code whose encoded form re-forms the token is dropped under --json only.
	if got := carriedCode(setTokenError(mk("refused", `X a"b`), token, true)); got != "" {
		t.Errorf("--json must drop a code that re-forms the token, got %q", got)
	}
	if got := carriedCode(setTokenError(mk("refused", `X a"b`), token, false)); got != `X a"b` {
		t.Errorf("the human path keeps the code, got %q", got)
	}
	if got := carriedCode(setTokenError(mk("refused", "FORBIDDEN"), token, true)); got != "FORBIDDEN" {
		t.Errorf("an ordinary code is kept, got %q", got)
	}
}

const fullTokenRefusalForTest = "the server refused the source token"
