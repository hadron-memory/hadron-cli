package asset

import (
	"fmt"
	"io"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// assetURLDTO is the stable --json shape for the hotlink lookup. PublicURL is a
// pointer so "no hotlink" is distinguishable from an empty string, and Reason
// explains a null rather than leaving the caller to guess.
type assetURLDTO struct {
	ID                  string  `json:"id"`
	URN                 string  `json:"urn"`
	Filename            string  `json:"filename"`
	PublicURL           *string `json:"publicUrl"`
	Reason              string  `json:"reason,omitempty"`
	PublicLinkEnabled   bool    `json:"publicLinkEnabled"`
	CanManagePublicLink bool    `json:"canManagePublicLink"`
	PublicLinkAvailable bool    `json:"publicLinkAvailable"`
}

func newCmdURL(f *cmdutil.Factory) *cobra.Command {
	var memory string
	cmd := &cobra.Command{
		Use:   "url <asset-ref>",
		Short: "Print a file's public link",
		Long: `Print the public link to a file. Anyone who has the link can download the file
without signing in: share it only where you would share the file itself.

Pass the asset's URN (it names the memory), or its id with -m <memory>.

When there is no link, the command prints why and exits 5 (--json: publicUrl
is null and reason says why):

  public link: off
      The file's public link is off, which is the default. Turn it on with
      hadron asset public-link <asset> --on --yes (needs admin access to the
      memory).
  its virus scan has not finished yet
      Wait, then run the command again.
  its virus scan blocked the file
      The file is never served.
  serving disabled on this deployment
      This server does not serve public links; only its operator can change
      that.
  no valid public origin configured, or the asset's memory is encrypted
      Either the server has no public address set (its operator can fix that),
      or the file is in an encrypted memory, whose files are never served
      publicly.

Do not build the link from the id yourself: when there is no link, there is
nothing safe to share.`,
		Example: `  hadron asset url hrn:asset:acme.com:kb:assets:01a189a840f4717c98af75cf4d7aed09
  hadron asset url 01a189a840f4717c98af75cf4d7aed09 -m hrn:mem:acme.com:kb`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := parseAssetRef(args[0])
			if err != nil {
				return err
			}
			memRef, err := memoryScope(ref, memory)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			memID, err := resolveMemoryID(cmd, client, memRef)
			if err != nil {
				return err
			}

			found, err := findAsset(cmd, client, memID, ref.ID)
			if err != nil {
				return err
			}

			dto := assetURLDTO{ID: found.Id, URN: found.Urn, Filename: found.Filename, PublicURL: found.PublicUrl, PublicLinkEnabled: found.PublicLinkEnabled, CanManagePublicLink: found.CanManagePublicLink, PublicLinkAvailable: found.PublicLinkAvailable}
			if found.PublicUrl == nil {
				dto.Reason = publicLinkState(found.PublicLinkEnabled, found.PublicLinkAvailable, found.PublicUrl)
				if found.PublicLinkEnabled && found.PublicLinkAvailable || string(found.ScanStatus) != "CLEAN" {
					dto.Reason += "; " + hotlinkAbsentReason(string(found.ScanStatus))
				}
			}

			if err := output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if dto.PublicURL == nil {
					fmt.Fprintf(f.IOStreams.ErrOut, "no public hotlink for %s — %s\n", output.SafeCell(dto.Filename), dto.Reason)
					return nil
				}
				fmt.Fprintln(w, *dto.PublicURL)
				fmt.Fprintf(f.IOStreams.ErrOut,
					"warning: this link is NOT access-controlled — anyone who has it can fetch %s\n", output.SafeCell(dto.Filename))
				return nil
			}); err != nil {
				return err
			}
			// The exit code lives OUT here, not in the human callback: --json
			// never runs that callback, so an absent hotlink would otherwise
			// exit 0 and let automation treat "not hotlinkable" as a
			// successful lookup. Silent because the reason is already on
			// stderr (human) or in the DTO's `reason` field (--json) — both
			// modes now report the same failure the same way.
			if dto.PublicURL == nil {
				return exitcode.Silent(exitcode.Conflict)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "the memory holding the asset (not needed when the ref is a URN)")
	return cmd
}

// hotlinkAbsentReason explains scan/eligibility after separately reporting
// the server-owned stored policy and deployment gate. An absent URL does not
// let the CLI choose between encryption and an unconfigured origin.
func hotlinkAbsentReason(scan string) string {
	switch scan {
	case "PENDING":
		return "its virus scan has not finished yet; retry once the scan status is CLEAN"
	case "BLOCKED":
		return "its virus scan blocked the file, so it is never served"
	default:
		return "no valid public origin configured, " +
			"or the asset's memory is encrypted (never hotlinkable) — the server does not say which"
	}
}

// findAsset locates one live asset in a memory by id, paging to exhaustion.
//
// There is no single-asset query on the server — publicUrl is only reachable
// through the listing — so this is a scan rather than a lookup. It pages
// rather than reading one default page, because the asset being looked up is
// as likely to be old as recent.
//
// Soft-deleted assets are deliberately NOT included: a deleted asset has no
// hotlink, so including them would turn a clean "no such asset" into a
// misleading "no hotlink, because its scan…" — and would invite handing out a
// URL for a deleted file if the server ever loosened that.
func findAsset(cmd *cobra.Command, client graphql.Client, memID, assetID string) (*assetNode, error) {
	skip, page := 0, assetPageSize
	for {
		resp, err := gen.MemoryAssets(cmd.Context(), client, memID, nil, nil, nil, &skip, &page)
		if err != nil {
			return nil, api.MapError(err)
		}
		p := resp.MemoryAssets
		if p == nil || len(p.Assets) == 0 {
			break
		}
		for _, a := range p.Assets {
			if a != nil && a.Id == assetID {
				return a, nil
			}
		}
		if !p.HasMore {
			break
		}
		skip += len(p.Assets)
	}
	return nil, exitcode.Newf(exitcode.NotFound,
		"no asset %q in that memory — check the id, or that you are pointing at the memory that holds it", assetID)
}
