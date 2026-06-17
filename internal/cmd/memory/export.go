package memory

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"
	yaml "go.yaml.in/yaml/v3"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// nodeBatchCap mirrors hadron-server's BATCH_READ_MAX_NODES (cor:api:040): a
// single nodeBatch call accepts at most 200 ids and fails loud above that, so
// the export fans out in fixed-size chunks. The server also enforces a ~1 MB
// response cap that can return a partial page (truncated=true) with the
// spillover ids in `omitted`; collectNodeBatch re-requests those.
const nodeBatchCap = 200

// nodesPageSize bounds one page of the shallow id-listing scan. Like the spec
// whole-corpus commands, the listing is paged to exhaustion — the server caps
// an unbounded nodes query at its default page and silently drops the tail.
const nodesPageSize = 500

// Short aliases for genqlient's deeply-nested generated names for the
// NodeBatch projection.
type (
	batchResult = gen.NodeBatchNodeBatchNodeBatchResult
	batchNode   = gen.NodeBatchNodeBatchNodeBatchResultNodesNode
	batchEdge   = gen.NodeBatchNodeBatchNodeBatchResultNodesNodeOutgoingEdgesEdge
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
		Use:   "export <memory-id-or-urn> --out <dir>",
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
		Example: `  hadron memory export acme.com:project-kb --out ./kb
  hadron memory export acme.com:project-kb -o ./kb --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "markdown" && format != "md" {
				return exitcode.Newf(exitcode.Usage, "unsupported --format %q (only markdown is supported)", format)
			}
			if strings.TrimSpace(outDir) == "" {
				return exitcode.Newf(exitcode.Usage, "--out <dir> is required")
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
			nodes, unavailable, err := collectNodeBatch(ids, func(chunk []string) (*batchResult, error) {
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
	cmd.Flags().StringVarP(&outDir, "out", "o", "", "output directory (required)")
	cmd.Flags().StringVar(&format, "format", "markdown", "output format (currently: markdown)")
	_ = cmd.MarkFlagRequired("out")
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

// collectNodeBatch fetches full nodes for ids in cap-sized chunks, re-queuing
// the spillover the server drops under its response-size cap. fetch is
// injected so the chunking/truncation loop is unit-testable without a server.
// Returned: the nodes (input order is not guaranteed across chunks),
// the union of ids the server reported unavailable, and the first error.
func collectNodeBatch(ids []string, fetch func([]string) (*batchResult, error)) ([]*batchNode, []string, error) {
	var nodes []*batchNode
	var unavailable []string
	queue := append([]string(nil), ids...)
	for len(queue) > 0 {
		n := nodeBatchCap
		if n > len(queue) {
			n = len(queue)
		}
		chunk := queue[:n]
		queue = queue[n:]

		res, err := fetch(chunk)
		if err != nil {
			return nil, nil, err
		}
		if res == nil {
			return nil, nil, fmt.Errorf("nodeBatch returned no result for %d id(s)", len(chunk))
		}
		nodes = append(nodes, res.Nodes...)
		unavailable = append(unavailable, res.Unavailable...)
		if res.Truncated {
			// Byte-cap spillover. The server always returns at least one node
			// per call, so re-queuing strictly shrinks the backlog; guard the
			// contract anyway so a server bug surfaces as an error, not a hang.
			if len(res.Nodes) == 0 {
				return nil, nil, fmt.Errorf("nodeBatch truncated without returning any node (%d omitted)", len(res.Omitted))
			}
			queue = append(queue, res.Omitted...)
		}
	}
	return nodes, unavailable, nil
}

// writeNodeMarkdown renders one node and writes it to <root>/<loc>.md,
// creating parent directories as needed.
func writeNodeMarkdown(root string, n *batchNode) error {
	if n == nil {
		return nil
	}
	path, err := nodeFilePath(root, n.Loc)
	if err != nil {
		return err
	}
	doc, err := renderNodeMarkdown(n)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(doc), 0o644)
}

// renderNodeMarkdown produces the full file: YAML frontmatter, then the node
// content as the body. Mirrors hadron-server's pushMemoryToGit file shape
// (`---\n<fm>\n---\n\n<body>\n`).
func renderNodeMarkdown(n *batchNode) (string, error) {
	fmYAML, err := marshalYAML(buildNodeFrontmatter(n))
	if err != nil {
		return "", err
	}
	body := ""
	if n.Content != nil {
		body = *n.Content
	}
	return fmt.Sprintf("---\n%s\n---\n\n%s\n", fmYAML, body), nil
}

// nodeFrontmatter is the YAML header for an exported node. Field order matches
// hadron-server's buildNodeFrontmatter so the diff against a server push stays
// readable; omitempty encodes its omit-on-default rules (see buildNodeFrontmatter).
type nodeFrontmatter struct {
	Name               string      `yaml:"name"`
	ID                 string      `yaml:"id"`
	Alias              string      `yaml:"alias,omitempty"`
	Type               string      `yaml:"type,omitempty"`
	Description        string      `yaml:"description,omitempty"`
	Abstract           string      `yaml:"abstract,omitempty"`
	AbstractOriginHash string      `yaml:"abstractOriginHash,omitempty"`
	ContentHash        string      `yaml:"contentHash,omitempty"`
	Tags               []string    `yaml:"tags,omitempty"`
	Seq                *int        `yaml:"seq,omitempty"`
	Data               any         `yaml:"data,omitempty"`
	Properties         any         `yaml:"properties,omitempty"`
	Nodes              []edgeEntry `yaml:"nodes,omitempty"`
}

// edgeEntry is one outgoing edge inside the frontmatter `nodes:` array. The
// importer keys off `id` and reads `rel` as the label; `loc` is carried for
// readability. condition/priority round-trip the edge's gating and order.
type edgeEntry struct {
	ID        string `yaml:"id"`
	Loc       string `yaml:"loc,omitempty"`
	Rel       string `yaml:"rel"`
	Condition any    `yaml:"condition,omitempty"`
	Priority  int    `yaml:"priority,omitempty"`
}

// buildNodeFrontmatter mirrors hadron-server's buildNodeFrontmatter
// (src/integrations/github/nodeFrontmatter.ts) field-for-field so a local
// export round-trips through the same importer. The importer upserts the
// importer-consumed fields (type→isLink, alias, description, abstract,
// abstractOriginHash, contentHash, tags, nodes) as `value ?? null`, so an
// omitted field is actively nulled on re-import — the omit rules here match
// the server's exactly. contentHash is recomputed from content (it is a DB
// column the GraphQL API doesn't expose) using the same sha256[:8] the server
// uses, so the value is identical. seq/data/properties aren't importer-consumed
// but are emitted for a faithful mirror; an empty {}/[] data or properties is
// dropped (omitempty), which is harmless since the importer ignores them.
func buildNodeFrontmatter(n *batchNode) nodeFrontmatter {
	fm := nodeFrontmatter{Name: n.Name, ID: n.Id}
	if n.Alias != nil {
		fm.Alias = *n.Alias
	}
	if n.NodeType != "" && n.NodeType != "info" {
		fm.Type = n.NodeType
	}
	if n.Description != nil {
		fm.Description = *n.Description
	}
	if n.Abstract != nil {
		fm.Abstract = *n.Abstract
	}
	if n.AbstractOriginHash != nil {
		fm.AbstractOriginHash = *n.AbstractOriginHash
	}
	if n.Content != nil {
		fm.ContentHash = contentHash(*n.Content)
	}
	fm.Tags = n.Tags
	fm.Seq = n.Seq
	fm.Data = decodeJSON(n.Data)
	fm.Properties = decodeJSON(n.Properties)
	fm.Nodes = buildEdgeEntries(n.OutgoingEdges)
	return fm
}

// buildEdgeEntries projects outgoing edges into the inline `nodes:` array,
// matching the server's buildEdgeFrontmatter: id always, loc when set, rel
// (the label, empty string when blank), condition when present, priority when
// non-zero. An edge with no target can't be addressed and is skipped. Returns
// nil when there are no edges so the `nodes:` key is omitted entirely.
func buildEdgeEntries(edges []*batchEdge) []edgeEntry {
	out := make([]edgeEntry, 0, len(edges))
	for _, e := range edges {
		if e == nil || e.Target == nil {
			continue
		}
		entry := edgeEntry{ID: e.Target.Id, Loc: e.Target.Loc, Rel: e.Label, Condition: decodeJSON(e.Condition)}
		if e.Priority != 0 {
			entry.Priority = e.Priority
		}
		out = append(out, entry)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// contentHash recomputes the server's content fingerprint: sha256 of the
// content, hex, first 8 chars; empty content has no hash. Matches
// hadron-server's computeContentHash (src/lib/contentHash.ts) so the exported
// contentHash equals the value the server would have written.
func contentHash(content string) string {
	if content == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])[:8]
}

// decodeJSON turns a GraphQL JSON scalar into a value yaml can render as
// proper YAML (not a quoted JSON string). A nil pointer, empty bytes, or a
// literal `null` decode to nil so the caller's omitempty drops the key.
func decodeJSON(raw *json.RawMessage) any {
	if raw == nil {
		return nil
	}
	trimmed := bytes.TrimSpace(*raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil
	}
	var v any
	if err := json.Unmarshal(trimmed, &v); err != nil {
		return nil
	}
	return v
}

// locToSegments splits a loc into filesystem path segments, rejecting empty,
// `.`, and `..` segments. Those feed directory creation and file writes, so a
// malformed loc (e.g. `a::b`, a trailing `:`, or a `..`) is a real hazard —
// fail loud rather than write outside the output tree. Mirrors the server's
// locToSegments guard.
func locToSegments(loc string) ([]string, error) {
	parts := strings.Split(loc, ":")
	for _, p := range parts {
		if p == "" || p == "." || p == ".." {
			return nil, fmt.Errorf("unsafe loc %q: empty, '.', or '..' path segments are not allowed", loc)
		}
	}
	return parts, nil
}

// nodeFilePath maps a loc to its on-disk markdown path under root. The
// empty-loc root node is README.md; every other node is <seg>/<seg>.md, so a
// node's path is stable whether or not it has children (children land in the
// sibling <seg>/ folder). Mirrors the server's nodeFilePath.
func nodeFilePath(root, loc string) (string, error) {
	if loc == "" {
		return filepath.Join(root, "README.md"), nil
	}
	segs, err := locToSegments(loc)
	if err != nil {
		return "", err
	}
	return filepath.Join(append([]string{root}, segs...)...) + ".md", nil
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
	fmYAML, err := marshalYAML(rootManifest{URN: m.Urn, Name: m.Name, Description: desc, Tags: m.Tags})
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

// marshalYAML encodes v as YAML with 2-space indents (matching the server's
// yaml writer) and trims the trailing newline so the caller controls the
// document framing.
func marshalYAML(v any) (string, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	if err := enc.Close(); err != nil {
		return "", err
	}
	return strings.TrimRight(buf.String(), "\n"), nil
}
