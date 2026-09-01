package coding

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// defaultRouteNodeType is what every live route target carries. Route targets
// are orientation, not gates — a runnable node is a task, which the router
// links to rather than owns.
const defaultRouteNodeType = "info"

// newRouteDTO is the --json contract for `coding preflight create`.
type newRouteDTO struct {
	Loc      string    `json:"loc"`
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Route    string    `json:"route"`
	Tags     []string  `json:"tags"`
	Seq      *int      `json:"seq"`
	Router   string    `json:"router"`
	EdgeID   string    `json:"edgeId"`
	BackEdge bool      `json:"backEdge"`
	BodyLine string    `json:"bodyLine"` // the line written into the router; "" when none
	Section  string    `json:"section"`  // the heading the line lands under; "" for the preamble
	Links    []linkDTO `json:"links"`
	DryRun   bool      `json:"dryRun"`
}

func newCmdPreflightAdd(f *cmdutil.Factory) *cobra.Command {
	var (
		memory, root string
		route        string
		description  string
		name         string
		symptom      string
		section      string
		content      string
		contentFile  string
		nodeType     string
		tags         []string
		links        []string
		seq          int
		noBackEdge   bool
		noBodyLine   bool
		dryRun       bool
	)
	cmd := &cobra.Command{
		Use:     "create <loc>",
		Aliases: []string{"add"},
		Short:   "Create a node and route the preflight router to it",
		Long: `Create a node and wire the preflight route that leads a reader to it.

A route is three things, and a node is only reachable when it has all of
them: the router's outgoing edge, the mirrored back-edge so the route reads
the same when someone lands on the node from search, and the routing line in
the router's BODY — which is what a human or an LLM actually scans.

--route is the action, phrased the way a developer experiences the task.
"to" is prepended when you leave it off, so ` + "`--route \"fix a flaky test\"`" + `
and ` + "`--route \"to fix a flaky test\"`" + ` are the same label, and both pass
` + "`coding preflight lint`" + `'s action-phrasing rule.

<loc> is the target's full loc — route targets live wherever they belong
(` + "`findings:…`" + `, ` + "`conventions:…`" + `, ` + "`ops:…`" + `), not under a
` + "`preflight:`" + ` prefix.

The routing line is spliced into the router's body as
` + "`- **\"<symptom>\"** → [[<loc>]] — <description>`" + `. Where it goes is
resolved BEFORE anything is written: a router with one bullet list takes it
automatically, one with several sections needs --section, and a router that
routes purely by edge label needs --no-body-line. An ambiguous router is a
usage error, not a half-finished write.`,
		Example: `  hadron coding preflight create findings:flaky-otp-timer -m hrn:mem:acme.com:kb \
    --route "fix a flaky OTP countdown test" \
    --description "The resend countdown must start before the network await, not after"
  hadron coding preflight create conventions:ref-param-naming -m hrn:mem:acme.com:kb \
    --route "add or rename a GraphQL argument that identifies an entity" \
    --description "Name it <entity>Ref and resolve through resolve<Entity>Ref" \
    --section "GraphQL read and write surfaces" --tag conventions`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mem, err := codingMemoryURN(memory)
			if err != nil {
				return err
			}
			loc, err := routeTargetLoc(root, args[0])
			if err != nil {
				return err
			}
			label, err := normalizeRoute(route)
			if err != nil {
				return err
			}
			if strings.TrimSpace(description) == "" {
				return exitcode.Newf(exitcode.Usage,
					"--description is required — it is both the node's one-liner and the text of the routing line")
			}
			if noBodyLine && cmd.Flags().Changed("section") {
				return exitcode.Newf(exitcode.Usage, "--section and --no-body-line are mutually exclusive")
			}
			parsedLinks, err := parseLinks(links)
			if err != nil {
				return err
			}
			nodeName := strings.TrimSpace(name)
			if nodeName == "" {
				nodeName = checkName(loc)
			}
			body, err := resolveCheckBody(content, contentFile, f.IOStreams.In)
			if err != nil {
				return err
			}
			if body == "" {
				body = scaffoldRouteBody(nodeName, description)
			}
			line := routingLine(routeSymptom(symptom, label), loc, description)

			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			ctx := cmd.Context()

			// Resolve the router first, and settle where its body line goes,
			// before writing anything: an unrouted node is the defect this
			// command exists to prevent, and an ambiguous body must not leave
			// one behind.
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
			plan := routingPlan{Skipped: true}
			if !noBodyLine {
				if plan, err = planRoutingLine(routerBody(router.Node.Content), section, line, wikiLink(loc)); err != nil {
					return err
				}
			}

			// The back-edge and the cross-links ride along with the node; only
			// the route itself cannot, because createNode's edges: are outgoing
			// from the node being created and the route runs the other way.
			// Link resolution is read-only, so it runs under --dry-run too — a
			// --link that resolves to nothing is exactly what a rehearsal is for.
			edges := []*gen.NodeEdgeInput{}
			if !noBackEdge {
				back := label
				edges = append(edges, &gen.NodeEdgeInput{TargetId: router.Node.Id, Name: &back})
			}
			outLinks := []linkDTO{}
			linkTargetIDs := []string{} // resolved ids, positionally aligned with outLinks
			for _, l := range parsedLinks {
				// A qualified ref must be resolved WITHOUT -m: ResolveNodeRef
				// reads its ref as a bare loc whenever a memory is given, which
				// would compose a cross-memory URN into this memory and resolve
				// to nothing (same trap as `review create`'s --link).
				linkMemory := memory
				if cmdutil.IsQualifiedNodeRef(l.Ref) {
					linkMemory = ""
				}
				targetID, rerr := cmdutil.ResolveNodeRef(cmd, client, linkMemory, l.Ref)
				if rerr != nil {
					return rerr
				}
				lbl := l.Label
				edges = append(edges, &gen.NodeEdgeInput{TargetId: targetID, Name: &lbl})
				outLinks = append(outLinks, linkDTO{Target: l.Ref, Label: l.Label})
				linkTargetIDs = append(linkTargetIDs, targetID)
			}

			if dryRun {
				dto := newRouteDTO{
					Loc: loc, Name: nodeName, Route: label, Tags: mergeTags(nil, tags),
					Router: root, BackEdge: !noBackEdge, Links: outLinks, DryRun: true,
				}
				if !plan.Skipped {
					dto.BodyLine, dto.Section = line, plan.Section
				}
				return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
					return renderRoutePlan(w, dto, true)
				})
			}

			input := gen.CreateNodeInput{
				MemoryId:    mem.Ref,
				Loc:         loc,
				Name:        nodeName,
				NodeType:    &nodeType,
				Description: &description,
				Content:     &body,
				Tags:        mergeTags(nil, tags),
				Edges:       edges,
			}
			if cmd.Flags().Changed("seq") {
				input.Seq = &seq
			}

			resp, err := gen.CreateNode(ctx, client, &input)
			if err != nil {
				return api.MapError(err)
			}
			if resp.CreateNode == nil {
				return exitcode.Newf(exitcode.Error, "createNode returned no node")
			}

			dto := newRouteDTO{
				Loc: resp.CreateNode.Loc, ID: resp.CreateNode.Id, Name: resp.CreateNode.Name,
				Route: label, Tags: resp.CreateNode.Tags, Seq: resp.CreateNode.Seq,
				Router: root, BackEdge: !noBackEdge, Links: outLinks,
			}
			if dto.Tags == nil {
				dto.Tags = []string{}
			}

			// createNode's response carries no edges, so the embedded ones —
			// the back-edge and every --link — are unverified. `review create`
			// re-reads for exactly this reason; without it the DTO would report
			// `backEdge: true` and a full `links` list for edges that silently
			// never materialised. The DTO is corrected to what actually landed.
			landed, err := confirmEmbeddedEdges(ctx, client, resp.CreateNode.Id)
			if err != nil {
				return partialRoute(f, dto, true, err,
					"created %s but could not read it back to confirm its edges — check it with `hadron node get %s -m %s`",
					dto.Loc, dto.Loc, mem.raw)
			}
			var missing []string
			if dto.BackEdge && !landed[router.Node.Id] {
				dto.BackEdge = false
				missing = append(missing, fmt.Sprintf(
					"the back-edge to %q (`hadron edge create -m %s --from %s --to %s --name %q`)",
					root, mem.raw, dto.Loc, root, label))
			}
			kept := []linkDTO{}
			for i, l := range outLinks {
				if landed[linkTargetIDs[i]] {
					kept = append(kept, l)
					continue
				}
				missing = append(missing, fmt.Sprintf(
					"the --link to %q (`hadron edge create -m %s --from %s --to %s --name %q`)",
					l.Target, mem.raw, dto.Loc, l.Target, l.Label))
			}
			dto.Links = kept

			edgeResp, err := gen.CreateEdge(ctx, client, router.Node.Id, resp.CreateNode.Id, label,
				nil, nil, nil, nil, nil, nil)
			if err != nil {
				// The node exists but nothing routes to it — a partial write,
				// which exits 1 by contract so a caller branching on the exit
				// code never reads an unreachable node as done.
				return partialRoute(f, dto, true, err,
					"created %s but the route from %q was not written — nothing leads to it; wire it with `hadron edge create -m %s --from %s --to %s --name %q`",
					dto.Loc, root, mem.raw, root, dto.Loc, label)
			}
			if edgeResp.CreateEdge != nil {
				dto.EdgeID = edgeResp.CreateEdge.Id
			}

			if !noBodyLine {
				written, err := appendRoutingLine(ctx, client, routerRef, section, line, wikiLink(loc))
				if err != nil {
					return partialRoute(f, dto, true, err,
						"created and routed %s, but the router's body was not updated — the route is invisible to anyone READING %q; add this line yourself:\n  %s",
						dto.Loc, root, line)
				}
				if !written.Skipped {
					dto.BodyLine, dto.Section = line, written.Section
				}
			}

			if err := output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				return renderRoutePlan(w, dto, true)
			}); err != nil {
				return err
			}
			if len(missing) > 0 {
				// The route itself landed, so the node IS reachable — but the
				// caller asked for edges that are not there, and a partial write
				// exits 1 so a caller branching on the code never reads it as
				// complete.
				return exitcode.Newf(exitcode.Error,
					"created and routed %s, but %s did not land; add %s",
					dto.Loc, plural(len(missing), "one edge", "some edges"), strings.Join(missing, ", "))
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory to add the node to, hrn:mem:<root>:<slug> (required)")
	cmd.Flags().StringVar(&root, "root", preflightRootLoc, "loc of the preflight router node")
	cmd.Flags().StringVar(&route, "route", "", `the action the route fires on ("to" is prepended if absent) (required)`)
	cmd.Flags().StringVar(&description, "description", "", "one-line description; also the routing line's text (required)")
	cmd.Flags().StringVar(&name, "name", "", "node name (default: the loc's last segment)")
	cmd.Flags().StringVar(&symptom, "symptom", "", "the routing line's quoted trigger (default: the route)")
	cmd.Flags().StringVar(&section, "section", "", "heading in the router's body to add the routing line under")
	cmd.Flags().StringVarP(&content, "content", "c", "", `node body ("-" reads stdin; default: a scaffold)`)
	cmd.Flags().StringVar(&contentFile, "content-file", "", "read the node body from a file")
	cmd.Flags().StringVar(&nodeType, "type", defaultRouteNodeType, "node type")
	cmd.Flags().StringArrayVar(&tags, "tag", nil, "tag (repeatable)")
	cmd.Flags().StringArrayVar(&links, "link", nil,
		"cross-link to a canonical node: <node-ref>[=<label>], a bare loc in this memory or a URN/id anywhere (repeatable)")
	cmd.Flags().IntVar(&seq, "seq", 0, "explicit sibling ordering")
	cmd.Flags().BoolVar(&noBackEdge, "no-back-edge", false, "skip the mirrored back-edge to the router")
	cmd.Flags().BoolVar(&noBodyLine, "no-body-line", false, "wire the edges only; leave the router's body alone")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be written — including where the routing line lands — without writing it")
	// The same set as `review create` (hadron-cli#533): every genuinely-required
	// flag marked, so cobra reports them together instead of one per re-run.
	// --description is required HERE and optional on `preflight route`, where it
	// defaults to the target node's own description — so this is a per-command
	// fact, not a group-wide one, and marking it there would refuse a call that
	// works today.
	_ = cmd.MarkFlagRequired("memory")
	_ = cmd.MarkFlagRequired("route")
	_ = cmd.MarkFlagRequired("description")
	return cmd
}

// renderRoutePlan is the human branch for both the rehearsal and the result.
// They print the same four lines on purpose: a --dry-run is only useful if what
// it shows is what you get.
func renderRoutePlan(w io.Writer, dto newRouteDTO, created bool) error {
	verb := map[[2]bool]string{
		{true, false}:  "✓ created",
		{true, true}:   "would create",
		{false, false}: "✓ routed",
		{false, true}:  "would route",
	}[[2]bool{created, dto.DryRun}]
	t := output.NewTable(w)
	t.Row(verb, dto.Loc, "("+dto.Route+")")
	t.Row("  route", dto.Router+" → "+dto.Loc, dto.EdgeID)
	if dto.BackEdge {
		t.Row("  back-edge", dto.Loc+" → "+dto.Router, "")
	}
	if dto.BodyLine != "" {
		where := "in " + dto.Router
		if dto.Section != "" {
			where = "under " + strconv.Quote(dto.Section)
		}
		t.Row("  body line", where, strings.TrimPrefix(dto.BodyLine, "- "))
	}
	return t.Flush()
}

// partialRoute reports a write that landed halfway. The DTO is still emitted —
// the node exists and a caller needs its id — but the exit code is 1, never 0.
//
// It renders through renderRoutePlan rather than its own table so the partial
// output cannot drift from the success output. It previously printed a
// hardcoded "✓ created", which `route` — a command that never creates
// anything — inherited as a false claim on every partial write.
func partialRoute(f *cmdutil.Factory, dto newRouteDTO, created bool, cause error, format string, a ...any) error {
	_ = output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		return renderRoutePlan(w, dto, created)
	})
	return exitcode.Newf(exitcode.Error, "%s\n  (%v)", fmt.Sprintf(format, a...), cause)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// confirmEmbeddedEdges re-reads a just-created node and returns the set of
// target ids its outgoing edges actually reach. createNode's response selects
// no edges, so the ones it was asked to embed are otherwise taken on trust —
// the same gap `review create`'s confirmParentEdge closes.
func confirmEmbeddedEdges(ctx context.Context, client graphql.Client, nodeID string) (map[string]bool, error) {
	resp, err := gen.GetNode(ctx, client, nodeID)
	if err != nil {
		return nil, api.MapError(err)
	}
	landed := map[string]bool{}
	if resp.Node == nil {
		return landed, nil // readable-back failure is reported as "nothing landed"
	}
	for _, e := range resp.Node.OutgoingEdges {
		if e != nil && e.Target != nil {
			landed[e.Target.Id] = true
		}
	}
	return landed, nil
}

// routeTargetLoc validates the loc a route points at. Unlike a review check
// this is a full loc, not a name under the root's prefix: live routers point at
// findings:, conventions: and ops: nodes, and at children of their own prefix.
func routeTargetLoc(root, loc string) (string, error) {
	l := strings.TrimSpace(loc)
	if l == "" {
		return "", exitcode.Newf(exitcode.Usage, "<loc> is empty")
	}
	if l == strings.TrimSpace(root) {
		return "", exitcode.Newf(exitcode.Usage, "<loc> %q is the router itself — a route must lead somewhere else", l)
	}
	if err := cmdutil.ValidateURNPath("<loc>", l); err != nil {
		return "", err
	}
	return l, nil
}

// normalizeRoute turns the action into the edge label the router's convention
// (and `preflight lint`'s route-label-phrasing rule) expects.
func normalizeRoute(route string) (string, error) {
	t := strings.Join(strings.Fields(route), " ")
	if t == "" {
		return "", exitcode.Newf(exitcode.Usage, "--route is required — it becomes the label a reader scans for")
	}
	lower := strings.ToLower(t)
	if lower == strings.TrimSpace(routePrefix) {
		return "", exitcode.Newf(exitcode.Usage,
			"--route %q is the bare stem — say what the reader is about to DO (e.g. \"fix a flaky test\")", route)
	}
	if !strings.HasPrefix(lower, routePrefix) {
		return routePrefix + t, nil
	}
	if strings.TrimSpace(t[len(routePrefix):]) == "" {
		return "", exitcode.Newf(exitcode.Usage,
			"--route %q is the bare stem — say what the reader is about to DO (e.g. \"fix a flaky test\")", route)
	}
	// Keep the caller's spelling of the stem; the rule is case-insensitive.
	return t, nil
}

// routeSymptom is the phrase the routing line quotes. It defaults to the route
// label, sentence-cased — the label already reads as the reader's intent, which
// is exactly what the quoted trigger is for.
func routeSymptom(symptom, label string) string {
	if s := strings.TrimSpace(symptom); s != "" {
		return s
	}
	r := []rune(label)
	if len(r) == 0 {
		return label
	}
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// routerBody dereferences the router's content, which is null on a node whose
// body was never written.
func routerBody(content *string) string {
	if content == nil {
		return ""
	}
	return *content
}

// appendRoutingLine re-reads the router and splices the line into its CURRENT
// body. The insertion was already validated against the body read at the top of
// the command; re-reading here means the write is applied to what the router
// says now, not to a copy two round trips stale.
//
// Only `content` is sent. Every other field is omitted, which the server reads
// as "preserve" — in particular `edges`, which would otherwise REPLACE the
// router's whole outgoing edge set and delete every route it has.
func appendRoutingLine(ctx context.Context, client graphql.Client, routerRef, section, line, linkKey string) (routingPlan, error) {
	fresh, err := gen.GetNode(ctx, client, routerRef)
	if err != nil {
		return routingPlan{}, api.MapError(err)
	}
	if fresh.Node == nil {
		return routingPlan{}, exitcode.Newf(exitcode.NotFound, "the router disappeared between the create and the body update")
	}
	plan, err := planRoutingLine(routerBody(fresh.Node.Content), section, line, linkKey)
	if err != nil || plan.Skipped {
		return plan, err // Skipped: already linked by hand, nothing to add
	}
	input := gen.UpdateNodeInput{Id: &fresh.Node.Id, Content: &plan.Body}
	if _, err := gen.UpdateNode(ctx, client, &input); err != nil {
		return routingPlan{}, api.MapError(err)
	}
	return plan, nil
}

// scaffoldRouteBody writes the skeleton a routed node uses: the rule up front,
// then the symptom and the mechanism, then the do/don't pair. A route target is
// read to understand something, so the scaffold leads with what it says rather
// than with a checklist.
func scaffoldRouteBody(name, description string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n%s\n\n", name, strings.TrimSpace(description))
	b.WriteString("## What you see\n\nTODO(symptom): the error, the wrong behaviour, or the question that brings a reader here — in the words they would use.\n\n")
	b.WriteString("## Why\n\nTODO(cause): the non-obvious mechanism. Cite `file:line` and verify each reference against the current code.\n\n")
	b.WriteString("## Do / don't\n\n- **Do**: TODO(do).\n- **Don't**: TODO(dont).\n")
	return b.String()
}
