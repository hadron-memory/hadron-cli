package hostpoller

import (
	"os"
	"path/filepath"
	"testing"
)

// cli#2059: the fallback state is bound to no file, so it can never be saved
// over an unreadable state file.
func TestEmptyStateCannotBeSaved2059(t *testing.T) {
	if err := EmptyState().save(); err == nil {
		t.Fatal("a state with no file saved without error")
	}
	// The cwd must be untouched too: no ".tmp" next to nothing.
	if _, err := os.Stat(filepath.Join(".", ".tmp")); err == nil {
		t.Fatal("a stray .tmp was written")
	}
}
