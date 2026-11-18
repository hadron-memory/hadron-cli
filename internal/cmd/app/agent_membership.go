package app

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// appAgentDTO is the stable --json shape of one AppAgent join row.
type appAgentDTO struct {
	AppID       string  `json:"appId"`
	AppURN      string  `json:"appUrn"`
	AgentID     string  `json:"agentId"`
	AgentURN    string  `json:"agentUrn"`
	AgentName   string  `json:"agentName"`
	PersonaRole *string `json:"personaRole"`
	InstalledAt string  `json:"installedAt"`
	Status      string  `json:"status"`
}

// appAgentRemovedDTO is the stable --json shape of a detach. The server returns
// ids only, and deliberately reports success whether or not the row existed.
type appAgentRemovedDTO struct {
	AppID   string `json:"appId"`
	AgentID string `json:"agentId"`
	Status  string `json:"status"`
}

func newCmdAgentAdd(f *cmdutil.Factory) *cobra.Command {
	var trainingMode bool
	cmd := &cobra.Command{
		Use:     "add <app> <agent>",
		Aliases: []string{"install"},
		Short:   "Attach an existing Agent to an existing App",
		Long: `Install an existing Agent into an existing App. Both refs take an ID or a URN.

This is not ` + "`hadron app install`" + `, which creates a NEW App from an Agent. This
changes which Agents an App you already have can use: adding a role Agent to a
second team App, putting back one that was uninstalled by mistake, or
re-attaching an Agent to reach the memories that built up under it (the
platform keeps those ready to re-attach if the Agent is installed again, and
this is the command that does it).

--training-mode turns on the App's training flag: the App's sessions are marked
as training data for the Agent's owner. The flag belongs to the App, not to the
Agent: it applies to every Agent installed in this App, so setting it here
changes it for all of them. It is only sent when you pass it; if you do not
want the App's sessions used as training data, leave it out.

You need CONTRIBUTOR or higher on the App's owning organization, or to own the
App if it is user-owned: plain App membership is not enough, because attaching
an Agent lets everyone in the App read the Agent's design. A FORBIDDEN says so.
Prints "✓ installed <agent> (<urn>) into <app urn>"; --json is the installed
row.`,
		Example: `  hadron app agent add hrn:app:acme.com:eng-team hrn:agent:acme.com:iris
  hadron app agent add acme.com:eng-team hrn:agent:acme.com:iris --training-mode`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			appRef, err := cmdutil.CanonicalAppRef("<app>", args[0])
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			var training *bool
			if cmd.Flags().Changed("training-mode") {
				training = &trainingMode
			}
			resp, err := gen.InstallAgentIntoApp(cmd.Context(), client, appRef, args[1], training)
			if err != nil {
				return cmdutil.InstallForbiddenGuidance(err)
			}
			if resp.InstallAgentIntoApp == nil || resp.InstallAgentIntoApp.AppAgent == nil {
				return exitcode.Newf(exitcode.Error, "server returned no AppAgent row")
			}
			row := resp.InstallAgentIntoApp.AppAgent
			dto := appAgentDTO{Status: "installed", InstalledAt: row.CreatedAt}
			if row.Agent != nil {
				dto.AgentID, dto.AgentURN, dto.AgentName = row.Agent.Id, row.Agent.Urn, row.Agent.Name
				dto.PersonaRole = row.Agent.PersonaRole
			}
			if row.App != nil {
				dto.AppID, dto.AppURN = row.App.Id, row.App.Urn
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ installed %s (%s) into %s\n", dto.AgentName, dto.AgentURN, dto.AppURN)
				return err
			})
		},
	}
	cmd.Flags().BoolVar(&trainingMode, "training-mode", false, "set the App's training flag (per-APP: affects every installed Agent)")
	return cmd
}

func newCmdAgentRemove(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "remove <app> <agent>",
		Aliases: []string{"rm", "uninstall"},
		Short:   "Detach an Agent from an App",
		Long: `Uninstall an Agent from an App — deletes the AppAgent row. Both refs take
an ID or a URN.

The Agent's per-(App, Agent, *) memories are NOT deleted (spec 023 FR-005):
they persist as orphans and become reachable again if the same Agent is
reinstalled, so this is reversible with ` + "`app agent add`" + `.

Idempotent server-side — it succeeds whether or not the row exists, so a
repeat run is not an error.

Authorization is CONTRIBUTOR+ on the App's owning org, or ownership of a
user-owned App.`,
		Example: `  hadron app agent remove hrn:app:acme.com:eng-team hrn:agent:acme.com:iris --yes`,
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			appRef, err := cmdutil.CanonicalAppRef("<app>", args[0])
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			// Prompted like the group's other removals even though the memories
			// survive: it changes who is on a roster, and for a team App that is
			// the thing people read to answer "who is working on this?".
			if err := cmdutil.ConfirmDeletion(f.IOStreams, yes,
				fmt.Sprintf("agent %s from app %s (its memories persist and return on reinstall)", args[1], appRef)); err != nil {
				return err
			}
			resp, err := gen.UninstallAgentFromApp(cmd.Context(), client, appRef, args[1])
			if err != nil {
				return cmdutil.InstallForbiddenGuidance(err)
			}
			if resp.UninstallAgentFromApp == nil {
				return exitcode.Newf(exitcode.Error, "server returned no result")
			}
			dto := appAgentRemovedDTO{
				AppID:   resp.UninstallAgentFromApp.AppId,
				AgentID: resp.UninstallAgentFromApp.AgentId,
				Status:  "uninstalled",
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w,
					"✓ uninstalled agent %s from app %s — its memories persist and return if it is reinstalled\n",
					dto.AgentID, dto.AppID)
				return err
			})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}
