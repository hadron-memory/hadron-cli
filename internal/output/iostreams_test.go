package output

import (
	"bufio"
	"strings"
	"testing"
)

// TestTTY promises "one line per prompt", and a command that asks TWICE is not
// hypothetical: `team worker release` prompts for the force-release, and then
// again when the server answers WORKER_HOLD_STALE.
//
// Each cmdutil.Confirm builds its OWN bufio.Scanner. A scanner fills a private
// buffer on its first Read, so a reader that hands over everything at once
// gives the whole script to the first scanner, which returns line 1 and takes
// the rest to the grave. The second Confirm then sees EOF and returns
// Cancelled — a DECLINE the test never wrote, indistinguishable from the
// prompt being correctly refused.
//
// That is the failure this whole seam exists to prevent, in the seam itself:
// a green result that proves nothing. Caught by @codex on PR #531.
func TestTestTTYAnswersSuccessivePrompts(t *testing.T) {
	io, _, _ := TestTTY("y\nn\n")

	// Two independent scanners, exactly as two Confirm calls would build them.
	first := bufio.NewScanner(io.In)
	if !first.Scan() {
		t.Fatal("the first prompt got no answer at all")
	}
	if got := strings.TrimSpace(first.Text()); got != "y" {
		t.Errorf("first answer = %q, want \"y\"", got)
	}

	second := bufio.NewScanner(io.In)
	if !second.Scan() {
		t.Fatal("the second prompt got EOF — the first scanner swallowed the script")
	}
	if got := strings.TrimSpace(second.Text()); got != "n" {
		t.Errorf("second answer = %q, want \"n\"", got)
	}

	// And the script is finite: a third prompt is unanswered, which is what
	// makes an over-prompting command fail loudly rather than reuse an answer.
	if third := bufio.NewScanner(io.In); third.Scan() {
		t.Errorf("a third prompt must find the script exhausted, got %q", third.Text())
	}
}

// A single Read must not span two lines even when the buffer could hold both —
// that is the property the scanner-per-prompt case rests on, asserted directly
// so a "simplification" back to strings.NewReader is caught here rather than in
// whichever command happens to prompt twice.
func TestTestTTYHandsOverOneLinePerRead(t *testing.T) {
	io, _, _ := TestTTY("yes\nno\n")
	buf := make([]byte, 64)
	n, err := io.In.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got := string(buf[:n]); got != "yes\n" {
		t.Errorf("one Read returned %q, want just the first line", got)
	}
}

// Test() must stay non-interactive: it is what every existing test uses, and
// the whole non-interactive-refusal surface is pinned against it.
func TestTestIsNotATerminal(t *testing.T) {
	io, _, _ := Test()
	if io.IsInputTerminal() {
		t.Error("Test() must not report an answerable terminal")
	}
	ttyIO, _, _ := TestTTY("y\n")
	if !ttyIO.IsInputTerminal() {
		t.Error("TestTTY() must report an answerable terminal")
	}
	if ttyIO.IsTerminal() {
		t.Error("TestTTY() leaves stdout non-terminal deliberately")
	}
}
