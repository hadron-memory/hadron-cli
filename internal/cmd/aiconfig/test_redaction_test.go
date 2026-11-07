package aiconfig

import (
	"errors"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func TestRedactTestError1538Controls(t *testing.T) {
	original := exitcode.New(exitcode.Forbidden, errors.New("permission denied"))
	for _, key := range []string{"", "sk-not-echoed"} {
		if got := redactTestError(original, key); got != original {
			t.Fatal("non-echo error chain changed")
		}
	}
	if got := redactTestError(nil, "secret"); got != nil {
		t.Fatal("nil error changed")
	}
	for _, key := range []string{"redacted", "[", "x"} {
		got := redactTestError(exitcode.Newf(exitcode.Usage, "invalid key %s", key), key)
		if exitcode.FromError(got) != exitcode.Usage {
			t.Fatal("classification changed")
		}
		// The chosen placeholder must not itself reintroduce the submitted value.
		if strings.Contains(got.Error(), key) {
			t.Fatal("short key was not removed")
		}
	}
}
