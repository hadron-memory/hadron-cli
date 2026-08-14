package app

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/approster"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// newCmdAgent builds `hadron app agent <command>` — the App-agents surface
// (#408).
//
// The AppAgent join is a general App concept (cor:dmo:050:03) and multi-agent
// Apps predate the team feature (spec 023), but the only command that listed
// an App's agents was `hadron team roster`. Someone holding a plain
// WORKSTATION App has no reason to look under `team`, so the likely wrong turn
// was `hadron agent list --app <a>` — which looks right and silently returns
// every readable agent, because --app is the App-CONTEXT flag rather than a
// filter (#383). The discoverability gap routed people at the one command that
// gives a plausible wrong answer.
//
// The write half (`agent add` / `agent remove`) is #389, which needs a server
// binding to attach an existing Agent to an existing App; this read needs none,
// so it lands independently.
func newCmdAgent(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "agent <command>",
		Aliases: []string{"agents"},
		Short:   "Agents installed in an App",
	}
	cmd.AddCommand(newCmdAgentList(f))
	return cmd
}

func newCmdAgentList(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "list [<app-ref>]",
		Aliases: []string{"ls"},
		Short:   "List the Agents installed in an App",
		Long: `List an App's installed Agents — the AppAgent join (cor:dmo:050:03), which
is what "which agents does this App run?" actually means.

The App is the positional ref (ID or URN); omit it to use --app or the
configured App context.

Note that ` + "`hadron agent list --app <a>`" + ` does NOT answer this: --app is the
persistent App-context flag, not a filter, so it returns every agent you can
read whichever App you name (#383). This command is the filter.

For a team App the same read is ` + "`hadron team roster`" + `, which adds the
worktree-binding fallback; the persona columns are blank here for a plain
single-agent App, which is the honest answer rather than an empty list.`,
		Example: `  hadron app agent list hrn:app:acme.com:support
  hadron app agent list --app acme.com::eng-team --json`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appRef := ""
			if len(args) == 1 {
				appRef = args[0]
			} else {
				ambient, err := f.App()
				if err != nil {
					return err
				}
				appRef = ambient
			}
			if appRef == "" {
				return exitcode.Newf(exitcode.Usage,
					"no App — pass one as the argument, use --app <ref>, or set a default with `hadron app set-active`")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			r, err := approster.Fetch(cmd.Context(), client, appRef)
			if err != nil {
				return err
			}
			return output.Write(f.IOStreams, f.JSON, r.Members, func(w io.Writer) error {
				return approster.Render(w, r)
			})
		},
	}
	return cmd
}
