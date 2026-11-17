package cmdutil

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"syscall"

	"golang.org/x/term"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// ReadSecretInput reads write-only secret material without ever taking it as
// an argument, where the shell history and the process list would keep it.
// source is the value of the command's file flag (named by flag, without the
// dashes):
//
//	"-"       read stdin
//	"@path"   read a file (a plain path works too)
//	""        read piped stdin, or prompt with label and no echo on a terminal
//
// Trailing newlines are trimmed; an empty value is a usage error naming what
// (e.g. "secret value"). The material itself never appears in an error.
func ReadSecretInput(ioStreams *output.IOStreams, source, flag, label, what string) (string, error) {
	var data []byte
	var err error
	switch {
	case source == "-":
		data, err = io.ReadAll(ioStreams.In)
	case strings.HasPrefix(source, "@"):
		data, err = os.ReadFile(strings.TrimPrefix(source, "@"))
	case source != "":
		data, err = os.ReadFile(source)
	case !ioStreams.IsInputTerminal():
		data, err = io.ReadAll(ioStreams.In)
	default:
		fmt.Fprintf(ioStreams.ErrOut, "%s: ", label)
		input, ok := ioStreams.In.(*os.File)
		if !ok {
			fmt.Fprintln(ioStreams.ErrOut)
			return "", exitcode.Newf(exitcode.Usage, "interactive secret prompt requires file-backed stdin")
		}
		data, err = term.ReadPassword(int(input.Fd()))
		fmt.Fprintln(ioStreams.ErrOut)
	}
	if err != nil {
		if source != "" && source != "-" {
			// Never %v the read error: os.ReadFile's *PathError repeats the
			// argument, which may be the secret itself pasted in place of a
			// path (#1490). Name the flag and a fixed failure class only.
			return "", exitcode.Newf(exitcode.Usage, "reading --%s: %s; pass -, @path or a file path", flag, readFailureClass(err))
		}
		return "", err
	}
	value := strings.TrimRight(string(data), "\r\n")
	if value == "" {
		return "", exitcode.Newf(exitcode.Usage, "no %s provided; pipe it on stdin, pass --%s, or run interactively", what, flag)
	}
	return value, nil
}

// readFailureClass describes why a secret-material file could not be read,
// as a fixed phrase that never includes the path (#1490).
func readFailureClass(err error) string {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "the file was not found"
	case errors.Is(err, fs.ErrPermission):
		return "the file is not readable (permission denied)"
	case errors.Is(err, syscall.EISDIR):
		return "the path is a directory, not a file"
	default:
		return "the file could not be read"
	}
}
