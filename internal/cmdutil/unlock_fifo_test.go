//go:build !windows

package cmdutil

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// A FIFO (or any non-regular file) is refused without opening it: opening a
// FIFO for reading would block the invocation until a writer appears.
func TestResolveUnlockRefusesNonRegularFiles(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skip("mkfifo:", err)
	}
	for _, path := range []string{fifo, dir} {
		done := make(chan error, 1)
		go func() {
			ios, _, _ := output.Test()
			_, err := ResolveUnlock(ios, UnlockFlags{Memories: []string{"acme.com:kb"}, Files: []string{path}, FilesSet: true})
			done <- err
		}()
		select {
		case err := <-done:
			if exitcode.FromError(err) != exitcode.Usage || !strings.Contains(err.Error(), "not a regular file") {
				t.Fatalf("%s: err=%v", path, err)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("%s: blocked opening a non-regular file", path)
		}
	}
}
