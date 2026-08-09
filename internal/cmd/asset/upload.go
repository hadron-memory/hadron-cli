package asset

import (
	"bytes"
	"fmt"
	"io"
	"mime"
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

// uploadTimeout bounds the presigned PUT. Matched to the download timeout:
// both move the same files over the same link.
const uploadTimeout = 30 * time.Minute

// sniffLen is how much of the file is read to detect a content type when the
// extension does not settle it. http.DetectContentType never looks at more.
const sniffLen = 512

// assetUploadDTO is the stable --json shape for a completed upload.
type assetUploadDTO struct {
	ID         string  `json:"id"`
	URN        string  `json:"urn"`
	Filename   string  `json:"filename"`
	MimeType   string  `json:"mimeType"`
	SizeBytes  int     `json:"sizeBytes"`
	ScanStatus string  `json:"scanStatus"`
	MemoryID   string  `json:"memoryId"`
	PublicURL  *string `json:"publicUrl"`
}

func newCmdUpload(f *cmdutil.Factory) *cobra.Command {
	var memory, description, mimeFlag, nameFlag string
	cmd := &cobra.Command{
		Use:   "upload <file> -m <memory>",
		Short: "Upload a file as an asset",
		Long: `Upload a local file into a memory as an asset.

The upload is three steps: the server reserves the asset and returns a
presigned URL, the bytes go straight to object storage, and a final call marks
the asset usable. The size and MIME type are declared up front, so the size cap
and MIME allowlist are enforced BEFORE any bytes move — a rejected upload costs
one round-trip, not a full transfer.

The MIME type is derived from the file extension, falling back to content
sniffing. Pass --mime when the file's extension lies or is absent; the server
rejects a type outside its allowlist and names the type it expected.

If the upload fails after the bytes are sent, the asset is left reserved but
not completed — it does not appear in "asset list" and can be re-uploaded.`,
		Example: `  hadron asset upload ./logo.png -m acme.com::kb
  hadron asset upload ./notes.md -m acme.com::kb --description "meeting notes"
  hadron asset upload ./data -m acme.com::kb --mime application/json --name data.json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if memory == "" {
				return exitcode.Newf(exitcode.Usage, "-m/--memory is required — an asset is uploaded into a memory")
			}
			src := args[0]
			info, err := os.Stat(src)
			if err != nil {
				return exitcode.Newf(exitcode.Usage, "cannot read %s: %v", src, err)
			}
			if info.IsDir() {
				return exitcode.Newf(exitcode.Usage, "%s is a directory — upload a single file", src)
			}
			// The server takes sizeBytes as Int, and a file larger than the
			// platform's cap is refused anyway; failing here keeps the number
			// from silently wrapping on a 32-bit Int.
			if info.Size() > int64(^uint32(0)>>1) {
				return exitcode.Newf(exitcode.Usage, "%s is too large to upload (%s)", src, humanSize64(info.Size()))
			}

			filename := nameFlag
			if filename == "" {
				filename = filepath.Base(src)
			}
			mimeType := mimeFlag
			if mimeType == "" {
				if mimeType, err = detectMime(src, filename); err != nil {
					return err
				}
			}

			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			memID, err := resolveMemoryID(cmd, client, memory)
			if err != nil {
				return err
			}

			// Step 1 — reserve. Typed rejections (size cap, MIME allowlist, a
			// memory not accepting uploads) surface here, verbatim.
			begun, err := gen.BeginAssetUpload(cmd.Context(), client, memID, filename, mimeType,
				int(info.Size()), gen.UploadIntentUser, optString(description))
			if err != nil {
				return api.MapError(err)
			}
			b := begun.BeginAssetUploadV2
			if b == nil {
				return exitcode.Newf(exitcode.Error, "the server did not return an upload reservation")
			}

			// Step 2 — the bytes, straight to object storage.
			if err := putBytes(cmd, f, src, info.Size(), b); err != nil {
				return err
			}

			// Step 3 — mark it usable.
			done, err := gen.CompleteAssetUpload(cmd.Context(), client, b.UploadId)
			if err != nil {
				// The malware verdict lands here, on the LAST step — the bytes
				// are already in storage — so it needs to say what happened to
				// them rather than read as a transport failure (#364).
				return uploadScanError(err, filename)
			}
			a := done.CompleteAssetUpload
			if a == nil {
				return exitcode.Newf(exitcode.Error, "the upload completed but the server returned no asset")
			}

			dto := assetUploadDTO{
				ID: a.Id, URN: a.Urn, Filename: a.Filename, MimeType: a.MimeType,
				SizeBytes: a.SizeBytes, ScanStatus: string(a.ScanStatus),
				MemoryID: a.MemoryId, PublicURL: a.PublicUrl,
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				fmt.Fprintf(w, "uploaded %s (%s, %s)\n%s\n", dto.Filename, humanSize(dto.SizeBytes), dto.MimeType, dto.URN)
				if dto.ScanStatus == "PENDING" {
					fmt.Fprintf(f.IOStreams.ErrOut,
						"note: virus scan is still pending — the asset is not downloadable until it is CLEAN\n")
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory ID or fully-qualified URN (required)")
	cmd.Flags().StringVar(&description, "description", "", "a description stored with the asset")
	cmd.Flags().StringVar(&mimeFlag, "mime", "", "MIME type (default: derived from the extension, else sniffed)")
	cmd.Flags().StringVar(&nameFlag, "name", "", "store under this filename (default: the file's own name)")
	return cmd
}

// putBytes performs step 2 — the presigned PUT.
//
// Like the download, this request carries NO Hadron credentials: it goes to
// object storage on a different origin, and the presigned URL is itself the
// authorization. Only the headers the server handed back are sent; inventing
// others (or dropping one) breaks the signature.
func putBytes(cmd *cobra.Command, f *cmdutil.Factory, src string, size int64, b *gen.BeginAssetUploadBeginAssetUploadV2BeginAssetUploadResult) error {
	fh, err := os.Open(src)
	if err != nil {
		return exitcode.Newf(exitcode.Error, "open %s: %v", src, err)
	}
	defer func() { _ = fh.Close() }()

	req, err := http.NewRequestWithContext(cmd.Context(), http.MethodPut, b.PutUrl, fh)
	if err != nil {
		return exitcode.Newf(exitcode.Error, "build upload request: %v", err)
	}
	// ContentLength must be set explicitly: an *os.File is not one of the body
	// types net/http can length-detect, and a chunked PUT breaks the signature.
	req.ContentLength = size
	for _, h := range b.PutHeaders {
		if h == nil {
			continue
		}
		req.Header.Set(h.Name, h.Value)
	}

	if size > 8<<20 { // 8 MiB — below this the transfer is over before it is read
		fmt.Fprintf(f.IOStreams.ErrOut, "uploading %s…\n", humanSize64(size))
	}
	resp, err := (&http.Client{Timeout: uploadTimeout}).Do(req)
	if err != nil {
		return exitcode.Newf(exitcode.Error, "upload: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		hint := ""
		if resp.StatusCode == http.StatusForbidden {
			hint = fmt.Sprintf(" (the presigned upload expired at %s — re-run)", b.ExpiresAt)
		}
		return exitcode.Newf(exitcode.Error, "upload rejected by storage: %s%s\n%s",
			resp.Status, hint, strings.TrimSpace(string(body)))
	}
	return nil
}

// detectMime derives a content type from the filename's extension, falling
// back to sniffing the leading bytes. The server enforces an allowlist, so a
// wrong guess is rejected before any bytes move — but guessing well means the
// caller rarely has to pass --mime.
func detectMime(path, filename string) (string, error) {
	if ct := mime.TypeByExtension(strings.ToLower(filepath.Ext(filename))); ct != "" {
		// TypeByExtension appends a charset for text types; the server matches
		// on the bare type, so drop the parameters.
		if i := strings.IndexByte(ct, ';'); i >= 0 {
			ct = strings.TrimSpace(ct[:i])
		}
		return ct, nil
	}
	fh, err := os.Open(path)
	if err != nil {
		return "", exitcode.Newf(exitcode.Error, "open %s: %v", path, err)
	}
	defer func() { _ = fh.Close() }()
	buf := make([]byte, sniffLen)
	n, err := io.ReadFull(fh, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return "", exitcode.Newf(exitcode.Error, "read %s: %v", path, err)
	}
	return http.DetectContentType(bytes.TrimRight(buf[:n], "\x00")), nil
}

// humanSize64 is humanSize for the int64 sizes the filesystem reports.
func humanSize64(n int64) string {
	if n > int64(^uint32(0)>>1) {
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	}
	return humanSize(int(n))
}
