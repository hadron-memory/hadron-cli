package cmdutil

import (
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/spf13/cobra"
)

// RetiredFlag names a flag that was removed. Bool marks a value-less flag
// (the --clear-* family); every other retired flag took a value.
type RetiredFlag struct {
	Name string
	Bool bool
}

// RetireFlags keeps removed flags parseable, hidden, so an old invocation is
// refused with `hint` (a migration route) instead of cobra's bare "unknown
// flag" (cli#1175). The refusal is a Usage error raised in PreRunE, before
// the command resolves auth or builds a client: it sends nothing. The flags
// are never usable and never listed in help. An existing PreRunE still runs
// after the refusal check.
func RetireFlags(cmd *cobra.Command, hint string, flags ...RetiredFlag) {
	for _, rf := range flags {
		if rf.Bool {
			cmd.Flags().Bool(rf.Name, false, "retired: refused, sends nothing")
		} else {
			cmd.Flags().String(rf.Name, "", "retired: refused, sends nothing")
		}
		_ = cmd.Flags().MarkHidden(rf.Name)
	}
	prior := cmd.PreRunE
	cmd.PreRunE = func(c *cobra.Command, args []string) error {
		for _, rf := range flags {
			if c.Flags().Changed(rf.Name) {
				return exitcode.Newf(exitcode.Usage, "--%s was retired: %s", rf.Name, hint)
			}
		}
		if prior != nil {
			return prior(c, args)
		}
		return nil
	}
}
