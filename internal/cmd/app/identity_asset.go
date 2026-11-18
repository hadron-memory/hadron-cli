package app

import (
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

type myAppDTO struct {
	App *appGetDTO `json:"app"`
}

func newCmdWhoami(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{Use: "whoami", Short: "Read the App authenticated by the current credential", Long: `Ask myApp which App the current App-key credential authenticates.
This does not use --app, the configured App or a worker binding as its target.
The server returns null for user credentials (JWT or user API key); only an
App key identifies an App. Null is success with {"app":null}, not a lookup of
an ambient App. Only identity
metadata is requested, never keys, settings or AI credentials. Text names the
App or says no App was returned; JSON is {app: metadata|null}.`, Example: `  hadron app whoami --json`, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		client, err := f.GraphQLClient()
		if err != nil {
			return err
		}
		r, err := gen.MyApp(cmd.Context(), client)
		if err != nil {
			return api.MapErrorKeepingCode(err)
		}
		if r == nil {
			return exitcode.Newf(exitcode.Error, "the server returned no App identity response")
		}
		d := myAppDTO{}
		if a := r.MyApp; a != nil {
			if a.Id == "" || a.Urn == "" {
				return exitcode.Newf(exitcode.Error, "the server returned an incomplete App identity")
			}
			d.App = &appGetDTO{ID: a.Id, URN: a.Urn, Name: a.Name, AppType: string(a.AppType), Description: a.Description}
		}
		return output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error {
			if d.App == nil {
				_, err := fmt.Fprintln(w, "No App was returned for this credential.")
				return err
			}
			a := d.App
			_, err := fmt.Fprintf(w, "id: %s\nurn: %s\nname: %s\ntype: %s\ndescription: %s\n", output.SafeCell(a.ID), output.SafeCell(a.URN), output.SafeCell(a.Name), output.SafeCell(a.AppType), output.SafeCell(optionalReadText(a.Description)))
			return err
		})
	}}
}

type assetUploadDTO struct {
	AppID    string `json:"appId"`
	AppURN   string `json:"appUrn"`
	AgentRef string `json:"agentRef"`
	Enabled  bool   `json:"enabled"`
}

func newCmdAssetUpload(f *cmdutil.Factory) *cobra.Command {
	parent := &cobra.Command{Use: "asset-upload <command>", Short: "Toggle uploads on an Agent's own backing App", Long: `Turn the file-upload tool (asset:upload) on or off for an Agent. Every Agent
has its own backing App, the App the platform keeps for it. The tool is one of
the entries on that App's list of tools its Agent may use, and these commands add
it to or remove it from that list. They do not touch any other App that has
installed the Agent, and they do not change a memory's own setting for whether it
accepts uploads.

Pass the Agent's ID or fully qualified URN. The server checks that you can read
the Agent, then that you are an administrator of its organization; an Agent owned
by a user instead of an organization is not supported yet. This CLI adds no role
check of its own. Both commands ask for confirmation (a script passes --yes) and
make exactly one change.`}
	for _, enabled := range []bool{true, false} {
		name := "disable"
		if enabled {
			name = "enable"
		}
		var yes bool
		cmd := &cobra.Command{Use: name + " <agent-ref> [--yes]", Short: name + " asset upload on the Agent's backing App", Long: strings.ToUpper(name[:1]) + name[1:] + ` the file-upload tool (asset:upload) on the Agent's own backing App
(see "hadron app asset-upload --help" for what that App is and who may do it).
It asks for confirmation; a script passes --yes. No App context is used: the
Agent you name selects the App.

It sets the state you asked for rather than toggling, so running it again is
harmless. It prints the backing App, the Agent you named and the state the
server reports ("asset upload enabled: true" or "false"); --json is {appId,
appUrn, agentRef, enabled}. The command checks that the server's answer matches
what you asked for and fails if it does not.

Refusals: exit 8 when you are not allowed (you are not an administrator of the
Agent's organization); exit 4 when there is no such Agent (a missing Agent and
one you cannot read look the same); exit 1 with a requestId
(INTERNAL_SERVER_ERROR) when the server failed. An older server may report any of
the three as that last kind of error. INTERNAL_SERVER_ERROR (exit 1) is an
unknown outcome: it can be a concealed refusal or a real server fault, so do not
assume nothing changed, and this command does not retry. A lost reply is the
same: run the command again to be sure.`, Example: "  hadron app asset-upload " + name + " hrn:agent:acme.com:bot --yes --json", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			ref := strings.TrimSpace(args[0])
			if ref == "" {
				return exitcode.Newf(exitcode.Usage, "pass an Agent ID or fully qualified URN")
			}
			if err := cmdutil.Confirm(f.IOStreams, yes, fmt.Sprintf("%s asset upload for Agent %s's backing App?", name, strconv.Quote(ref))); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			r, err := gen.SetAgentAssetUploadEnabled(cmd.Context(), client, &gen.SetAgentAssetUploadEnabledInput{AgentUrn: ref, Enabled: enabled})
			if err != nil {
				// Shared classification preserves the typed wire code; no scoped override.
				return api.MapErrorKeepingCode(err)
			}
			if r == nil || r.SetAgentAssetUploadEnabled == nil {
				return exitcode.Newf(exitcode.Error, "the server did not confirm the asset-upload setting")
			}
			a := r.SetAgentAssetUploadEnabled
			if a.Id == "" || a.Urn == "" || a.AgentTools == nil || slices.Contains(a.AgentTools, "asset:upload") != enabled {
				return exitcode.Newf(exitcode.Error, "the server did not confirm the requested asset-upload setting")
			}
			d := assetUploadDTO{AppID: a.Id, AppURN: a.Urn, AgentRef: ref, Enabled: enabled}
			return output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "app: %s\nagent: %s\nasset upload enabled: %t\n", output.SafeCell(d.AppURN), strconv.Quote(d.AgentRef), d.Enabled)
				return err
			})
		}}
		cmd.Flags().BoolVar(&yes, "yes", false, "skip confirmation (required non-interactively)")
		parent.AddCommand(cmd)
	}
	return parent
}
