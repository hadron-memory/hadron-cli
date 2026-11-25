package cmdutil

import (
	"errors"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2968: the shared connection helpers. The suggestion text is pinned
// byte for byte, because the two commands' messages must stay what they were
// before the helpers moved.
func TestConnectionStatusSuggestionIsByteIdenticalPerCommand(t *testing.T) {
	const lead = "The listing is paged, so follow its --offset hint before concluding it is gone. Check whether it still exists by listing connections with:\n  hadron "
	for _, cmd := range []string{"slack", "telegram"} {
		if got, want := ConnectionStatusSuggestion(cmd), lead+cmd+" status"; got != want {
			t.Errorf("%s: got %q, want %q", cmd, got, want)
		}
	}
}

func TestHasControlOrFormatCharacter(t *testing.T) {
	for s, want := range map[string]bool{
		"plain-id": false, "a\x1b[31m": true, "a\nb": true, "a\u200bb": true, "a\u2028b": true, "a\u2029b": true,
	} {
		if got := HasControlOrFormatCharacter(s); got != want {
			t.Errorf("%q: got %v, want %v", s, got, want)
		}
	}
}

// A refusal that echoes a submitted secret reaches the operator without it,
// whichever command built the base; the exit class survives.
func TestFinishConnectErrorWithholdsEverySecret(t *testing.T) {
	secrets := []string{"xoxb-SECRET-ONE", "xapp-SECRET-TWO"}
	raw := errors.New("server said xoxb-SECRET-ONE and xapp-SECRET-TWO")
	base := exitcode.Newf(exitcode.Error, "connect outcome unknown: %s", raw)
	got := FinishConnectError(raw, base, secrets)
	for _, s := range secrets {
		if strings.Contains(got.Error(), s) {
			t.Errorf("secret %q leaked: %v", s, got)
		}
	}
	if exitcode.FromError(got) != exitcode.Error {
		t.Errorf("exit class lost: %v", exitcode.FromError(got))
	}
}
