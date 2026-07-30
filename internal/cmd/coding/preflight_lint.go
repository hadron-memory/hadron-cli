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
	Routes      []graphEdge
	Targets     map[string]checkNode // resolved route targets, by loc
	Unavailable []string             // route targets that could not be read
	HomeMemory  string               // the preflight node's own memory id
}

func newCmdPreflightLint(f *cmdutil.Factory) *cobra.Command {
	var memory, root string
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

Errors exit 5; --strict promotes warnings to errors too.`,
		Example: `  hadron coding preflight lint -m micromentor.org::mmdata
  hadron coding preflight lint -m hadronmemory.com::dev --json --strict`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			mem, err := codingMemoryURN(memory)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			ctx := cmd.Context()

			// Outgoing, so the far endpoint is the edge's target — the opposite
			// end from the review tree's inbound edges.
			routes, err := fetchRootEdges(ctx, client, mem, root, false)
			if err != nil {
				return err
			}
			locs := make([]string, 0, len(routes))
			seen := map[string]bool{}
			home := ""
			for _, r := range routes {
				if r.Other == "" || seen[r.Other] {
					continue
				}
				seen[r.Other] = true
				locs = append(locs, r.Other)
			}
			sort.Strings(locs)
			targets, unavailable, err := fetchNodes(ctx, client, mem, locs)
			if err != nil {
				return err
			}
			// The router's own memory, to spot a route that left it.
			home = mem.Ref

			findings := lintPreflight(preflightInput{
				Routes: routes, Targets: targets, Unavailable: unavailable, HomeMemory: home,
			})
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
				if len(findings) == 0 {
					fmt.Fprintf(w, "✓ %d route(s) OK\n", len(routes))
					return nil
				}
				t := output.NewTable(w, "ROUTE", "SEVERITY", "RULE", "MESSAGE")
				for _, fd := range findings {
					t.Row(fd.Node, fd.Severity, fd.Rule, fd.Message)
				}
				return t.Flush()
			}); err != nil {
				return err
			}
			if hasError {
				return exitcode.Silent(exitcode.Conflict)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory to lint (org::memory)")
	cmd.Flags().StringVar(&root, "root", preflightRootLoc, "loc of the preflight router node")
	cmd.Flags().BoolVar(&strict, "strict", false, "treat warnings as errors")
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
		if r.Other == "" {
			continue
		}
		if unavailable[r.Other] && !reported[r.Other] {
			reported[r.Other] = true
			out = append(out, findingDTO{r.Other, "route-target-resolves", sevError,
				"route target could not be read — the route sends a reader nowhere"})
			continue // no point checking conventions on a node we can't see
		}
		if unavailable[r.Other] {
			continue
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

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Node != out[j].Node {
			return out[i].Node < out[j].Node
		}
		return out[i].Rule < out[j].Rule
	})
	return out
}

// sameMemory compares a memory id against the URN the command was scoped by.
// The edge carries an opaque memoryId while the scope is an org::memory URN, so
// a mismatch is only conclusive when both are the same kind of string; anything
// else is treated as "same" rather than risking a false positive.
func sameMemory(edgeMemoryID, scope string) bool {
	if edgeMemoryID == scope {
		return true
	}
	// A PK-shaped id can't be compared to a URN-shaped scope; don't guess.
	return !strings.Contains(scope, "::") || !strings.Contains(edgeMemoryID, "::")
}
