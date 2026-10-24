package asset

import (
	"fmt"
	"io"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

type publicLinkDTO struct {
	ID                  string  `json:"id"`
	PublicLinkEnabled   bool    `json:"publicLinkEnabled"`
	CanManagePublicLink bool    `json:"canManagePublicLink"`
	PublicLinkAvailable bool    `json:"publicLinkAvailable"`
	PublicURL           *string `json:"publicUrl"`
}

func newCmdPublicLink(f *cmdutil.Factory) *cobra.Command {
	var on, off, yes bool
	cmd := &cobra.Command{
		Use:   "public-link <asset-ref> (--on | --off)",
		Short: "Set an asset's anonymous public-link opt-in",
		Long: `Set the server-owned public-link switch for an asset ID or URN.

ON records permission for anyone with the link to download. Serving still
requires deployment availability and an eligible asset; the result reports
stored opt-in, deployment availability and public URL independently. A null
URL never proves the switch is OFF. ON requires confirmation (--yes for scripts).
OFF needs no confirmation. Management rights and refusals come from the server.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if on == off || cmd.Flags().Changed("on") && cmd.Flags().Changed("off") {
				return exitcode.Newf(exitcode.Usage, "choose exactly one of --on or --off")
			}
			ref, err := parseAssetRef(args[0])
			if err != nil {
				return err
			}
			if on {
				if err := cmdutil.Confirm(f.IOStreams, yes, "Allow anyone with the link to download this asset when serving is available? Deployment gates and asset eligibility still apply."); err != nil {
					return err
				}
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.SetAssetPublicLink(cmd.Context(), client, ref.ID, on)
			if err != nil {
				// Only this new command promises exit 8 for the asset manager
				// refusal. Preserve the shared mapping used by legacy asset writes.
				mapped := api.MapError(err)
				if api.HasErrorCode(err, "PERMISSION_DENIED") {
					return exitcode.New(exitcode.Forbidden, mapped)
				}
				return mapped
			}
			a := resp.SetAssetPublicLink
			if a == nil {
				return exitcode.Newf(exitcode.NotFound, "no asset found for %q", args[0])
			}
			dto := publicLinkDTO{a.Id, a.PublicLinkEnabled, a.CanManagePublicLink, a.PublicLinkAvailable, a.PublicUrl}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				fmt.Fprintln(w, publicLinkState(dto.PublicLinkEnabled, dto.PublicLinkAvailable, dto.PublicURL))
				fmt.Fprintf(w, "can manage public link: %t; deployment available: %t\n", dto.CanManagePublicLink, dto.PublicLinkAvailable)
				if dto.PublicURL != nil {
					fmt.Fprintln(w, *dto.PublicURL)
				}
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&on, "on", false, "record the anonymous-download opt-in")
	cmd.Flags().BoolVar(&off, "off", false, "disable the anonymous public link")
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm allowing anyone with the link to download")
	return cmd
}

func publicLinkState(enabled, available bool, url *string) string {
	if !enabled {
		return "public link: off"
	}
	if !available {
		return "public link: on, serving disabled on this deployment"
	}
	if url == nil {
		return "public link: on, no public URL on this server (asset eligibility or server origin may prevent a URL)"
	}
	return "public link: on"
}
