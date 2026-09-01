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
	// AUTHORED, not the scaffold. This used to be rubricBody(c, title) — the
	// generator's own output — which #545's scaffold-body rule correctly reports
	// as unauthored. That the linter's fixture for "a clean spec" was a spec
	// nobody had written is the issue's point arriving in its own test suite:
	// before that rule, an unauthored body was indistinguishable from an
	// authored one, here as much as in the corpus.
	//
	// It keeps the "what invalidates" heading, because the rubric requires it
	// and this fixture must still satisfy every OTHER rule.
	content := "# " + c.Format() + " — " + title + "\n\n" +
		"## Definition\n\nWhat " + loc + " governs, stated in one line.\n\n" +
		"## Rule\n\nThe rule, with an example and its edge cases.\n\n" +
		"## What invalidates this spec\n\nA change to the behaviour above.\n"
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

// abstractOf returns a spec node whose abstract is exactly n characters long,
// so the length rule can be probed at and either side of its bound.
func abstractOf(t *testing.T, loc string, n int) specNode {
	t.Helper()
	sn := cleanSpec(t, loc, "W2")
	abs := strings.Repeat("a", n)
	sn.Abstract = &abs
	return sn
}

func TestLintNodeAbstractLengthWithinBound(t *testing.T) {
	// The bound is a ceiling, not a target: everything up to and including it
	// is silent, so the rule can't nudge authors toward needlessly short
	// abstracts (#347 — retrieval is flat across ~700-1700 chars).
	for _, n := range []int{1, 800, abstractSoftMax} {
		if fs := lintNode(abstractOf(t, "msg:010:02", n)); hasRule(fs, "abstract-length") {
			t.Errorf("a %d-char abstract must not be flagged; got %v", n, fs)
		}
	}
}

func TestLintNodeAbstractLengthOverBound(t *testing.T) {
	fs := lintNode(abstractOf(t, "msg:010:02", abstractSoftMax+1))
	var found *lintFindingDTO
	for i := range fs {
		if fs[i].Rule == "abstract-length" {
			found = &fs[i]
		}
	}
	if found == nil {
		t.Fatalf("an over-long rule abstract should be flagged; got %v", fs)
	}
	if found.Severity != sevWarning {
		t.Errorf("rule-tier abstract-length should warn, got %q", found.Severity)
	}
	if !strings.Contains(found.Message, fmt.Sprint(abstractSoftMax+1)) {
		t.Errorf("message should name the actual length; got %q", found.Message)
	}
}

func TestLintNodeAbstractLengthFlowIsAdvisory(t *testing.T) {
	// Flows are pulled on demand rather than retrieved cold, so the same gap is
	// informational there — matching how the rest of the rubric tiers down.
	fs := lintNode(abstractOf(t, "msg:010:02:01", abstractSoftMax+400))
	for _, f := range fs {
		if f.Rule == "abstract-length" {
			if f.Severity != sevInfo {
				t.Errorf("flow-tier abstract-length should be info, got %q", f.Severity)
			}
			return
		}
	}
	t.Fatalf("expected abstract-length finding on a flow; got %v", fs)
}

func TestLintNodeAbstractLengthCountsCharsNotBytes(t *testing.T) {
	// Spec prose is full of em-dashes and arrows; counting bytes would flag an
	// abstract the server (which counts characters) considers well inside its
	// own cap.
	sn := cleanSpec(t, "msg:010:02", "W2")
	abs := strings.Repeat("—", abstractSoftMax) // 3 bytes each, 1 char each
	sn.Abstract = &abs
	if fs := lintNode(sn); hasRule(fs, "abstract-length") {
		t.Errorf("multi-byte characters must count as one each; got %v", fs)
	}
}

func TestLintNodeAbstractLengthNotReportedWhenMissing(t *testing.T) {
	// A missing abstract is already an error; adding a length finding on top
	// would be noise pointing at a field that doesn't exist yet.
	n := cleanSpec(t, "msg:010:02", "W2")
	n.Abstract = nil
	fs := lintNode(n)
	if !hasRule(fs, "abstract") {
		t.Fatalf("missing abstract should still be flagged; got %v", fs)
	}
	if hasRule(fs, "abstract-length") {
		t.Errorf("missing abstract should not also trip abstract-length; got %v", fs)
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

// #545: a writing-tool field delimiter inside a spec means one field has
// ABSORBED another, and the absorbed one may be the only copy of what it held.
//
// The incident: cor:api:090:00's provisions ended up inside its own abstract
// while its body stayed an unfilled scaffold. The only finding it produced was
// abstract-length — incidentally, because the swallowed body pushed it past the
// ceiling — and the obvious fix for THAT, truncating at the stray tag, would
// have destroyed the provisions.
func TestLintSerializationLeak(t *testing.T) {
	leakInAbstract := cleanSpec(t, "msg:010:02", "W2")
	abs := "Node search's general provisions — the shared contract.</abstract>\n<parameter name=\"content\">## Provisions"
	leakInAbstract.Abstract = &abs

	leakInBody := cleanSpec(t, "msg:010:03", "W3")
	body := "# msg:010:03\n\n<parameter name=\"content\">\n\n## What invalidates this spec\n\nnothing\n"
	leakInBody.Content = &body

	for _, tc := range []struct {
		name  string
		node  specNode
		field string
	}{
		{"abstract", leakInAbstract, "abstract"},
		{"body", leakInBody, "body"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs := lintNode(tc.node)
			if !hasRule(fs, "serialization-leak") {
				t.Fatalf("a leaked marker in the %s must be reported, got %v", tc.field, fs)
			}
			for _, f := range fs {
				if f.Rule != "serialization-leak" {
					continue
				}
				// ERROR, not warning: it must fail a corpus run rather than
				// accumulate in a list somebody skims.
				if f.Severity != sevError {
					t.Errorf("severity = %q, want %q", f.Severity, sevError)
				}
				// The message must name WHICH field absorbed the other — that is
				// the first thing an author needs before touching either — and
				// must warn against the destructive obvious fix.
				if !strings.Contains(f.Message, tc.field) {
					t.Errorf("message must name the field, got %q", f.Message)
				}
				if !strings.Contains(f.Message, "truncate") {
					t.Errorf("message must warn against truncating at the marker, got %q", f.Message)
				}
			}
		})
	}
}

// THE SELF-REFERENCE GUARD, and its inverse — one table, because the two
// directions are the same decision and splitting them let the second lag.
//
// A spec documenting this leak quotes the markers, and quoting must not
// re-trigger the rule. But making the guard generous risks turning it into a
// HIDING PLACE, which is worse: serialization-leak is an error-severity check
// on content that may be the only copy of itself, so a false positive is loud
// and cheap while a false negative is silent and permanent.
//
// Hence the governing rule in withoutCode — may false-positive, must never
// false-negative — and hence the `leak: true` rows here. @codex took three
// rounds on PR #547 to find the CommonMark forms the first two versions missed;
// the rows are kept together so the next form added has to answer both
// directions at once.
func TestLintSerializationLeakQuotingAndHiding(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		leak bool
	}{
		// Quoted — must NOT be reported.
		{"single-backtick span", "quoted `</abstract>` here", false},
		{"double-backtick span", "quoted ``</abstract>`` here", false},
		{"triple fence", "```\n</abstract>\n```\n", false},
		{"tilde fence", "~~~\n</abstract>\n~~~\n", false},
		{"four-backtick fence", "````\n</abstract>\n````\n", false},
		{"three-space indented fence", "   ```\n   </abstract>\n   ```\n", false},
		{"suffixed line is not a close", "```\n```not-a-close\n</abstract>\n```\n", false},
		{"grammar placeholders", "<org>:<slug> and <actor>", false},

		// Real — must STILL be reported. Each of these was a false negative in
		// some version of the scanner, i.e. the rule going blind.
		{"four-space line cannot open a fence", "    ```\ntext\n\nreal </abstract> leak\n", true},
		{"an unclosed fence must not blind the rest", "```\ncode\n\nreal </abstract> leak\n", true},
		{"a stray backtick opens nothing", "unclosed ` then </abstract> leaked", true},
		{"the plain case", "provisions</abstract>\n<parameter name=\"content\">", true},
		{"a backtick in a fence's info string is not an opener", "```foo`bar\nreal </abstract> leak\n```\n", true},
		// REPORTED on purpose. A CommonMark span may cross a newline, and round 2
		// of this review made the stripper follow it there — which then paired the
		// delimiters of fence-looking prose lines apart and swallowed a real
		// marker. The property decides it: a spurious finding is permitted, going
		// blind is not. The remedy for an author is to fence the example.
		{"a span crossing a newline is reported, not followed", "a `code\n</abstract>` b", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := cleanSpec(t, "msg:010:02", "W2")
			abs := tc.text
			n.Abstract = &abs
			if got := hasRule(lintNode(n), "serialization-leak"); got != tc.leak {
				t.Errorf("serialization-leak = %v, want %v, for %q", got, tc.leak, tc.text)
			}
		})
	}
}

// #545 rule B: the scaffold body. The rubric's one body-reading check tests for
// a "what invalidates" heading, and the scaffold SHIPS with that heading — so a
// never-authored body passed it, and an unauthored spec was indistinguishable
// from an authored one.
func TestLintScaffoldBody(t *testing.T) {
	n := cleanSpec(t, "msg:010:02", "W2")
	scaffold := rubricBody(mustCit(t, "msg:010:02"), "W2")
	n.Content = &scaffold

	fs := lintNode(n)
	if !hasRule(fs, "scaffold-body") {
		t.Fatalf("an unreplaced scaffold body must be reported, got %v", fs)
	}
	// The rubric's invalidates check must NOT fire — that is the whole point:
	// the scaffold satisfies it, which is why this rule had to exist.
	if hasRule(fs, "invalidates") {
		t.Error("the scaffold satisfies the invalidates check; if that fires, this test is not exercising the gap")
	}
}

// …and an UNTOUCHED contract is reported once, as placeholder-contract, not
// twice. placeholder-contract returns early precisely so a spec nobody has
// started is not also accused of having an unwritten body — which is true but
// not the finding its author needs.
func TestLintUntouchedContractIsNotAlsoScaffoldBody(t *testing.T) {
	c := mustCit(t, "msg:010:00")
	n := cleanSpec(t, "msg:010:00", "General provisions")
	placeholder := "TODO(abstract): describe what this contract sets."
	scaffold := rubricBody(c, "General provisions")
	n.Abstract, n.Content = &placeholder, &scaffold

	fs := lintNode(n)
	if !hasRule(fs, "placeholder-contract") {
		t.Fatalf("an untouched contract must be reported as such, got %v", fs)
	}
	if hasRule(fs, "scaffold-body") {
		t.Errorf("an untouched contract must not be double-reported, got %v", fs)
	}
}

// #545 rule B carries the SAME self-reference guard as rule A — a spec
// documenting `spec new` quotes its filler in an example, and matching that
// would call an authored spec unauthored. Rule A had the guard from the start
// and rule B did not; that was an inconsistency in the implementation rather
// than a case nobody had thought of (@codex, PR #547).
func TestLintScaffoldBodyIgnoresQuotedFiller(t *testing.T) {
	n := cleanSpec(t, "msg:010:02", "W2")
	quoted := "# msg:010:02 — W2\n\n## Rule\n\n`spec new` emits:\n\n```\nState the shared rules and defaults.\n```\n\nAuthored prose.\n\n## What invalidates this spec\n\nx\n"
	n.Content = &quoted
	if hasRule(lintNode(n), "scaffold-body") {
		t.Error("filler quoted in an example is documentation, not an unauthored body")
	}

	// …and the genuine article still fires, so the guard has not disarmed it.
	real := "# msg:010:02 — W2\n\n## Provisions\n\nState the shared rules and defaults.\n\n## What invalidates this spec\n\nx\n"
	n.Content = &real
	if !hasRule(lintNode(n), "scaffold-body") {
		t.Error("an unreplaced scaffold body must still be reported")
	}
}
