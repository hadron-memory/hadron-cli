package spec

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// describeDTO is the stable --json shape for `spec describe`: a NEUTRAL
// inventory of a memory's spec corpus (#709).
//
// It used to classify the corpus — a flat/product/mixed "scheme", per-tier
// counts by depth (modules, features, rules, flows), and the reserved contract
// code at each tier — and to read a scheme declared in the memory's data as
// authoritative. All of that was the fixed hierarchy #708/#709 remove, so none
// of it is reported any more. What remains are facts about the stored nodes,
// with no tier assigned to any depth.
type describeDTO struct {
	Memory string `json:"memory"`
	// Specs is how many nodes are specs (the spec tag or the spec role).
	Specs int `json:"specs"`
	// Roots are the distinct first loc segments of those specs, sorted.
	Roots []string `json:"roots"`
	// MaxDepth is the most segments any spec's loc has (0 when there are none).
	MaxDepth int `json:"maxDepth"`
	// LegacyNumbered counts the specs whose loc fits the legacy numbering —
	// what `spec new`'s allocation, `spec register` and `spec extract` still
	// operate on — and OutsideNumbering the rest. Neither is a validity
	// judgment: every spec is valid at any loc (#708).
	LegacyNumbered   int `json:"legacyNumbered"`
	OutsideNumbering int `json:"outsideNumbering"`
	// RetiredDeclaration is a flat/product scheme still stored in the
	// memory's data by the retired `--declare` (#709). It is reported so it
	// isn't mistaken for policy, and it is never read as one; the stored
	// value is left in place.
	RetiredDeclaration string `json:"retiredDeclaration,omitempty"`
}

func newCmdDescribe(f *cmdutil.Factory) *cobra.Command {
	var memory, declare string
	cmd := &cobra.Command{
		Use:     "describe",
		Aliases: []string{"desc"},
		Short:   "Inventory a memory's spec corpus",
		Long: `Inventory a memory's spec corpus: how many specs it holds, their root
segments, the deepest loc, and how many sit in the legacy numbering (which
"spec new"'s allocation, "spec register" and "spec extract" operate on).

Nothing is classified: a spec is valid at any loc, at any depth, and no
tier is assigned to a depth. A memory no longer has a flat or product-rooted
"scheme"; one still stored in the memory's data by the retired --declare is
shown as retired and ignored.`,
		Example: `  hadron spec describe -m hrn:mem:hadronmemory.com:specs
  hadron spec describe -m hrn:mem:micromentor.org:specs --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// #709: retired, and refused BEFORE any request, so an old
			// invocation can't silently succeed or write the memory's data.
			if cmd.Flags().Changed("declare") {
				return exitcode.Newf(exitcode.Usage,
					"--declare is retired (#709): a memory no longer declares a flat or product-rooted spec scheme, and nothing reads one. Nothing was written")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}

			memID, memURN, err := specMemoryID(f, cmd, client, memory)
			if err != nil {
				return err
			}
			memResp, err := gen.GetMemory(cmd.Context(), client, memID)
			if err != nil {
				return api.MapError(err)
			}
			var data *json.RawMessage
			if memResp.Memory != nil {
				data = memResp.Memory.Data
			}

			all, err := scanAllNodes(cmd.Context(), client, &memURN, nil, nil)
			if err != nil {
				return err
			}
			var locs []string
			for _, n := range all {
				if n != nil && isSpec(n.Tags, n.Role) {
					locs = append(locs, n.Loc)
				}
			}
			dto := describeInventory(memURN, locs)
			dto.RetiredDeclaration = schemeFromData(data)

			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				return renderDescribe(w, dto)
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory ID or fully-qualified URN (defaults to the memory set by hadron spec use, then the active memory)")
	// Kept only so an old invocation is refused with an explanation rather
	// than "unknown flag" (#709). Hidden: it does nothing.
	cmd.Flags().StringVar(&declare, "declare", "", "retired (#709): refused, writes nothing")
	_ = cmd.Flags().MarkHidden("declare")
	return cmd
}

// schemeFromData extracts the retired data.spec.scheme; "" if absent or
// unparseable. It is read only to DISCLOSE a stale declaration (#709), never
// as policy.
func schemeFromData(data *json.RawMessage) string {
	if data == nil || len(*data) == 0 {
		return ""
	}
	var d struct {
		Spec struct {
			Scheme string `json:"scheme"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(*data, &d); err != nil {
		return ""
	}
	return d.Spec.Scheme
}

// describeInventory builds the neutral inventory of the given spec locs.
func describeInventory(memURN string, locs []string) describeDTO {
	dto := describeDTO{Memory: memURN, Roots: []string{}}
	roots := map[string]bool{}
	for _, loc := range locs {
		dto.Specs++
		segs := strings.Split(loc, ":")
		roots[segs[0]] = true
		if len(segs) > dto.MaxDepth {
			dto.MaxDepth = len(segs)
		}
		if _, err := ParseCitation(loc); err == nil {
			dto.LegacyNumbered++
		} else {
			dto.OutsideNumbering++
		}
	}
	for r := range roots {
		dto.Roots = append(dto.Roots, r)
	}
	sort.Strings(dto.Roots)
	return dto
}

func renderDescribe(w io.Writer, d describeDTO) error {
	fmt.Fprintf(w, "Spec corpus — %s\n", d.Memory)
	if d.Specs == 0 {
		fmt.Fprintln(w, "  no specs yet — create one with `hadron spec new <loc> --title <title>`")
	} else {
		fmt.Fprintf(w, "  specs:     %d  (deepest loc: %d segments)\n", d.Specs, d.MaxDepth)
		fmt.Fprintf(w, "  roots:     %s\n", strings.Join(d.Roots, ", "))
		fmt.Fprintf(w, "  numbering: %d in the legacy numbering, %d outside it\n", d.LegacyNumbered, d.OutsideNumbering)
	}
	if d.RetiredDeclaration != "" {
		fmt.Fprintf(w, "  note:      this memory's data still declares a %q scheme; --declare is retired and nothing reads it\n", d.RetiredDeclaration)
	}
	return nil
}
