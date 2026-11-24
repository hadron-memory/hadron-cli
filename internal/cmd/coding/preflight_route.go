package coding

import (
	"context"
	"encoding/json"
	"errors"
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
		routeModelV  string
		attachTo     string
		crossMemory  bool
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

With ` + "`--route-model leaf`" + ` (or ` + "`preflightRouteModel`" + ` in .hadron/config.json)
the existing node gets ONE edge of its own, to the router or the --attach-to
area node, labelled ` + "`to <action>`" + `: nothing is written on the router's outgoing
set, there is no back-edge, and the body line is off unless --section is given
(it then lands in the attach target). The edge belongs to the node, so a target
in another memory needs write access there; the server decides and a refusal
exits as usual.

<node-ref> is a bare loc in -m's memory, or a fully-qualified URN/id anywhere.
Cross-memory routes are normal and supported: the thing worth routing to often
lives in another memory. The routing line then references it as
` + "`<loc>` in `<memory>`" + ` rather than a bare ` + "`[[loc]]`" + `, because a
wikilink resolves against the ROUTER's memory and would go nowhere.

--description supplies the routing line's text; omit it and the target node's
own description is used, which is usually what you want.

--cross-memory marks the route as an INTENTIONAL cross-memory route: it stores
{"crossMemory": true} in the route edge's data (never in its name), on the route
edge and, under the mirrored model, the back-edge. With --no-back-edge or the
leaf model there is no back-edge, so only the route edge takes the marker. ` + "`preflight lint`" + ` then skips
only ` + "`route-target-moved-memory`" + ` for it, so the warning keeps meaning "this
target moved" for every unmarked route; an unreadable, retired or badly labelled
target is still reported. ` + "`preflight list`" + ` shows the marker. A target in the
router's own memory is refused (there is nothing to mark). Marking an existing
route needs a re-run with the flag, which adds the marker and keeps the edge's
other data.

Re-running is safe: an edge that already exists (same label, same target) is
reported as already routed and is not created again, and a body that already
references the target is left alone; a body that does not is given its routing
line even when both edges exist, and the output says so; a --dry-run says the
same, without writing. A different edge at the same loc is not
that route: the server's refusal ("already occupied", exit 5) is passed through
as it is.

A write that lands only in part exits 1 after printing what was written: a
back-edge that failed or whose cross-memory marker was not confirmed, or a
router body that was not updated. The message says which part is missing.`,
		Example: `  hadron coding preflight route findings:flaky-otp-timer -m hrn:mem:acme.com:kb \
    --route "fix a flaky OTP countdown test"
  hadron coding preflight route hrn:node:acme.com:specs:tasks:create-platform-spec \
    -m hrn:mem:acme.com:dev --route "add or change a spec in the law corpus" \
    --section "Maintaining the memories themselves" --dry-run`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mem, err := codingScope(cmd, f, memory)
			if err != nil {
				return err
			}
			model, err := resolveRouteModel(cmd, routeModelV)
			if err != nil {
				return err
			}
			leaf := model == modelLeaf
			attachLoc, err := attachTarget(root, attachTo, leaf)
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

			routerRef, err := mem.nodeRef(attachLoc)
			if err != nil {
				return err
			}
			// RAW (#736): planned against the stored body appendRoutingLine
			// writes back, not a rendered copy.
			router, err := gen.GetNodeRaw(ctx, client, routerRef)
			if err != nil {
				return api.MapError(err)
			}
			if router.Node == nil {
				return exitcode.Newf(exitcode.NotFound,
					"no %q node in %s — create the router first, or pass --root", attachLoc, mem.raw)
			}

			// A qualified ref resolves WITHOUT a memory: ResolveNodeRef reads
			// its ref as a bare loc whenever one is given, which would compose a
			// cross-memory URN into this memory and resolve to nothing.
			//
			// The RESOLVED memory, not the flag (@codex on #561): with -m
			// optional, a bare `preflight route findings:x` would otherwise be
			// refused as unqualified even though the command had just worked out
			// which memory it lives in.
			targetMemory := mem.raw
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

			if crossMemory && target.Node.MemoryId == router.Node.MemoryId {
				return exitcode.Newf(exitcode.Usage,
					"%s is in the router's own memory, so there is no cross-memory route to mark — drop --cross-memory", target.Node.Loc)
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
				link = crossMemoryLink(target.Node.Loc, memoryLabel(target.Node.MemoryId, target.Node.Memory))
			}
			line := routingLineTo(routeSymptom(symptom, label), link, text)

			wantLine := bodyLineWanted(leaf, noBodyLine, cmd.Flags().Changed("section"))
			plan := routingPlan{Skipped: true}
			if wantLine {
				if plan, err = planRoutingLine(routerBody(router.Node.Content), section, line, link); err != nil {
					return err
				}
			}

			dto := newRouteDTO{
				Loc: target.Node.Loc, ID: target.Node.Id, Name: target.Node.Name,
				Route: label, Tags: target.Node.Tags, Seq: target.Node.Seq,
				Router: attachLoc, BackEdge: !leaf && !noBackEdge, Links: []linkDTO{}, DryRun: dryRun, Model: modelTag(leaf),
				CrossMemory: crossMemory,
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
			// Leaf: the edge lives on the target, so that is the set to look in.
			var existingFwd, existingBack *existingEdge
			if leaf {
				for _, e := range target.Node.OutgoingEdges {
					if e != nil && e.Target != nil && e.Target.Id == router.Node.Id && e.Name != nil && *e.Name == label {
						dto.EdgeID = e.Id
						existingFwd = &existingEdge{ID: e.Id, Data: e.Data}
					}
				}
			} else {
				for _, e := range router.Node.OutgoingEdges {
					if e != nil && e.Target != nil && e.Target.Id == targetID &&
						e.Name != nil && *e.Name == label {
						dto.EdgeID = e.Id
						existingFwd = &existingEdge{ID: e.Id, Data: e.Data}
					}
				}
				for _, e := range target.Node.OutgoingEdges {
					if e != nil && e.Target != nil && e.Target.Id == router.Node.Id && e.Name != nil && *e.Name == label {
						existingBack = &existingEdge{ID: e.Id, Data: e.Data}
					}
				}
			}
			// The reported marker is the edge's state, not only this run's request: a
			// re-run without the flag on a marked route still says so (cli#2736).
			if existingFwd != nil && edgeCrossMemory(existingFwd.Data) {
				dto.CrossMemory = true
			}

			// Already routed: every edge this run would write is on the graph and none
			// needs the marker added, so nothing is written to it (cli#2844).
			// The back-edge belongs to this run only when the mirrored model writes it: under
			// --no-back-edge or the leaf model it is not this run's work, so its marker
			// (or its absence) cannot keep the route from reading as already routed. A
			// --dry-run is judged the same way, so a rehearsal says what the run will.
			needsMarker := func(e *existingEdge) bool { return crossMemory && e != nil && !edgeCrossMemory(e.Data) }
			writesBack := !leaf && !noBackEdge
			if existingFwd != nil && (!writesBack || existingBack != nil) &&
				!needsMarker(existingFwd) && (!writesBack || !needsMarker(existingBack)) {
				dto.AlreadyRouted = true
			}

			if dryRun {
				return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
					return renderRoutePlan(w, dto, false)
				})
			}

			from, to := router.Node.Id, targetID
			fromLoc, toLoc := attachLoc, dto.Loc
			if leaf {
				from, to = targetID, router.Node.Id
				fromLoc, toLoc = dto.Loc, attachLoc
			}
			// An EXISTING edge is never re-created when marking: a real server refuses
			// createEdge on an occupied loc ("Edge loc ... is already occupied", QA on
			// cli#2736), so the edge already on the graph takes the marker through
			// updateEdge. Without --cross-memory the write is exactly what it was.
			fwdID, fwdMarked, err := writeRouteEdge(ctx, client, routeEdge{
				From: from, To: to, FromLoc: fromLoc, ToLoc: toLoc, Label: label,
				Existing: existingFwd, Mark: crossMemory, Memory: mem.raw,
			})
			if err != nil {
				return err
			}
			if fwdID != "" {
				dto.EdgeID = fwdID
			}
			if crossMemory {
				dto.CrossMemory = fwdMarked
			}

			if !leaf && !noBackEdge {
				_, backMarked, err := writeRouteEdge(ctx, client, routeEdge{
					From: targetID, To: router.Node.Id, FromLoc: dto.Loc, ToLoc: attachLoc, Label: label,
					Existing: existingBack, Mark: crossMemory, Memory: mem.raw, Back: true,
				})
				if err != nil {
					var markerErr *markerNotConfirmedError
					if errors.As(err, &markerErr) {
						// The back-edge exists; only its marker is unconfirmed.
						return partialRoute(f, dto, false, err,
							"routed %s and the back-edge to %q exists, but its cross-memory marker was not confirmed: %v",
							dto.Loc, root, err)
					}
					dto.BackEdge = false
					return partialRoute(f, dto, false, err,
						"routed %s, but its back-edge to %q may not have landed (a refusal, or an answer that was lost) — without it the route only works from the index; check with `hadron coding preflight list -m %s`, and if it is missing add it with `hadron edge create -m %s --from %s --to %s --name %s`",
						dto.Loc, root, cmdutil.ShellQuote(mem.raw), cmdutil.ShellQuote(mem.raw), cmdutil.ShellQuote(dto.Loc), cmdutil.ShellQuote(root), cmdutil.ShellQuote(label))
				}
				_ = backMarked
			}

			if wantLine {
				written, err := appendRoutingLine(ctx, client, routerRef, section, line, link)
				if err != nil {
					return partialRoute(f, dto, false, err,
						"routed %s, but the router's body was not updated — the route is invisible to anyone READING %q; add this line yourself:\n  %s",
						dto.Loc, attachLoc, line)
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
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory holding the router (defaults to this repository's)")
	cmd.Flags().StringVar(&root, "root", preflightRootLoc, "loc of the preflight router node")
	cmd.Flags().StringVar(&route, "route", "", `the action the route fires on ("to" is prepended if absent) (required)`)
	cmd.Flags().StringVar(&description, "description", "", "routing line text (default: the target node's own description)")
	cmd.Flags().StringVar(&symptom, "symptom", "", "the routing line's quoted trigger (default: the route)")
	cmd.Flags().StringVar(&section, "section", "", "heading in the router's body to add the routing line under")
	cmd.Flags().BoolVar(&noBackEdge, "no-back-edge", false, "skip the mirrored back-edge to the router")
	cmd.Flags().BoolVar(&noBodyLine, "no-body-line", false, "wire the edges only; leave the router's body alone")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be written, including where the routing line lands, without writing it")
	cmd.Flags().BoolVar(&crossMemory, "cross-memory", false, "mark the route as an intentional cross-memory route, so lint does not warn that its target moved memory")
	addRouteModelFlag(cmd, &routeModelV)
	cmd.Flags().StringVar(&attachTo, "attach-to", "", "leaf model: loc of the router or area node the node's edge points at (default: --root)")
	_ = cmd.MarkFlagRequired("route")
	// -m is OPTIONAL since #551, and the comment #533 left here is why this one
	// replaces it rather than sitting beside it: it said "there is no fallback",
	// which was true when written and is the exact sentence the fallback
	// falsifies. There are now four sources (flag, .hadron/config.json, the
	// configured memory, the repository name), and codingScope reports which one
	// answered.
	return cmd
}

// memoryLabel is what the routing line calls a cross-memory target's memory.
//
// It is READ from the server's answer, not parsed out of the ref the caller
// typed. An earlier version split the ref itself and got two spellings wrong: a
// prefixed legacy ref (`hrn:node:acme.com::specs::tasks:x`) split on single
// colons to an empty atom and emitted `hrn:mem:acme.com:`, and the `urn:node:`
// scheme the CLI also accepts fell through to the opaque id. The server already
// knows the answer and emits it canonically, and GetNode now carries it as
// `memory { urn }` (cli#463), so naming the memory costs no second query.
//
// Decoration, never a gate: `memory` is null when the caller may not read that
// record (hadron-server#2193), and the label then falls back to the opaque id
// rather than failing the command, because the route itself is fine. Never
// sliced or composed locally from the node's own urn.
func memoryLabel(memoryID string, m *gen.GetNodeNodeMemory) string {
	if m == nil || m.Urn == "" {
		return memoryID
	}
	return m.Urn
}

// existingEdge is an edge already on the graph, as the router's (or target's)
// read saw it.
type existingEdge struct {
	ID   string
	Data *json.RawMessage
}

// routeEdge is one edge write of `preflight route`.
type routeEdge struct {
	From, To, FromLoc, ToLoc, Label, Memory string
	Existing                                *existingEdge
	Mark                                    bool // write the cross-memory marker (cli#2736)
	Back                                    bool // the mirrored back-edge (messages only)
}

// markerNotConfirmedError says an edge exists but the marker update was not
// confirmed: the write may have committed, so nothing is retried.
type markerNotConfirmedError struct{ error }

// newMarkerNotConfirmed carries the exit class of what went wrong: a refusal exits
// as the refusal does, an answer that says nothing is Unavailable (unknown outcome).
func newMarkerNotConfirmed(code int, format string, args ...any) *markerNotConfirmedError {
	return &markerNotConfirmedError{exitcode.Newf(code, format, args...)}
}

func (e *markerNotConfirmedError) Unwrap() error { return e.error }

// writeRouteEdge writes one route edge and, when asked, makes sure it carries the
// marker. It returns the edge id and whether the marker is CONFIRMED on it.
func writeRouteEdge(ctx context.Context, client graphql.Client, e routeEdge) (string, bool, error) {
	if e.Existing != nil {
		// The edge the command read is already on the graph: never re-create it. A
		// real server refuses createEdge on an occupied loc (cli#2736 QA, cli#2844),
		// so a plain re-run reports it instead (cli#2844). Only the marker, when
		// asked for, is written, through updateEdge.
		if !e.Mark {
			return e.Existing.ID, false, nil
		}
		if edgeCrossMemory(e.Existing.Data) {
			return e.Existing.ID, true, nil
		}
		confirmed, err := updateCrossMemoryMarker(ctx, client, e, e.Existing.ID, e.Existing.Data)
		return e.Existing.ID, confirmed, err
	}
	var data *json.RawMessage
	if e.Mark {
		d, err := withCrossMemoryMarker(nil)
		if err != nil {
			return "", false, err
		}
		data = &d
	}
	resp, err := gen.CreateEdge(ctx, client, e.From, e.To, e.Label, nil, nil, nil, nil, nil, data)
	if err != nil {
		// Classify through api.MapError so auth, not-found and transport keep
		// their own exit codes; the wording stays agnostic about whether the
		// edge landed (a transport failure has an UNKNOWN outcome).
		mapped := api.MapError(err)
		if e.Back {
			return "", false, mapped // the caller words the back-edge failure
		}
		return "", false, exitcode.Newf(exitcode.FromError(mapped),
			"the route from %q to %s may not have been written — check with `hadron coding preflight list -m %s`: %v",
			e.FromLoc, e.ToLoc, e.Memory, mapped)
	}
	if resp == nil || resp.CreateEdge == nil {
		// An edge the server did not return may still have been written: an unknown
		// outcome with or without the marker, never exit 0 with no edge (cli#2736
		// LOCAL delta P3, cli#2844).
		return "", false, exitcode.Newf(exitcode.Unavailable,
			"the route from %q to %s may not have been written (the server returned no edge) — check with `hadron coding preflight list -m %s`; nothing was retried",
			e.FromLoc, e.ToLoc, e.Memory)
	}
	if !e.Mark || edgeCrossMemory(resp.CreateEdge.Data) {
		return resp.CreateEdge.Id, e.Mark, nil
	}
	// createEdge answered an edge without the marker (an edge that already
	// existed on a server that returns it): add it through updateEdge.
	confirmed, err := updateCrossMemoryMarker(ctx, client, e, resp.CreateEdge.Id, resp.CreateEdge.Data)
	return resp.CreateEdge.Id, confirmed, err
}

// updateCrossMemoryMarker adds the marker to an existing edge through updateEdge,
// keeping its other data exactly, and CONFIRMS it from the answer: a null result
// or data without the marker is an unknown outcome (the write may have
// committed), reported with a read-back instruction and never retried.
func updateCrossMemoryMarker(ctx context.Context, client graphql.Client, e routeEdge, edgeID string, current *json.RawMessage) (bool, error) {
	merged, err := withCrossMemoryMarker(current)
	if err != nil {
		return false, err
	}
	resp, err := gen.UpdateEdge(ctx, client, edgeID, nil, nil, nil, nil, nil, nil, &merged)
	if err != nil {
		mapped := api.MapError(err)
		return false, newMarkerNotConfirmed(exitcode.FromError(mapped),
			"the route %s → %s exists, but its cross-memory marker may not have been written — check with `hadron coding preflight list -m %s`; a re-run with --cross-memory only sets the marker: %v",
			e.FromLoc, e.ToLoc, e.Memory, mapped)
	}
	if resp == nil || resp.UpdateEdge == nil || !edgeCrossMemory(resp.UpdateEdge.Data) {
		return false, newMarkerNotConfirmed(exitcode.Unavailable,
			"the route %s → %s exists, but the server did not confirm its cross-memory marker, so it may not have been written — check with `hadron coding preflight list -m %s`; nothing was retried",
			e.FromLoc, e.ToLoc, e.Memory)
	}
	return true, nil
}
