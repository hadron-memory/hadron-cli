package cmdutil

import (
	"io"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// ReadDocumentStdin reads a DOCUMENT-shaped value from stdin (a node body, a
// message, a handoff) and refuses an interactive terminal (#643).
//
// A terminal in canonical mode has a line-discipline buffer — typically 4 KB —
// and a writer that exceeds it without the reader draining loses the overflow.
// So an agent driving the CLI through a PTY can hand over a 10 KB document,
// have the command exit 0, and store a silently truncated node. That was
// observed: an agent-authored report came back with unrelated sections missing
// and others joined together, matching the mangled terminal input rather than
// the source file.
//
// REFUSED rather than warned, because the damage is done before anything could
// warn: the bytes are lost in the kernel before the CLI reads them, so there is
// nothing to detect afterwards and no byte count here that would be
// trustworthy. The documented form — `cat file | hadron …` — is a pipe, not a
// terminal, so this refuses only an undocumented use whose one outcome is
// corruption.
//
// NOT every `-` reader should call this. `memory encrypt --data-key -` and
// `secret set --value -` read a SECRET from a terminal deliberately, so it
// never enters argv or shell history, and they are short and single-line —
// guarding them would break the safe path they exist to offer. The test is
// whether the value is a DOCUMENT, not whether the stream is a TTY.
//
// flag names the source in the diagnostic (e.g. `--content -`), and fileFlag
// the companion that reads a path (e.g. `--content-file`), so the remedy is
// specific to the caller rather than generic advice.
func ReadDocumentStdin(stdin io.Reader, stdinIsTerminal bool, flag, fileFlag string) (string, error) {
	if err := RefuseDocumentStdinFromTerminal(stdinIsTerminal, flag, fileFlag); err != nil {
		return "", err
	}
	data, err := io.ReadAll(stdin)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// RefuseDocumentStdinFromTerminal is ReadDocumentStdin's refusal on its own, so
// a command can fail FAST — before it resolves refs or fetches the node it is
// about to edit. `node update` reads the target before building its input, and
// a guard that only fired at the read would spend two network round trips
// before rejecting an argument it could have rejected immediately.
//
// One definition, two entry points: the message lives here and
// ReadDocumentStdin delegates, so an early check and a late one can never
// disagree about what is refused or what it says.
func RefuseDocumentStdinFromTerminal(stdinIsTerminal bool, flag, fileFlag string) error {
	if stdinIsTerminal {
		return exitcode.Newf(exitcode.Usage,
			"%s is reading from an interactive terminal, where the line discipline can "+
				"truncate or reorder large input BEFORE the CLI sees it — the write would "+
				"succeed and store corrupted content. Use %s <path>, or pipe it in "+
				"(cat file | hadron ...), which is not a terminal", flag, fileFlag)
	}
	return nil
}
