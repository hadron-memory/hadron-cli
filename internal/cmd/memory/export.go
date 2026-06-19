package memory

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
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

// nodesPageSize bounds one page of the shallow id-listing scan. Like the spec
// whole-corpus commands, the listing is paged to exhaustion — the server caps
// an unbounded nodes query at its default page and silently drops the tail.
const nodesPageSize = 500

// Short aliases for genqlient's deeply-nested generated names for the
// NodeBatch projection.
type (
	batchResult = gen.NodeBatchNodeBatchNodeBatchResult
	batchNode   = gen.NodeBatchNodeBatchNodeBatchResultNodesNode
)

// exportSummaryDTO is the stable --json shape for an export run.
type exportSummaryDTO struct {
	Memory        string   `json:"memory"`
	OutDir        string   `json:"outDir"`
	NodeCount     int      `json:"nodeCount"`
	SkippedData   int      `json:"skippedData"`
	WroteManifest bool     `json:"wroteManifest"`
	Unavailable   []string `json:"unavailable"`
}

func newCmdExport(f *cmdutil.Factory) *cobra.Command {
	var outDir, format string
	cmd := &cobra.Command{
		Use:   "export <memory-id-or-urn> [--out <dir>]",
		Short: "Export a memory's nodes to local markdown files",
		Long: `Export every node in a memory to a local directory as frontmatter
markdown — the same one-file-per-node layout hadron-server writes to a git
repo, but on your disk and without a configured remote.

Each node becomes <out>/<loc>.md (colons in the loc become path segments):
YAML frontmatter (name, id, type, description, abstract, tags, edges, …)
followed by the node content as the markdown body. The layout matches the
server's git export, so the tree round-trips back through the importer — an
export is a faithful, reviewable mirror of the memory.

Nodes are pulled in bulk (up to 200 per request). data-type nodes are
skipped (they carry no markdown body), matching the server's git export.
Existing files are overwritten; files for nodes that no longer exist are
left in place — export never deletes.`,
		Example: `  hadron memory export acme.com:project-kb            # to the current directory
  hadron memory export acme.com:project-kb --out ./kb
  hadron memory export acme.com:project-kb -o ./kb --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "markdown" && format != "md" {
				return exitcode.Newf(exitcode.Usage, "unsupported --format %q (only markdown is supported)", format)
			}
			// --out is optional and defaults to "." (current directory); an
			// explicit empty value falls back to "." rather than a bare path.
			if strings.TrimSpace(outDir) == "" {
				outDir = "."
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			memID, err := resolveMemoryID(cmd, client, args[0])
			if err != nil {
				return err
			}

			// 1. List every node (shallow, paged to exhaustion), partitioning
			//    out data-type nodes the markdown layout doesn't represent.
			listed, err := listAllNodeRefs(cmd.Context(), client, memID)
			if err != nil {
				return err
			}
			ids := make([]string, 0, len(listed))
			skippedData := 0
			for _, n := range listed {
				if n == nil {
					continue
				}
				if n.NodeType == "data" {
					skippedData++
					continue
				}
				ids = append(ids, n.Id)
			}

			// 2. Bulk-fetch the full nodes (content + edges) for those ids.
			nodes, unavailable, err := api.CollectNodeBatch(ids, func(chunk []string) (*batchResult, error) {
				resp, ferr := gen.NodeBatch(cmd.Context(), client, chunk)
				if ferr != nil {
					return nil, api.MapError(ferr)
				}
				return resp.NodeBatch, nil
			})
			if err != nil {
				return err
			}

			// 3. Write each node to <out>/<loc>.md.
			for _, n := range nodes {
				if err := writeNodeMarkdown(outDir, n); err != nil {
					return err
				}
			}

			// 4. Synthesize a root README.md manifest when no node owns the
			//    repo root — keeps the export self-describing and importable.
			wroteManifest, err := maybeWriteManifest(cmd.Context(), client, outDir, memID, nodes)
			if err != nil {
				return err
			}

			// Stable --json shape: an empty result is [], never null.
			if unavailable == nil {
				unavailable = []string{}
			}
			sort.Strings(unavailable)
			summary := exportSummaryDTO{
				Memory:        args[0],
				OutDir:        outDir,
				NodeCount:     len(nodes),
				SkippedData:   skippedData,
				WroteManifest: wroteManifest,
				Unavailable:   unavailable,
			}
			return output.Write(f.IOStreams, f.JSON, summary, func(w io.Writer) error {
				fmt.Fprintf(w, "✓ exported %d node(s) to %s\n", summary.NodeCount, outDir)
				if skippedData > 0 {
					fmt.Fprintf(w, "  skipped %d data node(s)\n", skippedData)
				}
				if wroteManifest {
					fmt.Fprintln(w, "  wrote root README.md manifest")
				}
				if len(unavailable) > 0 {
					fmt.Fprintf(w, "  warning: %d node(s) unavailable: %s\n", len(unavailable), strings.Join(unavailable, ", "))
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVarP(&outDir, "out", "o", ".", "output directory")
	cmd.Flags().StringVar(&format, "format", "markdown", "output format (currently: markdown)")
	return cmd
}

// listAllNodeRefs pages the shallow nodes listing to exhaustion, returning a
// ref (id, loc, nodeType) for every node in the memory. A short final page
// signals the tail; a full page means there may be more (the server truncates
// an unbounded query to one default page).
func listAllNodeRefs(ctx context.Context, client graphql.Client, memID string) ([]*gen.NodesNodesNode, error) {
	mem := memID
	var all []*gen.NodesNodesNode
	for offset := 0; ; offset += nodesPageSize {
		limit, off := nodesPageSize, offset
		resp, err := gen.Nodes(ctx, client, &mem, nil, nil, nil, nil, &limit, &off)
		if err != nil {
			return nil, api.MapError(err)
		}
		all = append(all, resp.Nodes...)
		if len(resp.Nodes) < nodesPageSize {
			return all, nil
		}
	}
}

// writeNodeMarkdown renders one node through the shared codec and writes it to
// <root>/<loc>.md, creating parent directories as needed. standalone is false:
// a tree export encodes loc in the path, so the file omits the self-describing
// loc/memory keys (matching the server's git export byte-for-byte).
func writeNodeMarkdown(root string, n *batchNode) error {
	if n == nil {
		return nil
	}
	path, err := nodedoc.NodeFilePath(root, n.Loc)
	if err != nil {
		return err
	}
	doc, err := nodedoc.RenderMarkdown(api.DocumentFromBatchNode(n), false)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(doc), 0o644)
}

// rootManifest is the frontmatter for a synthesized root README.md.
type rootManifest struct {
	URN         string   `yaml:"urn"`
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Tags        []string `yaml:"tags"`
}

// maybeWriteManifest writes a root README.md describing the memory when no
// node owns the repo root and no README.md already exists — mirroring step 6
// of the server's pushMemoryToGit so the export is self-describing and
// importable. The "root-ish node" test (a short, colon-free loc) matches the
// server's heuristic; an existing README is never clobbered.
func maybeWriteManifest(ctx context.Context, client graphql.Client, root, memID string, nodes []*batchNode) (bool, error) {
	for _, n := range nodes {
		if n != nil && !strings.Contains(n.Loc, ":") && len(n.Loc) < 20 {
			return false, nil
		}
	}
	readme := filepath.Join(root, "README.md")
	if _, err := os.Stat(readme); err == nil {
		return false, nil
	}

	resp, err := gen.GetMemory(ctx, client, memID)
	if err != nil {
		return false, api.MapError(err)
	}
	m := resp.Memory
	if m == nil {
		return false, nil
	}

	// description: shortDescription ?? description ?? "" (null-coalesce, so an
	// explicit empty short description is kept). body: description ?? "".
	desc := ""
	if m.ShortDescription != nil {
		desc = *m.ShortDescription
	} else if m.Description != nil {
		desc = *m.Description
	}
	body := ""
	if m.Description != nil {
		body = *m.Description
	}
	fmYAML, err := nodedoc.MarshalYAML(rootManifest{URN: m.Urn, Name: m.Name, Description: desc, Tags: m.Tags})
	if err != nil {
		return false, err
	}
	doc := fmt.Sprintf("---\n%s\n---\n\n# %s\n\n%s\n", fmYAML, m.Name, body)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return false, err
	}
	if err := os.WriteFile(readme, []byte(doc), 0o644); err != nil {
		return false, err
	}
	return true, nil
}
