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
The server may return null (for example for a JWT user without an App): that
is success with {"app":null}, not a lookup of an ambient App. Only identity
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
	parent := &cobra.Command{Use: "asset-upload <command>", Short: "Toggle uploads on an Agent's own backing App", Long: `Enable or disable the asset:upload tool on the App backing an Agent, not an
arbitrary App that installed it. Pass the Agent's ID or fully qualified URN.
The server first checks Agent readability, then requires an org admin; a
user-owned Agent is currently unsupported. It resolves the Agent's own App by
its URN and org. The CLI performs no role or tenant checks itself.
This toggles the v1 tool allowlist, not a memory's acceptsUploads setting.`}
	for _, enabled := range []bool{true, false} {
		name := "disable"
		if enabled {
			name = "enable"
		}
		var yes bool
		cmd := &cobra.Command{Use: name + " <agent-ref> [--yes]", Short: name + " asset upload on the Agent's backing App", Long: `Change the Agent's backing App once, after confirmation. Scripts pass --yes.
No local App context selects the target. Both states are sent explicitly,
including enabled:false when disabling. The returned App's allowlist confirms
the result; JSON is {appId,appUrn,agentRef,enabled}. No credential is requested.
A lost reply is an unknown outcome; this command never retries the mutation.`, Example: "  hadron app asset-upload " + name + " hrn:agent:acme.com:bot --yes --json", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
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
				// The asset setting uses PERMISSION_DENIED for an authenticated
				// non-admin. Scope this exit to the operation: legacy asset delete/
				// restore deliberately keep their established exit 1.
				if api.HasErrorCode(err, "PERMISSION_DENIED") {
					return exitcode.New(exitcode.Forbidden, api.MapErrorKeepingCode(err))
				}
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
		cmd.Long += "\n\n" + parent.Long
		parent.AddCommand(cmd)
	}
	return parent
}
