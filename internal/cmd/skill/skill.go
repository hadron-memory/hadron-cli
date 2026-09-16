// Package skill implements `hadron skill ...` — the maintenance surface for
// the skill files exported from runnable task nodes (hadron-cli#580, design
// in docs/plans/skill-command-group.md). Three verbs, no more: `lint` reads
// the corpus and touches no disk, `status` reads both and writes nothing,
// `export` is the only writer. This file carries what they share: selecting
// the declaring nodes, reading them RAW through the batch read, and resolving
// the export prefix each memory's owner decides.
package skill

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Khan/genqlient/graphql"
	urnlib "github.com/hadron-memory/urn-lib-go"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/api/gqltypes"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/nodedoc"
	"github.com/hadron-memory/hadron-cli/internal/skilldoc"
)

// Short aliases for genqlient's nested generated names.
type (
	batchResult = gen.NodeBatchNodeBatchNodeBatchResult
	batchNode   = gen.NodeBatchNodeBatchNodeBatchResultNodesNode
)

// listPageSize bounds one page of the shallow id scan; paged to exhaustion
// because the server caps an unbounded listing at one default page (#23).
const listPageSize = 500

// NewCmdSkill returns the `hadron skill` command group.
func NewCmdSkill(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "skill <command>",
		Aliases: []string{"skills"},
		Short:   "Maintain the skills exported from runnable task nodes",
		Long: `Maintain the skill surface exported from Hadron task nodes.

A task node opts in by declaring properties.skill — an object whose
"description" is the trigger text a skill host matches against. The node is
the source and the skill file is a build artifact: the skill's NAME is never
stored, it is derived at export as <prefix> + the loc below "tasks:" with
":" replaced by "-" (tasks:create-release-tag → hadron-create-release-tag).
The prefix is the owning org's Organization.skillPrefix for an org-owned
memory and "hadron-" for a user-owned one; --prefix overrides either.

  lint    check declaring nodes against the corpus rules (no disk)`,
	}
	cmd.AddCommand(newCmdLint(f))
	return cmd
}

// memoryInfo is the slice of a memory the skill commands need: identity, who
// owns it, and the prefix that owner chose.
type memoryInfo struct {
	ID             string
	URN            string
	OrganizationID *string
	SkillPrefix    *string
}

// selectorFlags are the three mutually exclusive ways to name the nodes a
// verb works on. Exactly one is required: there is deliberately no
// active-memory fallback, because a verb that silently targets whatever memory
// happened to be active is how a customer's tasks end up on the wrong disk.
type selectorFlags struct {
	memories []string
	all      bool
	nodes    []string
}

func (s *selectorFlags) register(cmd *cobra.Command) {
	cmd.Flags().StringArrayVarP(&s.memories, "memory", "m", nil, "memory to scan (repeatable): hrn:mem:<root>:<slug>, <root>::<slug>, or an id")
	cmd.Flags().BoolVar(&s.all, "all", false, "every memory you can read: your orgs' memories, memories shared with you, and other orgs' PUBLIC memories (every class)")
	cmd.Flags().StringArrayVar(&s.nodes, "node", nil, "a specific node (repeatable): hrn:node:<root>:<slug>:<loc> or an id")
}

func (s *selectorFlags) validate() error {
	n := 0
	if len(s.memories) > 0 {
		n++
	}
	if s.all {
		n++
	}
	if len(s.nodes) > 0 {
		n++
	}
	if n != 1 {
		return exitcode.Newf(exitcode.Usage, "specify exactly one of -m/--memory <memory>..., --all, or --node <ref>...")
	}
	return nil
}

// selection is what a verb works on once the selector is resolved: the
// declaring nodes read RAW, the refs that listed but could not be read, and
// the memory each node belongs to.
type selection struct {
	nodes       []*batchNode
	unavailable []string
	memories    map[string]*memoryInfo // by memory id
}

// selectNodes resolves the selector to nodes + memories. Discovery is
// server-side: findNodes with a `where` predicate on properties.skill /
// properties.claudeSkill existing (#719) — deliberately NOT an isRunnable
// filter, which would hide exactly the nodes the not-runnable lint rule exists
// to catch. Bodies come through nodeBatch, the one read that returns content
// raw (a single-ref read compiles Mustache and blanks placeholders — D6).
func selectNodes(cmd *cobra.Command, client graphql.Client, sel *selectorFlags) (*selection, error) {
	out := &selection{memories: map[string]*memoryInfo{}}

	var mems []*memoryInfo
	switch {
	case sel.all:
		all, err := allMemories(cmd, client)
		if err != nil {
			return nil, err
		}
		mems = all
	case len(sel.memories) > 0:
		for _, ref := range sel.memories {
			m, err := lookupMemory(cmd, client, ref)
			if err != nil {
				return nil, err
			}
			mems = append(mems, m)
		}
	}

	var refs []string
	if len(mems) > 0 {
		ids := make([]string, 0, len(mems))
		for _, m := range mems {
			out.memories[m.ID] = m
			ids = append(ids, m.ID)
		}
		// One filtered listing across every selected memory, not one per
		// memory: NodeFilter.memoryIds takes the whole set and paging is
		// independent of it, so `--all` over 50+ memories costs a page or two
		// instead of 50+ round trips (most of which returned nothing).
		listed, err := listDeclaredIDs(cmd, client, ids)
		if err != nil {
			return nil, err
		}
		refs = append(refs, listed...)
	}
	for _, ref := range sel.nodes {
		canon, err := canonicalNodeArg(ref)
		if err != nil {
			return nil, err
		}
		refs = append(refs, canon)
	}
	refs = dedupe(refs) // a ref named twice must not lint twice, nor collide with itself
	if len(refs) == 0 {
		return out, nil
	}

	nodes, unavailable, err := fetchNodes(cmd, client, refs)
	if err != nil {
		return nil, err
	}
	out.nodes = nodes
	out.unavailable = unavailable

	// --node refs name their memory only through the node; resolve any memory
	// the listing pass did not already load (memory(ref:) takes a PK).
	for _, n := range nodes {
		if _, ok := out.memories[n.MemoryId]; ok {
			continue
		}
		m, err := lookupMemory(cmd, client, n.MemoryId)
		if err != nil {
			return nil, err
		}
		out.memories[m.ID] = m
	}
	sort.Slice(out.nodes, func(i, j int) bool { return out.nodes[i].Urn < out.nodes[j].Urn })
	return out, nil
}

// lookupMemory resolves a memory ref (URN in any accepted grammar, or a PK)
// through memory(ref:), which dispatches server-side.
func lookupMemory(cmd *cobra.Command, client graphql.Client, ref string) (*memoryInfo, error) {
	resp, err := gen.GetMemory(cmd.Context(), client, cmdutil.CanonicalMemoryRef(ref))
	if err != nil {
		return nil, api.MapError(err)
	}
	m := resp.Memory
	if m == nil {
		return nil, exitcode.Newf(exitcode.NotFound,
			"no memory found for %q — expected a memory id or a URN: hrn:mem:<root>:<slug>, the <root>::<slug> short form, or the legacy hrn:memory: prefix", ref)
	}
	info := &memoryInfo{ID: m.Id, URN: m.Urn, OrganizationID: m.OrganizationId}
	if m.Organization != nil {
		info.SkillPrefix = m.Organization.SkillPrefix
	}
	return info, nil
}

// allMemories lists every memory the caller can read — own-org, shared with
// them, and other orgs' PUBLIC memories — each drained through the shared
// api.CollectAll pager (which pages on the envelope's total, not on a
// page-length heuristic), every class in each, de-duplicated by id.
func allMemories(cmd *cobra.Command, client graphql.Client) ([]*memoryInfo, error) {
	// Every class, explicitly: a nil filter excludes agent-system memories by
	// default (memories.graphql), and a task declared in one would otherwise
	// be missed while `--all` reports a clean corpus — the same
	// all-clear-wider-than-the-read shape as an isRunnable scan (§4.1).
	all := &gen.MemoryFilter{MemoryClasses: gen.AllMemoryClass}
	pub := gen.MemoryVisibilityPublic
	public := &gen.MemoryFilter{MemoryClasses: gen.AllMemoryClass, Visibility: &pub}
	type item = gen.MemoriesMemoriesMemoriesPageItemsMemory
	listing := func(filter *gen.MemoryFilter) func(int, int) ([]*item, int, error) {
		return func(limit, offset int) ([]*item, int, error) {
			resp, err := gen.Memories(cmd.Context(), client, filter, &limit, &offset)
			if err != nil {
				return nil, 0, api.MapError(err)
			}
			if resp.Memories == nil {
				return nil, 0, nil
			}
			return resp.Memories.Items, resp.Memories.Total, nil
		}
	}
	seen := map[string]bool{}
	var out []*memoryInfo
	add := func(id, urn string, orgID *string, org interface{ GetSkillPrefix() *string }) {
		if seen[id] {
			return
		}
		seen[id] = true
		m := &memoryInfo{ID: id, URN: urn, OrganizationID: orgID}
		if org != nil {
			m.SkillPrefix = org.GetSkillPrefix()
		}
		out = append(out, m)
	}
	for _, filter := range []*gen.MemoryFilter{all, public} {
		items, err := api.CollectAll(listing(filter))
		if err != nil {
			return nil, err
		}
		for _, m := range items {
			var org interface{ GetSkillPrefix() *string }
			if m.Organization != nil {
				org = m.Organization
			}
			add(m.Id, m.Urn, m.OrganizationId, org)
		}
	}
	shared, err := api.CollectAll(func(limit, offset int) ([]*gen.MemoriesSharedWithMeMemoriesMemoriesPageItemsMemory, int, error) {
		resp, err := gen.MemoriesSharedWithMe(cmd.Context(), client, &limit, &offset, gen.AllMemoryClass)
		if err != nil {
			return nil, 0, api.MapError(err)
		}
		if resp.Memories == nil {
			return nil, 0, nil
		}
		return resp.Memories.Items, resp.Memories.Total, nil
	})
	if err != nil {
		return nil, err
	}
	for _, m := range shared {
		var org interface{ GetSkillPrefix() *string }
		if m.Organization != nil {
			org = m.Organization
		}
		add(m.Id, m.Urn, m.OrganizationId, org)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].URN < out[j].URN })
	return out, nil
}

// listDeclaredIDs pages the shallow listing of the declaring nodes —
// properties.skill or properties.claudeSkill present — across the given
// memories, to exhaustion. The page size (500) is under the server's
// findNodes clamp (GRAPH_PAGE_MAX 2000), so a short page really is the end;
// a request above the clamp would be silently cut and read as one.
func listDeclaredIDs(cmd *cobra.Command, client graphql.Client, memIDs []string) ([]string, error) {
	col := gqltypes.NodeWhereColumnProperties
	exists := true
	where := &gqltypes.NodeWhereInput{Or: []*gqltypes.NodeWhereInput{
		{Field: &col, Path: []string{"skill"}, Exists: &exists},
		{Field: &col, Path: []string{"claudeSkill"}, Exists: &exists},
	}}
	filter := &gen.NodeFilter{MemoryIds: memIDs, Where: where}
	sortBy := gen.NodeSortLoc
	var ids []string
	for offset := 0; ; offset += listPageSize {
		limit, off := listPageSize, offset
		page, err := api.FindNodes(cmd.Context(), client, nil, nil, filter, &sortBy, nil, &limit, &off)
		if err != nil {
			return nil, api.MapError(err)
		}
		for _, n := range page.Nodes {
			ids = append(ids, n.Id)
		}
		if len(page.Nodes) < listPageSize {
			return ids, nil
		}
	}
}

// fetchNodes reads full nodes in batches, re-queuing byte-cap spillover, and
// surfaces refs the server reported unavailable rather than dropping them.
func fetchNodes(cmd *cobra.Command, client graphql.Client, refs []string) ([]*batchNode, []string, error) {
	return api.CollectNodeBatch(refs, func(chunk []string) (*batchResult, error) {
		resp, err := gen.NodeBatch(cmd.Context(), client, chunk, nil, nil)
		if err != nil {
			return nil, api.MapError(err)
		}
		if resp.NodeBatch == nil {
			return nil, exitcode.Newf(exitcode.Error, "nodeBatch returned no result")
		}
		return resp.NodeBatch, nil
	})
}

// dedupe keeps the first occurrence of each ref, in order.
func dedupe(refs []string) []string {
	seen := map[string]bool{}
	out := refs[:0]
	for _, r := range refs {
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	return out
}

// canonicalNodeArg canonicalizes a --node value for nodeBatch, which takes an
// id or a fully-qualified URN. A bare loc (`tasks:foo`) or a half-qualified
// ref is refused HERE with the accepted forms named: sent through, the server
// fails the whole batch with a shape error that names the GraphQL field, not
// the flag (review:canonical-ref-handling). A colon-free token is a raw id and
// passes through untouched.
func canonicalNodeArg(ref string) (string, error) {
	canon := cmdutil.CanonicalNodeRef(ref)
	switch {
	case !strings.Contains(canon, ":"):
		return canon, nil // a raw id
	case !urnlib.HasSchemePrefix(canon):
		return "", exitcode.Newf(exitcode.Usage,
			"--node %q is not a fully-qualified node URN — expected hrn:node:<root>:<slug>:<loc> or a node id; a bare loc has no memory to resolve in (lint reads whole memories with -m)", ref)
	case urnlib.AssertFullyQualifiedUrn(canon, "node") != nil:
		// A scheme-prefixed ref of another KIND (hrn:mem:…, hrn:app:…) would
		// fail the whole batch server-side with an error naming the GraphQL
		// field; refuse it here, naming the flag (Codex on #589, round 6).
		return "", exitcode.Newf(exitcode.Usage,
			"--node %q is not a node URN — expected hrn:node:<root>:<slug>:<loc> or a node id", ref)
	}
	return canon, nil
}

// resolvePrefix decides a memory's export prefix (D7): an explicit override
// wins; a user-owned memory (no org) takes the platform's; an org-owned
// memory takes its org's chosen prefix. Known is false when the org has
// chosen none — lint reports that per memory (skilldoc.LintPrefixes) and
// export refuses; there is deliberately no client-side table to fall back on.
func resolvePrefix(m *memoryInfo, override string) skilldoc.Prefix {
	switch {
	case override != "":
		return skilldoc.Prefix{Value: override, Known: true}
	case m.OrganizationID == nil:
		return skilldoc.Prefix{Value: skilldoc.DefaultPrefix, Known: true}
	case m.SkillPrefix != nil && *m.SkillPrefix != "":
		return skilldoc.Prefix{Value: *m.SkillPrefix, Known: true}
	}
	return skilldoc.Prefix{}
}

// validatePrefixFlag applies the server's own prefix rule to an override, so
// --prefix cannot mint a name the org field could never hold. A --prefix that
// was GIVEN but is empty is refused rather than read as absent: an unset shell
// variable expands to "", and the server cannot tell that from an intent —
// the same reason `worker update` refuses an empty --prompt-override.
func validatePrefixFlag(cmd *cobra.Command, p string) error {
	if !cmd.Flags().Changed("prefix") {
		return nil
	}
	if p == "" {
		return exitcode.Newf(exitcode.Usage, "--prefix is empty — pass a prefix (e.g. hadron-, mm-) or omit the flag to use the org's")
	}
	if !skilldoc.ValidPrefix(p) {
		return exitcode.Newf(exitcode.Usage, "--prefix %q must be lowercase letters/digits ending in a hyphen (e.g. hadron-, mm-)", p)
	}
	return nil
}

// toSkillNode projects a batch node onto the contract's view of it. The
// properties JSON is decoded here, once, so the rules read a map.
func toSkillNode(n *batchNode, memURN string) skilldoc.Node {
	sn := skilldoc.Node{URN: n.Urn, Loc: n.Loc, MemoryURN: memURN}
	if n.IsRunnable != nil {
		sn.IsRunnable = *n.IsRunnable
	}
	if n.Content != nil {
		sn.Content = *n.Content
	}
	if props, ok := nodedoc.DecodeJSON(n.Properties).(map[string]any); ok {
		sn.Properties = props
	}
	return sn
}

// describeUnavailable is the shared wording for a ref the listing returned but
// the batch refused — "not found, or not readable by you", by the server's own
// merged envelope (cor:api:040); the split is deliberate and not ours to undo.
func describeUnavailable(ref string) string {
	return fmt.Sprintf("%s listed but could not be read (not found, or not readable by you) — skipped", ref)
}
