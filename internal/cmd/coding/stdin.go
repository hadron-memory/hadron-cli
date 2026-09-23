package coding

import "github.com/hadron-memory/hadron-cli/internal/cmdutil"

// refuseContentStdin refuses `--content -` from an interactive terminal for
// the check-authoring commands (#648). A check or preflight body is a
// DOCUMENT, not a secret, and a terminal's line discipline can truncate it
// before the CLI reads a byte. Called FIRST in RunE: codingScope may resolve
// the memory over the network, and an argument this invalid should not cost
// that round trip. resolveCheckBody does not repeat the check — every caller
// has already made it, so a second copy could never fire.
func refuseContentStdin(stdinIsTerminal bool, content string) error {
	if content != "-" {
		return nil
	}
	return cmdutil.RefuseDocumentStdinFromTerminal(stdinIsTerminal, "--content -", "--content-file")
}

// refuseDiffStdin refuses `review run --diff -` from an interactive terminal.
// The diff is parsed for the changed paths, so a truncated one silently drops
// files, and the run then selects checks for a change set smaller than the
// real one: an all-clear wider than the read.
func refuseDiffStdin(stdinIsTerminal bool, diffSpec string) error {
	if diffSpec != "-" {
		return nil
	}
	return cmdutil.RefuseDocumentStdinFromTerminal(stdinIsTerminal, "--diff -", "--diff")
}
