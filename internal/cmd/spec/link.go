package spec

import (
	"fmt"
	"io"

	"github.com/Khan/genqlient/graphql"
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
	// AlreadyLinked is true when the edge (same target, same label) existed
	// before this run, which wrote nothing; EdgeID is then that edge (#687).
	AlreadyLinked bool `json:"alreadyLinked"`
	DryRun        bool `json:"dryRun"`
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

Both endpoints must already exist and be specs, at any valid loc (the
"spec" tag or the spec/spec.* role family); reach for
` + "`edge add`" + ` to link arbitrary nodes, or across memories. With no --label, a
sentence-style label is synthesized from the two titles in the corpus
convention ("documents <from> on the <to> entity"); refine it with
` + "`edge update`" + `.

Rerunning is safe: when <from> already has this edge (same target, same
label), it reports "already linked" and writes nothing.`,
		Example: `  hadron spec link cor:dmo:020:04 cor:dmo:060:02 -m hrn:mem:hadronmemory.com:specs \
    --label "documents the nodeType field of Node"
  hadron spec link cor:dmo:020:04 cor:dmo:060:02 -m hrn:mem:hadronmemory.com:specs --dry-run`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			from, err := validateSpecLoc(args[0])
			if err != nil {
				return err
			}
			to, err := validateSpecLoc(args[1])
			if err != nil {
				return err
			}
			if from == to {
				return exitcode.Newf(exitcode.Usage, "cannot link a spec to itself (%s)", from)
			}

			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			memURN, err := specMemoryURN(f, cmd, client, memory)
			if err != nil {
				return err
			}

			// Fetch both endpoints: a clear per-endpoint NotFound, the "spec" tag
			// guard, and the names for the default label. Same-corpus is structural
			// — both are resolved under -m, so no cross-memory check is needed.
			fromNode, err := fetchSpecLinkEndpoint(cmd, client, memURN, from)
			if err != nil {
				return err
			}
			toNode, err := fetchSpecLinkEndpoint(cmd, client, memURN, to)
			if err != nil {
				return err
			}

			if label == "" {
				label = defaultRefLabel(titleFromName(fromNode.Name), toNode.Name)
			}

			result := linkResultDTO{
				From:     from,
				To:       to,
				Label:    label,
				MemoryID: memURN,
				DryRun:   dryRun,
			}
			// `spec link` is the repair the remedies name (#687), so a re-run
			// must not stack a duplicate: an edge already there is reported,
			// and nothing is written.
			if id, ok := existingLink(fromNode, toNode.Id, label); ok {
				result.EdgeID, result.AlreadyLinked = id, true
			} else if !dryRun {
				// dry-run and the executed path render the same result; only the
				// write differs, so the create is gated and the output is unified.
				resp, err := gen.CreateEdge(cmd.Context(), client, fromNode.Id, toNode.Id, label, nil, nil, nil, nil, nil, nil)
				if err != nil {
					// A concurrent run can create the same edge between the read
					// and the write; the server then refuses the duplicate with an
					// opaque error. Success means the edge exists, so re-read: an
					// edge found there is the link, anything else is the error.
					if again, rerr := fetchSpecNode(cmd, client, memURN, from); rerr == nil {
						if id, ok := existingLink(again, toNode.Id, label); ok {
							result.EdgeID, result.AlreadyLinked = id, true
							return output.Write(f.IOStreams, f.JSON, result, func(w io.Writer) error {
								return renderLinkResult(w, result)
							})
						}
					}
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
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory ID or fully-qualified URN (defaults to the memory set by hadron spec use, then the active memory)")
	cmd.Flags().StringVar(&label, "label", "", "edge label (default: synthesized from the two titles)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the planned edge without writing anything")
	return cmd
}

// requireSpec rejects an endpoint that is not a spec (isSpec) — `spec link` is the
// convention-aware path for spec↔spec cross-refs; `edge add` handles arbitrary
// nodes.
func requireSpec(tags []string, role *string, loc string) error {
	if !isSpec(tags, role) {
		return exitcode.Newf(exitcode.Usage,
			"%s is not a spec (no \"spec\" tag or spec role) — use `hadron edge add` for arbitrary nodes", loc)
	}
	return nil
}

func fetchSpecLinkEndpoint(cmd *cobra.Command, client graphql.Client, memoryURN, loc string) (*gen.GetNodeNode, error) {
	n, err := fetchSpecNode(cmd, client, memoryURN, loc)
	if err != nil {
		return nil, err
	}
	if err := requireSpec(n.Tags, n.Role, n.Loc); err != nil {
		return nil, err
	}
	return n, nil
}

// existingLink finds from's outgoing edge to targetID under label: the same
// edge `spec link` would create.
func existingLink(from *gen.GetNodeNode, targetID, label string) (string, bool) {
	for _, e := range from.OutgoingEdges {
		if e != nil && e.Target != nil && e.Target.Id == targetID && e.Name != nil && *e.Name == label {
			return e.Id, true
		}
	}
	return "", false
}

func renderLinkResult(w io.Writer, r linkResultDTO) error {
	verb := "✓ linked"
	switch {
	case r.AlreadyLinked:
		verb = "✓ already linked"
	case r.DryRun:
		verb = "would link"
	}
	fmt.Fprintf(w, "%s %s → %s  (%s)\n", verb, r.From, r.To, r.Label)
	return nil
}
