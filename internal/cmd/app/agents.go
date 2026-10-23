package app

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
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
// The write half (`agent add` / `agent remove`, #389) wraps the server's
// installAgentIntoApp / uninstallAgentFromApp — see agent_membership.go.
func newCmdAgent(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "agent <command>",
		Aliases: []string{"agents"},
		Short:   "Agents installed in an App",
	}
	cmd.AddCommand(newCmdAgentList(f))
	cmd.AddCommand(newCmdAgentAdd(f))
	cmd.AddCommand(newCmdAgentRemove(f))
	cmd.AddCommand(newCmdAgentSetBootPrompt(f))
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

For a team App this is the INSTALL roster — the cast pool. The named staff
(workers cast from these agents) is ` + "`hadron team worker list`" + ` (#428). The
ROLE column is blank for an agent without persona dressing, which is the
honest answer rather than an empty list.`,
		Example: `  hadron app agent list hrn:app:acme.com:support
  hadron app agent list --app hrn:app:acme.com:eng-team --json`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appRef := ""
			if len(args) == 1 {
				var err error
				if appRef, err = cmdutil.CanonicalAppRef("<app-ref>", args[0]); err != nil {
					return err
				}
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

// appAgentBootPromptDTO is the stable --json shape of `app agent
// set-boot-prompt`: the install's pointer after the write. The Agent and App
// keys are null when the server withholds them (an installation alone does not
// grant a read of the definition); the pointer pair is null when unset or
// withheld.
type appAgentBootPromptDTO struct {
	AppID                   *string `json:"appId"`
	AppURN                  *string `json:"appUrn"`
	AgentID                 *string `json:"agentId"`
	AgentURN                *string `json:"agentUrn"`
	AgentName               *string `json:"agentName"`
	BootPromptRef           *string `json:"bootPromptRef"`
	BootPromptTargetDeleted *bool   `json:"bootPromptTargetDeleted"`
}

// newCmdAgentSetBootPrompt backs `hadron app agent set-boot-prompt` (cli#916
// C2, #2045 S8): an installed Agent's boot-prompt pointer for one App. As
// `agent set-boot-prompt`: one mutation, an explicit null clears, and no
// worker session is sent (R23).
func newCmdAgentSetBootPrompt(f *cmdutil.Factory) *cobra.Command {
	var clear bool
	var memory string
	cmd := &cobra.Command{
		Use:   "set-boot-prompt <app> <agent> (<node-ref> | --clear)",
		Short: "Point an installed Agent at a compiled boot prompt for this App, or clear it",
		Long: `Set the boot-prompt pointer of an Agent installed in this App, or remove it
with --clear. The node must be a compiled boot prompt in this App's install
memory for that Agent; the server checks that, and the CLI looks nothing up
first. Setting the pointer does not approve the prompt: approval is checked
when it is served.

<node-ref> is a node id or a fully-qualified node URN, or a bare loc with
-m/--memory. Only the App's manager may set it, acting as themselves: the
command never sends a bound worktree's worker session, and the server refuses
one (exit 8).

Exit codes: 2 for a node that is not a compiled prompt, has the wrong role or
is in the wrong memory; 4 when the node is missing or not readable (one answer
for both); 8 when you may not manage the App.`,
		Example: `  hadron app agent set-boot-prompt hrn:app:acme.com:eng-team hrn:agent:acme.com:iris hrn:node:acme.com:eng-team-iris:boot:compiled
  hadron app agent set-boot-prompt hrn:app:acme.com:eng-team hrn:agent:acme.com:iris --clear`,
		Args: cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			appRef, err := cmdutil.CanonicalAppRef("<app>", args[0])
			if err != nil {
				return err
			}
			agentRef := strings.TrimSpace(args[1])
			if agentRef == "" {
				return exitcode.Newf(exitcode.Usage, "the agent reference is empty")
			}
			nodeRef, err := cmdutil.BootPromptNodeRef(memory, args[2:], clear)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.SetAppAgentBootPrompt(cmd.Context(), client, appRef, agentRef, nodeRef)
			if err != nil {
				return api.MapError(err)
			}
			row := resp.SetAppAgentBootPrompt
			if row == nil {
				return exitcode.Newf(exitcode.Error, "server returned no AppAgent row")
			}
			dto := appAgentBootPromptDTO{BootPromptRef: row.BootPromptRef, BootPromptTargetDeleted: row.BootPromptTargetDeleted}
			if row.App != nil {
				dto.AppID, dto.AppURN = &row.App.Id, &row.App.Urn
			}
			if row.Agent != nil {
				dto.AgentID, dto.AgentURN, dto.AgentName = &row.Agent.Id, &row.Agent.Urn, &row.Agent.Name
			}
			verb := "✓ set boot prompt of"
			if clear {
				verb = "✓ cleared boot prompt of"
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "%s %s in %s\n  boot prompt: %s\n", verb, orRef(dto.AgentURN, agentRef), orRef(dto.AppURN, appRef), cmdutil.BootPromptLabel(dto.BootPromptRef, dto.BootPromptTargetDeleted))
				return err
			})
		},
	}
	cmd.Flags().BoolVar(&clear, "clear", false, "remove the install's boot-prompt pointer instead of setting one")
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory of a bare <node-ref> loc (hrn:mem:<root>:<slug>)")
	return cmd
}

// orRef prints the server's URN when it returned one, else the ref as sent.
func orRef(urn *string, sent string) string {
	if urn != nil && *urn != "" {
		return *urn
	}
	return sent
}
