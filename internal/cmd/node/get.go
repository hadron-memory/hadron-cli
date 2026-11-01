package node

import (
	"fmt"
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

func newCmdGet(f *cmdutil.Factory) *cobra.Command {
	var memory, locPrefix, install string
	var raw, bodyOnly bool
	cmd := &cobra.Command{
		Use:   "get <node-urn>... | <loc>... (-m <memory> | --install <app>/<agent>) | --prefix <loc>",
		Short: "Show one or more nodes, including content and edges",
		Long: `Show a node: its name, content, data and edges. Name it by its URN, or by its
loc (its path inside a memory, such as findings:flaky-ci) together with -m:

  hadron node get findings:flaky-ci -m hrn:mem:acme.com:kb

A register entry also has an action URN, hrn:action:<root>:<agent-slug>:<code>,
which names the same node and is accepted anywhere a node URN is (it needs no -m):

  hadron node get hrn:action:hadronmemory.com:team-agent:task.finish.open-children

A node URN is hrn:node:<root>:<memory>:<loc>. The hrn:node: prefix is required
because a loc has colons of its own. The legacy <org>::<memory>::<loc> form is
still accepted. A bare loc without -m (or --install) is refused, since the
same loc can exist in several memories.

To edit a node's content, read it with --body-only, edit the file, and write
it back with node update --content-file:

  hadron node get findings:flaky-ci -m hrn:mem:acme.com:kb --body-only > body.md
  hadron node update findings:flaky-ci -m hrn:mem:acme.com:kb --content-file body.md

--body-only prints the content exactly as stored and nothing else, so the
round trip changes only what you edited. Do not save an ordinary read back
into a node: it has filled in the content's placeholders (see Details), and
the text form of --raw prints a header above the content.

To read several nodes at once, pass several refs, or --prefix <loc> -m
<memory> for every node whose loc starts with <loc> (an empty --prefix is the
whole memory). Unlike node list, this returns full content and edges. The
output is then {nodes, unavailable}.

--json carries createdAt and createdByUser: the server's {handle, urn} for the
user who created the node, or null. A null author means "not shown to you or
not known" (for example, the node was written before authors were recorded),
never "nobody". The text form prints a "created by:" line only when there is a
handle or URN to show. The user behind a node's last edit is not shown here.

Exit codes: 4 when the node is missing or you cannot read it (the server does
not say which), and also when any ref of several is "unavailable", so a partial
read is never mistaken for a complete one; 2 for a malformed ref, which fails
the whole call so a typo is not mistaken for a missing node; 1 for a server or
transport failure, or 7 when no answer arrived. Exit 1 and 7 say nothing about
whether the node exists. With --json the error envelope is on stdout.

Details:

Rendered or raw. A node's content can hold template placeholders, written
{{name}}, that are filled in from the node's data when it is read. A
one-node read fills them in by default (a placeholder with no value becomes
empty); --raw shows the content as stored, placeholders intact. Reads of
several nodes or of a --prefix are always raw. In --json, "rendered" is true
only when placeholders were filled in. With --json, --body-only prints
{id, urn, loc, content, rendered: false}.

Several nodes go in one request up to 200; a larger set is split into as few
requests as the server allows. --prefix fails, rather than truncating, past the
server's cap on nodes.

Feedback counts (commentSummary in --json) come from a separate read: they are
null (text: unavailable) when that read fails or the server is older, never a
guessed zero.

` + installDetails("") + ``,
		Example: `  hadron node get hrn:node:hadronmemory.com:dev:start-here
  hadron node get start-here -m hrn:mem:hadronmemory.com:dev --json
  hadron node get start-here preflight instructions -m hrn:mem:hadronmemory.com:dev --json
  hadron node get --prefix findings: -m hrn:mem:hadronmemory.com:dev --json
  hadron node get tasks:start-worker-session -m hrn:mem:hadronmemory.com:core --raw --json
  hadron node get tasks:start-worker-session -m hrn:mem:hadronmemory.com:core --body-only > body.md`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if bodyOnly && wantsLintWarnings(cmd) {
				return exitcode.Newf(exitcode.Usage, "--lint-warnings cannot be combined with --body-only")
			}
			target, err := installTarget(cmd, install, args)
			if err != nil {
				return err
			}
			prefixMode := cmd.Flags().Changed("prefix")
			// The two forms are mutually exclusive server-side, so reject the
			// combination here with a message that names both rather than
			// letting the server answer with its own phrasing.
			if prefixMode && len(args) > 0 {
				return exitcode.Newf(exitcode.Usage,
					"--prefix reads a subtree and cannot be combined with explicit node refs — drop one")
			}
			if prefixMode && strings.TrimSpace(memory) == "" && target == nil {
				return exitcode.Newf(exitcode.Usage, "--prefix needs -m/--memory hrn:mem:<root>:<slug> to scope the subtree")
			}
			if !prefixMode && len(args) == 0 {
				return exitcode.Newf(exitcode.Usage,
					"specify at least one node ref, or --prefix <loc> -m <memory> to read a subtree")
			}
			if bodyOnly && (prefixMode || len(args) > 1) {
				return exitcode.Newf(exitcode.Usage,
					"--body-only prints one node's stored body, so it takes exactly one ref — not several, and not --prefix")
			}

			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}

			if target != nil {
				memory, err = cmdutil.InstallMemoryRef(cmd.Context(), client, target)
				if err != nil {
					return err
				}
			}

			// One ref keeps the single-node shape: that output is a stable
			// contract and callers of `node get <ref>` must not have to care
			// that a batch form now exists.
			if !prefixMode && len(args) == 1 {
				id, err := cmdutil.ResolveNodeRef(cmd, client, memory, args[0])
				if err != nil {
					return err
				}
				if bodyOnly {
					return writeBodyOnly(cmd, f, client, id, args[0])
				}
				var dto nodeDetailDTO
				err = readConsistently(cmd, client, revisionSelector{refs: []string{id}}, func() ([]*nodeDetailDTO, error) {
					node, err := fetchNodeByID(cmd, client, id, args[0], raw)
					if err != nil {
						return nil, err
					}
					dto = detailDTO(node)
					dto.Rendered = !raw
					return []*nodeDetailDTO{&dto}, nil
				})
				if err != nil {
					return err
				}
				loadCommentSummaries(cmd.Context(), f, client, []*nodeDetailDTO{&dto})
				// cli#878 part 3: advisory, and gated on the server advertising
				// signing, so an off or older server pays no refused round trip.
				// The RESOLVED id, never args[0]: a bare loc plus -m already
				// resolved here, and the raw spelling is ambiguous across
				// memories (Andi's cycle-1 P2).
				loadSignatureSummary(cmd.Context(), f, client, id, &dto)
				dto.LintWarnings = loadLintWarnings(cmd, client, id)
				return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
					return renderNodeDetail(w, dto)
				})
			}

			// nodeBatch(refs:) takes ids and node URNs and refuses any other URN
			// kind, which would fail the whole batch. An action URN is therefore
			// resolved to its register node's id first; one that names nothing
			// is reported unavailable, as an absent node ref is (#1138).
			var absentActions []string
			if !prefixMode {
				args, absentActions, err = resolveActionRefs(cmd, client, args)
				if err != nil {
					return err
				}
				if len(args) == 0 {
					return emitNodeBatch(f, nodeBatchDTO{Nodes: []nodeDetailDTO{}, Unavailable: absentActions})
				}
			}
			sel, err := batchRevisionSelector(memory, locPrefix, args, prefixMode)
			if err != nil {
				return err
			}
			var dto nodeBatchDTO
			err = readConsistently(cmd, client, sel, func() ([]*nodeDetailDTO, error) {
				nodes, unavailable, err := fetchNodeBatch(cmd, client, memory, locPrefix, args, prefixMode)
				if err != nil {
					return nil, err
				}
				dto = nodeBatchDTO{Nodes: []nodeDetailDTO{}, Unavailable: []string{}}
				for _, n := range nodes {
					if n == nil {
						continue
					}
					dto.Nodes = append(dto.Nodes, batchDetailDTO(n))
				}
				dto.Unavailable = append(dto.Unavailable, unavailable...)
				dto.Unavailable = append(dto.Unavailable, absentActions...)
				ptrs := make([]*nodeDetailDTO, len(dto.Nodes))
				for i := range dto.Nodes {
					ptrs[i] = &dto.Nodes[i]
				}
				return ptrs, nil
			})
			if err != nil {
				return err
			}
			ptrs := make([]*nodeDetailDTO, len(dto.Nodes))
			for i := range dto.Nodes {
				ptrs[i] = &dto.Nodes[i]
			}
			loadCommentSummaries(cmd.Context(), f, client, ptrs)
			for _, n := range ptrs {
				n.LintWarnings = loadLintWarnings(cmd, client, n.ID)
			}
			return emitNodeBatch(f, dto)
		},
	}
	cmd.Flags().StringVar(&install, "install", "", installFlagHelp)
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory (hrn:mem:<root>:<slug>) to resolve a bare <loc> against")
	cmd.Flags().StringVar(&locPrefix, "prefix", "", "read every node under this loc prefix in one call (needs -m; empty means the whole memory)")
	cmd.Flags().BoolVar(&raw, "raw", false, "return the stored body with {{…}} placeholders intact, not the rendered one (to edit content, use --body-only)")
	cmd.Flags().BoolVar(&bodyOnly, "body-only", false, "print only the stored body of one node, byte for byte — the read to edit from (implies --raw; alias: --content-only)")
	// The spelling `spec get` already accepts (#99 item 5).
	cmdutil.WithFlagAliases(cmd, map[string]string{"content-only": "body-only"})
	enableLintWarnings(cmd, "--body-only")
	return cmd
}

// nodeBodyDTO is `node get --body-only --json`: the stored body and just
// enough to name the node it came from. rendered is always false, said
// anyway so the shape matches a full read's.
type nodeBodyDTO struct {
	ID       string `json:"id"`
	URN      string `json:"urn"`
	Loc      string `json:"loc"`
	Content  string `json:"content"`
	Rendered bool   `json:"rendered"`
}

// writeBodyOnly prints one node's STORED body and nothing else (#873). The
// bytes are the body's own: a newline is added only for a terminal, where
// the prompt would otherwise land on the last line, never into a pipe or
// file, so a round trip through `node update --content-file` is exact.
func writeBodyOnly(cmd *cobra.Command, f *cmdutil.Factory, client graphql.Client, id, ref string) error {
	node, err := fetchNodeByID(cmd, client, id, ref, true)
	if err != nil {
		return err
	}
	dto := nodeBodyDTO{ID: node.Id, URN: node.Urn, Loc: node.Loc}
	if node.Content != nil {
		dto.Content = *node.Content
	}
	return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		if _, err := io.WriteString(w, dto.Content); err != nil {
			return err
		}
		if f.IOStreams.IsTerminal() && dto.Content != "" && !strings.HasSuffix(dto.Content, "\n") {
			_, err := io.WriteString(w, "\n")
			return err
		}
		return nil
	})
}

// renderNodeDetail prints one node in the human layout. Shared so a node read
// on its own and the same node read in a batch look identical.
func renderNodeDetail(w io.Writer, dto nodeDetailDTO) error {
	fmt.Fprintf(w, "%s\n  loc: %s\n", dto.Name, dto.Loc)
	// The fully-qualified URN, then the link that opens it, in that order and
	// adjacent (#515). This is the framing the MCP node read already uses, and
	// what every role-agent briefing means by "never hand-build a portal link —
	// copy the URL line a node read prints". Until now the CLI's node read
	// printed neither, so that instruction was unsatisfiable here.
	//
	// Each is printed only when the server sent it. No placeholder, no dash,
	// and above all no locally-composed fallback: a link to a guessed origin
	// fails silently for whoever clicks it (cor:api:230:01). The URN survives
	// the link's absence — they are independent answers, and losing the link
	// must not cost the caller the reference.
	if dto.URN != "" {
		fmt.Fprintf(w, "  urn: %s\n", dto.URN)
	}
	if dto.ActionURN != "" {
		fmt.Fprintf(w, "  action urn: %s\n", dto.ActionURN)
	}
	if dto.PortalURL != "" {
		fmt.Fprintf(w, "  URL: %s\n", dto.PortalURL)
	}
	fmt.Fprintf(w, "  type: %s\n", dto.NodeType)
	if dto.ObjectType != nil && *dto.ObjectType != "" {
		fmt.Fprintf(w, "  object-type: %s\n", *dto.ObjectType)
	}
	fmt.Fprintf(w, "  runnable: %t\n", dto.IsRunnable)
	// Printed whenever the field is NON-NULL. Ungoverned (null) is the
	// overwhelming majority and a `role: -` line on every node would be noise;
	// anything else changes which door may write the node, so it earns a line.
	//
	// An EMPTY role is shown too, and shown as odd (@copilot on #615). It is a
	// historical state; current server writers normalize blanks to null, and it matches no kind — so hiding it alongside null would
	// make the one node a reader most needs to notice look ordinary.
	if dto.Role != nil {
		if *dto.Role == "" {
			fmt.Fprintf(w, "  role: \"\" (empty — matches no kind; clear it to null or set a value)\n")
		} else {
			fmt.Fprintf(w, "  role: %s\n", *dto.Role)
		}
	}
	if dto.Description != nil && *dto.Description != "" {
		fmt.Fprintf(w, "  about: %s\n", *dto.Description)
	}
	if len(dto.Tags) > 0 {
		fmt.Fprintf(w, "  tags: %v\n", dto.Tags)
	}
	fmt.Fprintf(w, "  updated: %s\n", dto.UpdatedAt)
	// hadron-cli#1513: only a printable author prints; null and an empty object
	// print nothing (never "nobody"/"unknown"), and --json carries the object.
	if by := dto.CreatedByUser.Display(256); by != "" {
		fmt.Fprintf(w, "  created by: %s\n", by)
	}
	if dto.Revision != nil {
		fmt.Fprintf(w, "  revision: %d\n", *dto.Revision)
	} else {
		fmt.Fprintln(w, "  revision: unknown (the server predates node revisions)")
	}
	if dto.CommentSummary != nil {
		fmt.Fprintf(w, "  feedback: %d comments; %d open threads; %d resolved threads\n", dto.CommentSummary.Comments, dto.CommentSummary.OpenThreads, dto.CommentSummary.ResolvedThreads)
	} else {
		fmt.Fprintln(w, "  feedback: unavailable")
	}
	renderSignatureLine(w, dto)
	renderStamps(w, "  ", dto.Authorship, dto.ContentValidation, "this revision")
	renderApprovalStatus(w, "  ", dto.ApprovalStatus)
	renderMintStatus(w, "  ", dto.MintStatus)
	if dto.Data != nil && len(*dto.Data) > 0 {
		if dataStr := string(*dto.Data); dataStr != "null" {
			fmt.Fprintf(w, "  data: %s\n", dataStr)
		}
	}
	if dto.Properties != nil && len(*dto.Properties) > 0 {
		if propStr := string(*dto.Properties); propStr != "null" {
			fmt.Fprintf(w, "  properties: %s\n", propStr)
		}
	}
	if len(dto.OutgoingEdges) > 0 || len(dto.IncomingEdges) > 0 {
		fmt.Fprintln(w, "  edges:")
		for _, e := range dto.OutgoingEdges {
			fmt.Fprintf(w, "    → %s (%s)\n", e.Loc, edgeRel(e))
		}
		for _, e := range dto.IncomingEdges {
			fmt.Fprintf(w, "    ← %s (%s)\n", e.Loc, edgeRel(e))
		}
	}
	if err := renderLintWarnings(w, dto.LintWarnings); err != nil {
		return err
	}
	if dto.Content != nil && *dto.Content != "" {
		if !dto.Rendered {
			fmt.Fprintln(w, "  content: stored body ({{…}} placeholders not compiled)")
		}
		fmt.Fprintf(w, "\n%s\n", *dto.Content)
	} else if dto.Abstract != nil && *dto.Abstract != "" {
		fmt.Fprintf(w, "\n(abstract)\n%s\n", *dto.Abstract)
	}
	return nil
}

// edgeRefDTO is one edge endpoint in node output.
type edgeRefDTO struct {
	EdgeID     string `json:"edgeId"`
	Name       string `json:"name"`
	EdgeLoc    string `json:"edgeLoc"`
	IsRunnable bool   `json:"isRunnable"`
	Priority   int    `json:"priority"`
	NodeID     string `json:"nodeId"`
	Loc        string `json:"loc"`
	MemoryID   string `json:"memoryId"`
}

// edgeRel is the relationship shown for an edge: its name, or its loc when the
// name is empty (spec 037).
func edgeRel(e edgeRefDTO) string {
	if e.Name != "" {
		return e.Name
	}
	return e.EdgeLoc
}

func edgeRefOf(edgeID string, name *string, edgeLoc string, isRunnable *bool, priority int, nodeID, loc, memoryID string) edgeRefDTO {
	n := ""
	if name != nil {
		n = *name
	}
	run := false
	if isRunnable != nil {
		run = *isRunnable
	}
	return edgeRefDTO{
		EdgeID: edgeID, Name: n, EdgeLoc: edgeLoc, IsRunnable: run, Priority: priority,
		NodeID: nodeID, Loc: loc, MemoryID: memoryID,
	}
}

func detailDTO(n *gen.GetNodeNode) nodeDetailDTO {
	dto := nodeDetailDTO{
		nodeDTO: nodeDTO{
			ID: n.Id,
			// urn is non-nullable server-side; portalUrl is not, and strVal
			// keeps an absent one out of the --json shape (omitempty) rather
			// than emitting "".
			URN:        n.Urn,
			ActionURN:  strVal(n.ActionUrn),
			PortalURL:  strVal(n.PortalUrl),
			MemoryID:   n.MemoryId,
			Loc:        n.Loc,
			Name:       n.Name,
			NodeType:   n.NodeType,
			Tags:       n.Tags,
			IsRunnable: boolVal(n.IsRunnable),
			Role:       n.Role,
			UpdatedAt:  n.UpdatedAt,
		},
		ObjectType:         n.ObjectType,
		Description:        n.Description,
		Abstract:           n.Abstract,
		AbstractOriginHash: n.AbstractOriginHash,
		Content:            n.Content,
		Data:               n.Data,
		Properties:         n.Properties,
		Seq:                n.Seq,
		CreatedAt:          n.CreatedAt,
		OutgoingEdges:      []edgeRefDTO{},
		IncomingEdges:      []edgeRefDTO{},
	}
	for _, e := range n.OutgoingEdges {
		// #781: target/source is null when its memory is unreadable to the
		// caller (a cross-memory edge's far endpoint) — surface a blank ref
		// rather than dereferencing nil.
		tid, tloc, tmem := "", "", ""
		if e.Target != nil {
			tid, tloc, tmem = e.Target.Id, e.Target.Loc, e.Target.MemoryId
		}
		dto.OutgoingEdges = append(dto.OutgoingEdges,
			edgeRefOf(e.Id, e.Name, e.Loc, e.IsRunnable, e.Priority, tid, tloc, tmem))
	}
	for _, e := range n.IncomingEdges {
		sid, sloc, smem := "", "", ""
		if e.Source != nil {
			sid, sloc, smem = e.Source.Id, e.Source.Loc, e.Source.MemoryId
		}
		dto.IncomingEdges = append(dto.IncomingEdges,
			edgeRefOf(e.Id, e.Name, e.Loc, e.IsRunnable, e.Priority, sid, sloc, smem))
	}
	if u := n.CreatedByUser; u != nil {
		dto.CreatedByUser = cmdutil.NewAttribution(u.Handle, u.Urn)
	}
	return dto
}

// consistentReadAttempts bounds readConsistently's retries.
const consistentReadAttempts = 3

// revisionSelector names the same nodes the content read names: explicit
// refs (ids or canonical refs), or a memory + loc prefix.
type revisionSelector struct {
	refs   []string
	memory *string
	prefix *string
}

// revisionSupport is what one probe learned about the server.
type revisionSupport int

const (
	// supportUnknown: the probe made no request (it had no nodes to name).
	supportUnknown revisionSupport = iota
	// supportYes: every call returned revisions.
	supportYes
	// supportNo: every call was refused as an unknown field.
	supportNo
	// supportMixed: some calls returned revisions and some were refused — a
	// rolling or mixed deployment answering one probe from both versions.
	supportMixed
)

// readConsistently reads nodes BETWEEN two revision reads and keeps them only
// when every node's revision is the same before and after (@codex on #724).
// A revision advances on every authoring change, so equal brackets mean the
// content read IS that revision; a timestamp comparison could not promise
// that, since two writes can share one. Otherwise the whole read repeats, and
// after the last attempt it fails rather than print a pairing it could not
// verify.
//
// Against a server that predates revisions (both probes say so) every
// Revision stays nil — null means exactly that, never a guess and never a
// race. Support that appears, vanishes or is split within a probe is a server
// changing under the read, and the read repeats. A content read that returned
// no nodes has nothing to pair: the after-probe then makes no request and
// takes the before-probe's answer, so an empty result stays an empty result.
//
// The guarantee is about the nodes PRINTED. A node the before-read saw that
// the content read no longer returns (deleted or made unreadable in between)
// is correctly absent from a read of that moment; every node that IS printed
// carries the revision of the content printed.
func readConsistently(cmd *cobra.Command, client graphql.Client, sel revisionSelector, read func() ([]*nodeDetailDTO, error)) error {
	// Stamp support is learned once and kept across attempts: a server that
	// refused the stamps once is not asked again (cli#752).
	stamps := &stampReads{}
	for attempt := 1; ; attempt++ {
		before, supportBefore, err := liveRevisions(cmd, client, sel, stamps)
		if err != nil {
			return err
		}
		dtos, err := read()
		if err != nil {
			return err
		}
		ids := make([]string, 0, len(dtos))
		for _, d := range dtos {
			ids = append(ids, d.ID)
		}
		// The stamps printed are the AFTER probe's: the one whose revisions
		// must equal the before-probe's for the read to be kept, so each
		// printed stamp describes the printed revision.
		stamps.byID = map[string]nodeStamps{}
		after, supportAfter, err := liveRevisions(cmd, client, revisionSelector{refs: ids}, stamps)
		if err != nil {
			return err
		}
		if supportAfter == supportUnknown {
			supportAfter = supportBefore // nothing printed, nothing to pair
		}
		switch {
		case supportBefore == supportNo && supportAfter == supportNo:
			return nil
		case supportBefore == supportYes && supportAfter == supportYes && pairRevisions(dtos, before, after):
			stamps.apply(dtos)
			return nil
		}
		// A changed revision, a node gone missing, or support that differs
		// between or within the probes (@copilot, @codex on #724).
		if attempt == consistentReadAttempts {
			return exitcode.Newf(exitcode.Conflict,
				"the node changed while it was being read, %d times running, so its revision could not be paired with its content; try again", consistentReadAttempts)
		}
	}
}

// pairRevisions sets each DTO's Revision when its node carried the same
// revision before and after the content read, and reports whether every node
// did. A node absent from either read (created, deleted or made unavailable in
// between) is a change like an edit.
func pairRevisions(dtos []*nodeDetailDTO, before, after map[string]int) bool {
	for _, d := range dtos {
		b, okB := before[d.ID]
		a, okA := after[d.ID]
		if !okB || !okA || a != b {
			return false
		}
	}
	for _, d := range dtos {
		rev := after[d.ID]
		d.Revision = &rev
	}
	return true
}

// liveRevisions reads id → revision for the nodes sel names, and what the
// probe learned about the server's support (see revisionSupport). Explicit
// refs go in api.NodeBatchCap-sized calls; a prefix read's byte-cap spillover
// is re-read by id. Any failure other than the unknown-field refusal,
// including a null envelope, is the command's error.
func liveRevisions(cmd *cobra.Command, client graphql.Client, sel revisionSelector, stamps *stampReads) (map[string]int, revisionSupport, error) {
	revs := map[string]int{}
	var yes, no bool
	ask := func(refs []string, memory, prefix *string) (*gen.NodeLiveRevisionsNodeBatchNodeBatchResult, error) {
		if !stamps.mintUnsupported {
			resp, err := gen.NodeLiveRevisionsMinted(cmd.Context(), client, refs, memory, prefix)
			switch {
			case err == nil && resp.NodeBatch != nil:
				yes = true
				out := &gen.NodeLiveRevisionsNodeBatchNodeBatchResult{Truncated: resp.NodeBatch.Truncated, Omitted: resp.NodeBatch.Omitted, Unavailable: resp.NodeBatch.Unavailable}
				for _, n := range resp.NodeBatch.Nodes {
					if n == nil {
						continue
					}
					revs[n.Id] = n.Revision
					if stamps.byID != nil {
						st := nodeStamps{}
						if n.Authorship != nil {
							st.authorship = authorshipFrom(&n.Authorship.NodeAuthorshipFields)
						}
						if n.ContentValidation != nil {
							st.validation = contentValidationFrom(&n.ContentValidation.NodeContentValidationFields)
						}
						if n.ApprovalStatus != nil {
							st.approval = approvalStatusFrom(&n.ApprovalStatus.NodeApprovalStatusFields)
						}
						if n.MintStatus != nil {
							st.mint = mintStatusFrom(&n.MintStatus.NodeMintStatusFields)
						}
						stamps.byID[n.Id] = st
					}
				}
				return out, nil
			case err == nil:
				return nil, exitcode.Newf(exitcode.Error, "the server returned no result for the node mint read")
			case isUnknownFieldErr(err, "mintStatus") || isUnknownFieldErr(err, "NodeMintStatus"):
				stamps.mintUnsupported = true
			case isUnknownFieldErr(err, "approvalStatus") || isUnknownFieldErr(err, "NodeApprovalStatus") || isUnknownFieldErr(err, "NodeApproval"):
				stamps.mintUnsupported = true
				stamps.approvalUnsupported = true
			case isUnknownFieldErr(err, "authorship") || isUnknownFieldErr(err, "contentValidation") || isUnknownFieldErr(err, "NodeAuthorship") || isUnknownFieldErr(err, "NodeContentValidationStatus"):
				stamps.mintUnsupported = true
				stamps.approvalUnsupported = true
				stamps.unsupported = true
			case isUnknownFieldErr(err, "revision") || isUnknownFieldErr(err, "nodeBatch"):
				stamps.mintUnsupported = true
				stamps.approvalUnsupported = true
				stamps.unsupported = true
				no = true
				return nil, nil
			default:
				return nil, api.MapError(err)
			}
		}
		// #1591: the approval can be recorded without advancing the node's
		// revision. Read it with the AFTER probe so the displayed status is
		// at least as recent as the content whose revision was paired.
		if !stamps.approvalUnsupported {
			resp, err := gen.NodeLiveRevisionsApproved(cmd.Context(), client, refs, memory, prefix)
			switch {
			case err == nil && resp.NodeBatch != nil:
				yes = true
				out := &gen.NodeLiveRevisionsNodeBatchNodeBatchResult{
					Truncated: resp.NodeBatch.Truncated, Omitted: resp.NodeBatch.Omitted, Unavailable: resp.NodeBatch.Unavailable,
				}
				for _, n := range resp.NodeBatch.Nodes {
					if n == nil {
						continue
					}
					revs[n.Id] = n.Revision
					if stamps.byID != nil {
						st := nodeStamps{}
						if n.Authorship != nil {
							st.authorship = authorshipFrom(&n.Authorship.NodeAuthorshipFields)
						}
						if n.ContentValidation != nil {
							st.validation = contentValidationFrom(&n.ContentValidation.NodeContentValidationFields)
						}
						if n.ApprovalStatus != nil {
							st.approval = approvalStatusFrom(&n.ApprovalStatus.NodeApprovalStatusFields)
						}
						stamps.byID[n.Id] = st
					}
				}
				return out, nil
			case err == nil:
				return nil, exitcode.Newf(exitcode.Error, "the server returned no result for the node approval read")
			case isUnknownFieldErr(err, "approvalStatus") || isUnknownFieldErr(err, "NodeApprovalStatus") ||
				isUnknownFieldErr(err, "NodeApproval"):
				stamps.approvalUnsupported = true
			case isUnknownFieldErr(err, "authorship") || isUnknownFieldErr(err, "contentValidation") ||
				isUnknownFieldErr(err, "NodeAuthorship") || isUnknownFieldErr(err, "NodeContentValidationStatus"):
				stamps.approvalUnsupported = true
				stamps.unsupported = true
			case isUnknownFieldErr(err, "revision") || isUnknownFieldErr(err, "nodeBatch"):
				stamps.approvalUnsupported = true
				stamps.unsupported = true
				no = true
				return nil, nil
			default:
				return nil, api.MapError(err)
			}
		}
		// The stamped read first (cli#752). A server that predates the stamps
		// refuses them by name, and the plain read below still gets the
		// revisions: losing the stamps must not cost the revision.
		if !stamps.unsupported {
			resp, err := gen.NodeLiveRevisionsStamped(cmd.Context(), client, refs, memory, prefix)
			switch {
			case err == nil && resp.NodeBatch != nil:
				yes = true
				out := &gen.NodeLiveRevisionsNodeBatchNodeBatchResult{
					Truncated: resp.NodeBatch.Truncated, Omitted: resp.NodeBatch.Omitted, Unavailable: resp.NodeBatch.Unavailable,
				}
				for _, n := range resp.NodeBatch.Nodes {
					if n == nil {
						continue
					}
					revs[n.Id] = n.Revision
					if stamps.byID != nil {
						st := nodeStamps{}
						if n.Authorship != nil {
							st.authorship = authorshipFrom(&n.Authorship.NodeAuthorshipFields)
						}
						if n.ContentValidation != nil {
							st.validation = contentValidationFrom(&n.ContentValidation.NodeContentValidationFields)
						}
						stamps.byID[n.Id] = st
					}
				}
				return out, nil
			case err == nil:
				return nil, exitcode.Newf(exitcode.Error, "the server returned no result for the node revision read")
			case isUnknownFieldErr(err, "authorship") || isUnknownFieldErr(err, "contentValidation") ||
				isUnknownFieldErr(err, "NodeAuthorship") || isUnknownFieldErr(err, "NodeContentValidationStatus"):
				stamps.unsupported = true
			case isUnknownFieldErr(err, "revision") || isUnknownFieldErr(err, "nodeBatch"):
				// Predates revisions, so predates the stamps too.
				stamps.unsupported = true
				no = true
				return nil, nil
			default:
				return nil, api.MapError(err)
			}
		}
		resp, err := gen.NodeLiveRevisions(cmd.Context(), client, refs, memory, prefix)
		if err != nil {
			// A server without nodeBatch at all predates revisions too; the
			// single-ref content read (GetNode) still works there, so neither
			// refusal may cost it the read (@copilot on #724).
			if isUnknownFieldErr(err, "revision") || isUnknownFieldErr(err, "nodeBatch") {
				no = true
				return nil, nil
			}
			return nil, api.MapError(err)
		}
		if resp.NodeBatch == nil {
			return nil, exitcode.Newf(exitcode.Error, "the server returned no result for the node revision read")
		}
		yes = true
		for _, n := range resp.NodeBatch.Nodes {
			if n != nil {
				revs[n.Id] = n.Revision
			}
		}
		return resp.NodeBatch, nil
	}
	// byRefs reads the revisions of refs, following a truncated answer through
	// its omitted refs until none remain (#1185): the server caps a nodeBatch
	// response by content bytes whatever the query selects, so a revision-only
	// read of several large nodes comes back truncated, and a node it left out
	// is not a node that changed. A pass that omits as many refs as it was
	// asked for read nothing, and repeating it would never end.
	byRefs := func(refs []string) error {
		for start := 0; start < len(refs); start += api.NodeBatchCap {
			end := min(start+api.NodeBatchCap, len(refs))
			pending := refs[start:end]
			for len(pending) > 0 {
				r, err := ask(pending, nil, nil)
				if err != nil {
					return err
				}
				if r == nil || !r.Truncated || len(r.Omitted) == 0 {
					break
				}
				if len(r.Omitted) >= len(pending) {
					return exitcode.Newf(exitcode.Error, "the server made no progress reading node revisions: it left out all %d requested nodes", len(pending))
				}
				pending = r.Omitted
			}
		}
		return nil
	}
	if sel.prefix != nil {
		r, err := ask(nil, sel.memory, sel.prefix)
		if err != nil {
			return nil, supportUnknown, err
		}
		if r != nil && r.Truncated {
			if err := byRefs(r.Omitted); err != nil {
				return nil, supportUnknown, err
			}
		}
	} else if err := byRefs(sel.refs); err != nil {
		return nil, supportUnknown, err
	}
	switch {
	case yes && no:
		return nil, supportMixed, nil
	case yes:
		return revs, supportYes, nil
	case no:
		return nil, supportNo, nil
	}
	return revs, supportUnknown, nil
}

// fetchNodeByID reads one node by its resolved id; ref is what the caller
// typed, for the not-found message. raw selects the stored body (GetNodeRaw)
// over the rendered one (#736); both return the same node type.
//
// "not found or not readable by you": since cor:api:140:03 a node in a memory
// the caller cannot open answers exactly like a missing one.
func fetchNodeByID(cmd *cobra.Command, client graphql.Client, id, ref string, raw bool) (*gen.GetNodeNode, error) {
	var node *gen.GetNodeNode
	if raw {
		resp, err := gen.GetNodeRaw(cmd.Context(), client, id)
		if err != nil {
			return nil, api.MapError(err)
		}
		node = resp.Node
	} else {
		resp, err := gen.GetNode(cmd.Context(), client, id)
		if err != nil {
			return nil, api.MapError(err)
		}
		node = resp.Node
	}
	if node == nil {
		return nil, exitcode.Newf(exitcode.NotFound, "node %q not found, or not readable by you", ref)
	}
	return node, nil
}

// fetchNode resolves a node reference (a full URN, or a bare loc within
// memory) and returns the full node.
func fetchNode(cmd *cobra.Command, client graphql.Client, memory, ref string) (*gen.GetNodeNode, error) {
	id, err := cmdutil.ResolveNodeRef(cmd, client, memory, ref)
	if err != nil {
		return nil, err
	}
	resp, err := gen.GetNode(cmd.Context(), client, id)
	if err != nil {
		return nil, api.MapError(err)
	}
	if resp.Node == nil {
		return nil, exitcode.Newf(exitcode.NotFound, "node %q not found", ref)
	}
	return resp.Node, nil
}

// nodeStamps is one node's stamps from a revision probe (cli#752).
type nodeStamps struct {
	authorship *authorshipDTO
	validation *contentValidationDTO
	approval   *approvalStatusDTO
	mint       *mintStatusDTO
}

// stampReads carries what the revision probes learned about the stamps:
// whether the server supports them at all, and — for the probe that is
// currently collecting (the AFTER probe; byID is nil otherwise) — each
// node's stamps.
type stampReads struct {
	unsupported         bool
	approvalUnsupported bool
	mintUnsupported     bool
	byID                map[string]nodeStamps
}

// apply sets each DTO's stamps from the after-probe. On a server without them
// both stay nil: contentValidation: null is the "can't say" answer.
func (s *stampReads) apply(dtos []*nodeDetailDTO) {
	if s.unsupported {
		return
	}
	for _, d := range dtos {
		if st, ok := s.byID[d.ID]; ok {
			d.Authorship, d.ContentValidation = st.authorship, st.validation
			d.ApprovalStatus = st.approval
			d.MintStatus = st.mint
		}
	}
}
