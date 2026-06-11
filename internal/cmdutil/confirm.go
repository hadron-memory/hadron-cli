package cmdutil

import (
	"bufio"
	"fmt"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// ConfirmDeletion gates destructive commands. --yes skips the prompt;
// otherwise an interactive terminal asks y/N, and a non-interactive
// caller (scripts, agents) is refused with a Usage error so deletions
// are always explicit.
func ConfirmDeletion(io *output.IOStreams, yes bool, what string) error {
	if yes {
		return nil
	}
	// The prompt is answered on stdin (and rendered on stderr), so
	// stdin is what must be interactive — a redirected stdout doesn't
	// preclude asking.
	if !io.IsInputTerminal() {
		return exitcode.Newf(exitcode.Usage, "refusing to delete %s without --yes in non-interactive mode", what)
	}
	fmt.Fprintf(io.ErrOut, "Delete %s? This cannot be undone. (y/N) ", what)
	scanner := bufio.NewScanner(io.In)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return fmt.Errorf("reading confirmation: %w", err)
		}
		return exitcode.Silent(exitcode.Cancelled)
	}
	answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
	if answer != "y" && answer != "yes" {
		fmt.Fprintln(io.ErrOut, "Aborted.")
		return exitcode.Silent(exitcode.Cancelled)
	}
	return nil
}
