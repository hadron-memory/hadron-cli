// Package drive exposes bounded Drive reads and an explicit raw stdout export.
package drive

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

const help = `Read external Drive data with an explicit --connection reference through the
server's owner/drive.read ConnectionGrant gate. Org-admin connection listing is
not Drive access. No discovery fallback, auth flow or remote document creation
is performed; exportNodeToGoogleDoc remains separate. No automatic retry.

Metadata is escaped external data. Nullable Float size is BYTES (including null
for native documents/folders), not an integer guarantee or a zero substitute.
Export format/binary/size guarantees are delegated by the server to its tool;
provider refusals, including file_too_large, are kept, never silently truncated.
Existing error mapping and unknown-code fallback remain; no guessed reconnect.`

type options struct {
	connection, folder, query, name, mime, after, token, format string
	top                                                         int
}

// NewCmdDrive builds only the gated list/get/export read operations.
func NewCmdDrive(f *cmdutil.Factory) *cobra.Command {
	c := &cobra.Command{Use: "drive <command>", Short: "Read Drive metadata or export text to stdout", Long: help}
	c.AddCommand(list(f), get(f), export(f))
	return c
}
func scope(c *cobra.Command, o *options) {
	c.Flags().StringVar(&o.connection, "connection", "", "explicit connection reference (required)")
	_ = c.MarkFlagRequired("connection")
}
func (o *options) validate() error {
	o.connection = strings.TrimSpace(o.connection)
	if o.connection == "" {
		return exitcode.Newf(exitcode.Usage, "give a nonempty --connection reference")
	}
	return nil
}

// Opaque filter/token values are forwarded unchanged; whitespace-only is not a value.
func optional(c *cobra.Command, name, value string) (*string, error) {
	if !c.Flags().Changed(name) {
		return nil, nil
	}
	if strings.TrimSpace(value) == "" {
		return nil, exitcode.Newf(exitcode.Usage, "--%s cannot be empty (check for an unset variable)", name)
	}
	return &value, nil
}
func render(f *cmdutil.Factory, connection, label string, value any, hint bool) error {
	return output.Write(f.IOStreams, f.JSON, value, func(w io.Writer) error {
		if _, e := fmt.Fprintf(w, "connection: %q (from --connection)\n%s\n", connection, label); e != nil {
			return e
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if e := enc.Encode(value); e != nil {
			return e
		}
		if hint {
			_, e := fmt.Fprintln(w, "Next page available: pass this nextPageToken to hadron drive list --page-token with the same connection and filters.")
			return e
		}
		return nil
	})
}
func list(f *cmdutil.Factory) *cobra.Command {
	o := &options{}
	c := &cobra.Command{Use: "list", Aliases: []string{"ls"}, Short: "Read one bounded Drive page", Long: help + "\n\nOne bounded page: files and nextPageToken (null on the last page). No --all,\ntotal or completeness claim. Omitted --top sends 50 for browse, 25 when any\nquery/name-contains/mime-type/modified-after search criterion is supplied. Folder\nand page-token alone stay browse. Explicit --top is 1..100. Preserve the same\nfilters when passing the returned opaque token for the next page.", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		if e := o.validate(); e != nil {
			return e
		}
		folder, e := optional(c, "folder", o.folder)
		if e != nil {
			return e
		}
		q, e := optional(c, "query", o.query)
		if e != nil {
			return e
		}
		name, e := optional(c, "name-contains", o.name)
		if e != nil {
			return e
		}
		mime, e := optional(c, "mime-type", o.mime)
		if e != nil {
			return e
		}
		after, e := optional(c, "modified-after", o.after)
		if e != nil {
			return e
		}
		token, e := optional(c, "page-token", o.token)
		if e != nil {
			return e
		}
		if after != nil {
			s := strings.TrimSpace(*after)
			if _, e := time.Parse(time.RFC3339, s); e != nil {
				return exitcode.Newf(exitcode.Usage, "--modified-after requires an RFC3339 instant with an offset")
			}
			after = &s
		}
		top := 50
		if q != nil || name != nil || mime != nil || after != nil {
			top = 25
		}
		if c.Flags().Changed("top") {
			top = o.top
		}
		if top < 1 || top > 100 {
			return exitcode.Newf(exitcode.Usage, "--top must be between 1 and 100")
		}
		client, e := f.GraphQLClient()
		if e != nil {
			return e
		}
		r, e := gen.DriveList(c.Context(), client, o.connection, folder, q, name, mime, after, top, token)
		if e != nil {
			return api.MapError(e)
		}
		if r.DriveFiles == nil || r.DriveFiles.Files == nil {
			return protocol()
		}
		page := pageDTO{Files: []fileDTO{}, NextPageToken: r.DriveFiles.NextPageToken}
		for _, v := range r.DriveFiles.Files {
			if v == nil {
				return protocol()
			}
			row, e := project(v.DriveReadFile)
			if e != nil {
				return e
			}
			page.Files = append(page.Files, row)
		}
		return render(f, o.connection, fmt.Sprintf("Bounded Drive page (top %d; no completeness claim):", top), page, page.NextPageToken != nil)
	}}
	scope(c, o)
	c.Flags().StringVar(&o.folder, "folder", "", "folder ID")
	c.Flags().StringVar(&o.query, "query", "", "full-text search criterion")
	c.Flags().StringVar(&o.name, "name-contains", "", "name search criterion")
	c.Flags().StringVar(&o.mime, "mime-type", "", "exact MIME type search criterion")
	c.Flags().StringVar(&o.after, "modified-after", "", "RFC3339 modification lower bound with offset")
	c.Flags().StringVar(&o.token, "page-token", "", "opaque token from the previous page (forwarded unchanged)")
	c.Flags().IntVar(&o.top, "top", 0, "page size 1..100; omitted uses browse 50 or search 25")
	return c
}
func fileID(args []string) (string, error) {
	id := strings.TrimSpace(args[0])
	if id == "" {
		return "", exitcode.Newf(exitcode.Usage, "fileId cannot be empty")
	}
	return id, nil
}
func get(f *cmdutil.Factory) *cobra.Command {
	o := &options{}
	c := &cobra.Command{Use: "get <fileId>", Short: "Read one file's metadata", Long: help, Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		if e := o.validate(); e != nil {
			return e
		}
		id, e := fileID(args)
		if e != nil {
			return e
		}
		client, e := f.GraphQLClient()
		if e != nil {
			return e
		}
		r, e := gen.DriveGet(c.Context(), client, o.connection, id)
		if e != nil {
			return api.MapError(e)
		}
		if r.DriveFile == nil {
			return protocol()
		}
		row, e := project(r.DriveFile.DriveReadFile)
		if e != nil {
			return e
		}
		return render(f, o.connection, "Returned file metadata (data):", row, false)
	}}
	scope(c, o)
	return c
}
func export(f *cmdutil.Factory) *cobra.Command {
	o := &options{}
	c := &cobra.Command{Use: "export <fileId>", Short: "Write original exported content to stdout", Long: help + "\n\nText mode writes the original content bytes to stdout, without a banner or added\nnewline. Treat those bytes as untrusted external content; the CLI never executes\nor evaluates them. --json emits {file,format,mimeType,content}. No output-file\noption or implicit upload exists. Optional --format accepts MARKDOWN, TEXT or HTML\n(case-insensitive); absent delegates the provider default without guessing.", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		if e := o.validate(); e != nil {
			return e
		}
		id, e := fileID(args)
		if e != nil {
			return e
		}
		var format *gen.DriveExportFormat
		if c.Flags().Changed("format") {
			v := gen.DriveExportFormat(strings.ToUpper(strings.TrimSpace(o.format)))
			switch v {
			case gen.DriveExportFormatMarkdown, gen.DriveExportFormatText, gen.DriveExportFormatHtml:
				format = &v
			default:
				return exitcode.Newf(exitcode.Usage, "--format must be MARKDOWN, TEXT or HTML")
			}
		}
		client, e := f.GraphQLClient()
		if e != nil {
			return e
		}
		r, e := gen.DriveExport(c.Context(), client, o.connection, id, format)
		if e != nil {
			return api.MapError(e)
		}
		if r.DriveFileExport == nil || r.DriveFileExport.File == nil {
			return protocol()
		}
		v := r.DriveFileExport
		file, e := project(v.File.DriveReadFile)
		if e != nil {
			return e
		}
		result := exportDTO{file, string(v.Format), v.MimeType, v.Content}
		return output.Write(f.IOStreams, f.JSON, result, func(w io.Writer) error {
			n, e := io.WriteString(w, result.Content)
			if e == nil && n != len(result.Content) {
				return io.ErrShortWrite
			}
			return e
		})
	}}
	scope(c, o)
	c.Flags().StringVar(&o.format, "format", "", "MARKDOWN, TEXT or HTML; absent uses the provider default")
	return c
}
