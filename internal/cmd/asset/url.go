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
	ID        string  `json:"id"`
	URN       string  `json:"urn"`
	Filename  string  `json:"filename"`
	PublicURL *string `json:"publicUrl"`
	Reason    string  `json:"reason,omitempty"`
}

func newCmdURL(f *cmdutil.Factory) *cobra.Command {
	var memory string
	cmd := &cobra.Command{
		Use:   "url <asset-ref>",
		Short: "Print an asset's public hotlink",
		Long: `Print the stable, UNAUTHENTICATED hotlink to an asset's bytes.

Anyone with this URL can fetch the file — there is no read gate on it. Do not
treat it as access-controlled, and do not paste it anywhere you would not paste
the file itself.

The hotlink comes from the asset listing, which is memory-addressed, so this
command needs to know the holding memory: pass the asset's URN (which carries
it) or add -m.

The URL is absent — and this command says why — when the deployment has no
canonical origin configured, when the asset is not yet scanned CLEAN, or when
its memory is encrypted (an anonymous request holds no session key, so an
encrypted memory is never hotlinkable). Never construct the URL yourself from
the id; an absent hotlink means there is genuinely nothing safe to hand out.`,
		Example: `  hadron asset url hrn:asset:acme.com:kb:assets:01j2x…
  hadron asset url 01j2x… -m acme.com::kb`,
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

			dto := assetURLDTO{ID: found.Id, URN: found.Urn, Filename: found.Filename, PublicURL: found.PublicUrl}
			if found.PublicUrl == nil {
				dto.Reason = hotlinkAbsentReason(string(found.ScanStatus))
			}

			if err := output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if dto.PublicURL == nil {
					fmt.Fprintf(f.IOStreams.ErrOut, "no public hotlink for %s — %s\n", dto.Filename, dto.Reason)
					return nil
				}
				fmt.Fprintln(w, *dto.PublicURL)
				fmt.Fprintf(f.IOStreams.ErrOut,
					"warning: this link is NOT access-controlled — anyone who has it can fetch %s\n", dto.Filename)
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

// hotlinkAbsentReason turns a null publicUrl into the actionable half of the
// answer. Scan status is the one cause the caller can read off the asset;
// otherwise it is a deployment or encryption property, so both are named.
func hotlinkAbsentReason(scan string) string {
	switch scan {
	case "PENDING":
		return "its virus scan has not finished yet; retry once the scan status is CLEAN"
	case "BLOCKED":
		return "its virus scan blocked the file, so it is never served"
	default:
		return "its memory is encrypted (never hotlinkable), or this deployment has no public origin configured"
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
