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
	"github.com/hadron-memory/hadron-cli/internal/nodedoc"
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
	var outFile, format string
	cmd := &cobra.Command{
		Use:   "export <node-urn>",
		Short: "Export a single node to a portable file (markdown or JSON)",
		Long: `Export one node by its fully-qualified URN (<org>:<memory>:<loc>) to a
self-contained file you can review, edit, move, and import back with
` + "`hadron node import`" + ` — into the same memory, a different memory, or a
fresh server.

The markdown form is identical to what ` + "`hadron memory export`" + ` writes,
plus two self-describing keys (loc, memory) so a lone file knows where it came
from. --format json emits the same node as one canonical JSON object.

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
			id, err := cmdutil.ResolveNodeURN(cmd, client, args[0])
			if err != nil {
				return err
			}

			// Read the full node via the bulk path (the only projection that
			// carries alias + properties). A single id that lists but can't be
			// read comes back empty (the nodes-list-vs-read visibility gap) — a
			// clean NotFound, never a silent empty file.
			nodes, _, err := api.CollectNodeBatch([]string{id}, func(chunk []string) (*gen.NodeBatchNodeBatchNodeBatchResult, error) {
				resp, ferr := gen.NodeBatch(cmd.Context(), client, chunk)
				if ferr != nil {
					return nil, api.MapError(ferr)
				}
				return resp.NodeBatch, nil
			})
			if err != nil {
				return err
			}
			if len(nodes) == 0 || nodes[0] == nil {
				return exitcode.Newf(exitcode.NotFound, "node %q is not readable", args[0])
			}

			n := nodes[0]
			doc := api.DocumentFromBatchNode(n)
			doc.MemoryURN = resolveMemoryURN(cmd, client, n.MemoryId)

			var rendered string
			if fmtName == "json" {
				rendered, err = nodedoc.RenderJSON(doc)
			} else {
				rendered, err = nodedoc.RenderMarkdown(doc, true)
			}
			if err != nil {
				return err
			}

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
			summary := exportNodeSummaryDTO{
				Node:    doc.Name,
				Loc:     doc.Loc,
				Memory:  doc.MemoryURN,
				OutFile: outFile,
				Format:  fmtName,
				Bytes:   len(rendered),
			}
			return output.Write(f.IOStreams, f.JSON, summary, func(w io.Writer) error {
				ref := doc.Loc
				if ref == "" {
					ref = doc.Name
				}
				fmt.Fprintf(w, "✓ exported %s to %s (%d bytes)\n", ref, outFile, summary.Bytes)
				return nil
			})
		},
	}
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

// resolveMemoryURN maps a memory id to its URN via myMemories so a standalone
// export is self-describing (memory: <org>:<memory>). Falls back to the id when
// the memory isn't among the caller's memories — the file still imports, just
// keyed by id rather than URN.
func resolveMemoryURN(cmd *cobra.Command, client graphql.Client, memID string) string {
	includeAgentSystem := true
	resp, err := gen.MyMemories(cmd.Context(), client, &includeAgentSystem)
	if err != nil {
		return memID
	}
	for _, m := range resp.MyMemories {
		if m.Id == memID {
			return m.Urn
		}
	}
	return memID
}
