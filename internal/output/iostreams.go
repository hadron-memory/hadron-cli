package output

import (
	"bytes"
	"io"
	"os"

	"github.com/mattn/go-isatty"
)

// IOStreams bundles the CLI's input/output handles so commands never
// touch os.Stdin/Stdout directly, keeping them testable.
type IOStreams struct {
	In     io.Reader
	Out    io.Writer
	ErrOut io.Writer

	outIsTerminal bool
}

// System returns IOStreams wired to the real process streams.
func System() *IOStreams {
	return &IOStreams{
		In:            os.Stdin,
		Out:           os.Stdout,
		ErrOut:        os.Stderr,
		outIsTerminal: isatty.IsTerminal(os.Stdout.Fd()) || isatty.IsCygwinTerminal(os.Stdout.Fd()),
	}
}

// Test returns IOStreams backed by buffers, plus the stdout and
// stderr buffers for assertions.
func Test() (*IOStreams, *bytes.Buffer, *bytes.Buffer) {
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	return &IOStreams{In: &bytes.Buffer{}, Out: out, ErrOut: errOut}, out, errOut
}

// IsTerminal reports whether stdout is a TTY.
func (s *IOStreams) IsTerminal() bool { return s.outIsTerminal }
