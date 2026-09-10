package chat

import (
	"errors"
	"testing"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// errReader fails every Read — a stand-in for a stdin whose read errors
// (a closed pipe, an I/O fault) mid-consume.
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("stdin blew up") }

// #390 (#574 review): the `--body -` stdin path shares the exit-code contract
// with --body-file — a read failure is Usage (2), not the generic 1 the raw
// error classifies as. The command-level test covers a missing --body-file;
// this covers the sibling stdin branch, which a real command can't easily
// drive (its stdin is a buffer).
func TestResolveBodyStdinReadErrorIsUsage(t *testing.T) {
	_, err := ResolveBody(&cobra.Command{}, "-", "", errReader{})
	if err == nil {
		t.Fatal("a failed stdin read must be an error")
	}
	if code := exitcode.FromError(err); code != exitcode.Usage {
		t.Errorf("exit = %d, want Usage (2)", code)
	}
}
