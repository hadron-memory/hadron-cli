package output

import (
	"bytes"
	"io"
	"os"
	"strings"

	"github.com/mattn/go-isatty"
)

// IOStreams bundles the CLI's input/output handles so commands never
// touch os.Stdin/Stdout directly, keeping them testable.
type IOStreams struct {
	In     io.Reader
	Out    io.Writer
	ErrOut io.Writer

	outIsTerminal bool
	inIsTerminal  bool
}

// System returns IOStreams wired to the real process streams.
func System() *IOStreams {
	return &IOStreams{
		In:            os.Stdin,
		Out:           os.Stdout,
		ErrOut:        os.Stderr,
		outIsTerminal: isatty.IsTerminal(os.Stdout.Fd()) || isatty.IsCygwinTerminal(os.Stdout.Fd()),
		inIsTerminal:  isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd()),
	}
}

// Test returns IOStreams backed by buffers, plus the stdout and
// stderr buffers for assertions.
func Test() (*IOStreams, *bytes.Buffer, *bytes.Buffer) {
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	return &IOStreams{In: &bytes.Buffer{}, Out: out, ErrOut: errOut}, out, errOut
}

// TestTTY returns IOStreams whose stdin is an answerable terminal (#525), with
// `answers` standing in for what a person types — one line per prompt, in
// order. Stdout is left non-terminal. Returns the stdout and stderr buffers;
// a prompt renders on stderr.
//
// Why it exists: cmdutil.Confirm and ConfirmDeletion refuse as non-interactive
// BEFORE composing their prompt, and Test() has no way to say otherwise, so
// every confirmation in the CLI was beyond the reach of a command-level test —
// a door with no handle that each new confirmation inherits.
//
// Three properties, each load-bearing:
//
//   - The answers are a CONSTRUCTOR argument, not a field set afterwards.
//     Test()'s In is an empty buffer, so flipping the TTY bit alone makes
//     scanner.Scan() return false and Confirm returns Cancelled — a different
//     unreachable branch, indistinguishable from a person answering "no". A
//     seam that can be half-used yields a green test proving nothing.
//   - One line PER READ, via lineReader rather than strings.NewReader. See
//     lineReader for why; a command that prompts twice is the case.
//   - Stdout stays non-terminal. Confirm reads stdin only, and interactive
//     stdin with redirected stdout is a real process state (`hadron … | less`),
//     so this is an honest configuration rather than a test-only shape. Widen
//     it when something needs the stdout branch, not before.
func TestTTY(answers string) (*IOStreams, *bytes.Buffer, *bytes.Buffer) {
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	return &IOStreams{
		In:           &lineReader{rest: answers},
		Out:          out,
		ErrOut:       errOut,
		inIsTerminal: true,
	}, out, errOut
}

// lineReader hands over at most ONE line per Read, which is what lets a script
// of answers survive more than one prompt.
//
// Each cmdutil.Confirm builds its own bufio.Scanner, and a scanner fills a
// private buffer on its first Read. Given a strings.Reader the first scanner
// therefore takes the WHOLE script, returns line 1, and discards the rest when
// it goes out of scope — so a command that asks twice (`worker release` prompts
// for the force-release, then again on WORKER_HOLD_STALE) sees EOF on the
// second prompt and returns Cancelled. That is a decline nobody wrote, and it
// looks exactly like the prompt working: the seam's own failure mode is the one
// it was built to eliminate. Found by @codex on PR #531.
//
// Stopping at the newline keeps each scanner's buffered read to a single
// answer, so the reader's position is where the next Confirm expects it.
type lineReader struct{ rest string }

func (r *lineReader) Read(p []byte) (int, error) {
	if r.rest == "" {
		return 0, io.EOF
	}
	line := r.rest
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i+1]
	}
	n := copy(p, line)
	r.rest = r.rest[n:]
	return n, nil
}

// IsTerminal reports whether stdout is a TTY.
func (s *IOStreams) IsTerminal() bool { return s.outIsTerminal }

// IsInputTerminal reports whether stdin is a TTY (i.e. a prompt can
// actually be answered).
func (s *IOStreams) IsInputTerminal() bool { return s.inIsTerminal }
