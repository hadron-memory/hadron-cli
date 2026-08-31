package coding

import (
	"context"
	"io"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// `route` is `create`'s other half. `create` mints the node AND wires it;
// this wires a node that already exists — which is the commoner case for a
// router, because the thing worth routing to is usually a finding or a task
// somebody already wrote, frequently in ANOTHER memory (dev's router points
// into hadron-server, hadron-portal and core).
//
// Doing that by hand is three separate writes, and the third — the body line —
// is the one people forget, which is precisely the half-wired route the group
// exists to detect.

func newCmdPreflightRoute(f *cmdutil.Factory) *cobra.Command {
	var (
		memory, root string
		route        string
		description  string
		symptom      string
		section      string
		noBackEdge   bool
		noBodyLine   bool
		dryRun       bool
	)
	cmd := &cobra.Command{
		Use:   "route <node-ref>",
		Short: "Route the preflight router at a node that already exists",
		Long: `Wire a preflight route to an EXISTING node.

` + "`create`" + ` mints a node and routes to it; ` + "`route`" + ` takes a node that is
already there. It writes the same three things, because a node is reachable
only when it has all of them: the router's outgoing edge, the mirrored
back-edge, and the routing line in the router's BODY — which is what a human
or an LLM reading the router actually scans.

<node-ref> is a bare loc in -m's memory, or a fully-qualified URN/id anywhere.
Cross-memory routes are normal and supported: the thing worth routing to often
lives in another memory. The routing line then references it as
` + "`<loc>` in `<memory>`" + ` rather than a bare ` + "`[[loc]]`" + `, because a
wikilink resolves against the ROUTER's memory and would go nowhere.

--description supplies the routing line's text; omit it and the target node's
own description is used, which is usually what you want.

Re-running is safe: an edge that already exists is not duplicated, and a body
that already references the target is left alone.`,
		Example: `  hadron coding preflight route findings:flaky-otp-timer -m hrn:mem:acme.com:kb \
    --route "fix a flaky OTP countdown test"
  hadron coding preflight route hrn:node:acme.com:specs:tasks:create-platform-spec \
    -m hrn:mem:acme.com:dev --route "add or change a spec in the law corpus" \
    --section "Maintaining the memories themselves" --dry-run`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mem, err := codingMemoryURN(memory)
			if err != nil {
				return err
			}
			label, err := normalizeRoute(route)
			if err != nil {
				return err
			}
			if noBodyLine && cmd.Flags().Changed("section") {
				return exitcode.Newf(exitcode.Usage, "--section and --no-body-line are mutually exclusive")
			}

			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			ctx := cmd.Context()

			routerRef, err := mem.nodeRef(root)
			if err != nil {
				return err
			}
			router, err := gen.GetNode(ctx, client, routerRef)
			if err != nil {
				return api.MapError(err)
			}
			if router.Node == nil {
				return exitcode.Newf(exitcode.NotFound,
					"no %q node in %s — create the router first, or pass --root", root, mem.raw)
			}

			// A qualified ref resolves WITHOUT -m: ResolveNodeRef reads its ref
			// as a bare loc whenever a memory is given, which would compose a
			// cross-memory URN into this memory and resolve to nothing.
			targetMemory := memory
			if cmdutil.IsQualifiedNodeRef(args[0]) {
				targetMemory = ""
			}
			targetID, err := cmdutil.ResolveNodeRef(cmd, client, targetMemory, args[0])
			if err != nil {
				return err
			}
			if targetID == router.Node.Id {
				return exitcode.Newf(exitcode.Usage,
					"%q is the router itself — a route must lead somewhere else", args[0])
			}
			// Read the target for its loc, memory and description. This is the
			// difference from `create`: the node is already authored, so its own
			// one-liner is the routing line's natural text.
			target, err := gen.GetNode(ctx, client, targetID)
			if err != nil {
				return api.MapError(err)
			}
			if target.Node == nil {
				return exitcode.Newf(exitcode.NotFound, "node %q could not be read", args[0])
			}

			text := strings.TrimSpace(description)
			if text == "" && target.Node.Description != nil {
				text = strings.TrimSpace(*target.Node.Description)
			}
			if text == "" {
				return exitcode.Newf(exitcode.Usage,
					"%s has no description, so the routing line would have no text — pass --description", target.Node.Loc)
			}

			// Cross-memory targets cannot use a bare wikilink (cor:urn:020:01).
			link := wikiLink(target.Node.Loc)
			if target.Node.MemoryId != router.Node.MemoryId {
				link = crossMemoryLink(target.Node.Loc, memoryLabel(ctx, client, target.Node.MemoryId))
			}
			line := routingLineTo(routeSymptom(symptom, label), link, text)

			plan := routingPlan{Skipped: true}
			if !noBodyLine {
				if plan, err = planRoutingLine(routerBody(router.Node.Content), section, line, link); err != nil {
					return err
				}
			}

			dto := newRouteDTO{
				Loc: target.Node.Loc, ID: target.Node.Id, Name: target.Node.Name,
				Route: label, Tags: target.Node.Tags, Seq: target.Node.Seq,
				Router: root, BackEdge: !noBackEdge, Links: []linkDTO{}, DryRun: dryRun,
			}
			if dto.Tags == nil {
				dto.Tags = []string{}
			}
			// BodyLine/Section describe the line ACTUALLY written, so they are
			// filled from the plan only for a rehearsal, and otherwise only
			// once appendRoutingLine has landed. Populating them up front made
			// a partial write report a body repair that had not happened.
			if dryRun && !plan.Skipped {
				dto.BodyLine, dto.Section = line, plan.Section
			}

			// Already-routed is reported, not re-written: createEdge is
			// idempotent on the derived loc, but saying so beats a silent
			// no-op. The LABEL has to match too — a differently-labelled edge
			// to the same node is a DIFFERENT route, and naming its id here
			// would report the wrong edge while createEdge went on to add a
			// second one.
			for _, e := range router.Node.OutgoingEdges {
				if e != nil && e.Target != nil && e.Target.Id == targetID &&
					e.Name != nil && *e.Name == label {
					dto.EdgeID = e.Id
				}
			}

			if dryRun {
				return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
					return renderRoutePlan(w, dto, false)
				})
			}

			fwd, err := gen.CreateEdge(ctx, client, router.Node.Id, targetID, label, nil, nil, nil, nil, nil, nil)
			if err != nil {
				// Classify through api.MapError so auth, not-found and
				// transport keep their own exit codes — a script must be able
				// to tell a retryable outage from an ordinary failure. The
				// wording stays agnostic about whether the edge landed:
				// a transport failure has an UNKNOWN outcome.
				mapped := api.MapError(err)
				return exitcode.Newf(exitcode.FromError(mapped),
					"the route from %q to %s may not have been written — check with `hadron coding preflight list -m %s`: %v",
					root, dto.Loc, mem.raw, mapped)
			}
			if fwd.CreateEdge != nil {
				dto.EdgeID = fwd.CreateEdge.Id
			}

			if !noBackEdge {
				if _, err := gen.CreateEdge(ctx, client, targetID, router.Node.Id, label,
					nil, nil, nil, nil, nil, nil); err != nil {
					dto.BackEdge = false
					return partialRoute(f, dto, false, err,
						"routed %s, but its back-edge to %q did not land — the route only works from the index; add it with `hadron edge create --from %s --to %s --name %q`",
						dto.Loc, root, dto.Loc, root, label)
				}
			}

			if !noBodyLine {
				written, err := appendRoutingLine(ctx, client, routerRef, section, line, link)
				if err != nil {
					return partialRoute(f, dto, false, err,
						"routed %s, but the router's body was not updated — the route is invisible to anyone READING %q; add this line yourself:\n  %s",
						dto.Loc, root, line)
				}
				if !written.Skipped {
					dto.BodyLine, dto.Section = line, written.Section
				}
			}

			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				return renderRoutePlan(w, dto, false)
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory holding the router, hrn:mem:<root>:<slug> (required)")
	cmd.Flags().StringVar(&root, "root", preflightRootLoc, "loc of the preflight router node")
	cmd.Flags().StringVar(&route, "route", "", `the action the route fires on ("to" is prepended if absent)`)
	cmd.Flags().StringVar(&description, "description", "", "routing line text (default: the target node's own description)")
	cmd.Flags().StringVar(&symptom, "symptom", "", "the routing line's quoted trigger (default: the route)")
	cmd.Flags().StringVar(&section, "section", "", "heading in the router's body to add the routing line under")
	cmd.Flags().BoolVar(&noBackEdge, "no-back-edge", false, "skip the mirrored back-edge to the router")
	cmd.Flags().BoolVar(&noBodyLine, "no-body-line", false, "wire the edges only; leave the router's body alone")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be written, including where the routing line lands, without writing it")
	_ = cmd.MarkFlagRequired("route")
	// -m is required on every command in this group (hadron-cli#533):
	// codingMemoryURN refuses an empty one and there is no fallback, so marking
	// it lets cobra report it alongside the other missing flags in one message.
	_ = cmd.MarkFlagRequired("memory")
	return cmd
}

// memoryLabel is what the routing line calls a cross-memory target's memory.
//
// It is LOOKED UP, not parsed out of the ref the caller typed. An earlier
// version split the ref itself and got two spellings wrong: a prefixed legacy
// ref (`hrn:node:acme.com::specs::tasks:x`) split on single colons to an empty
// atom and emitted `hrn:mem:acme.com:`, and the `urn:node:` scheme the CLI also
// accepts fell through to the opaque id. Since input is Postel-liberal by
// design, every accepted spelling would have to be handled — whereas the server
// already knows the answer and emits it canonically.
//
// Decoration, never a gate: a memory the caller cannot read falls back to the
// id rather than failing the command, because the route itself is fine.
func memoryLabel(ctx context.Context, client graphql.Client, memoryID string) string {
	resp, err := gen.GetMemory(ctx, client, memoryID)
	if err != nil || resp.Memory == nil || resp.Memory.Urn == "" {
		return memoryID
	}
	return resp.Memory.Urn
}
