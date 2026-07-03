package node

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// exportNodeSummaryDTO is the stable --json shape when exporting to a file.
// (Exporting to stdout emits the document itself, never this wrapper.)
type exportNodeSummaryDTO struct {
	Node    string `json:"node"`
	Loc     string `json:"loc"`
	Memory  string `json:"memory"`
	OutFile string `json:"outFile"`
	Format  string `json:"format"`
	Bytes   int    `json:"bytes"`
}

func newCmdExport(f *cmdutil.Factory) *cobra.Command {
	var outFile, format, memory string
	cmd := &cobra.Command{
		Use:   "export <node-urn> | <loc> -m <memory>",
		Short: "Export a single node to a portable file (markdown or JSON)",
		Long: `Export one node by its fully-qualified URN (<org>::<memory>::<loc>) to a
self-contained file you can review, edit, move, and import back with
` + "`hadron node import`" + ` — into the same memory, a different memory, or a
fresh server.

The markdown carries two self-describing keys (loc, memory) so a lone file
knows where it came from. --format json emits the same node as one canonical
JSON object.

The file is rendered by the server (one renderer shared with the portal and
every other API client), so the bytes are identical everywhere; it round-trips
back through ` + "`hadron node import`" + `. Requires a server that supports
server-side export (hadron-server #386).

Writes to stdout by default (so it composes in a pipe with node import);
-o <file> writes a file instead. When --out ends in .md or .json and --format
is unset, the format is inferred from the extension.`,
		Example: `  hadron node export acme.com:kb:findings:flaky-ci            # markdown to stdout
  hadron node export acme.com:kb:findings:flaky-ci -o flaky.md
  hadron node export acme.com:kb:findings:flaky-ci --format json -o flaky.json
  hadron node export acme.com:kb:x | hadron node import -m acme.com:kb2 -`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fmtName, err := resolveDocFormat(format, outFile, cmd.Flags().Changed("format"))
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			id, err := cmdutil.ResolveNodeRef(cmd, client, memory, args[0])
			if err != nil {
				return err
			}

			// One renderer for every client: the SERVER renders the node, so the
			// bytes are byte-for-byte what the portal and any other API client get
			// (#106). full: true is the canonical, import-round-trippable form.
			exportFmt := gen.NodeExportFormatMd
			if fmtName == "json" {
				exportFmt = gen.NodeExportFormatJson
			}
			resp, err := gen.NodeExport(cmd.Context(), client, id, exportFmt)
			if err != nil {
				if isUnknownFieldErr(err, "nodeExport") {
					return exitcode.Newf(exitcode.Usage,
						"this hadron-server is too old to render exports server-side (no nodeExport field; needs hadron-server #386) — upgrade the server")
				}
				return api.MapError(err)
			}
			if resp == nil || resp.NodeExport == nil {
				return exitcode.Newf(exitcode.NotFound, "node %q is not readable", args[0])
			}
			rendered := resp.NodeExport.Data

			// stdout: the document IS the output — no summary wrapper, even with
			// --json (don't corrupt a piped md/json stream).
			if outFile == "" || outFile == "-" {
				_, werr := io.WriteString(f.IOStreams.Out, rendered)
				return werr
			}

			if dir := filepath.Dir(outFile); dir != "" {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					return err
				}
			}
			if err := os.WriteFile(outFile, []byte(rendered), 0o644); err != nil {
				return err
			}

			// The render carries no identifying metadata, so read the node's
			// loc/name/memory for the summary (file path only — stdout never
			// emits it). Best-effort: a missing read just leaves the fields blank.
			loc, name, memURN := exportSummaryMeta(cmd, client, id)
			summary := exportNodeSummaryDTO{
				Node:    name,
				Loc:     loc,
				Memory:  memURN,
				OutFile: outFile,
				Format:  fmtName,
				Bytes:   resp.NodeExport.Bytes,
			}
			return output.Write(f.IOStreams, f.JSON, summary, func(w io.Writer) error {
				// Fall back through loc → name → the original ref so the message
				// is never "✓ exported  to …" when the metadata read came up empty.
				ref := loc
				if ref == "" {
					ref = name
				}
				if ref == "" {
					ref = args[0]
				}
				fmt.Fprintf(w, "✓ exported %s to %s (%d bytes)\n", ref, outFile, summary.Bytes)
				return nil
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory (org::memory) to resolve a bare <loc> against")
	cmd.Flags().StringVarP(&outFile, "out", "o", "", `output file ("-" or unset writes to stdout)`)
	cmd.Flags().StringVar(&format, "format", "md", "output format: md or json")
	return cmd
}

// resolveDocFormat normalizes --format (md|markdown|json), inferring from the
// --out extension when --format wasn't explicitly set. An explicit --format
// always wins.
func resolveDocFormat(format, outFile string, explicit bool) (string, error) {
	if !explicit && outFile != "" && outFile != "-" {
		switch strings.ToLower(filepath.Ext(outFile)) {
		case ".json":
			return "json", nil
		case ".md", ".markdown":
			return "md", nil
		}
	}
	switch strings.ToLower(format) {
	case "md", "markdown":
		return "md", nil
	case "json":
		return "json", nil
	default:
		return "", exitcode.Newf(exitcode.Usage, "unsupported --format %q (want md or json)", format)
	}
}

// exportSummaryMeta reads a node's loc, name, and memory URN for the file-write
// summary — the server's render returns the bytes but no identifying metadata.
// One query carries memory { urn }, so there's no second memory-list round-trip.
// Best-effort: an unreadable node yields empty fields rather than failing an
// export whose file is already written.
func exportSummaryMeta(cmd *cobra.Command, client graphql.Client, id string) (loc, name, memURN string) {
	resp, err := gen.NodeExportMeta(cmd.Context(), client, id)
	if err != nil || resp == nil || resp.Node == nil {
		return "", "", ""
	}
	n := resp.Node
	// memory is nullable on Node; fall back to the id so the summary still
	// names the memory, just not by URN.
	memURN = n.MemoryId
	if n.Memory != nil && n.Memory.Urn != "" {
		memURN = n.Memory.Urn
	}
	return n.Loc, n.Name, memURN
}

// isUnknownFieldErr reports whether err is a GraphQL schema-validation failure
// for an unknown field — the signature of calling a newer field against an
// older server. Matched on the field name plus the validation phrasing so a
// node literally named in some other error doesn't trip it.
func isUnknownFieldErr(err error, field string) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	if !strings.Contains(msg, field) {
		return false
	}
	return strings.Contains(msg, "Cannot query field") ||
		strings.Contains(msg, "Unknown field") ||
		strings.Contains(msg, "GRAPHQL_VALIDATION_FAILED")
}

