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

// TestTTY returns IOStreams whose STDIN is an answerable terminal, with
// `answers` standing in for what a person would type — one line per prompt.
//
// This is the seam for hadron-cli#525. Test() leaves inIsTerminal false and
// there is no setter, so cmdutil.Confirm and ConfirmDeletion refuse as
// non-interactive BEFORE composing their prompt — putting every confirmation in
// the CLI beyond the reach of a command-level test. Not one untested guard: a
// door with no handle, which every future confirmation inherits. The concrete
// cost was a mutation flipping `staleReleasePrompt`'s meKnown argument to true
// and passing the entire suite (PR #524), because the call site behind Confirm
// could not be driven. Extracting the composition pins the STRING; only this
// pins the CHOICE OF ARGUMENTS.
//
// The answers are a CONSTRUCTOR ARGUMENT rather than a field a caller sets
// afterwards, because the half-configured state is a trap that looks like a
// result. Test()'s In is an empty buffer, so flipping the TTY bit alone makes
// scanner.Scan() return false and Confirm returns Cancelled — a DIFFERENT
// unreachable branch, and one indistinguishable from a person answering "no".
// Requiring the answers up front means the seam cannot be half-used.
//
// Stdout is deliberately NOT marked a terminal. Confirm reads stdin only, and
// interactive-stdin-with-redirected-stdout is a real configuration a user has
// (`hadron … | less` from a shell), so this is an honest process state rather
// than a shape that exists only in tests. Widen it when something needs the
// stdout branch, not before.
func TestTTY(answers string) (*IOStreams, *bytes.Buffer, *bytes.Buffer) {
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	return &IOStreams{
		In:           strings.NewReader(answers),
		Out:          out,
		ErrOut:       errOut,
		inIsTerminal: true,
	}, out, errOut
}

// IsTerminal reports whether stdout is a TTY.
func (s *IOStreams) IsTerminal() bool { return s.outIsTerminal }

// IsInputTerminal reports whether stdin is a TTY (i.e. a prompt can
// actually be answered).
func (s *IOStreams) IsInputTerminal() bool { return s.inIsTerminal }
