package node

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/Khan/genqlient/graphql"
	"github.com/bmatcuk/doublestar/v4"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// treeImportOpts carries the recursive-mode (`-r`) inputs — mapping a local
// directory tree onto a memory subtree (dirs→branch nodes, text files→leaf
// nodes, hierarchy→parent→child `contains` edges). See
// docs/plans/node-import-recursive.md.
type treeImportOpts struct {
	dir         string
	memory      string
	under       string // loc prefix prepended to the whole tree
	nodeType    string
	onConflict  string // "error" | "skip"
	include     []string
	exclude     []string
	hidden      bool
	maxFileSize int64
	dryRun      bool
}

// planNode is one node the walk decided to create, before any mutation. A
// branch (directory) carries children; a leaf (text file) carries content.
// content on a branch is a folded README/index (see foldNames).
type planNode struct {
	loc      string
	name     string // display name: filename with extension, or dir name
	kind     string // "branch" | "leaf"
	content  string
	relPath  string // path relative to the import root, for reporting
	children []*planNode
}

// skipEntry / collisionEntry / createdNodeDTO are the stable --json shapes.
type skipEntry struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type collisionEntry struct {
	Path string `json:"path"`
	Loc  string `json:"loc"`
}

type createdNodeDTO struct {
	Loc    string `json:"loc"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	NodeID string `json:"nodeId"`
}

// importTreeSummaryDTO is the stable --json shape for a recursive import. `mode`
// discriminates it from the restore/content shapes.
type importTreeSummaryDTO struct {
	Mode         string           `json:"mode"` // always "tree"
	Memory       string           `json:"memory"`
	Root         string           `json:"root"`
	DryRun       bool             `json:"dryRun"`
	Created      []createdNodeDTO `json:"created"`
	Existing     []string         `json:"existing"`   // locs already present (--on-conflict skip)
	Unresolved   []string         `json:"unresolved"` // skipped locs whose id couldn't be resolved (parent edge dropped)
	Skipped      []skipEntry      `json:"skipped"`    // files omitted during planning
	Collisions   []collisionEntry `json:"collisions"`
	EdgesWired   int              `json:"edgesWired"`
	NodesCreated int              `json:"nodesCreated"`
}

// foldNames are directory landing docs whose content folds into the directory's
// branch node instead of becoming a separate child (highest priority first).
var foldNames = []string{"readme.md", "readme.markdown", "index.md"}

func foldRank(name string) int {
	l := strings.ToLower(name)
	for i, f := range foldNames {
		if l == f {
			return i
		}
	}
	return -1
}

// runImportTree walks o.dir, builds the whole plan (slugify + collisions +
// README fold + filters) BEFORE any write, then creates the nodes bottom-up so
// each directory node can carry its children's `contains` edges inline (zero
// extra round-trips, no resolveUrn lag).
func runImportTree(cmd *cobra.Command, f *cmdutil.Factory, o treeImportOpts) error {
	info, err := os.Stat(o.dir)
	if err != nil {
		return exitcode.Newf(exitcode.Usage, "reading %s: %v", o.dir, err)
	}
	if !info.IsDir() {
		return exitcode.Newf(exitcode.Usage, "%s is not a directory — recursive import (-r) expects a directory", o.dir)
	}
	if o.memory == "" {
		return exitcode.Newf(exitcode.Usage, "-m/--memory is required")
	}
	if o.under != "" {
		if err := cmdutil.ValidateURNPath("--under", o.under); err != nil {
			return err
		}
	}
	if o.onConflict != "error" && o.onConflict != "skip" {
		return exitcode.Newf(exitcode.Usage, "--on-conflict must be error or skip (got %q)", o.onConflict)
	}
	// Validate globs up front — a malformed pattern that Match() silently treats
	// as "no match" would make a filter appear to work while dropping everything.
	for _, pat := range append(append([]string{}, o.include...), o.exclude...) {
		if !doublestar.ValidatePattern(pat) {
			return exitcode.Newf(exitcode.Usage, "invalid glob pattern %q", pat)
		}
	}

	abs, err := filepath.Abs(o.dir)
	if err != nil {
		return exitcode.Newf(exitcode.Usage, "resolving %s: %v", o.dir, err)
	}
	origBase := filepath.Base(abs)
	base := slugifyAtom(origBase)
	rootLoc := base
	if o.under != "" {
		rootLoc = o.under + ":" + base
	}

	res := &planResult{skipped: []skipEntry{}, collisions: []collisionEntry{}}
	p := &planner{o: o, res: res}
	// The reporting/glob path is relative to the import ROOT (empty here), so a
	// documented root-relative glob like `--include '*.md'` matches `README.md`,
	// not `<basename>/README.md`.
	root, err := p.planDir(o.dir, "", rootLoc, origBase)
	if err != nil {
		return err
	}
	// Every loc is validated before a single mutation, so a bad slug fails with
	// zero side effects.
	if err := validateLocs(root); err != nil {
		return err
	}

	if o.dryRun {
		created := []createdNodeDTO{}
		edges := collectDryRun(root, &created)
		dto := importTreeSummaryDTO{
			Mode: "tree", Memory: o.memory, Root: rootLoc, DryRun: true,
			Created: created, Existing: []string{}, Unresolved: []string{}, Skipped: res.skipped,
			Collisions: res.collisions, EdgesWired: edges, NodesCreated: len(created),
		}
		return emitTreeSummary(f, dto, root)
	}

	client, err := f.GraphQLClient()
	if err != nil {
		return err
	}
	ex := &treeExecutor{
		ctx: cmd.Context(), client: client,
		memory: o.memory, nodeType: o.nodeType, onConflict: o.onConflict,
		created: []createdNodeDTO{}, existing: []string{}, unresolved: []string{},
	}
	_, createErr := ex.create(root)

	dto := importTreeSummaryDTO{
		Mode: "tree", Memory: o.memory, Root: rootLoc,
		Created: ex.created, Existing: ex.existing, Unresolved: ex.unresolved, Skipped: res.skipped,
		Collisions: res.collisions, EdgesWired: ex.edgesWired, NodesCreated: len(ex.created),
	}
	// A mid-tree create failure is partial — emit what landed, then surface the
	// error (non-zero exit) so a caller doesn't read partial as complete.
	if emitErr := emitTreeSummary(f, dto, root); emitErr != nil && createErr == nil {
		return emitErr
	}
	return createErr
}

// planResult accumulates the non-node outcomes of the walk (skipped files,
// renamed collisions) as the plan tree is built.
type planResult struct {
	skipped    []skipEntry
	collisions []collisionEntry
}

type planner struct {
	o   treeImportOpts
	res *planResult
}

// planDir builds the plan subtree for absDir, whose branch node is `loc` /
// `displayName` and whose reporting path is relPath.
func (p *planner) planDir(absDir, relPath, loc, displayName string) (*planNode, error) {
	node := &planNode{loc: loc, name: displayName, kind: "branch", relPath: relPath}

	entries, err := os.ReadDir(absDir)
	if err != nil {
		return nil, exitcode.Newf(exitcode.Usage, "reading %s: %v", absDir, err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	// Pick the fold winner by name+filters first (its content becomes this
	// branch's body rather than a child).
	foldName, foldBest := "", len(foldNames)
	for _, e := range entries {
		if e.IsDir() || !e.Type().IsRegular() {
			continue
		}
		n := e.Name()
		rel := filepath.ToSlash(filepath.Join(relPath, n))
		if p.skipReason(n, rel, false, sizeOf(e)) != "" {
			continue
		}
		if r := foldRank(n); r >= 0 && r < foldBest {
			foldBest, foldName = r, n
		}
	}

	type kidSpec struct {
		isDir          bool
		abs, rel, orig string
		atom, content  string
	}
	var kids []*kidSpec

	for _, e := range entries {
		n := e.Name()
		rel := filepath.ToSlash(filepath.Join(relPath, n))
		abs := filepath.Join(absDir, n)
		if reason := p.skipReason(n, rel, e.IsDir(), sizeOf(e)); reason != "" {
			p.res.skipped = append(p.res.skipped, skipEntry{Path: rel, Reason: reason})
			continue
		}
		if e.IsDir() {
			kids = append(kids, &kidSpec{isDir: true, abs: abs, rel: rel, orig: n, atom: slugifyAtom(n)})
			continue
		}
		// Only regular files become nodes. A symlink (its Type carries
		// ModeSymlink even when it points at a file) could pull in content from
		// outside the import root, and a FIFO/device/socket would block or hang
		// os.ReadFile — skip both, reported.
		if !e.Type().IsRegular() {
			p.res.skipped = append(p.res.skipped, skipEntry{Path: rel, Reason: "irregular"})
			continue
		}
		data, rerr := os.ReadFile(abs)
		if rerr != nil {
			p.res.skipped = append(p.res.skipped, skipEntry{Path: rel, Reason: "unreadable"})
			continue
		}
		if !isText(data) {
			p.res.skipped = append(p.res.skipped, skipEntry{Path: rel, Reason: "binary"})
			continue
		}
		if n == foldName {
			node.content = string(data)
			continue
		}
		kids = append(kids, &kidSpec{isDir: false, abs: abs, rel: rel, orig: n, atom: slugifyAtom(stripExt(n)), content: string(data)})
	}

	// Resolve loc-atom collisions among siblings (dirs and files share the
	// namespace); assignment is deterministic in the sorted order. The suffix
	// advances until it lands on an atom no sibling already owns — a literal
	// `setup-2.md` beside two `setup.*` must not steal the renamed `setup`'s slot.
	used := map[string]bool{}
	for _, k := range kids {
		if !used[k.atom] {
			used[k.atom] = true
			continue
		}
		for i := 2; ; i++ {
			cand := fmt.Sprintf("%s-%d", k.atom, i)
			if !used[cand] {
				used[cand] = true
				p.res.collisions = append(p.res.collisions, collisionEntry{Path: k.rel, Loc: loc + ":" + cand})
				k.atom = cand
				break
			}
		}
	}

	for _, k := range kids {
		childLoc := loc + ":" + k.atom
		if k.isDir {
			cn, cerr := p.planDir(k.abs, k.rel, childLoc, k.orig)
			if cerr != nil {
				return nil, cerr
			}
			node.children = append(node.children, cn)
		} else {
			node.children = append(node.children, &planNode{loc: childLoc, name: k.orig, kind: "leaf", content: k.content, relPath: k.rel})
		}
	}
	return node, nil
}

// skipReason returns why an entry is excluded, or "" to keep it. Directories
// are only pruned by name/exclude filters (never by --include or size), so the
// walk still descends to find included files deeper in the tree.
func (p *planner) skipReason(name, rel string, isDir bool, size int64) string {
	if name == ".git" {
		return "vcs"
	}
	if strings.HasPrefix(name, ".") && !p.o.hidden {
		return "hidden"
	}
	for _, ex := range p.o.exclude {
		if ok, _ := doublestar.Match(ex, rel); ok {
			return "excluded"
		}
	}
	if isDir {
		return ""
	}
	if len(p.o.include) > 0 {
		matched := false
		for _, in := range p.o.include {
			if ok, _ := doublestar.Match(in, rel); ok {
				matched = true
				break
			}
		}
		if !matched {
			return "not-included"
		}
	}
	if size > p.o.maxFileSize {
		return "too-large"
	}
	return ""
}

// treeExecutor creates the plan tree bottom-up, attaching each branch's
// children as inline `contains` edges.
type treeExecutor struct {
	ctx                          context.Context
	client                       graphql.Client
	memory, nodeType, onConflict string
	created                      []createdNodeDTO
	existing                     []string
	unresolved                   []string
	edgesWired                   int
}

// create writes n after its children (post-order), so their ids are known and
// can ride along as this node's outgoing `contains` edges. Returns n's node id.
func (e *treeExecutor) create(n *planNode) (string, error) {
	var edges []*gen.NodeEdgeInput
	for _, c := range n.children {
		id, err := e.create(c)
		if err != nil {
			return "", err
		}
		if id != "" {
			name := "contains"
			edges = append(edges, &gen.NodeEdgeInput{Name: &name, TargetId: id})
		}
	}

	input := &gen.CreateNodeInput{MemoryId: e.memory, Loc: n.loc, Name: n.name}
	if n.content != "" {
		input.Content = &n.content
	}
	if e.nodeType != "" {
		nt := e.nodeType
		input.NodeType = &nt
	}
	if len(edges) > 0 {
		input.Edges = edges
	}

	resp, err := gen.CreateNode(e.ctx, e.client, input)
	if err != nil {
		if api.HasErrorCode(err, "NodeLocConflictError") {
			if e.onConflict == "skip" {
				return e.skipExisting(n, edges)
			}
			return "", exitcode.Newf(exitcode.Conflict,
				"a node already exists at %s — re-run with --on-conflict skip to import only the missing nodes (some nodes may already have been created)", n.loc)
		}
		return "", api.MapError(err)
	}
	e.created = append(e.created, createdNodeDTO{Loc: resp.CreateNode.Loc, Name: n.name, Kind: n.kind, NodeID: resp.CreateNode.Id})
	e.edgesWired += len(edges)
	return resp.CreateNode.Id, nil
}

// skipExisting handles a node whose loc already exists under --on-conflict skip:
// it resolves the pre-existing node's id (so its parent's `contains` edge can
// still point at it), and — because a skipped BRANCH node is never rewritten —
// wires `contains` edges from it to the children this run just created, so those
// children aren't left orphaned under a pre-existing directory. A node whose id
// can't be resolved is recorded under `unresolved` rather than silently dropping
// the parent's edge.
func (e *treeExecutor) skipExisting(n *planNode, edges []*gen.NodeEdgeInput) (string, error) {
	e.existing = append(e.existing, n.loc)
	id, found := e.resolveExistingID(n.loc)
	if !found {
		e.unresolved = append(e.unresolved, n.loc)
		return "", nil
	}
	// Best-effort: an edge that already exists (a prior run, or a child that
	// also pre-existed) is rejected server-side on its derived loc — ignore it.
	for _, ed := range edges {
		if _, werr := gen.CreateEdge(e.ctx, e.client, id, ed.TargetId, "contains", nil, nil, nil, nil, nil, nil); werr == nil {
			e.edgesWired++
		}
	}
	return id, nil
}

// resolveExistingID looks up the id of a node already present at loc. It first
// composes the exact node URN (works for an org::memory ref); a raw memory id
// can't form a URN, so it falls back to a loc-prefix listing and an exact-loc
// match (mirroring import.go's nodeExists). A pre-existing node has no creation
// lag, so no retry is needed.
func (e *treeExecutor) resolveExistingID(loc string) (string, bool) {
	if urn := cmdutil.NodeURN(e.memory, loc); urn != "" {
		if resp, err := gen.ResolveUrn(e.ctx, e.client, urn); err == nil && resp.ResolveUrn != nil && resp.ResolveUrn.Kind == "node" {
			return resp.ResolveUrn.Id, true
		}
	}
	limit := 200
	filter := &gen.NodeFilter{MemoryIds: []string{e.memory}, LocPrefix: &loc}
	sortLoc := gen.NodeSortLoc
	for offset := 0; ; offset += limit {
		off := offset
		page, err := api.FindNodes(e.ctx, e.client, nil, nil, filter, &sortLoc, nil, &limit, &off)
		if err != nil {
			return "", false
		}
		for _, nd := range page.Nodes {
			if nd != nil && nd.Loc == loc {
				return nd.Id, true
			}
		}
		if len(page.Nodes) < limit {
			return "", false
		}
	}
}

// collectDryRun mirrors create's post-order walk without mutating: it appends a
// would-create entry (no nodeId) per node and returns the edge count.
func collectDryRun(n *planNode, out *[]createdNodeDTO) int {
	edges := 0
	for _, c := range n.children {
		edges += collectDryRun(c, out)
	}
	*out = append(*out, createdNodeDTO{Loc: n.loc, Name: n.name, Kind: n.kind})
	return edges + len(n.children)
}

func validateLocs(n *planNode) error {
	if err := cmdutil.ValidateURNPath("loc", n.loc); err != nil {
		return err
	}
	for _, c := range n.children {
		if err := validateLocs(c); err != nil {
			return err
		}
	}
	return nil
}

// slugifyAtom maps an arbitrary path component to a legal loc atom (slugRule:
// 1-64 chars of [A-Za-z0-9._-], starting and ending alphanumeric). Illegal-char
// runs collapse to a single '-'; the result is trimmed to an alphanumeric
// boundary and capped at 64.
func slugifyAtom(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		switch {
		case isLowerAlnum(r) || r == '.' || r == '_' || r == '-':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := trimToAlnum(b.String())
	if len(out) > 64 {
		out = trimToAlnum(out[:64])
	}
	if out == "" {
		return "n" // defensive: a name of only illegal chars still needs a loc
	}
	return out
}

func trimToAlnum(s string) string {
	return strings.TrimFunc(s, func(r rune) bool { return !isLowerAlnum(r) })
}

// isLowerAlnum reports whether r is a lowercase letter or digit — the legal
// start/end char of a loc atom (slugifyAtom lowercases first).
func isLowerAlnum(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
}

// stripExt drops a file's final extension for its loc atom (the display name
// keeps it). A dotfile whose whole name is the "extension" is left as-is.
func stripExt(name string) string {
	ext := filepath.Ext(name)
	if ext == "" || ext == name {
		return name
	}
	return strings.TrimSuffix(name, ext)
}

// isText reports whether data looks like UTF-8 text — no NUL byte in the first
// 8 KiB and valid UTF-8. An empty file counts as text.
func isText(data []byte) bool {
	const sniff = 8192
	head := data
	if len(head) > sniff {
		head = head[:sniff]
	}
	if bytes.IndexByte(head, 0) >= 0 {
		return false
	}
	return utf8.Valid(head)
}

func sizeOf(e os.DirEntry) int64 {
	info, err := e.Info()
	if err != nil {
		return 0
	}
	return info.Size()
}

func emitTreeSummary(f *cmdutil.Factory, dto importTreeSummaryDTO, root *planNode) error {
	return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		if dto.DryRun {
			fmt.Fprintf(w, "[dry-run] would create %d node(s) under %s in %s (%d edge(s))\n",
				dto.NodesCreated, dto.Root, dto.Memory, dto.EdgesWired)
			printTree(w, root, "")
		} else {
			fmt.Fprintf(w, "✓ imported %s into %s — %d node(s), %d edge(s)\n",
				dto.Root, dto.Memory, dto.NodesCreated, dto.EdgesWired)
		}
		if len(dto.Existing) > 0 {
			fmt.Fprintf(w, "  %d node(s) already existed (left as-is):\n", len(dto.Existing))
			for _, l := range dto.Existing {
				fmt.Fprintf(w, "    - %s\n", l)
			}
		}
		if len(dto.Unresolved) > 0 {
			fmt.Fprintf(w, "  %d existing node(s) could not be resolved — their parent edge was dropped:\n", len(dto.Unresolved))
			for _, l := range dto.Unresolved {
				fmt.Fprintf(w, "    - %s\n", l)
			}
		}
		if len(dto.Collisions) > 0 {
			fmt.Fprintf(w, "  %d loc collision(s) renamed:\n", len(dto.Collisions))
			for _, c := range dto.Collisions {
				fmt.Fprintf(w, "    - %s → %s\n", c.Path, c.Loc)
			}
		}
		if len(dto.Skipped) > 0 {
			fmt.Fprintf(w, "  %d file(s)/dir(s) skipped:\n", len(dto.Skipped))
			for _, s := range dto.Skipped {
				fmt.Fprintf(w, "    - %s (%s)\n", s.Path, s.Reason)
			}
		}
		return nil
	})
}

func printTree(w io.Writer, n *planNode, indent string) {
	marker := ""
	if n.kind == "branch" {
		marker = "/"
	}
	fmt.Fprintf(w, "%s%s%s  (%s)\n", indent, n.name, marker, n.loc)
	for _, c := range n.children {
		printTree(w, c, indent+"  ")
	}
}
