package cmdutil

import (
	"bufio"
	"fmt"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// showPrompt renders the question on stderr and fails closed when it could not
// be written: a "y" given to a question nobody saw is not consent (#1077), so the
// caller must not go on to read stdin. The "Aborted." write on a declined answer
// stays unchecked on purpose: the action has already been refused by then.
func showPrompt(io *output.IOStreams, format string, args ...any) error {
	if _, err := fmt.Fprintf(io.ErrOut, format, args...); err != nil {
		return exitcode.Newf(exitcode.Error, "could not show the confirmation prompt (%v); nothing was sent", err)
	}
	return nil
}

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
	if err := showPrompt(io, "Delete %s? This cannot be undone. (y/N) ", what); err != nil {
		return err
	}
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

// Confirm gates a significant but non-deletion action behind the same
// rule as ConfirmDeletion: --yes skips the prompt, an interactive terminal
// is asked y/N, and a non-interactive caller without --yes is refused with
// a Usage error so the action is always explicit. `prompt` is the full
// question, without the trailing " (y/N)".
func Confirm(io *output.IOStreams, yes bool, prompt string) error {
	if yes {
		return nil
	}
	if !io.IsInputTerminal() {
		return exitcode.Newf(exitcode.Usage, "refusing to proceed without --yes in non-interactive mode")
	}
	if err := showPrompt(io, "%s (y/N) ", prompt); err != nil {
		return err
	}
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
