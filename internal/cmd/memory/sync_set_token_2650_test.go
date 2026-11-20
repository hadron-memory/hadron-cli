package memory

import (
	"errors"
	"strings"
	"testing"
	"unicode"

	"github.com/vektah/gqlparser/v2/gqlerror"
)

// TestSetTokenErrorEmptyTokenBranchIsCapped2650 pins cli#2650 item 2: the
// empty-token branch of setTokenError returned the server's answer uncapped,
// while the other branch caps it at 2000 runes and neutralises control
// characters. The branch is unreachable today (ReadSecretInput refuses an
// empty value), so only a unit call can reach it.
func TestSetTokenErrorEmptyTokenBranchIsCapped2650(t *testing.T) {
	long := "boom\x1b[31m" + strings.Repeat("x", 3000) + "\n\x07tail"
	err := setTokenError(&gqlerror.Error{Message: long}, "")
	msg := err.Error()
	if n := len([]rune(msg)); n > 2000 {
		t.Errorf("message is %d runes, want at most 2000", n)
	}
	for _, r := range msg {
		if unicode.IsControl(r) {
			t.Errorf("message carries control character %q", r)
		}
	}
}

// TestSetTokenErrorKeepsServerCodeUnlessItEchoesToken2650 pins cli#2650 item 4
// for the guard in setTokenError: a server code that contains the token is
// dropped, and an ordinary code is kept. Removing the guard fails the first
// case.
func TestSetTokenErrorKeepsServerCodeUnlessItEchoesToken2650(t *testing.T) {
	const token = "tok-SENTINEL-2650"
	mk := func(code string) error {
		return &gqlerror.Error{Message: "refused", Extensions: map[string]any{"code": code}}
	}
	if got := carriedCode(setTokenError(mk("BAD "+token), token)); got != "" {
		t.Errorf("a server code that holds the token must be dropped, got %q", got)
	}
	if got := carriedCode(setTokenError(mk("FORBIDDEN"), token)); got != "FORBIDDEN" {
		t.Errorf("an ordinary server code must be kept, got %q", got)
	}
}

// carriedCode reads the server code the way the --json error envelope does
// (root.go): through the ServerCode method of the returned error.
func carriedCode(err error) string {
	var coded interface{ ServerCode() string }
	if errors.As(err, &coded) {
		return coded.ServerCode()
	}
	return ""
}

// TestRedactToken2650 is the direct table for redactToken (blind F6/F2): the
// literal and JSON-escaped spellings (including Go's HTML escaping of < > &),
// no escaped spelling when the token needs none, and an empty token is a no-op.
func TestRedactToken2650(t *testing.T) {
	for _, c := range []struct{ name, in, token, want string }{
		{"literal", "bad abc", "abc", "bad [REDACTED]"},
		{"escaped quote", `bad a\"b and a"b`, `a"b`, "bad [REDACTED] and [REDACTED]"},
		{"html-escaped", `echo a\u003cb and a<b`, "a<b", "echo [REDACTED] and [REDACTED]"},
		{"node-style echo, no html escaping", `echo a\"<b`, `a"<b`, "echo [REDACTED]"},
		{"go-style echo, html escaped", `echo a\"\u003cb`, `a"<b`, "echo [REDACTED]"},
		{"trailing quote is fully redacted", `echo abc\"`, `abc"`, "echo [REDACTED]"},
		{"empty token", "unchanged", "", "unchanged"},
	} {
		if got := redactToken(c.in, c.token); got != c.want {
			t.Errorf("%s: redactToken(%q, %q) = %q, want %q", c.name, c.in, c.token, got, c.want)
		}
	}
}

// TestSetTokenErrorTokenBranchIsCapped2650 pins the cap on the NON-empty token
// branch (the empty branch has its own pin): removing SafeLine there must fail.
func TestSetTokenErrorTokenBranchIsCapped2650(t *testing.T) {
	const token = "tok-SENTINEL-2650"
	long := "boom\x1b[31m " + token + strings.Repeat("x", 3000) + "\n\x07tail"
	msg := setTokenError(&gqlerror.Error{Message: long}, token).Error()
	if n := len([]rune(msg)); n > maxErrMsgRunes {
		t.Errorf("message is %d runes, want at most %d", n, maxErrMsgRunes)
	}
	for _, r := range msg {
		if unicode.IsControl(r) {
			t.Errorf("message carries control character %q", r)
		}
	}
	if strings.Contains(msg, token) {
		t.Errorf("token leaked: %q", msg)
	}
}
