package memory

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// dataKeyInput is where `memory encrypt` and `memory link-user` get their data
// key (#1657, ruling 0148): a mode-checked file (--data-key-file, "-" for
// stdin), stdin (--data-key -), or, deprecated, a literal in argv. A raw key in
// argv shows in `ps`, /proc/<pid>/cmdline and shell history, so a literal still
// works for this release but warns once on stderr; its refusal is a later
// change. One shared type keeps the two commands from drifting.
type dataKeyInput struct {
	literal      string // --data-key
	literalSet   bool   // --data-key was given (even as "")
	filePath     string // --data-key-file
	fileSet      bool   // --data-key-file was given (even as "")
	fileKey      string // the key, read up front from a regular file
	fileKeyReady bool
}

// newDataKeyInput checks the data-key flags of one run: --data-key and
// --data-key-file are mutually exclusive, an empty --data-key-file= is refused
// (presence, not emptiness: cli#1340), and a key file is read, and its mode
// checked, BEFORE any request. "-" (stdin) is read later, after the
// confirmation, because it consumes the same stream the prompt is answered on;
// on a terminal it is refused here, before that prompt (cli#2689).
func newDataKeyInput(cmd *cobra.Command, ios *output.IOStreams, literal, filePath string) (*dataKeyInput, error) {
	d := &dataKeyInput{
		literal: literal, literalSet: cmd.Flags().Changed("data-key"),
		filePath: filePath, fileSet: cmd.Flags().Changed("data-key-file"),
	}
	if d.fileSet && d.literalSet {
		return nil, exitcode.Newf(exitcode.Usage, "--data-key and --data-key-file are mutually exclusive: give one. Nothing was sent")
	}
	if d.fileSet && strings.TrimSpace(filePath) == "" {
		return nil, exitcode.Newf(exitcode.Usage, "--data-key-file is empty: name a file, or - for stdin. Nothing was sent")
	}
	if d.fileSet && filePath == "-" {
		if err := cmdutil.RefuseSecretStdinOnTerminal(ios, "--data-key-file -", "pass a key file path to --data-key-file"); err != nil {
			return nil, err
		}
	}
	if d.literalSet && literal == "-" {
		if err := cmdutil.RefuseSecretStdinOnTerminal(ios, "--data-key -", "pass a key file with --data-key-file"); err != nil {
			return nil, err
		}
	}
	if d.fileSet && filePath != "-" {
		data, err := cmdutil.ReadSecretFile(filePath, "--data-key-file")
		if err != nil {
			return nil, err
		}
		key := strings.TrimSpace(string(data))
		if key == "" {
			return nil, exitcode.Newf(exitcode.Usage, "--data-key-file holds no key. Nothing was sent")
		}
		d.fileKey, d.fileKeyReady = key, true
	}
	return d, nil
}

// Supplied reports whether any data-key flag was given.
func (d *dataKeyInput) Supplied() bool { return d.fileSet || d.literalSet }

// WarnLiteral prints the one deprecation line when the key is a literal in
// argv. It never names or echoes the value, and "-" (stdin) is the supported
// form and prints nothing.
func (d *dataKeyInput) WarnLiteral(stderr io.Writer) {
	if d.fileSet || !d.literalSet || d.literal == "-" || strings.TrimSpace(d.literal) == "" {
		return
	}
	fmt.Fprintln(stderr, "warning: a literal --data-key is deprecated: the key is visible in process listings and shell history. Use --data-key-file <file> (mode 600; - reads stdin) instead. A later release will refuse a literal.")
}

// Read returns the key: the file read up front, stdin for "-", or the literal.
func (d *dataKeyInput) Read(stdin io.Reader) (string, error) {
	switch {
	case d.fileKeyReady:
		return d.fileKey, nil
	case d.fileSet: // "-"
		data, err := io.ReadAll(stdin)
		if err != nil {
			return "", exitcode.Newf(exitcode.Usage, "reading --data-key-file from stdin: the input could not be read. Nothing was sent")
		}
		key := strings.TrimSpace(string(data))
		if key == "" {
			return "", exitcode.Newf(exitcode.Usage, "--data-key-file holds no key. Nothing was sent")
		}
		return key, nil
	}
	return readDataKey(d.literal, stdin)
}
