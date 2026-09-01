package coding

import (
	"fmt"
	"io"
	"sort"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// routeDTO is one row of `coding preflight list` — the --json contract.
//
// MemoryID is the target's own memory id, which is not always the router's: a
// route may legitimately cross memories, and one that did so by accident is
// what `route-target-moved-memory` warns about.
type routeDTO struct {
	Target   string `json:"target"` // the target's loc; empty when the projection was redacted
	TargetID string `json:"targetId"`
	Label    string `json:"label"`
	EdgeID   string `json:"edgeId"`
	MemoryID string `json:"memoryId"`
	Status   string `json:"status"` // ok | broken
}

func newCmdPreflightList(f *cmdutil.Factory) *cobra.Command {
	var memory, root string
	var brokenOnly bool
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List the preflight router's outgoing routes",
		Long: `List every route out of the preflight node, in target order.

preflight is scanned as a table of contents: a reader matches their intent
against the route labels, so this is the router as they see it. A route
whose target cannot be read lists with status ` + "`broken`" + ` — it sends
the reader nowhere.

This is a read-only view: a broken route is a row, not an exit code — a
listing full of them still exits 0. (A usage, auth or not-found error
exits non-zero as everywhere else.) ` + "`coding preflight lint`" + ` is the
command whose exit code reflects findings.`,
		Example: `  hadron coding preflight list -m hrn:mem:hadronmemory.com:dev
  hadron coding preflight list -m hrn:mem:micromentor.org:mmdata --broken --json`,
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
			in, err := collectPreflight(cmd.Context(), client, mem, root)
			if err != nil {
				return err
			}

			rows := routeRows(in, lintPreflight(in))
			if brokenOnly {
				rows = keepBrokenRoutes(rows)
			}

			return output.Write(f.IOStreams, f.JSON, rows, func(w io.Writer) error {
				if len(rows) == 0 {
					fmt.Fprintf(w, "no routes out of %q in %s\n", root, mem.raw)
					return nil
				}
				t := output.NewTable(w, "TARGET", "STATUS", "LABEL")
				for _, r := range rows {
					t.Row(dash(r.Target), r.Status, dash(r.Label))
				}
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory to list, hrn:mem:<root>:<slug> (required)")
	cmd.Flags().StringVar(&root, "root", preflightRootLoc, "loc of the preflight router node")
	cmd.Flags().BoolVar(&brokenOnly, "broken", false, "only routes whose target could not be read")
	// -m is required on every command in this group (hadron-cli#533):
	// codingMemoryURN refuses an empty one and there is no fallback, so marking
	// it lets cobra report it alongside the other missing flags in one message.
	_ = cmd.MarkFlagRequired("memory")
	return cmd
}

// routeRows projects the router's outgoing edges into list rows, ordered by
// target loc — the same order lintPreflight reports in, so the two views line
// up row for row.
func routeRows(in preflightInput, findings []findingDTO) []routeDTO {
	broken := brokenNodes(findings)
	unavailable := map[string]bool{}
	for _, u := range in.Unavailable {
		unavailable[u] = true
	}

	rows := []routeDTO{}
	for _, r := range in.Routes {
		status := statusOK
		// A redacted endpoint has no loc to key a finding by, so it is matched
		// by the same synthetic name the linter reports it under.
		if broken[r.Other] || broken[r.endpointName()] || unavailable[r.Other] || r.Other == "" {
			status = statusBroken
		}
		rows = append(rows, routeDTO{
			Target: r.Other, TargetID: r.OtherID, Label: r.Label,
			EdgeID: r.ID, MemoryID: r.MemoryID, Status: status,
		})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Target != rows[j].Target {
			return rows[i].Target < rows[j].Target
		}
		return rows[i].Label < rows[j].Label
	})
	return rows
}

func keepBrokenRoutes(rows []routeDTO) []routeDTO {
	out := []routeDTO{}
	for _, r := range rows {
		if r.Status != statusOK {
			out = append(out, r)
		}
	}
	return out
}
