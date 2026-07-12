package node

import (
	"encoding/base64"
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
		Short: "Export a single node to a portable file (markdown, JSON, or PDF)",
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

--format pdf renders a PDF server-side (the same renderer the portal's
"Download → PDF" uses). It's binary, so it must go to a file: pass -o <file>
(writing PDF to stdout is refused). full/sections don't apply to a PDF render.

Writes to stdout by default (so it composes in a pipe with node import);
-o <file> writes a file instead. When --out ends in .md, .json, or .pdf and
--format is unset, the format is inferred from the extension.`,
		Example: `  hadron node export acme.com::kb::findings:flaky-ci            # markdown to stdout
  hadron node export acme.com::kb::findings:flaky-ci -o flaky.md
  hadron node export acme.com::kb::findings:flaky-ci --format json -o flaky.json
  hadron node export acme.com::kb::findings:flaky-ci --format pdf -o flaky.pdf
  hadron node export acme.com::kb::x | hadron node import -m acme.com::kb2 -`,
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
			switch fmtName {
			case "json":
				exportFmt = gen.NodeExportFormatJson
			case "pdf":
				exportFmt = gen.NodeExportFormatPdf
			}
			resp, err := gen.NodeExport(cmd.Context(), client, id, exportFmt)
			if err != nil {
				if isUnknownFieldErr(err, "nodeExport") {
					return exitcode.Newf(exitcode.Usage,
						"this hadron-server is too old to render exports server-side (no nodeExport field; needs hadron-server #386) — upgrade the server")
				}
				// An older server has nodeExport but not the PDF renderer, so it
				// rejects the PDF enum value at validation time (#109).
				if fmtName == "pdf" && isUnknownExportFormatErr(err) {
					return exitcode.Newf(exitcode.Usage,
						"this hadron-server doesn't support PDF export (unknown NodeExportFormat value PDF) — upgrade the server")
				}
				return api.MapError(err)
			}
			if resp == nil || resp.NodeExport == nil {
				return exitcode.Newf(exitcode.NotFound, "node %q is not readable", args[0])
			}

			// The payload encoding is deterministic by format: md/json come back as
			// TEXT (pass through), pdf as BASE64 — decode it to the real bytes
			// before writing, never the base64 text (#109). Keying off the
			// requested format (not a queried `encoding` field) keeps the md/json
			// query working against a server that predates the encoding field.
			isBinary := fmtName == "pdf"
			var outBytes []byte
			if isBinary {
				decoded, derr := base64.StdEncoding.DecodeString(resp.NodeExport.Data)
				if derr != nil {
					return exitcode.Newf(exitcode.Error, "server returned an undecodable base64 %s payload: %v", fmtName, derr)
				}
				outBytes = decoded
			} else {
				outBytes = []byte(resp.NodeExport.Data)
			}

			// stdout: the document IS the output — no summary wrapper, even with
			// --json (don't corrupt a piped md/json stream). A binary format has
			// no safe stdout representation, so require -o for it.
			if outFile == "" || outFile == "-" {
				if isBinary {
					return exitcode.Newf(exitcode.Usage, "%s export is binary — write it to a file with -o <file>", fmtName)
				}
				_, werr := f.IOStreams.Out.Write(outBytes)
				return werr
			}

			if dir := filepath.Dir(outFile); dir != "" {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					return err
				}
			}
			if err := os.WriteFile(outFile, outBytes, 0o644); err != nil {
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
				Bytes:   len(outBytes),
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
	cmd.Flags().StringVar(&format, "format", "md", "output format: md, json, or pdf (pdf requires -o)")
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
		case ".pdf":
			return "pdf", nil
		}
	}
	switch strings.ToLower(format) {
	case "md", "markdown":
		return "md", nil
	case "json":
		return "json", nil
	case "pdf":
		return "pdf", nil
	default:
		return "", exitcode.Newf(exitcode.Usage, "unsupported --format %q (want md, json, or pdf)", format)
	}
}

// isUnknownExportFormatErr reports whether err is the GraphQL validation failure
// an older server raises for the PDF enum value it doesn't know — the value (not
// the field) is unknown, so it reads differently from isUnknownFieldErr.
func isUnknownExportFormatErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	if !strings.Contains(msg, "PDF") {
		return false
	}
	return strings.Contains(msg, "does not exist in") ||
		strings.Contains(msg, "GRAPHQL_VALIDATION_FAILED") ||
		strings.Contains(msg, "NodeExportFormat")
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

