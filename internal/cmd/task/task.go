package task

import (
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
)

// NewCmdTask builds the `hadron task` command group.
func NewCmdTask(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task",
		Short: "Print a task node's prompt, or run it under an App",
		Long: `A task node is a node marked runnable: its text is a prompt for an agent, and
it may declare arguments it accepts.

  hadron task render <task>   read-only: print the prompt with arguments filled in
  hadron task run <task>      prints the same prompt; with --app <ref> it instead
                              STARTS a real run under that App and prints the run id

Nothing executes unless you pass --app to "task run". Follow a started run with
"hadron run get <run-id>" (status, cost, failure reason).`,
	}
	cmd.AddCommand(newCmdRun(f))
	cmd.AddCommand(newCmdRender(f))
	return cmd
}
