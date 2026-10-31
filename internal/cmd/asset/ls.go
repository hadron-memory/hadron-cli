package asset

import (
	"fmt"
	"io"
	"strings"

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
	ScanSignature       *string `json:"scanSignature"`
	UploadedAt          string  `json:"uploadedAt"`
	UploadedBy          *string `json:"uploadedBy"`
	DeletedAt           *string `json:"deletedAt"`
	MemoryID            string  `json:"memoryId"`
	PublicURL           *string `json:"publicUrl"`
	PublicLinkEnabled   bool    `json:"publicLinkEnabled"`
	CanManagePublicLink bool    `json:"canManagePublicLink"`
	PublicLinkAvailable bool    `json:"publicLinkAvailable"`
}

// assetListDTO is the stable --json shape for a listing.
type assetListDTO struct {
	Memory string     `json:"memory"`
	Total  int        `json:"total"`
	Assets []assetDTO `json:"assets"`
}

// assetAllDTO is the stable --json shape for the cross-memory list (--all,
// cli#1341): there is no single memory, and each asset carries its memoryId.
type assetAllDTO struct {
	Total  int        `json:"total"`
	Assets []assetDTO `json:"assets"`
}

// assetScanStatuses are the AssetScanStatus values --scan-status takes.
var assetScanStatuses = map[string]gen.AssetScanStatus{}

func init() {
	for _, s := range gen.AllAssetScanStatus {
		assetScanStatuses[strings.ToUpper(string(s))] = s
	}
}

func newCmdList(f *cmdutil.Factory) *cobra.Command {
	var memories []string
	var mime, org, scanStatus string
	var mine, includeDeleted, all bool
	var limit, offset int
	cmd := &cobra.Command{
		Use:     "list -m <memory> | --all",
		Aliases: []string{"ls"},
		Short:   "List a memory's assets, or every asset you can read",
		Long: `List the files held by one memory: id, filename, type, size, virus-scan status,
upload time and public-link state. CAN MANAGE says whether you may switch the
public link; AVAILABLE says whether this server serves public links at all.

-m is required, unless --all lists every asset held by a memory you can read.
With --all, -m is optional and repeatable and narrows to those memories, --org
to one organization (a non-member gets an empty list), and --scan-status to one
virus-scan status (PENDING, CLEAN or BLOCKED); the table gains a MEMORY
column.

Everyone who can read the memory sees every uploader's files; --mine keeps only
yours. --include-deleted adds files deleted in the last 24 hours (asset restore
can still bring them back) and files the virus scan blocked.

Every asset is listed by default. --limit and --offset fetch one page instead.`,
		Example: `  hadron asset list -m hrn:mem:acme.com:kb
  hadron asset list -m hrn:mem:acme.com:kb --mine --mime image/png
  hadron asset list -m hrn:mem:acme.com:kb --include-deleted --json
  hadron asset list --all --mine
  hadron asset list --all --org acme.com --scan-status BLOCKED`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			changed := cmd.Flags().Changed
			if !all {
				for _, name := range []string{"org", "scan-status"} {
					if changed(name) {
						return exitcode.Newf(exitcode.Usage, "--%s applies only with --all (the cross-memory list). Nothing was sent", name)
					}
				}
				if len(memories) != 1 || strings.TrimSpace(memories[0]) == "" {
					return exitcode.Newf(exitcode.Usage, "-m/--memory is required, once — asset listing is memory-addressed (or pass --all to list every asset you can read)")
				}
			}
			memory := ""
			if len(memories) > 0 {
				memory = memories[0]
			}
			if limit < 0 || offset < 0 {
				return exitcode.Newf(exitcode.Usage, "--limit and --offset must not be negative")
			}
			// `--limit 0` would ask the server for a zero-row page, which
			// silently reads as "no assets" rather than as the mistake it is.
			if cmd.Flags().Changed("limit") && limit == 0 {
				return exitcode.Newf(exitcode.Usage, "--limit must be at least 1 (omit it to list every asset)")
			}

			if all {
				return listAllAssets(cmd, f, allAssetsArgs{
					memories: memories, org: org, mime: mime, scanStatus: scanStatus,
					mine: mine, includeDeleted: includeDeleted, limit: limit, offset: offset,
				})
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
						PublicLinkEnabled: a.PublicLinkEnabled, CanManagePublicLink: a.CanManagePublicLink, PublicLinkAvailable: a.PublicLinkAvailable,
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
				return assetTable(w, dto.Assets, false)
			})
		},
	}
	cmd.Flags().StringArrayVarP(&memories, "memory", "m", nil, "memory ID or fully-qualified URN (required; with --all optional and repeatable)")
	cmd.Flags().BoolVar(&all, "all", false, "list every asset held by a memory you can read")
	cmd.Flags().StringVar(&org, "org", "", "with --all: only assets in this organization's memories")
	cmd.Flags().StringVar(&scanStatus, "scan-status", "", "with --all: only assets with this virus-scan status (PENDING, CLEAN, BLOCKED)")
	cmd.Flags().StringVar(&mime, "mime", "", "only assets with this MIME type")
	cmd.Flags().BoolVar(&mine, "mine", false, "only assets you uploaded")
	cmd.Flags().BoolVar(&includeDeleted, "include-deleted", false, "include soft-deleted assets (restorable within the retention window)")
	cmd.Flags().IntVar(&limit, "limit", 0, "maximum assets to fetch in one page (default: all)")
	cmd.Flags().IntVar(&offset, "offset", 0, "pagination offset (implies a single page)")
	return cmd
}

// assetTable renders assets; withMemory adds the MEMORY column the
// cross-memory list needs.
func assetTable(w io.Writer, assets []assetDTO, withMemory bool) error {
	cols := []string{"ID", "FILENAME", "TYPE", "SIZE", "SCAN", "UPLOADED", "PUBLIC LINK", "CAN MANAGE", "AVAILABLE"}
	if withMemory {
		cols = append([]string{"MEMORY"}, cols...)
	}
	t := output.NewTable(w, cols...)
	for _, a := range assets {
		scan := a.ScanStatus
		// The signature is what makes a BLOCKED row actionable — which engine
		// matched, on a row whose bytes are gone. It is only ever set there, so
		// nothing else grows a column.
		if a.ScanSignature != nil && *a.ScanSignature != "" {
			scan += " (" + *a.ScanSignature + ")"
		}
		if a.DeletedAt != nil {
			scan += " (deleted)"
		}
		row := []string{output.SafeCell(a.ID), output.SafeCell(a.Filename), output.SafeCell(a.MimeType), humanSize(a.SizeBytes), output.SafeCell(scan), output.SafeCell(a.UploadedAt), output.SafeCell(publicLinkState(a.PublicLinkEnabled, a.PublicLinkAvailable, a.PublicURL)), fmt.Sprint(a.CanManagePublicLink), fmt.Sprint(a.PublicLinkAvailable)}
		if withMemory {
			row = append([]string{output.SafeCell(a.MemoryID)}, row...)
		}
		t.Row(row...)
	}
	return t.Flush()
}

type allAssetsArgs struct {
	memories              []string
	org, mime, scanStatus string
	mine, includeDeleted  bool
	limit, offset         int
}

// listAllAssets is asset list --all: the cross-memory assets() list (#891).
// It pages to exhaustion like the per-memory list (#23) unless --limit or
// --offset asks for one page.
func listAllAssets(cmd *cobra.Command, f *cmdutil.Factory, a allAssetsArgs) error {
	changed := cmd.Flags().Changed
	filter := &gen.AssetFilter{}
	if changed("memory") {
		for _, m := range a.memories {
			if strings.TrimSpace(m) == "" {
				return exitcode.Newf(exitcode.Usage, "-m/--memory is empty: name a memory, or drop -m to list every memory. Nothing was sent")
			}
		}
		for _, m := range a.memories {
			filter.MemoryRefs = append(filter.MemoryRefs, cmdutil.CanonicalMemoryRef(m))
		}
	}
	if changed("scan-status") {
		s, ok := assetScanStatuses[strings.ToUpper(strings.TrimSpace(a.scanStatus))]
		if !ok {
			return exitcode.Newf(exitcode.Usage, "invalid --scan-status %q (want PENDING, CLEAN or BLOCKED). Nothing was sent", a.scanStatus)
		}
		filter.ScanStatus = &s
	}
	if changed("mime") {
		if strings.TrimSpace(a.mime) == "" {
			return exitcode.Newf(exitcode.Usage, "--mime is empty: name a MIME type, or drop the flag. Nothing was sent")
		}
		filter.MimeType = &a.mime
	}
	filter.Mine = optBool(a.mine)
	filter.IncludeDeleted = optBool(a.includeDeleted)
	var orgArg *string
	if changed("org") {
		if strings.TrimSpace(a.org) == "" {
			return exitcode.Newf(exitcode.Usage, "--org is empty: name an organization, or drop the flag. Nothing was sent")
		}
		orgArg = &a.org
	}
	if a.limit < 0 || a.offset < 0 {
		return exitcode.Newf(exitcode.Usage, "--limit and --offset must not be negative")
	}
	if changed("limit") && a.limit == 0 {
		return exitcode.Newf(exitcode.Usage, "--limit must be at least 1 (omit it to list every asset)")
	}

	client, err := f.GraphQLClient()
	if err != nil {
		return err
	}
	dto := assetAllDTO{Assets: []assetDTO{}}
	single := a.limit > 0 || a.offset > 0
	skip, size := a.offset, assetPageSize
	if a.limit > 0 {
		size = a.limit
	}
	for {
		resp, err := gen.ListAssets(cmd.Context(), client, filter, &size, &skip, orgArg)
		if err != nil {
			return api.MapError(err)
		}
		page := resp.Assets
		dto.Total = page.Total
		for _, x := range page.Items {
			if x == nil {
				continue
			}
			dto.Assets = append(dto.Assets, assetDTO{
				ID: x.Id, URN: x.Urn, Filename: x.Filename, MimeType: x.MimeType,
				SizeBytes: x.SizeBytes, Description: x.Description,
				ScanStatus: string(x.ScanStatus), ScanSignature: x.ScanSignature,
				UploadedAt: x.UploadedAt, UploadedBy: x.UploadedBy, DeletedAt: x.DeletedAt,
				MemoryID: x.MemoryId, PublicURL: x.PublicUrl,
				PublicLinkEnabled: x.PublicLinkEnabled, CanManagePublicLink: x.CanManagePublicLink, PublicLinkAvailable: x.PublicLinkAvailable,
			})
		}
		skip += len(page.Items)
		if single || len(page.Items) == 0 || skip >= page.Total {
			break
		}
	}
	return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		if len(dto.Assets) == 0 {
			fmt.Fprintln(w, "no assets")
			return nil
		}
		return assetTable(w, dto.Assets, true)
	})
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
