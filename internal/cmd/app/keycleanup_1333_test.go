package app

import (
	"errors"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// A refusal whose cleanup also failed keeps both the server code (for the
// --json envelope) and the exit code, and names the cleanup failure.
func TestWithCleanupKeepsServerCodeAndExit(t *testing.T) {
	failure := &codedError{error: exitcode.Newf(exitcode.Forbidden, "refused"), code: "FORBIDDEN"}
	got := withCleanup(failure, errors.New("cannot remove key file"))
	var coded interface{ ServerCode() string }
	if !errors.As(got, &coded) || coded.ServerCode() != "FORBIDDEN" {
		t.Fatalf("server code lost: %v", got)
	}
	if exitcode.FromError(got) != exitcode.Forbidden {
		t.Errorf("exit = %d", exitcode.FromError(got))
	}
	if msg := got.Error(); msg != "refused. Also: cannot remove key file" {
		t.Errorf("message = %q", msg)
	}
	if plain := withCleanup(exitcode.Newf(exitcode.Error, "x"), errors.New("y")); errors.As(plain, &coded) {
		t.Errorf("an uncoded failure must stay uncoded")
	}
}
