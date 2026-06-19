package node

import (
	"fmt"
	"io"
	"os"
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

// importNodeSummaryDTO is the stable --json shape for an import run.
type importNodeSummaryDTO struct {
	Memory       string   `json:"memory"`
	Loc          string   `json:"loc"`
	Action       string   `json:"action"`
	NodeID       string   `json:"nodeId"`
	EdgesWired   int      `json:"edgesWired"`
	UnwiredEdges []string `json:"unwiredEdges"`
}

func newCmdImport(f *cmdutil.Factory) *cobra.Command {
	var (
		memory     string
		loc        string
		format     string
		withEdges  bool
		createOnly bool
		dryRun     bool
	)
	cmd := &cobra.Command{
		Use:   "import <file-path|->",
		Short: "Import a node from a file, creating or updating it",
		Long: `Import a node from a file produced by ` + "`hadron node export`" + ` (or any
frontmatter-markdown / canonical-JSON node file). A node already at the target
loc is updated; otherwise a new one is created. Read "-" to import from stdin,
so an export pipes straight into an import.

The target memory and loc come from the file's self-describing keys; -m/--memory
and --loc override them (and let you re-home a node into a different memory).
Outgoing edges are imported only with --with-edges (off by default so an import
never makes surprising edge mutations).`,
		Example: `  hadron node import flaky.md                       # self-describing file
  hadron node import flaky.md -m acme.com:kb2        # retarget to another memory
  hadron node import --format json flaky.json --with-edges
  hadron node export acme.com:kb:x | hadron node import -m acme.com:kb2 -`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]
			fmtName, err := resolveDocFormat(format, path, cmd.Flags().Changed("format"))
			if err != nil {
				return err
			}

			data, err := readImportSource(path, f.IOStreams.In)
			if err != nil {
				return err
			}
			if strings.TrimSpace(string(data)) == "" {
				return exitcode.Newf(exitcode.Usage, "empty input — nothing to import")
			}

			var doc *nodedoc.Document
			if fmtName == "json" {
				doc, err = nodedoc.ParseJSON(data)
			} else {
				doc, err = nodedoc.ParseMarkdown(data)
			}
			if err != nil {
				return exitcode.Newf(exitcode.Usage, "%v", err)
			}

			// Target resolution: flag > frontmatter > error.
			memoryRef := firstNonEmpty(memory, doc.MemoryURN)
			if memoryRef == "" {
				return exitcode.Newf(exitcode.Usage, "no target memory — pass -m <memory> or include a `memory:` key in the file")
			}
			targetLoc := firstNonEmpty(loc, doc.Loc)
			if targetLoc == "" {
				return exitcode.Newf(exitcode.Usage, "no target loc — pass --loc <loc> or include a `loc:` key in the file")
			}
			if strings.TrimSpace(doc.Name) == "" {
				return exitcode.Newf(exitcode.Usage, "file has no `name` — a node name is required")
			}

			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}

			// Classify create vs update by a best-effort existence probe before
			// the upsert (the upsert response can't tell us which happened).
			action := "created"
			if nodeExists(cmd, client, memoryRef, targetLoc) {
				action = "updated"
			}

			if dryRun {
				return emitImportSummary(f, importNodeSummaryDTO{
					Memory: memoryRef, Loc: targetLoc, Action: action,
					EdgesWired: 0, UnwiredEdges: []string{},
				}, true, withEdges, len(doc.Edges))
			}

			input, err := buildNodeInput(doc, memoryRef, targetLoc, createOnly)
			if err != nil {
				return err
			}
			resp, err := gen.UpsertNode(cmd.Context(), client, input)
			if err != nil {
				return api.MapError(err)
			}
			node := resp.UpsertNode

			edgesWired, unwired := 0, []string{}
			if withEdges && len(doc.Edges) > 0 {
				edgesWired, unwired, err = wireEdges(cmd, client, memoryRef, node.Id, doc.Edges)
				if err != nil {
					return err
				}
			}

			return emitImportSummary(f, importNodeSummaryDTO{
				Memory: memoryRef, Loc: node.Loc, Action: action, NodeID: node.Id,
				EdgesWired: edgesWired, UnwiredEdges: unwired,
			}, false, withEdges, len(doc.Edges))
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "target memory ID or URN (overrides the file's memory key)")
	cmd.Flags().StringVar(&loc, "loc", "", "target loc (overrides the file's loc key)")
	cmd.Flags().StringVar(&format, "format", "md", "input format: md or json (inferred from the file extension when unset)")
	cmd.Flags().BoolVar(&withEdges, "with-edges", false, "also wire the file's outgoing edges (best-effort)")
	cmd.Flags().BoolVar(&createOnly, "create-only", false, "fail if the loc already exists (no update)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "parse and classify without mutating")
	return cmd
}

// readImportSource reads the node file, or stdin when path is "-".
func readImportSource(path string, stdin io.Reader) ([]byte, error) {
	if path == "-" {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return nil, err
		}
		return data, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, exitcode.Newf(exitcode.Usage, "reading %s: %v", path, err)
	}
	return data, nil
}

// buildNodeInput maps a Document onto the upsert input. Empty optional fields
// are omitted (preserve-on-update) rather than sent as a clear; the id and the
// recompute-only hashes (contentHash, abstractOriginHash) are intentionally not
// sent — the upsert keys on (memory, loc) and the server owns the hashes.
func buildNodeInput(doc *nodedoc.Document, memoryRef, targetLoc string, createOnly bool) (*gen.NodeInput, error) {
	input := &gen.NodeInput{
		MemoryId: memoryRef,
		Loc:      targetLoc,
		Name:     doc.Name,
	}
	if doc.Content != "" {
		input.Content = &doc.Content
	}
	if doc.Type != "" {
		input.NodeType = &doc.Type
	}
	if doc.Alias != "" {
		input.Alias = &doc.Alias
	}
	if doc.Description != "" {
		input.Description = &doc.Description
	}
	if doc.Abstract != "" {
		input.Abstract = &doc.Abstract
	}
	if len(doc.Tags) > 0 {
		input.Tags = doc.Tags
	}
	if doc.Seq != nil {
		input.Seq = doc.Seq
	}
	if doc.Data != nil {
		data, err := nodedoc.EncodeJSON(doc.Data)
		if err != nil {
			return nil, exitcode.Newf(exitcode.Usage, "encoding data: %v", err)
		}
		input.Data = data
	}
	if doc.Properties != nil {
		props, err := nodedoc.EncodeJSON(doc.Properties)
		if err != nil {
			return nil, exitcode.Newf(exitcode.Usage, "encoding properties: %v", err)
		}
		input.Properties = props
	}
	if createOnly {
		input.CreateOnly = &createOnly
	}
	return input, nil
}

// nodeExists best-effort probes whether a node already lives at (memory, loc),
// to label the import created vs updated. Any lookup error yields false: the
// upsert that follows is authoritative for real failures (auth/transport), and
// a dry run degrades to "would create".
func nodeExists(cmd *cobra.Command, client graphql.Client, memoryRef, loc string) bool {
	// A URN-addressed memory resolves to an exact node URN in one round-trip.
	if strings.Contains(memoryRef, ":") {
		resp, err := gen.ResolveUrn(cmd.Context(), client, "hrn:node:"+memoryRef+":"+loc)
		return err == nil && resp.ResolveUrn != nil && resp.ResolveUrn.Kind == "node"
	}
	// A raw memory id: list by loc prefix and match the exact loc.
	mem, prefix, limit := memoryRef, loc, 200
	for offset := 0; ; offset += limit {
		off := offset
		resp, err := gen.Nodes(cmd.Context(), client, &mem, &prefix, nil, nil, nil, &limit, &off)
		if err != nil {
			return false
		}
		for _, nd := range resp.Nodes {
			if nd != nil && nd.Loc == loc {
				return true
			}
		}
		if len(resp.Nodes) < limit {
			return false
		}
	}
}

// emitImportSummary writes the --json DTO or the human line. dryRun and
// withEdges/fileEdges tune the human hint (would-do vs did; the unwired-edges
// note vs the "re-run with --with-edges" nudge).
func emitImportSummary(f *cmdutil.Factory, s importNodeSummaryDTO, dryRun, withEdges bool, fileEdges int) error {
	return output.Write(f.IOStreams, f.JSON, s, func(w io.Writer) error {
		if dryRun {
			verb := "create"
			if s.Action == "updated" {
				verb = "update"
			}
			fmt.Fprintf(w, "[dry-run] would %s %s:%s\n", verb, s.Memory, s.Loc)
		} else {
			fmt.Fprintf(w, "✓ %s %s:%s\n", s.Action, s.Memory, s.Loc)
		}
		switch {
		case withEdges:
			fmt.Fprintf(w, "  wired %d edge(s)\n", s.EdgesWired)
			if len(s.UnwiredEdges) > 0 {
				fmt.Fprintf(w, "  %d edge(s) had unresolved targets: %s\n", len(s.UnwiredEdges), strings.Join(s.UnwiredEdges, ", "))
			}
		case fileEdges > 0:
			fmt.Fprintf(w, "  %d edge(s) in file — re-run with --with-edges to wire them\n", fileEdges)
		}
		return nil
	})
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
