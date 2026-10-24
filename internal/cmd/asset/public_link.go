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

// Explicit DTOs keep generated response types out of the public JSON contract.
type assetPublicLinkDTO struct {
	ID                  string  `json:"id"`
	URN                 string  `json:"urn"`
	Filename            string  `json:"filename"`
	PublicLinkEnabled   bool    `json:"publicLinkEnabled"`
	PublicLinkAvailable bool    `json:"publicLinkAvailable"`
	CanManagePublicLink bool    `json:"canManagePublicLink"`
	PublicURL           *string `json:"publicUrl"`
}

func publicLinkStatus(enabled, available bool, url *string) string {
	if !enabled {
		return "public link: off"
	}
	if !available {
		return "public link: on, serving disabled on this deployment"
	}
	if url == nil {
		return "public link: on, no public URL on this server"
	}
	return "public link: on"
}

func newCmdPublicLink(f *cmdutil.Factory) *cobra.Command {
	var on, off, yes bool
	cmd := &cobra.Command{
		Use:   "public-link <asset-ref> (--on | --off)",
		Short: "Set an asset's anonymous public-link opt-in",
		Long: `Set the server-owned public-link switch for an asset id or URN.

Choose exactly one of --on or --off. ON permits anyone with the link to
download when serving is enabled and the asset is eligible; it never bypasses
the deployment gate, scanning or encryption rules. ON requires confirmation
on a terminal, or --yes non-interactively. OFF needs no confirmation.

The server checks management rights. The result reports stored opt-in,
deployment availability and the public URL independently. A null URL never
means the stored switch is off.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if on == off {
				return exitcode.Newf(exitcode.Usage, "choose exactly one of --on or --off")
			}
			ref, err := parseAssetRef(args[0])
			if err != nil {
				return err
			}
			if on {
				if err := cmdutil.Confirm(f.IOStreams, yes, "Enable public link? Anyone with the link can download when serving is enabled and the asset is eligible; this deployment may have serving disabled."); err != nil {
					return err
				}
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.SetAssetPublicLink(cmd.Context(), client, ref.ID, on)
			if err != nil {
				return api.MapError(err)
			}
			a := resp.SetAssetPublicLink
			dto := assetPublicLinkDTO{ID: a.Id, URN: a.Urn, Filename: a.Filename, PublicLinkEnabled: a.PublicLinkEnabled, PublicLinkAvailable: a.PublicLinkAvailable, CanManagePublicLink: a.CanManagePublicLink, PublicURL: a.PublicUrl}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				fmt.Fprintf(w, "%s: %s\n", dto.Filename, publicLinkStatus(dto.PublicLinkEnabled, dto.PublicLinkAvailable, dto.PublicURL))
				fmt.Fprintf(w, "can manage public link: %t\n", dto.CanManagePublicLink)
				if dto.PublicURL != nil {
					fmt.Fprintln(w, *dto.PublicURL)
				} else {
					fmt.Fprintln(w, "public URL: none")
				}
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&on, "on", false, "enable the anonymous-link opt-in (requires confirmation)")
	cmd.Flags().BoolVar(&off, "off", false, "disable the anonymous-link opt-in")
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm enabling the anonymous public link")
	return cmd
}
