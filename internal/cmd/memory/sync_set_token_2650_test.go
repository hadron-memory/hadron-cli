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
	if got := carriedCode(setTokenError(mk("BAD "+token), token)); strings.Contains(got, token) {
		t.Errorf("server code still carries the token: %q", got)
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
