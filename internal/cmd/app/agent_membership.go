package app

import (
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
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

// forbiddenGuidance turns the server's bare FORBIDDEN into the actual rule
// (#389). The gate here is NOT App membership: installing an existing Agent is
// itself a read grant on that Agent's design — its system memory becomes
// readable from every App context and the returned Agent carries its
// systemPrompt — so it sits at the level that can already read the org's
// Agents. hadron-server#952 proposed admitting non-reader AppMembers and that
// was declined for exactly this reason, which means a team member who is not an
// org member will hit this and deserves better than one word.
func forbiddenGuidance(err error) error {
	if !api.HasErrorCode(err, "FORBIDDEN") {
		return api.MapError(err)
	}
	mapped := api.MapError(err)
	return exitcode.Newf(exitcode.FromError(mapped),
		"%v — installing or removing an Agent needs CONTRIBUTOR+ on the App's owning org "+
			"(or ownership of a user-owned App). App membership is deliberately not enough: "+
			"attaching an Agent grants read access to its design, including its system prompt",
		unwrapMessage(mapped))
}

func unwrapMessage(err error) error {
	var coded *exitcode.CodedError
	if errors.As(err, &coded) {
		return coded.Err
	}
	return err
}

func newCmdAgentAdd(f *cmdutil.Factory) *cobra.Command {
	var trainingMode bool
	cmd := &cobra.Command{
		Use:     "add <app> <agent>",
		Aliases: []string{"install"},
		Short:   "Attach an existing Agent to an existing App",
		Long: `Install an Agent into an App — a row in the AppAgent join (spec 023 US1,
cor:dmo:050:03). Both refs take an ID or a URN.

This is not ` + "`hadron app install`" + `, which creates a NEW App from an Agent.
This composes the roster of an App you already have: adding a role agent to a
second team App's cast pool, putting back one uninstalled by mistake, or
re-attaching an Agent to reach the memories accumulated under it —
cor:dmo:050:03 keeps those "ready to re-attach if the Agent is installed
again", and this is the surface that does it.

--training-mode is PER-APP, not per-Agent (spec 023 FR-001), so setting it
here changes the flag for every Agent installed in this App. It is omitted
entirely unless you pass it.

Authorization is CONTRIBUTOR+ on the App's owning org, or ownership of a
user-owned App — deliberately NOT plain App membership, since attaching an
Agent grants read access to its design.`,
		Example: `  hadron app agent add hrn:app:acme.com:eng-team hrn:agent:acme.com:iris
  hadron app agent add app_123 agt_456 --training-mode`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			var training *bool
			if cmd.Flags().Changed("training-mode") {
				training = &trainingMode
			}
			resp, err := gen.InstallAgentIntoApp(cmd.Context(), client, args[0], args[1], training)
			if err != nil {
				return forbiddenGuidance(err)
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
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			// Prompted like the group's other removals even though the memories
			// survive: it changes who is on a roster, and for a team App that is
			// the thing people read to answer "who is working on this?".
			if err := cmdutil.ConfirmDeletion(f.IOStreams, yes,
				fmt.Sprintf("agent %s from app %s (its memories persist and return on reinstall)", args[1], args[0])); err != nil {
				return err
			}
			resp, err := gen.UninstallAgentFromApp(cmd.Context(), client, args[0], args[1])
			if err != nil {
				return forbiddenGuidance(err)
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
