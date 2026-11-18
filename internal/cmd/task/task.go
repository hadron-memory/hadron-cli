package task

import (
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
)

// NewCmdTask builds the `hadron task` command group.
func NewCmdTask(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task",
		Short: "Run or render a task node",
	}
	cmd.AddCommand(newCmdRun(f))
	cmd.AddCommand(newCmdRender(f))
	return cmd
}
