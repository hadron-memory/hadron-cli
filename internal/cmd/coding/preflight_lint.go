package coding

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// routePrefix is preflight's labelling convention: routes read as an action,
// "to do X …", so the router scans like a table of contents.
const routePrefix = "to "

// retiredTags mark a node that should no longer be routed to.
var retiredTags = []string{"retired", "superseded", "tombstone", "deprecated"}

// preflightInput is everything the preflight rule engine needs.
type preflightInput struct {
	Model       routeModel           // mirrored reads Routes only; leaf also reads Leaves (cli#1025)
	Routes      []graphEdge          // the router's OUTGOING edges (the legacy routes under the leaf model)
	Leaves      []graphEdge          // leaf model: edges INTO the router, one per leaf-owned route
	RouterBody  string               // leaf model: the router's body, scanned for [[links]] with no leaf edge
	Targets     map[string]checkNode // resolved route targets, by loc
	Unavailable []string             // route targets that could not be read
	HomeMemory  string               // the preflight node's own memory id
}

func newCmdPreflightLint(f *cmdutil.Factory) *cobra.Command {
	var memory, root, routeModelV string
	var strict bool
	cmd := &cobra.Command{
		Use:     "lint",
		Aliases: []string{"check", "validate"},
		Short:   "Validate the preflight router's outgoing routes",
		Long: `Validate every route out of the preflight node.

preflight routes symptom → finding along its outgoing edges, so a route to
a node that no longer resolves sends a reader nowhere — stale routing is
worse than missing routing. That is the one error here; the labelling and
lifecycle conventions are warnings.

Errors exit 5; --strict promotes warnings to errors too.

Under --route-model leaf (or preflightRouteModel in .hadron/config.json) the
routes are the edges INTO the router, one per leaf, and the same conventions
apply to them: the label, a retired leaf, a leaf in another memory. A route
OUT of the router is a legacy route still to migrate (legacy-outgoing-route),
and a [[link]] in the router's own body to a node with no edge to the router
is leaf-missing-edge (area-node bodies are not scanned). Edges from memories
you cannot read are invisible, so lint prints how many incoming edges it saw
and never claims completeness. The default, mirrored, is unchanged.`,
		Example: `  hadron coding preflight lint -m hrn:mem:micromentor.org:mmdata
  hadron coding preflight lint -m hrn:mem:hadronmemory.com:dev --json --strict`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			mem, err := codingScope(cmd, f, memory)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			ctx := cmd.Context()

			model, err := resolveRouteModel(cmd, routeModelV)
			if err != nil {
				return err
			}
			in, err := collectPreflight(ctx, client, mem, root, model)
			if err != nil {
				return err
			}
			findings := lintPreflight(in)
			if strict {
				for i := range findings {
					if findings[i].Severity == sevWarning {
						findings[i].Severity = sevError
					}
				}
			}
			hasError := false
			for _, fd := range findings {
				if fd.Severity == sevError {
					hasError = true
					break
				}
			}

			if err := output.Write(f.IOStreams, f.JSON, findings, func(w io.Writer) error {
				// The note rides the human branch only: --json stays a bare array.
				note := func() {
					if in.Model == modelLeaf {
						fmt.Fprintf(w, "note: %d incoming edges visible to this caller; edges from memories you cannot read are not counted\n", len(in.Leaves))
					}
				}
				if len(findings) == 0 {
					fmt.Fprintf(w, "✓ %d route(s) OK\n", len(routeRows(in, findings)))
					note()
					return nil
				}
				t := output.NewTable(w, "ROUTE", "SEVERITY", "RULE", "MESSAGE")
				for _, fd := range findings {
					t.Row(output.SafeCell(fd.Node), output.SafeCell(fd.Severity), output.SafeCell(fd.Rule), output.SafeCell(fd.Message))
				}
				if err := t.Flush(); err != nil {
					return err
				}
				note()
				return nil
			}); err != nil {
				return err
			}
			if hasError {
				return exitcode.Silent(exitcode.Conflict)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory to lint (defaults to this repository's)")
	cmd.Flags().StringVar(&root, "root", preflightRootLoc, "loc of the preflight router node")
	cmd.Flags().BoolVar(&strict, "strict", false, "treat warnings as errors")
	addRouteModelFlag(cmd, &routeModelV)
	// -m is OPTIONAL since #551, and the comment #533 left here is why this one
	// replaces it rather than sitting beside it: it said "there is no fallback",
	// which was true when written and is the exact sentence the fallback
	// falsifies. There are now four sources (flag, .hadron/config.json, the
	// configured memory, the repository name), and codingScope reports which one
	// answered.
	return cmd
}

// lintPreflight is the pure rule engine over the router's outgoing routes.
func lintPreflight(in preflightInput) []findingDTO {
	out := []findingDTO{}
	unavailable := map[string]bool{}
	for _, u := range in.Unavailable {
		unavailable[u] = true
	}

	routes := append([]graphEdge(nil), in.Routes...)
	sort.SliceStable(routes, func(i, j int) bool { return routes[i].Other < routes[j].Other })

	reported := map[string]bool{}
	for _, r := range routes {
		// A redacted endpoint projection leaves no loc at all. Skipping it
		// would make the command's only error rule blind to exactly the
		// unreadable-target case it exists for, and let it report a clean
		// router.
		if r.Other == "" {
			name := r.endpointName()
			if reported[name] {
				continue
			}
			reported[name] = true
			out = append(out, findingDTO{name, "route-target-resolves", sevError,
				"route target could not be read — the route sends a reader nowhere"})
			continue
		}
		if unavailable[r.Other] {
			if reported[r.Other] {
				continue
			}
			reported[r.Other] = true
			out = append(out, findingDTO{r.Other, "route-target-resolves", sevError,
				"route target could not be read — the route sends a reader nowhere"})
			continue // no point checking conventions on a node we can't see
		}
		label := strings.TrimSpace(r.Label)
		switch {
		case label == "":
			out = append(out, findingDTO{r.Other, "route-label-phrasing", sevWarning,
				"route has no label — preflight scans by label, so this route is unreadable"})
		case !strings.HasPrefix(strings.ToLower(label), routePrefix):
			out = append(out, findingDTO{r.Other, "route-label-phrasing", sevWarning,
				fmt.Sprintf("route label %q is not action-phrased — expected it to start %q", label, strings.TrimSpace(routePrefix))})
		}
		t, ok := in.Targets[r.Other]
		if !ok {
			continue
		}
		for _, rt := range retiredTags {
			if hasTag(t.Tags, rt) {
				out = append(out, findingDTO{r.Other, "route-target-retired", sevWarning,
					fmt.Sprintf("route target is tagged %q — retired nodes should not be routed to", rt)})
				break
			}
		}
		if in.HomeMemory != "" && r.MemoryID != "" && !sameMemory(r.MemoryID, in.HomeMemory) {
			out = append(out, findingDTO{r.Other, "route-target-moved-memory", sevWarning,
				fmt.Sprintf("route target lives in %s, not the router's own memory", r.MemoryID)})
		}
	}

	if in.Model == modelLeaf {
		out = append(out, lintLeafModel(in, unavailable)...)
	}

	// A leaf that is also a legacy route is read from both ends; say each
	// finding once.
	seen := map[findingDTO]bool{}
	deduped := out[:0]
	for _, fd := range out {
		if !seen[fd] {
			seen[fd] = true
			deduped = append(deduped, fd)
		}
	}
	out = deduped

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Node != out[j].Node {
			return out[i].Node < out[j].Node
		}
		return out[i].Rule < out[j].Rule
	})
	return out
}

// lintLeafModel is what the leaf model adds (cli#1025): the conventions carried
// over to leaf-owned incoming edges, plus the two rules that only exist there.
// route-target-resolves does not carry: an incoming edge cannot dangle, because
// deleting its leaf deletes the edge.
func lintLeafModel(in preflightInput, unavailable map[string]bool) []findingDTO {
	out := []findingDTO{}
	linked := map[string]bool{} // locs carrying an edge TO the router
	for _, l := range in.Leaves {
		linked[l.Other] = true
		if unavailable[l.Other] {
			out = append(out, findingDTO{l.Other, "route-source-unreadable", sevWarning,
				"this node routes to the router but could not be read"})
			continue
		}
		label := strings.TrimSpace(l.Label)
		switch {
		case label == "":
			out = append(out, findingDTO{l.Other, "route-label-phrasing", sevWarning,
				"edge to the router has no label — preflight scans by label, so this route is unreadable"})
		case !strings.HasPrefix(strings.ToLower(label), routePrefix):
			out = append(out, findingDTO{l.Other, "route-label-phrasing", sevWarning,
				fmt.Sprintf("edge label %q is not action-phrased — expected it to start %q", label, strings.TrimSpace(routePrefix))})
		}
		if t, ok := in.Targets[l.Other]; ok {
			for _, rt := range retiredTags {
				if hasTag(t.Tags, rt) {
					out = append(out, findingDTO{l.Other, "route-target-retired", sevWarning,
						fmt.Sprintf("route target is tagged %q — retired nodes should not be routed to", rt)})
					break
				}
			}
		}
		if in.HomeMemory != "" && l.MemoryID != "" && !sameMemory(l.MemoryID, in.HomeMemory) {
			out = append(out, findingDTO{l.Other, "route-target-moved-memory", sevWarning,
				fmt.Sprintf("route target lives in %s, not the router's own memory", l.MemoryID)})
		}
	}
	for _, r := range in.Routes {
		out = append(out, findingDTO{r.endpointName(), "legacy-outgoing-route", sevWarning,
			"the router still owns an outgoing route here — the leaf model has the leaf own the edge; run `hadron coding preflight route <this node> --route-model leaf --route "to …"`, then remove the router's edge (`hadron edge rm`)"})
	}
	for _, loc := range bodyWikilinks(in.RouterBody) {
		if linked[loc] {
			continue
		}
		out = append(out, findingDTO{loc, "leaf-missing-edge", sevWarning,
			"the router's body links this node but it carries no edge to the router — a reader of the node never finds the route"})
	}
	return out
}

// sameMemory compares two memory ids from the SAME GraphQL projection — the
// root node's and the endpoint's — so they are directly comparable.
//
// An earlier version compared against the -m flag's canonical ref instead.
// CanonicalMemoryRef emits the flat hrn:mem:<root>:<slug> form, which contains
// no "::", so its "don't guess across spellings" guard matched every time and
// route-target-moved-memory could never fire in a real run. Its unit test
// passed only because it supplied the legacy org::mem spelling by hand.
func sameMemory(endpointMemoryID, rootMemoryID string) bool {
	if endpointMemoryID == "" || rootMemoryID == "" {
		return true // nothing conclusive to compare
	}
	return endpointMemoryID == rootMemoryID
}
