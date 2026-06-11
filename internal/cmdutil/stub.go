package cmdutil

import (
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// NewStubCommand registers a not-yet-implemented command so the v1
// surface is visible in help output. Running it exits with the
// Usage code.
func NewStubCommand(use, short string) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short + " (not yet implemented)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return exitcode.Newf(exitcode.Usage, "`hadron %s` is not implemented yet", cmd.CommandPath()[len("hadron "):])
		},
	}
}
