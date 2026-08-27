package team

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// lastResort decides what the failed-spill branch may PRINT, and printing is
// the last option rather than the first (PR #528 review, @codex).
//
// A handoff is a stint's working notes — what is blocked, what is half-done,
// which customer — and stderr is retained in CI logs and agent transcripts. So
// the content goes out only when it genuinely has nowhere else to be.
//
// Driven here rather than through the command because the interesting case —
// a source file that existed at read time and is gone by report time — cannot
// be sequenced from outside the process.
func TestLastResort(t *testing.T) {
	const prose = "blocked on the vendor key; do not re-run the migration"

	t.Run("a file that still exists is pointed at, not reprinted", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "handoff.md")
		if err := os.WriteFile(path, []byte(prose), 0o600); err != nil {
			t.Fatal(err)
		}
		got := lastResort(handoffRescue{text: prose, src: handoffSource{path: path}})
		if !strings.Contains(got, path) {
			t.Errorf("it must name the file that still holds it: %q", got)
		}
		if strings.Contains(got, prose) {
			t.Errorf("already safe on disk; reprinting into a log is needless exposure: %q", got)
		}
	})

	// THE REASON THE Stat IS THERE. The spill may have failed because the disk
	// filled or the volume went away — either could have taken the source with
	// it. Pointing at a file that is not there would be the most confident
	// possible way to lose the prose.
	t.Run("a file that has vanished falls back to printing", func(t *testing.T) {
		gone := filepath.Join(t.TempDir(), "never-written.md")
		got := lastResort(handoffRescue{text: prose, src: handoffSource{path: gone}})
		if !strings.Contains(got, prose) {
			t.Errorf("the in-memory copy is the only one left: %q", got)
		}
		if strings.Contains(got, "unchanged at") {
			t.Errorf("it must not point at a file that no longer exists: %q", got)
		}
	})

	t.Run("a consumed pipe is printed", func(t *testing.T) {
		got := lastResort(handoffRescue{text: prose, src: handoffSource{fromStdin: true}})
		if !strings.Contains(got, prose) || !strings.Contains(got, "handoff begins") {
			t.Errorf("a consumed pipe has no other copy, and multi-line prose needs delimiters: %q", got)
		}
	})

	t.Run("an inline argument is not reprinted", func(t *testing.T) {
		got := lastResort(handoffRescue{text: prose, src: handoffSource{}})
		if strings.Contains(got, prose) {
			t.Errorf("the shell history or calling process still holds it: %q", got)
		}
		if !strings.Contains(got, "--handoff-file") {
			t.Errorf("it must still say how to recover: %q", got)
		}
	})
}
