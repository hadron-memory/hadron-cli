package spec

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// linkResultDTO is the --json shape for `spec link`: the cross-reference edge
// it created (or, with --dry-run, would create) between two specs.
type linkResultDTO struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Label    string `json:"label"`
	MemoryID string `json:"memoryId"`
	EdgeID   string `json:"edgeId,omitempty"`
	DryRun   bool   `json:"dryRun"`
}

func newCmdLink(f *cmdutil.Factory) *cobra.Command {
	var (
		memory string
		label  string
		dryRun bool
	)
	cmd := &cobra.Command{
		Use:   "link <from-citation> <to-citation>",
		Short: "Cross-reference one spec from another (a convention-aware edge)",
		Long: `Create a cross-reference edge between two specs in the same corpus,
addressing both by their bare citations rather than the fully-qualified node
URNs ` + "`edge add`" + ` needs.

The corpus convention is that the more specific spec cites the more general
one — a field spec points at the entity it belongs to, a flow at its rule. So
<from> is the specific/citing spec and <to> is the general/cited one: the same
direction ` + "`spec extract`" + ` wires automatically.

Both endpoints must already exist and carry the "spec" tag (reach for
` + "`edge add`" + ` to link arbitrary nodes, or across memories). With no --label, a
sentence-style label is synthesized from the two titles in the corpus
convention ("documents <from> on the <to> entity"); refine it with
` + "`edge update`" + `.`,
		Example: `  hadron spec link cor:dmo:020:04 cor:dmo:060:02 -m hadronmemory.com::specs \
    --label "documents the nodeType field of Node"
  hadron spec link cor:dmo:020:04 cor:dmo:060:02 -m hadronmemory.com::specs --dry-run`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			from, err := ParseCitation(args[0])
			if err != nil {
				return err
			}
			to, err := ParseCitation(args[1])
			if err != nil {
				return err
			}
			if from.Format() == to.Format() {
				return exitcode.Newf(exitcode.Usage, "cannot link a spec to itself (%s)", from.Format())
			}

			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			memURN, err := resolveSpecMemoryURN(cmd, client, memory)
			if err != nil {
				return err
			}

			// Fetch both endpoints: a clear per-endpoint NotFound, the "spec" tag
			// guard, and the names for the default label. Same-corpus is structural
			// — both are resolved under -m, so no cross-memory check is needed.
			fromNode, err := fetchSpecNode(cmd, client, memURN, from.Format())
			if err != nil {
				return err
			}
			if err := requireSpecTag(fromNode.Tags, from.Format()); err != nil {
				return err
			}
			toNode, err := fetchSpecNode(cmd, client, memURN, to.Format())
			if err != nil {
				return err
			}
			if err := requireSpecTag(toNode.Tags, to.Format()); err != nil {
				return err
			}

			if label == "" {
				label = defaultRefLabel(titleFromName(fromNode.Name), toNode.Name)
			}

			result := linkResultDTO{
				From:     from.Format(),
				To:       to.Format(),
				Label:    label,
				MemoryID: memURN,
				DryRun:   dryRun,
			}
			// dry-run and the executed path render the same result; only the
			// write differs, so the create is gated and the output is unified.
			if !dryRun {
				resp, err := gen.CreateEdge(cmd.Context(), client, fromNode.Id, toNode.Id, label, nil, nil, nil, nil, nil, nil)
				if err != nil {
					return api.MapError(err)
				}
				if resp.CreateEdge == nil {
					return exitcode.Newf(exitcode.Error, "createEdge returned no edge")
				}
				result.EdgeID = resp.CreateEdge.Id
			}

			return output.Write(f.IOStreams, f.JSON, result, func(w io.Writer) error {
				return renderLinkResult(w, result)
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory ID or fully-qualified URN (required)")
	cmd.Flags().StringVar(&label, "label", "", "edge label (default: synthesized from the two titles)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the planned edge without writing anything")
	_ = cmd.MarkFlagRequired("memory")
	return cmd
}

// requireSpecTag rejects an endpoint that is not a spec — `spec link` is the
// convention-aware path for spec↔spec cross-refs; `edge add` handles arbitrary
// nodes.
func requireSpecTag(tags []string, loc string) error {
	if !hasTag(tags, "spec") {
		return exitcode.Newf(exitcode.Usage,
			"%s is not a spec (no \"spec\" tag) — use `hadron edge add` for arbitrary nodes", loc)
	}
	return nil
}

func renderLinkResult(w io.Writer, r linkResultDTO) error {
	verb := "✓ linked"
	if r.DryRun {
		verb = "would link"
	}
	fmt.Fprintf(w, "%s %s → %s  (%s)\n", verb, r.From, r.To, r.Label)
	return nil
}
