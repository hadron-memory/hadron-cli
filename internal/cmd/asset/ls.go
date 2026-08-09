package asset

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// assetPageSize bounds one page of the listing scan. The server defaults to 20
// and the whole-scope contract is "every asset in the memory", so the listing
// pages to exhaustion rather than showing a default page and silently dropping
// the tail (#23).
const assetPageSize = 100

// assetDTO is the stable --json shape for one asset.
//
// PublicURL and Description are pointers because the server genuinely returns
// null for both — publicUrl when the deployment has no BASE_URL, the asset is
// not CLEAN, or its memory is encrypted. Flattening to "" would read as "the
// asset has an empty hotlink" rather than "there is no hotlink".
type assetDTO struct {
	ID          string  `json:"id"`
	URN         string  `json:"urn"`
	Filename    string  `json:"filename"`
	MimeType    string  `json:"mimeType"`
	SizeBytes   int     `json:"sizeBytes"`
	Description *string `json:"description"`
	ScanStatus  string  `json:"scanStatus"`
	// ScanSignature is the engine signature recorded when the verdict was
	// BLOCKED (#896) — the row is kept as an audit tombstone after its bytes
	// are deleted. A pointer, and null on every non-BLOCKED row: "" would read
	// as "scanned, matched nothing named", which is not what a CLEAN row means.
	ScanSignature *string `json:"scanSignature"`
	UploadedAt    string  `json:"uploadedAt"`
	UploadedBy    *string `json:"uploadedBy"`
	DeletedAt     *string `json:"deletedAt"`
	MemoryID      string  `json:"memoryId"`
	PublicURL     *string `json:"publicUrl"`
}

// assetListDTO is the stable --json shape for a listing.
type assetListDTO struct {
	Memory string     `json:"memory"`
	Total  int        `json:"total"`
	Assets []assetDTO `json:"assets"`
}

func newCmdList(f *cmdutil.Factory) *cobra.Command {
	var memory, mime string
	var mine, includeDeleted bool
	var limit, offset int
	cmd := &cobra.Command{
		Use:     "list -m <memory>",
		Aliases: []string{"ls"},
		Short:   "List a memory's assets",
		Long: `List the assets held by a memory.

-m is required. Asset listing is memory-addressed server-side, so there is no
"every asset I can read" query; the CLI does not fan out across memories to
fake one, because that would be an N+1 whose cost and partial-failure modes are
invisible from the output. Track: hadron-server#891.

Listing is gated on READ access to the memory, so it returns every uploader's
assets, not only your own — pass --mine to narrow to yours.

By default every asset is listed (the query pages to exhaustion). Pass --limit
or --offset to fetch a single explicit page instead.`,
		Example: `  hadron asset list -m acme.com::kb
  hadron asset list -m acme.com::kb --mine --mime image/png
  hadron asset list -m acme.com::kb --include-deleted --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if memory == "" {
				return exitcode.Newf(exitcode.Usage, "-m/--memory is required — asset listing is memory-addressed")
			}
			if limit < 0 || offset < 0 {
				return exitcode.Newf(exitcode.Usage, "--limit and --offset must not be negative")
			}
			// `--limit 0` would ask the server for a zero-row page, which
			// silently reads as "no assets" rather than as the mistake it is.
			if cmd.Flags().Changed("limit") && limit == 0 {
				return exitcode.Newf(exitcode.Usage, "--limit must be at least 1 (omit it to list every asset)")
			}

			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			memID, err := resolveMemoryID(cmd, client, memory)
			if err != nil {
				return err
			}

			var (
				collected []assetDTO
				total     int
			)
			// Bare `list` lists the whole memory, so page to exhaustion (#23).
			// An explicit --limit OR --offset is deliberate user-driven
			// pagination, so it is honored verbatim as a single page —
			// matching `spec list`. Paging on from an explicit --offset would
			// quietly return far more than the caller asked to page through.
			skip := offset
			pageSize := assetPageSize
			single := limit > 0 || offset > 0
			if limit > 0 {
				pageSize = limit
			}
			for {
				resp, err := gen.MemoryAssets(cmd.Context(), client, memID,
					optString(mime), optBool(mine), optBool(includeDeleted), &skip, &pageSize)
				if err != nil {
					return api.MapError(err)
				}
				page := resp.MemoryAssets
				if page == nil {
					break
				}
				total = page.Total
				for _, a := range page.Assets {
					if a == nil {
						continue
					}
					collected = append(collected, assetDTO{
						ID: a.Id, URN: a.Urn, Filename: a.Filename, MimeType: a.MimeType,
						SizeBytes: a.SizeBytes, Description: a.Description,
						ScanStatus: string(a.ScanStatus), ScanSignature: a.ScanSignature,
						UploadedAt: a.UploadedAt,
						UploadedBy: a.UploadedBy, DeletedAt: a.DeletedAt,
						MemoryID: a.MemoryId, PublicURL: a.PublicUrl,
					})
				}
				if single || !page.HasMore || len(page.Assets) == 0 {
					break
				}
				skip += len(page.Assets)
			}

			dto := assetListDTO{
				Memory: cmdutil.CanonicalMemoryRef(memory),
				Total:  total,
				Assets: collected,
			}
			if dto.Assets == nil {
				dto.Assets = []assetDTO{}
			}

			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if len(dto.Assets) == 0 {
					fmt.Fprintf(w, "no assets in %s\n", dto.Memory)
					return nil
				}
				t := output.NewTable(w, "ID", "FILENAME", "TYPE", "SIZE", "SCAN", "UPLOADED")
				for _, a := range dto.Assets {
					scan := a.ScanStatus
					// The signature is what makes a BLOCKED row actionable —
					// which engine matched, on a row whose bytes are gone. It
					// is only ever set there, so nothing else grows a column.
					if a.ScanSignature != nil && *a.ScanSignature != "" {
						scan += " (" + *a.ScanSignature + ")"
					}
					if a.DeletedAt != nil {
						scan += " (deleted)"
					}
					t.Row(a.ID, a.Filename, a.MimeType, humanSize(a.SizeBytes), scan, a.UploadedAt)
				}
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory ID or fully-qualified URN (required)")
	cmd.Flags().StringVar(&mime, "mime", "", "only assets with this MIME type")
	cmd.Flags().BoolVar(&mine, "mine", false, "only assets you uploaded")
	cmd.Flags().BoolVar(&includeDeleted, "include-deleted", false, "include soft-deleted assets (restorable within the retention window)")
	cmd.Flags().IntVar(&limit, "limit", 0, "maximum assets to fetch in one page (default: all)")
	cmd.Flags().IntVar(&offset, "offset", 0, "pagination offset (implies a single page)")
	return cmd
}

// optString / optBool return nil for the zero value so genqlient omits the
// variable rather than sending an explicit null — the server reads an omitted
// filter as "no filter" and a null as a value.
func optString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func optBool(b bool) *bool {
	if !b {
		return nil
	}
	return &b
}

// humanSize renders a byte count for the table. Assets are files, and a raw
// byte count is the wrong unit for a human scanning a list.
func humanSize(n int) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := int64(n) / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
