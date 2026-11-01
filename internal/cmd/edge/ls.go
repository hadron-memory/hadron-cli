package edge

import (
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// edgeListDTO is one row in `edge list` output.
type edgeListDTO struct {
	ID         string `json:"id"`
	Direction  string `json:"direction"` // outgoing | incoming
	Name       string `json:"name"`
	Loc        string `json:"loc"`
	IsRunnable bool   `json:"isRunnable"`
	Priority   int    `json:"priority"`
	OtherID    string `json:"otherNodeId"`
	OtherLoc   string `json:"otherNodeLoc"`
}

func edgeListRow(id, dir string, name *string, loc string, isRunnable *bool, priority int, otherID, otherLoc string) edgeListDTO {
	n := ""
	if name != nil {
		n = *name
	}
	run := false
	if isRunnable != nil {
		run = *isRunnable
	}
	return edgeListDTO{
		ID: id, Direction: dir, Name: n, Loc: loc, IsRunnable: run, Priority: priority,
		OtherID: otherID, OtherLoc: otherLoc,
	}
}

// edgeFilter narrows a node's edges. The zero value matches everything, so an
// unfiltered `edge list` is unchanged.
//
// To and From are directional by construction: an edge TO x is one this node
// points at (outgoing), an edge FROM x is one pointing at this node (incoming).
// Each matches the far endpoint by loc OR id, since both are printed in --json
// and either is what a caller has to hand.
type edgeFilter struct {
	Direction string // "" | incoming | outgoing
	Name      string // case-insensitive substring of the edge label
	To        string // far endpoint loc or id, outgoing only
	From      string // far endpoint loc or id, incoming only
}

func (fl edgeFilter) match(e edgeListDTO) bool {
	fl.Name, fl.To, fl.From = strings.TrimSpace(fl.Name), strings.TrimSpace(fl.To), strings.TrimSpace(fl.From)
	if fl.Direction != "" && e.Direction != fl.Direction {
		return false
	}
	if fl.Name != "" && !strings.Contains(strings.ToLower(e.Name), strings.ToLower(fl.Name)) {
		return false
	}
	if fl.To != "" && (e.Direction != "outgoing" || !endpointIs(e, fl.To)) {
		return false
	}
	if fl.From != "" && (e.Direction != "incoming" || !endpointIs(e, fl.From)) {
		return false
	}
	return true
}

// endpointIs reports whether the far endpoint is ref, by loc or by id. A
// redacted endpoint (#781) carries neither, so it never matches — correct: the
// caller asked for a specific node and this edge can't be shown to be it.
func endpointIs(e edgeListDTO, ref string) bool {
	return (e.OtherLoc != "" && e.OtherLoc == ref) || (e.OtherID != "" && e.OtherID == ref)
}

// filterEdges applies fl, always returning a non-nil slice so --json renders
// [] rather than null.
func filterEdges(edges []edgeListDTO, fl edgeFilter) []edgeListDTO {
	out := []edgeListDTO{}
	for _, e := range edges {
		if fl.match(e) {
			out = append(out, e)
		}
	}
	return out
}

// validate rejects filters that can only ever match nothing, or that would
// silently widen the result instead of narrowing it.
//
// provided names the flags the user actually passed, which an empty value
// alone can't tell us: `--to "$TARGET"` with TARGET unset reaches us as an
// explicitly-supplied "". Treating that as "no filter" returned EVERY edge —
// the opposite of what the caller asked for, and silent. It is a usage error.
func (fl edgeFilter) validate(provided map[string]bool) error {
	for flag, val := range map[string]string{
		"direction": fl.Direction, "name": fl.Name, "to": fl.To, "from": fl.From,
	} {
		if provided[flag] && strings.TrimSpace(val) == "" {
			return exitcode.Newf(exitcode.Usage,
				"--%s was given an empty value — omit the flag to not filter on it (a shell variable that expanded to nothing?)", flag)
		}
	}
	switch fl.Direction {
	case "", "incoming", "outgoing":
	default:
		return exitcode.Newf(exitcode.Usage, "--direction %q must be \"incoming\" or \"outgoing\"", fl.Direction)
	}
	if fl.To != "" && fl.From != "" {
		return exitcode.Newf(exitcode.Usage, "--to and --from are mutually exclusive — an edge has one far endpoint, and the two name opposite directions")
	}
	if fl.To != "" && fl.Direction == "incoming" {
		return exitcode.Newf(exitcode.Usage, "--to selects outgoing edges, so it cannot be combined with --direction incoming")
	}
	if fl.From != "" && fl.Direction == "outgoing" {
		return exitcode.Newf(exitcode.Usage, "--from selects incoming edges, so it cannot be combined with --direction outgoing")
	}
	return nil
}

func newCmdLs(f *cmdutil.Factory) *cobra.Command {
	var memory string
	var fl edgeFilter
	var u uniformListFlags
	cmd := &cobra.Command{
		Use:     "list <node-urn> | <loc> -m <memory> | [-m <memory>] [--source <node>] [--target <node>]",
		Aliases: []string{"ls"},
		Short:   "List a node's edges, or every edge you can read",
		Long: `List a node's edges: the links it makes (outgoing) and the links other
nodes make to it (incoming). Name the node by its node URN, or by a bare loc
with -m/--memory.

Each row shows the direction (→ outgoing, ← incoming), the relationship name
(the edge's loc when it has none), the node at the other end, and the edge id
that edge update and edge rm take:

  DIR  REL        NODE               EDGE-ID
  ←    routes-to  findings:flaky-ci  <edge-id>

A node at the other end that lives in a memory you cannot read is shown
without its loc.

Without filters every edge is listed. --direction, --name, --to and --from
only narrow that list, so a filtered --json run is a subset of an unfiltered
one. --to and --from name the node at the other end, by its loc or id: --to
keeps the links this node makes to it, --from the links it makes to this node.

Without a node, edge list lists every edge you can read, in loc order, with
the node at each end and the edge's memory being the source node's:

  FROM               REL        TO          EDGE-ID
  findings:flaky-ci  routes-to  start-here  <edge-id>

-m keeps one memory's edges, --source the edges leaving a node, --target the
edges arriving at one (a node id or node URN; a bare loc is refused),
--runnable the edges marked runnable, --runnable=false those explicitly
marked not runnable (an edge never marked either way matches neither),
--include-deleted adds deleted ones, and --org one organization's memories.
A memory, node or organization you cannot read, or that does not exist, gives
an empty list rather than "not found". Every edge is
listed unless --limit or --offset asks for one page; --json prints
{total, edges}. --direction, --name, --to and --from need a node.`,
		Example: `  hadron edge list hrn:node:hadronmemory.com:dev:start-here
  hadron edge list start-here -m hrn:mem:hadronmemory.com:dev
  hadron edge list review -m hrn:mem:micromentor.org:mmdata --direction incoming
  hadron edge list preflight -m hrn:mem:hadronmemory.com:dev --name routes-to
  hadron edge list preflight -m hrn:mem:hadronmemory.com:dev --to findings:prisma-upsert-not-race-safe
  hadron edge list -m hrn:mem:hadronmemory.com:dev --json
  hadron edge list --target hrn:node:hadronmemory.com:dev:preflight`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			wrong, form := nodeOnly, "need a node: name one, or drop them to list every edge"
			if len(args) == 1 {
				wrong, form = uniformOnly, "list edges without a node, so they cannot be combined with one"
			}
			for _, name := range wrong {
				if cmd.Flags().Changed(name) {
					return exitcode.Newf(exitcode.Usage, "--%s: --%s %s. Nothing was sent", name, strings.Join(wrong, ", --"), form)
				}
			}
			if len(args) == 0 {
				return listUniformEdges(cmd, f, memory, u)
			}
			provided := map[string]bool{}
			for _, n := range []string{"direction", "name", "to", "from"} {
				provided[n] = cmd.Flags().Changed(n)
			}
			if err := fl.validate(provided); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			id, err := cmdutil.ResolveNodeRef(cmd, client, memory, args[0])
			if err != nil {
				return err
			}
			resp, err := gen.GetNode(cmd.Context(), client, id)
			if err != nil {
				return api.MapError(err)
			}
			if resp.Node == nil {
				return exitcode.Newf(exitcode.NotFound, "node %q not found", args[0])
			}

			edges := []edgeListDTO{}
			for _, e := range resp.Node.OutgoingEdges {
				// #781: target is null when its memory is unreadable to the
				// caller (a cross-memory edge's far endpoint) — leave the node
				// id/loc blank rather than dereferencing nil.
				tid, tloc := "", ""
				if e.Target != nil {
					tid, tloc = e.Target.Id, e.Target.Loc
				}
				edges = append(edges, edgeListRow(e.Id, "outgoing", e.Name, e.Loc, e.IsRunnable, e.Priority, tid, tloc))
			}
			for _, e := range resp.Node.IncomingEdges {
				sid, sloc := "", ""
				if e.Source != nil {
					sid, sloc = e.Source.Id, e.Source.Loc
				}
				edges = append(edges, edgeListRow(e.Id, "incoming", e.Name, e.Loc, e.IsRunnable, e.Priority, sid, sloc))
			}

			edges = filterEdges(edges, fl)

			return output.Write(f.IOStreams, f.JSON, edges, func(w io.Writer) error {
				t := output.NewTable(w, "DIR", "REL", "NODE", "EDGE-ID")
				for _, e := range edges {
					arrow := "→"
					if e.Direction == "incoming" {
						arrow = "←"
					}
					rel := e.Name
					if rel == "" {
						rel = e.Loc
					}
					t.Row(arrow, output.SafeCell(rel), output.SafeCell(e.OtherLoc), output.SafeCell(e.ID))
				}
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory (hrn:mem:<root>:<slug>) to resolve a bare <loc> against; without a node, only that memory's edges")
	cmd.Flags().StringVar(&u.source, "source", "", "without a node: only edges leaving this node (id or URN)")
	cmd.Flags().StringVar(&u.target, "target", "", "without a node: only edges arriving at this node (id or URN)")
	cmd.Flags().BoolVar(&u.runnable, "runnable", false, "without a node: only edges marked runnable (--runnable=false: only those explicitly marked not runnable)")
	cmd.Flags().BoolVar(&u.includeDeleted, "include-deleted", false, "without a node: include deleted edges")
	cmd.Flags().StringVar(&u.org, "org", "", "without a node: only edges in this organization's memories")
	cmd.Flags().IntVar(&u.limit, "limit", 0, "without a node: fetch one page of at most N edges (default: all)")
	cmd.Flags().IntVar(&u.offset, "offset", 0, "without a node: pagination offset (implies one page)")
	cmd.Flags().StringVar(&fl.Direction, "direction", "", `only "incoming" or only "outgoing" edges`)
	cmd.Flags().StringVar(&fl.Name, "name", "", "only edges whose relationship name contains this (case-insensitive)")
	cmd.Flags().StringVar(&fl.To, "to", "", "only outgoing edges whose target is this loc or id")
	cmd.Flags().StringVar(&fl.From, "from", "", "only incoming edges whose source is this loc or id")
	return cmd
}
