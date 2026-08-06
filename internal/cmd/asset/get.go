package asset

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// downloadTimeout bounds the presigned GET. Generous because assets are files
// and may be large, but bounded so a stalled transfer fails rather than hangs.
const downloadTimeout = 30 * time.Minute

// assetGetDTO is the stable --json shape for a completed download.
type assetGetDTO struct {
	ID        string `json:"id"`
	Filename  string `json:"filename"`
	MimeType  string `json:"mimeType"`
	SizeBytes int    `json:"sizeBytes"`
	Path      string `json:"path"`
	Written   int64  `json:"written"`
}

func newCmdGet(f *cmdutil.Factory) *cobra.Command {
	var out string
	var force bool
	cmd := &cobra.Command{
		Use:     "get <asset-ref>",
		Aliases: []string{"download"},
		Short:   "Download an asset's bytes",
		Long: `Download an asset to a file.

<asset-ref> is an asset id or URN. The download itself needs no memory: the
server resolves the asset and gates on its holding memory's read access.

Writes to the asset's own filename in the working directory unless -o names a
path; -o - streams the bytes to stdout (for piping). An existing file is NOT
overwritten without --force, because the default path comes from server-side
metadata rather than from something you typed.

Downloads are gated on virus scanning. An asset whose scan is still PENDING, or
which was BLOCKED, is refused by the server — the reason comes back verbatim
rather than as a generic failure.`,
		Example: `  hadron asset get 01j2x…
  hadron asset get hrn:asset:acme.com:kb:assets:01j2x… -o ./logo.png
  hadron asset get 01j2x… -o - | file -`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := parseAssetRef(args[0])
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}

			resp, err := gen.AssetDownloadUrl(cmd.Context(), client, ref.ID, nil)
			if err != nil {
				return api.MapError(err)
			}
			d := resp.AssetDownloadUrl
			if d == nil {
				return exitcode.Newf(exitcode.NotFound, "no asset found for %q", args[0])
			}

			path := out
			if path == "" {
				// Never let a server-supplied filename escape the working
				// directory: it is attacker-influenceable (whoever uploaded
				// the file chose it), so strip any directory component.
				path = filepath.Base(filepath.Clean(d.Filename))
				if path == "." || path == string(filepath.Separator) || path == "" {
					return exitcode.Newf(exitcode.Error,
						"asset has no usable filename (%q) — pass -o <path>", d.Filename)
				}
			}

			var written int64
			if path == "-" {
				if written, err = streamTo(cmd, f.IOStreams.Out, d.Url); err != nil {
					return err
				}
			} else {
				if !force {
					if _, serr := os.Stat(path); serr == nil {
						return exitcode.Newf(exitcode.Conflict,
							"%s already exists — pass --force to overwrite, or -o <path>", path)
					}
				}
				if written, err = downloadToFile(cmd, path, d.Url); err != nil {
					return err
				}
			}

			if path == "-" {
				return nil // the bytes ARE the output; no report to add
			}
			dto := assetGetDTO{
				ID: ref.ID, Filename: d.Filename, MimeType: d.MimeType,
				SizeBytes: d.SizeBytes, Path: path, Written: written,
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(wr io.Writer) error {
				fmt.Fprintf(wr, "wrote %s (%s, %s)\n", dto.Path, humanSize(int(dto.Written)), dto.MimeType)
				return nil
			})
		},
	}
	cmd.Flags().StringVarP(&out, "out", "o", "", `write to this path ("-" for stdout; default: the asset's filename)`)
	cmd.Flags().BoolVar(&force, "force", false, "overwrite the output file if it exists")
	return cmd
}

// downloadToFile streams the asset to a sibling temp file and renames it over
// the destination only once the transfer has fully succeeded.
//
// The indirection is the point. Writing straight to the destination means
// os.Create truncates it up front, so a mid-transfer failure — an expired
// presigned URL, a dropped connection — leaves the caller with a corrupt file,
// and cleaning that up destroys the original they were overwriting with
// --force. Downloading beside it and renaming makes the replacement atomic:
// the destination is either untouched or completely replaced.
//
// The temp file is created in the destination's own directory so the rename
// stays within one filesystem (os.Rename across devices fails).
func downloadToFile(cmd *cobra.Command, path, url string) (int64, error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".part-*")
	if err != nil {
		return 0, exitcode.Newf(exitcode.Error, "create a temporary file next to %s: %v", path, err)
	}
	tmpName := tmp.Name()
	// Belt and braces: on every failure path the partial file goes away, and
	// the destination is never touched until the rename below.
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}()

	written, err := streamTo(cmd, tmp, url)
	if err != nil {
		return 0, err
	}
	if err := tmp.Close(); err != nil {
		return 0, exitcode.Newf(exitcode.Error, "finish writing %s: %v", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return 0, exitcode.Newf(exitcode.Error, "move the download into place at %s: %v", path, err)
	}
	return written, nil
}

// streamTo fetches the presigned URL and copies it to w.
//
// This request deliberately carries NO Hadron credentials. The URL is presigned
// and points at object storage on a different origin; attaching the bearer
// token would leak it to that host for no benefit. A fresh client is used
// rather than the GraphQL one for the same reason.
func streamTo(cmd *cobra.Command, w io.Writer, url string) (int64, error) {
	req, err := http.NewRequestWithContext(cmd.Context(), http.MethodGet, url, nil)
	if err != nil {
		return 0, exitcode.Newf(exitcode.Error, "build download request: %v", err)
	}
	resp, err := (&http.Client{Timeout: downloadTimeout}).Do(req)
	if err != nil {
		return 0, exitcode.Newf(exitcode.Error, "download: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		// The presigned URL is short-TTL; an expired one is the likeliest
		// non-2xx and is worth naming, since retrying just works.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		hint := ""
		if resp.StatusCode == http.StatusForbidden {
			hint = " (the presigned link may have expired — re-run to mint a new one)"
		}
		return 0, exitcode.Newf(exitcode.Error, "download failed: %s%s\n%s",
			resp.Status, hint, strings.TrimSpace(string(body)))
	}
	n, err := io.Copy(w, resp.Body)
	if err != nil {
		return n, exitcode.Newf(exitcode.Error, "write asset bytes: %v", err)
	}
	return n, nil
}
