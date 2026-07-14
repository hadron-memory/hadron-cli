package spec

import (
	"fmt"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// lintMem is a stand-in memory URN for the corpus-lint tests; it qualifies the
// node refs in the inheritance-edge remedy message.
const lintMem = "acme.com::specs"

// cleanSpec builds a fully rubric-compliant spec node at loc, with a ToC
// edge to its parent.
func cleanSpec(t *testing.T, loc, title string) specNode {
	t.Helper()
	c := mustCit(t, loc)
	abs := "Abstract describing " + loc + " for semantic search."
	content := rubricBody(c, title)
	sn := specNode{
		Loc:         loc,
		Name:        specName(c, title),
		NodeType:    "info",
		Tags:        []string{"spec", "topic"},
		Abstract:    &abs,
		Content:     &content,
		DataVersion: "0.0.1",
	}
	if p, ok := c.Parent(); ok {
		sn.OutEdges = append(sn.OutEdges, specEdge{Name: "toc", Loc: p.Format()})
	}
	return sn
}

func hasRule(fs []lintFindingDTO, rule string) bool {
	for _, f := range fs {
		if f.Rule == rule {
			return true
		}
	}
	return false
}

func hasRuleFor(fs []lintFindingDTO, citation, rule string) bool {
	for _, f := range fs {
		if f.Citation == citation && f.Rule == rule {
			return true
		}
	}
	return false
}

func TestLintNodeClean(t *testing.T) {
	if fs := lintNode(cleanSpec(t, "msg:010:02", "W2")); len(fs) != 0 {
		t.Errorf("clean spec should have no findings, got %v", fs)
	}
}

func TestLintNodeProblems(t *testing.T) {
	bad := cleanSpec(t, "msg:010:02", "W2")
	bad.Name = "wrong name"
	bad.Abstract = nil
	empty := ""
	bad.Content = &empty
	bad.NodeType = "finding"
	fs := lintNode(bad)
	for _, want := range []string{"name-prefix", "nodetype-info", "abstract", "invalidates"} {
		if !hasRule(fs, want) {
			t.Errorf("expected %q finding; got %v", want, fs)
		}
	}
}

func TestLintNodePlaceholderAbstract(t *testing.T) {
	n := cleanSpec(t, "msg:010:02", "W2")
	ph := placeholderAbstract(mustCit(t, "msg:010:02"), "W2")
	n.Abstract = &ph
	if !hasRule(lintNode(n), "abstract") {
		t.Error("placeholder abstract should trip the abstract rule")
	}
}

func TestLintNodePlaceholderContractExempt(t *testing.T) {
	// #99 item 1: a feature :00 contract is co-scaffolded automatically with a
	// new feature. While it still carries its scaffold placeholder abstract the
	// author hasn't engaged it, so it's exempt from the rubric errors instead
	// of forcing a contract node nobody asked for.
	c := mustCit(t, "msg:010:00")
	if !c.IsContract() {
		t.Fatalf("%s should be a contract", c.Format())
	}
	abs := tierAbstract(c, "W-series general provisions")
	body := contractBody(c, "W-series general provisions")
	n := specNode{
		Loc:         c.Format(),
		Name:        specName(c, "W-series general provisions"),
		NodeType:    "info",
		Tags:        []string{"spec"},
		Abstract:    &abs,
		Content:     &body,
		DataVersion: "0.0.1",
	}
	fs := lintNode(n)
	if hasRule(fs, "abstract") || hasRule(fs, "invalidates") {
		t.Errorf("untouched placeholder contract must not trip rubric errors; got %v", fs)
	}
	if !hasRule(fs, "placeholder-contract") {
		t.Errorf("expected placeholder-contract info finding; got %v", fs)
	}
	for _, f := range fs {
		if f.Severity == sevError {
			t.Errorf("placeholder contract must yield no errors; got %v", f)
		}
	}
}

func TestLintNodeEngagedContractFullRubric(t *testing.T) {
	// Once the author replaces the placeholder abstract the contract is
	// engaged, and the full rubric applies again — a missing "what invalidates"
	// statement is flagged.
	c := mustCit(t, "msg:010:00")
	abs := "Shared definitions and defaults every W-series rule inherits."
	body := "# msg:010:00 — provisions\n\n## Provisions\n\nShared rules.\n"
	n := specNode{
		Loc:      c.Format(),
		Name:     specName(c, "provisions"),
		NodeType: "info",
		Tags:     []string{"spec"},
		Abstract: &abs,
		Content:  &body,
	}
	fs := lintNode(n)
	if hasRule(fs, "placeholder-contract") {
		t.Errorf("an engaged contract must not be treated as a placeholder; got %v", fs)
	}
	if !hasRule(fs, "invalidates") {
		t.Errorf("engaged contract missing 'what invalidates' should be flagged; got %v", fs)
	}
}

func TestLintNodeReportsAllRubricGapsAtOnce(t *testing.T) {
	// #99 item 2: every rubric gap for a node is reported in one pass, not
	// surfaced one-at-a-time across reruns.
	c := mustCit(t, "msg:010:02")
	body := "# msg:010:02 — W2\n\n## Definition\n\nx\n" // no "what invalidates"
	n := specNode{
		Loc:      "msg:010:02",
		Name:     specName(c, "W2"),
		NodeType: "info",
		Tags:     []string{"spec"},
		Abstract: nil, // missing abstract
		Content:  &body,
	}
	fs := lintNode(n)
	if !hasRule(fs, "abstract") || !hasRule(fs, "invalidates") {
		t.Errorf("both abstract and invalidates gaps must be reported together; got %v", fs)
	}
}

func TestLintNodeHeaderLight(t *testing.T) {
	// A module/feature header (level < 3) only gets the universal checks,
	// not the spec rubric (no abstract/invalidates requirement).
	header := specNode{Loc: "msg:010", Name: "msg:010 — W-series", NodeType: "info", Tags: []string{"spec", "p1"}}
	if fs := lintNode(header); len(fs) != 0 {
		t.Errorf("header node should pass the light checks, got %v", fs)
	}
}

func TestLintNodeHeaderMissingSpecTag(t *testing.T) {
	header := specNode{Loc: "msg:010", Name: "msg:010 — W-series", NodeType: "info", Tags: []string{"p1"}}
	fs := lintNode(header)
	if !hasRule(fs, "tag-spec") {
		t.Errorf("header node missing the spec tag should be flagged; got %v", fs)
	}
	if hasRule(fs, "abstract") || hasRule(fs, "invalidates") {
		t.Errorf("header node should still skip rule-level rubric checks; got %v", fs)
	}
}

func TestLintNodeUnavailable(t *testing.T) {
	fs := lintNode(specNode{Loc: "msg:010:02", Unavailable: true})
	if len(fs) != 1 || fs[0].Rule != "unavailable" || fs[0].Severity != sevError {
		t.Fatalf("unavailable listed node should produce one explicit error, got %v", fs)
	}
}

func TestLintCorpusInheritanceAndParent(t *testing.T) {
	nodes := []specNode{
		{Loc: "msg", Name: "msg — Messaging", NodeType: "info", Tags: []string{"spec", "p1"}},
		{Loc: "msg:010", Name: "msg:010 — W-series", NodeType: "info", Tags: []string{"spec", "p1"}},
		cleanSpec(t, "msg:010:00", "Shared contract"),
		cleanSpec(t, "msg:010:02", "W2"), // has ToC edge, but no inheritance edge to :00
	}
	fs := lintCorpus(nodes, "", lintMem)
	if !hasRuleFor(fs, "msg:010:02", "inheritance-edge") {
		t.Errorf("expected inheritance-edge warning on msg:010:02; got %v", fs)
	}
	if hasRule(fs, "parent-exists") {
		t.Errorf("no parent should be missing; got %v", fs)
	}
	// #35: the message must name the exact, copy-pasteable remedy with
	// fully-qualified node refs (the manual back-wire an author would run).
	msg := messageFor(fs, "msg:010:02", "inheritance-edge")
	for _, want := range []string{
		"hadron edge add",
		"--from acme.com::specs::msg:010:02",
		"--to acme.com::specs::msg:010:00",
		inheritEdgeLabel,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("inheritance-edge message must contain %q; got %q", want, msg)
		}
	}
}

// messageFor returns the message of the first finding matching (citation, rule).
func messageFor(fs []lintFindingDTO, citation, rule string) string {
	for _, f := range fs {
		if f.Citation == citation && f.Rule == rule {
			return f.Message
		}
	}
	return ""
}

func equalIntSlices(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestLintCorpusOrphanParent(t *testing.T) {
	fs := lintCorpus([]specNode{cleanSpec(t, "msg:010:02", "W2")}, "", lintMem)
	if !hasRuleFor(fs, "msg:010:02", "parent-exists") {
		t.Errorf("expected parent-exists error for orphan; got %v", fs)
	}
}

func TestLintCorpusScopedRootParentAboveScope(t *testing.T) {
	// Regression for #21: a --product/--module scoped lint (scopeRoot
	// "cor:acl") must not flag the scope's own root for its parent (cor, the
	// product root) living above the scanned subtree.
	nodes := []specNode{
		{Loc: "cor:acl", Name: "cor:acl — Access control", NodeType: "info", Tags: []string{"spec", "p0"}},
		{Loc: "cor:acl:010", Name: "cor:acl:010 — Roles", NodeType: "info", Tags: []string{"spec", "p1"}},
		cleanSpec(t, "cor:acl:010:02", "Role check"),
	}
	if fs := lintCorpus(nodes, "cor:acl", lintMem); hasRule(fs, "parent-exists") {
		t.Errorf("scoped lint must not flag the scope root's above-scope parent; got %v", fs)
	}
	// Whole-corpus semantics (scopeRoot "") still treat the same set as an
	// orphan: cor:acl's parent cor is genuinely absent.
	if fs := lintCorpus(nodes, "", lintMem); !hasRuleFor(fs, "cor:acl", "parent-exists") {
		t.Errorf("unscoped lint should flag cor:acl's missing parent; got %v", fs)
	}
}

func TestLintCorpusScopedMissingIntermediate(t *testing.T) {
	// A genuinely dangling intermediate inside the scope (cor:acl:010 missing
	// under scope root cor:acl) must still be reported — only the scope
	// boundary's parent is exempt.
	nodes := []specNode{
		{Loc: "cor:acl", Name: "cor:acl — Access control", NodeType: "info", Tags: []string{"spec", "p0"}},
		cleanSpec(t, "cor:acl:010:02", "Role check"), // parent cor:acl:010 is absent
	}
	if fs := lintCorpus(nodes, "cor:acl", lintMem); !hasRuleFor(fs, "cor:acl:010:02", "parent-exists") {
		t.Errorf("a missing intermediate inside the scope must still be flagged; got %v", fs)
	}
}

func TestLintCorpusDuplicate(t *testing.T) {
	a := cleanSpec(t, "msg:010:02", "W2")
	b := cleanSpec(t, "msg:010:02", "W2 dup")
	fs := lintCorpus([]specNode{a, b}, "", lintMem)
	if !hasRule(fs, "duplicate-loc") {
		t.Errorf("expected duplicate-loc error; got %v", fs)
	}
}

func TestLintCorpusProductInheritance(t *testing.T) {
	// A product's module root should inherit the product's :gen contract.
	nodes := []specNode{
		{Loc: "cli", Name: "cli — CLI", NodeType: "info", Tags: []string{"spec", "p0"}},
		{Loc: "cli:gen", Name: "cli:gen — general provisions", NodeType: "info", Tags: []string{"spec", "p0"}},
		{Loc: "cli:cha", Name: "cli:cha — chat", NodeType: "info", Tags: []string{"spec", "p1"}},
	}
	fs := lintCorpus(nodes, "", lintMem)
	if !hasRuleFor(fs, "cli:cha", "inheritance-edge") {
		t.Errorf("expected inheritance-edge warning cli:cha → cli:gen; got %v", fs)
	}
	if hasRule(fs, "parent-exists") {
		t.Errorf("no parent should be missing; got %v", fs)
	}
	if hasRule(fs, "mixed-arity") {
		t.Errorf("a pure product corpus is not mixed; got %v", fs)
	}
}

func TestLintCorpusMixedArity(t *testing.T) {
	nodes := []specNode{
		{Loc: "msg", Name: "msg — Messaging", NodeType: "info", Tags: []string{"spec", "p0"}},
		{Loc: "msg:010", Name: "msg:010 — F", NodeType: "info", Tags: []string{"spec", "p1"}},
		cleanSpec(t, "msg:010:02", "W2"),
		{Loc: "cli", Name: "cli — CLI", NodeType: "info", Tags: []string{"spec", "p0"}},
		{Loc: "cli:cha", Name: "cli:cha — chat", NodeType: "info", Tags: []string{"spec", "p1"}},
	}
	if !hasRule(lintCorpus(nodes, "", lintMem), "mixed-arity") {
		t.Errorf("expected mixed-arity warning; got %v", lintCorpus(nodes, "", lintMem))
	}
}

func TestLintCorpusCleanReturnsEmptySlice(t *testing.T) {
	nodes := []specNode{
		{Loc: "msg", Name: "msg — Messaging", NodeType: "info", Tags: []string{"spec", "p0"}},
		{Loc: "msg:010", Name: "msg:010 — W-series", NodeType: "info", Tags: []string{"spec", "p1"}},
		cleanSpec(t, "msg:010:02", "W2"),
	}
	fs := lintCorpus(nodes, "", lintMem)
	if fs == nil {
		t.Fatal("clean corpus findings must be an empty slice, not nil")
	}
	if len(fs) != 0 {
		t.Fatalf("clean corpus should have no findings, got %v", fs)
	}
}

// TestLintScopeError covers the mutual-exclusion rules for `spec lint` scope
// selectors: a positional <citation>, --prefix, --product/--module, and --all
// are mutually exclusive; any single one (or none) is valid.
func TestLintScopeError(t *testing.T) {
	cases := []struct {
		name        string
		hasCitation bool
		prefix      string
		product     string
		module      string
		all         bool
		wantErr     string // substring; "" means no error
	}{
		{name: "none", wantErr: ""},
		{name: "citation only", hasCitation: true, wantErr: ""},
		{name: "prefix only", prefix: "cor:api:140", wantErr: ""},
		{name: "product only", product: "cor", wantErr: ""},
		{name: "module only", module: "api", wantErr: ""},
		{name: "all only", all: true, wantErr: ""},
		{name: "citation + prefix", hasCitation: true, prefix: "cor:api", wantErr: "<citation> argument cannot be combined"},
		{name: "citation + product", hasCitation: true, product: "cor", wantErr: "<citation> argument cannot be combined"},
		{name: "citation + all", hasCitation: true, all: true, wantErr: "<citation> argument cannot be combined"},
		{name: "prefix + product", prefix: "cor:api", product: "cor", wantErr: "--prefix cannot be combined"},
		{name: "prefix + module", prefix: "cor:api", module: "api", wantErr: "--prefix cannot be combined"},
		{name: "prefix + all", prefix: "cor:api", all: true, wantErr: "--prefix cannot be combined"},
		{name: "all + product", product: "cor", all: true, wantErr: "--all cannot be combined"},
		{name: "all + module", module: "api", all: true, wantErr: "--all cannot be combined"},
		{name: "all + product + module", product: "cor", module: "api", all: true, wantErr: "--all cannot be combined"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := lintScopeError(tc.hasCitation, tc.prefix, tc.product, tc.module, tc.all)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("expected no error, got %v", err)
			case tc.wantErr != "" && err == nil:
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			case tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr):
				t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

type specBatchResult = gen.NodeBatchNodeBatchNodeBatchResult
type specBatchResultNode = gen.NodeBatchNodeBatchNodeBatchResultNodesNode

func specBatchNodesWithIDs(ids ...string) []*specBatchResultNode {
	out := make([]*specBatchResultNode, len(ids))
	for i, id := range ids {
		out[i] = &specBatchResultNode{Id: id}
	}
	return out
}

func TestCollectSpecDetailBatchChunksByCap(t *testing.T) {
	ids := make([]string, api.NodeBatchCap+1)
	for i := range ids {
		ids[i] = fmt.Sprintf("id-%d", i)
	}
	var sizes []int
	got, unavailable, err := collectSpecDetailBatch(ids, func(chunk []string) (*specBatchResult, error) {
		sizes = append(sizes, len(chunk))
		return &specBatchResult{Nodes: specBatchNodesWithIDs(chunk...)}, nil
	})
	if err != nil {
		t.Fatalf("collectSpecDetailBatch: %v", err)
	}
	if len(got) != len(ids) {
		t.Fatalf("got %d nodes, want %d", len(got), len(ids))
	}
	if len(unavailable) != 0 {
		t.Fatalf("unexpected unavailable ids: %v", unavailable)
	}
	if want := []int{api.NodeBatchCap, 1}; !equalIntSlices(sizes, want) {
		t.Fatalf("chunk sizes = %v, want %v", sizes, want)
	}
}

func TestCollectSpecDetailBatchRequeuesTruncatedAndUnavailable(t *testing.T) {
	calls := 0
	got, unavailable, err := collectSpecDetailBatch([]string{"a", "b", "c"}, func(chunk []string) (*specBatchResult, error) {
		calls++
		switch calls {
		case 1:
			return &specBatchResult{Nodes: specBatchNodesWithIDs("a"), Truncated: true, Omitted: []string{"b", "c"}}, nil
		default:
			return &specBatchResult{Nodes: specBatchNodesWithIDs("b"), Unavailable: []string{"c"}}, nil
		}
	})
	if err != nil {
		t.Fatalf("collectSpecDetailBatch: %v", err)
	}
	if len(got) != 2 || got[0].Id != "a" || got[1].Id != "b" {
		t.Fatalf("got nodes %+v, want a and b", got)
	}
	if len(unavailable) != 1 || unavailable[0] != "c" {
		t.Fatalf("unavailable = %v, want [c]", unavailable)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}
