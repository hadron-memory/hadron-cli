package team

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// newCmdWorkerSetBootPrompt backs `hadron team worker set-boot-prompt`
// (cli#916 C2, #2045 S8): a customised worker's boot-prompt pointer. One
// mutation, nodeRef always on the wire (an explicit null clears). The App's
// manager acts as themselves (R23): setWorkerBootPrompt is deliberately not
// on the session-attribution allowlist (write_attribution.go), so a bound
// worktree's session is never sent.
func newCmdWorkerSetBootPrompt(f *cmdutil.Factory) *cobra.Command {
	var clear bool
	var memory string
	cmd := &cobra.Command{
		Use:   "set-boot-prompt <worker> (<node-ref> | --clear)",
		Short: "Point a worker at a compiled boot prompt, or clear the pointer",
		Long: `Set a worker's own boot-prompt pointer (for a customised worker), or remove it
with --clear. The node must be a compiled boot prompt in the worker's App's
install memory for the worker's Agent; the server checks that, and the CLI
looks nothing up first. Setting the pointer does not approve the prompt:
approval is checked when it is served.

<worker> is a name within the App, an id or a worker URN. <node-ref> is a node
id or a fully-qualified node URN, or a bare loc with -m/--memory. Only the
App's manager may set it, acting as themselves: the command never sends this
worktree's worker session, and the server refuses one (exit 8).

Exit codes: 2 for a node that is not a compiled prompt, has the wrong role or
is in the wrong memory; 4 when the node is missing or not readable (one answer
for both); 8 when you may not manage the App.`,
		Example: `  hadron team worker set-boot-prompt Iris hrn:node:acme.com:eng-team-iris:boot:workers:wkr1:compiled
  hadron team worker set-boot-prompt Iris --clear`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			nodeRef, err := cmdutil.BootPromptNodeRef(memory, args[1:], clear)
			if err != nil {
				return err
			}
			worker, err := workerForArg(cmd, f, args[0])
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.SetWorkerBootPrompt(cmd.Context(), client, worker.Id, nodeRef)
			if err != nil {
				return api.MapError(err)
			}
			if resp.SetWorkerBootPrompt == nil {
				return exitcode.Newf(exitcode.Error, "server returned no worker")
			}
			dto := workerDTOFromFields(resp.SetWorkerBootPrompt.WorkerFields)
			verb := "✓ set boot prompt of"
			if clear {
				verb = "✓ cleared boot prompt of"
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "%s worker %s\n  boot prompt: %s\n", verb, dto.Name, cmdutil.BootPromptLabel(dto.BootPromptRef, dto.BootPromptTargetDeleted))
				return err
			})
		},
	}
	cmd.Flags().BoolVar(&clear, "clear", false, "remove the worker's boot-prompt pointer instead of setting one")
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory of a bare <node-ref> loc (hrn:mem:<root>:<slug>)")
	return cmd
}
