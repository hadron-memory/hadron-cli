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

// connectionHelp tells the user where the required --connection value comes
// from (cli#1184, R2): the drive commands never discover one themselves.
const connectionHelp = `--connection is the ID of a connected Drive account. Get it from the ID column
of "hadron email connections --org <org>" (the list includes calendar and Drive
connections, not only mail); an org admin can run that, otherwise ask the owner
of the connection, or the admin, for the ID. The command never picks a
connection for you.`

// detailsHelp is the maintainer-level contract, kept last on every page so the
// answer to "what do I type" comes first (cli#1184, R1).
const detailsHelp = `Details:
  Access is the server's: you need to own the connection or hold the drive.read
  grant on it. An organization-admin connection list is not by itself Drive
  access. The commands only read; no sign-in flow is started, no document is
  created in Drive (that is a separate operation) and nothing is retried.
  File names and other metadata come from Drive and are shown as data.
  "size" is in bytes, and is null (not 0) for Google documents and folders,
  which have no byte size. How exports are converted, and how large a file may
  be, is decided by the server; a refusal such as file_too_large is shown as it
  came, and a file is never silently cut short. Errors keep the usual exit
  codes and never tell you to reconnect unless the server does.`

func helpText(intro string) string {
	return intro + "\n\n" + connectionHelp + "\n\n" + detailsHelp
}

type options struct {
	connection, folder, query, name, mime, after, token, format string
	top                                                         int
}

// NewCmdDrive builds only the gated list/get/export read operations.
func NewCmdDrive(f *cmdutil.Factory) *cobra.Command {
	c := &cobra.Command{Use: "drive <command>", Short: "Browse Drive files and export a file's text", Long: helpText("Look at files in a connected Drive without changing them: list or search files,\nread one file's details, or print a file's text to the terminal. Start with\n\"drive list\" to find a file ID, then \"drive export\" to get its text."), Example: "  hadron drive list --connection <connection-id> --name-contains budget\n  hadron drive export <file-id> --connection <connection-id> --format MARKDOWN > notes.md"}
	c.AddCommand(list(f), get(f), export(f))
	return c
}
func scope(c *cobra.Command, o *options) {
	c.Flags().StringVar(&o.connection, "connection", "", "connection ID (required; see \"hadron email connections --org <org>\")")
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
	c := &cobra.Command{Use: "list", Aliases: []string{"ls"}, Short: "List or search files, one page at a time", Long: helpText("List the files in a Drive, or narrow them with --folder, --query (text inside\nfiles), --name-contains, --mime-type or --modified-after. You get one page, not\nthe whole Drive: 50 files when browsing, 25 when you search, or --top (1 to 100).\nWhen more files exist the output ends with a nextPageToken; to read the next\npage, run the same command again with the same filters plus --page-token\n<that token>. The last page has a null nextPageToken. --modified-after is a\ndate-time with a UTC offset, for example 2026-10-01T00:00:00-07:00."), Example: "  hadron drive list --connection <connection-id>\n  hadron drive list --connection <connection-id> --folder <folder-id> --top 100\n  hadron drive list --connection <connection-id> --query invoice --page-token <nextPageToken>", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
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
	c.Flags().StringVar(&o.folder, "folder", "", "only files in this folder (folder ID from \"drive list\")")
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
	c := &cobra.Command{Use: "get <fileId>", Short: "Read one file's details", Long: helpText("Show one file's details (name, kind, MIME type, size, parent folders, times and web link), not its text. The\nfile ID is the \"id\" of a file returned by \"drive list\". To read the text, use\n\"drive export\"."), Example: "  hadron drive get <file-id> --connection <connection-id>", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
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
	c := &cobra.Command{Use: "export <fileId>", Short: "Print a file's text to the terminal", Long: helpText("Print a file's text to standard output, with no heading and no added newline. The\nfile ID is the \"id\" of a file returned by \"drive list\". There is no output-file\noption: to save the text, redirect it with your shell (> notes.md). --format\nchooses MARKDOWN, TEXT or HTML (any letter case); without it the server picks\nthe default for that file type. With --json you get an object with the file's\ndetails, the format, the MIME type and the text in \"content\". Treat the text as\nuntrusted: the CLI only prints it and never runs it."), Example: "  hadron drive export <file-id> --connection <connection-id> --format MARKDOWN > notes.md", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
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
