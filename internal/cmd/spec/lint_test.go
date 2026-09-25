package spec

import (
	"fmt"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// lintMem is a stand-in memory URN for the corpus-lint tests; it is the -m of
// the inheritance-edge remedy message.
const lintMem = "acme.com::specs"

// cleanSpec builds a spec node at loc that trips no lint rule.
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
	// It keeps the "what invalidates" heading from when the rubric required
	// it (retired by #708); harmless, and it keeps the fixture a realistic body.
	content := "# " + c.Format() + " — " + title + "\n\n" +
		"## Definition\n\nWhat " + loc + " governs, stated in one line.\n\n" +
		"## Rule\n\nThe rule, with an example and its edge cases.\n\n" +
		"## What invalidates this spec\n\nA change to the behaviour above.\n"
	// FINGERPRINTED against this body (#335). "Clean" includes a verified
	// abstract: one with a body and no fingerprint has never been checked
	// against it, which reads as unverified rather than verified (server
	// #1128). Derived from `content` rather than hardcoded, so editing the
	// fixture body cannot turn every clean-spec test stale at a distance.
	hash := contentHash(content)
	sn := specNode{
		// RAW, as a batch read returns it — the abstract checks are gated on
		// this, because a compiled body cannot be compared against a
		// fingerprint taken over the source.
		ContentIsRaw:       true,
		Loc:                loc,
		Name:               specName(c, title),
		NodeType:           "info",
		Tags:               []string{"spec", "topic"},
		Abstract:           &abs,
		AbstractOriginHash: &hash,
		Content:            &content,
		DataVersion:        "0.0.1",
	}
	if p, ok := c.Parent(); ok {
		sn.OutEdges = append(sn.OutEdges, specEdge{Name: "toc", Loc: p.Format()})
	}
	return sn
}

// specHeader builds a module/feature header node whose body INDEXES the
// children it is given — which is what makes it clean under `index-incomplete`
// (#605).
//
// The inline literals this replaced carried no body at all, so the suite's
// "clean corpus" was one whose indexes routed to nothing. That is #545's lesson
// one tier up: there, the fixture for a clean SPEC was a spec nobody had
// written; here, the fixture for a clean CORPUS was a corpus whose parents
// named none of their children. A fixture that cannot satisfy a rule is not
// evidence the rule is wrong.
func specHeader(t *testing.T, loc, title string, children ...string) specNode {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "# %s — %s\n\n## Index\n\n", loc, title)
	for _, child := range children {
		fmt.Fprintf(&b, "- [`%s`](hrn:node:acme.com:specs:%s) — what %s governs.\n", child, child, child)
	}
	content := b.String()
	return specNode{
		Loc: loc, Name: loc + " — " + title, NodeType: "info",
		Tags: []string{"spec", "topic"}, Content: &content, ContentIsRaw: true,
	}
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
	if fs := lintNode(cleanSpec(t, "msg:010:02", "W2"), ""); len(fs) != 0 {
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
	fs := lintNode(bad, "")
	for _, want := range []string{"name-prefix", "nodetype-info"} {
		if !hasRule(fs, want) {
			t.Errorf("expected %q finding; got %v", want, fs)
		}
	}
	// #708: the old rubric is gone, so the missing abstract and the missing
	// "what invalidates" statement are no longer findings.
	for _, gone := range removedRubricRules {
		if hasRule(fs, gone) {
			t.Errorf("the retired rubric rule %q still fired; got %v", gone, fs)
		}
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
		if fs := lintNode(abstractOf(t, "msg:010:02", n), ""); hasRule(fs, "abstract-length") {
			t.Errorf("a %d-char abstract must not be flagged; got %v", n, fs)
		}
	}
}

func TestLintNodeAbstractLengthOverBound(t *testing.T) {
	fs := lintNode(abstractOf(t, "msg:010:02", abstractSoftMax+1), "")
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
	//
	// The fixture is in the SOFT range on purpose. It used to be
	// abstractSoftMax+400, which is exactly 2000 — the hard cap — so it was
	// asserting "flows tier down" with a value that is simultaneously "one edit
	// from unwritable". Those are different findings (#539), and the test now
	// picks a length that can only be the first.
	fs := lintNode(abstractOf(t, "msg:010:02:01", abstractSoftMax+100), "")
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
	if fs := lintNode(sn, ""); hasRule(fs, "abstract-length") {
		t.Errorf("multi-byte characters must count as one each; got %v", fs)
	}
}

func TestLintNodeAbstractLengthNotReportedWhenMissing(t *testing.T) {
	// A length finding on a missing abstract would point at a field that
	// doesn't exist. (A missing abstract is itself no longer a finding, #708.)
	n := cleanSpec(t, "msg:010:02", "W2")
	n.Abstract = nil
	fs := lintNode(n, "")
	if hasRule(fs, "abstract-length") {
		t.Errorf("missing abstract should not also trip abstract-length; got %v", fs)
	}
}

func TestLintNodeHeaderLight(t *testing.T) {
	// A module/feature header (level < 3) only gets the universal checks,
	// not the spec rubric (no abstract/invalidates requirement).
	header := specNode{Loc: "msg:010", Name: "msg:010 — W-series", NodeType: "info", Tags: []string{"spec", "p1"}}
	if fs := lintNode(header, ""); len(fs) != 0 {
		t.Errorf("header node should pass the light checks, got %v", fs)
	}
}

func TestLintNodeHeaderMissingSpecTag(t *testing.T) {
	header := specNode{Loc: "msg:010", Name: "msg:010 — W-series", NodeType: "info", Tags: []string{"p1"}}
	fs := lintNode(header, "")
	if !hasRule(fs, "tag-spec") {
		t.Errorf("header node missing the spec tag should be flagged; got %v", fs)
	}
	if hasRule(fs, "abstract") || hasRule(fs, "invalidates") {
		t.Errorf("header node should still skip rule-level rubric checks; got %v", fs)
	}
}

func TestLintNodeUnavailable(t *testing.T) {
	fs := lintNode(specNode{Loc: "msg:010:02", Unavailable: true}, "")
	if len(fs) != 1 || fs[0].Rule != "unavailable" || fs[0].Severity != sevError {
		t.Fatalf("unavailable listed node should produce one explicit error, got %v", fs)
	}
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

func TestLintCorpusDuplicate(t *testing.T) {
	a := cleanSpec(t, "msg:010:02", "W2")
	b := cleanSpec(t, "msg:010:02", "W2 dup")
	fs := lintCorpus([]specNode{a, b}, lintMem)
	if !hasRule(fs, "duplicate-loc") {
		t.Errorf("expected duplicate-loc error; got %v", fs)
	}
}

// #709: a memory that mixes the old flat and product-rooted numberings is no
// longer flagged — the `mixed-arity` rule went with the scheme it enforced.
func TestLintCorpusNoLongerFlagsMixedArity(t *testing.T) {
	nodes := []specNode{
		{Loc: "msg", Name: "msg — Messaging", NodeType: "info", Tags: []string{"spec", "p0"}},
		{Loc: "msg:010", Name: "msg:010 — F", NodeType: "info", Tags: []string{"spec", "p1"}},
		cleanSpec(t, "msg:010:02", "W2"),
		{Loc: "cli", Name: "cli — CLI", NodeType: "info", Tags: []string{"spec", "p0"}},
		{Loc: "cli:cha", Name: "cli:cha — chat", NodeType: "info", Tags: []string{"spec", "p1"}},
	}
	if fs := lintCorpus(nodes, lintMem); hasRule(fs, "mixed-arity") {
		t.Errorf("mixed-arity is retired (#709); got %v", fs)
	}
}

func TestLintCorpusCleanReturnsEmptySlice(t *testing.T) {
	nodes := []specNode{
		specHeader(t, "msg", "Messaging", "msg:010"),
		specHeader(t, "msg:010", "W-series", "msg:010:02"),
		cleanSpec(t, "msg:010:02", "W2"),
	}
	fs := lintCorpus(nodes, lintMem)
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
			fs := lintNode(tc.node, "")
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
		// FENCED — must NOT be reported. A fence is the only exemption, and
		// that is the settled scope after five review rounds: every false
		// negative came from pairing backticks for inline spans, so spans are
		// not stripped at all. An inline-quoted marker IS reported, and the
		// remedy is to fence the example.
		{"triple fence", "```\n</abstract>\n```\n", false},
		{"tilde fence", "~~~\n</abstract>\n~~~\n", false},
		{"four-backtick fence", "````\n</abstract>\n````\n", false},
		{"three-space indented fence", "   ```\n   </abstract>\n   ```\n", false},
		{"suffixed line is not a close", "```\n```not-a-close\n</abstract>\n```\n", false},
		// Only spaces and tabs may follow a closing run; TrimSpace also trimmed
		// NBSP, closing a fence CommonMark leaves open.
		{"a non-breaking space does not close a fence", "```\ncode\n```\u00a0\n</abstract> still fenced\n```\n", false},
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
		{"a span crossing a newline is reported", "a `code\n</abstract>` b", true},
		{"an inline span is reported; fence it instead", "quoted `</abstract>` here", true},
		{"a double-backtick span likewise", "quoted ``</abstract>`` here", true},
		// CommonMark makes \` a LITERAL backtick, so no span exists and the
		// marker is prose. Pairing backticks would have hidden it — the third
		// distinct way span logic went blind, and the reason there is none.
		{"escaped backticks open nothing", "prose \\`literal </abstract> tail\\` more", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := cleanSpec(t, "msg:010:02", "W2")
			abs := tc.text
			n.Abstract = &abs
			if got := hasRule(lintNode(n, ""), "serialization-leak"); got != tc.leak {
				t.Errorf("serialization-leak = %v, want %v, for %q", got, tc.leak, tc.text)
			}
		})
	}
}

// The hard cap does NOT tier down: a flow's write fails at 2000 exactly as a
// rule's does, so the escalation applies at every level. The severity ladder is
// about how much a LONG abstract matters; the cap is about whether the node can
// be edited at all.
func TestLintNodeAbstractLengthEscalatesAtTheHardCapEvenForAFlow(t *testing.T) {
	fs := lintNode(abstractOf(t, "msg:010:02:01", abstractHardMax-10), "")
	for _, f := range fs {
		if f.Rule == "abstract-length" {
			if f.Severity != sevError {
				t.Errorf("a flow %d chars from the cap must still escalate, got %q", 10, f.Severity)
			}
			if !strings.Contains(f.Message, "rejected at write time") {
				t.Errorf("the finding must say what happens next: %q", f.Message)
			}
			return
		}
	}
	t.Fatalf("expected an abstract-length finding; got %v", fs)
}

// HEADROOM IS THE NUMBER THE AUTHOR NEEDED, and it was never printed (#539).
//
// The old message said the same thing at 1601 as at 1990 — the first is
// advisory, the second is ten characters from a rejected write. @Ada hit the
// cap three times in a row amending one node, because the information that
// would have let her write it once existed at lint time and stayed there.
func TestLintNodeAbstractLengthReportsHeadroomNotJustOverage(t *testing.T) {
	fs := lintNode(abstractOf(t, "msg:010:02", 1922), "")
	for _, f := range fs {
		if f.Rule != "abstract-length" {
			continue
		}
		for _, want := range []string{"1922", "78", "2000"} {
			if !strings.Contains(f.Message, want) {
				t.Errorf("the finding must carry %q — the distance to the wall is the actionable half: %q", want, f.Message)
			}
		}
		return
	}
	t.Fatalf("expected an abstract-length finding; got %v", fs)
}

// Near the cap, "distill it" is the WRONG advice, and the finding must stop
// giving it: on a spec whose sentences are all on-subject, cutting one drops a
// contract. The remedy is a split.
func TestNearTheCapTheAdviceChangesFromDistillToSplit(t *testing.T) {
	soft := lintNode(abstractOf(t, "msg:010:02", abstractSoftMax+50), "")
	tight := lintNode(abstractOf(t, "msg:010:02", abstractHardMax-20), "")
	msgOf := func(fs []lintFindingDTO) string {
		for _, f := range fs {
			if f.Rule == "abstract-length" {
				return f.Message
			}
		}
		return ""
	}
	if m := msgOf(soft); !strings.Contains(m, "distill it") {
		t.Errorf("comfortably past the soft bound, distilling is right: %q", m)
	}
	if m := msgOf(tight); strings.Contains(m, "distill it") {
		t.Errorf("a sentence from the cap, distilling drops a contract — the advice must change: %q", m)
	}
	if m := msgOf(tight); !strings.Contains(m, "split") {
		t.Errorf("the near-cap finding must name the real remedy: %q", m)
	}
}

// A conjunction in the title is a LEAD, never a finding on its own: plenty of
// single-subject titles contain "and" ("create and update"), so alone it would
// be noise. It only ever adds a clause to a finding that already fired on
// length — which is why this asserts the pairing, not the predicate.
func TestTitleConjunction(t *testing.T) {
	for _, tc := range []struct{ title, want string }{
		{"cor:agt:020:03 — Sessions, liveness, and provenance", "and"},
		{"cor:agt:020:02 — Worker allocation and permanence", "and"},
		{"msg:010:02 — Delivery", ""},
		// The citation half never counts: a loc has colons, not conjunctions,
		// and splitting on the em-dash first keeps it out of range entirely.
		{"cor:and:010 — Delivery", ""},
		// EVERY supported separator, because a helper with three branches and
		// one tested branch is two branches that can be dropped without anything
		// going red (@copilot, suppressed in the verdict body of #565).
		{"cor:agt:020:02 — Allocation & permanence", "&"},
		{"cor:acl:110 — Read/write access", "/"},
		// Case-insensitive: a title-cased conjunction is the same conjunction.
		{"cor:agt:020:03 — Sessions AND provenance", "and"},
		// "and" INSIDE a word is not a conjunction — the separator carries its
		// spaces for exactly this reason.
		{"msg:010:02 — Standards", ""},
	} {
		if got := titleConjunction(tc.title); got != tc.want {
			t.Errorf("titleConjunction(%q) = %q, want %q", tc.title, got, tc.want)
		}
	}
}

// The SOFT-range message reports headroom too, and this exists because a
// mutation said otherwise: blanking the headroom numbers left the suite green.
//
// The headroom test above uses 1922, which is inside the tight band and takes
// the ESCALATED message — so the ordinary warning's numbers, which is what most
// authors will actually see, had no assertion at all.
func TestTheSoftRangeMessageAlsoReportsHeadroom(t *testing.T) {
	const l = abstractSoftMax + 100 // 1700: past the soft bound, far from the cap
	fs := lintNode(abstractOf(t, "msg:010:02", l), "")
	for _, f := range fs {
		if f.Rule != "abstract-length" {
			continue
		}
		if f.Severity != sevWarning {
			t.Fatalf("this length must stay advisory, got %q", f.Severity)
		}
		// "300 of headroom" without a unit reads ambiguously (@copilot); the
		// escalated message already said "chars", so this is one vocabulary
		// rather than two.
		for _, want := range []string{"1700 chars", "300 chars of headroom", "2000-char hard cap"} {
			if !strings.Contains(f.Message, want) {
				t.Errorf("the warning must carry %q: %q", want, f.Message)
			}
		}
		return
	}
	t.Fatalf("expected an abstract-length finding; got %v", fs)
}

// The conjunction clause is CONDITIONAL, and that is the whole reason it is
// safe to print: a title naming one subject must not be told it names two.
//
// Also from a green mutation — hard-coding the clause on changed nothing any
// test could see, because the only conjunction assertion was on the helper in
// isolation and never on the pairing.
func TestTheSplitHintOnlyAppearsWhenTheTitleNamesTwoSubjects(t *testing.T) {
	msgFor := func(title string) string {
		sn := cleanSpec(t, "msg:010:02", title)
		abs := strings.Repeat("a", abstractHardMax-20)
		sn.Abstract = &abs
		for _, f := range lintNode(sn, "") {
			if f.Rule == "abstract-length" {
				return f.Message
			}
		}
		t.Fatalf("expected an abstract-length finding for %q", title)
		return ""
	}
	if m := msgFor("Delivery"); strings.Contains(m, "already suggests") {
		t.Errorf("a single-subject title must not be told it names two: %q", m)
	}
	if m := msgFor("Sessions, liveness and provenance"); !strings.Contains(m, "already suggests") {
		t.Errorf("a title naming two subjects is the lead worth printing: %q", m)
	}
}

// The failure is CONDITIONAL, and saying otherwise was an overclaim both bots
// caught on #565.
//
// The server rejects a write whose abstract EXCEEDS the cap — not "the next
// edit". At 1922 an equal-length replacement is fine, and so is adding up to 78
// characters. Since the finding goes on to recommend a supersede-level split,
// the stronger claim would have justified a costly restructure on a node that
// did not need one yet: a claim outrunning its evidence, in the one sentence
// meant to make the reader act.
func TestTheNearCapFindingDoesNotOverclaimWhatFails(t *testing.T) {
	fs := lintNode(abstractOf(t, "msg:010:02", 1922), "")
	for _, f := range fs {
		if f.Rule != "abstract-length" {
			continue
		}
		if !strings.Contains(f.Message, "grows it past") {
			t.Errorf("the finding must name the condition, not assert every edit fails: %q", f.Message)
		}
		// The retired overclaim, in the spelling it shipped in.
		if strings.Contains(f.Message, "the next edit fails") {
			t.Errorf("only an edit that lengthens the abstract is rejected: %q", f.Message)
		}
		return
	}
	t.Fatalf("expected an abstract-length finding; got %v", fs)
}

// AT OR PAST the cap, headroom is zero or negative — and a negative must never
// reach the reader as "only -48 from the hard cap" (@copilot, #565).
//
// Over-cap is reachable from data written before the cap existed, so it is a
// real state rather than a defensive branch, and the sentence changes: nothing
// about "headroom" is true there, and what the author needs to know is that any
// update which does not shorten the abstract is refused.
func TestAtOrPastTheCapTheFindingDoesNotPrintNegativeHeadroom(t *testing.T) {
	for _, l := range []int{abstractHardMax, abstractHardMax + 48} {
		fs := lintNode(abstractOf(t, "msg:010:02", l), "")
		var msg string
		for _, f := range fs {
			if f.Rule == "abstract-length" {
				msg = f.Message
				if f.Severity != sevError {
					t.Errorf("%d chars must escalate, got %q", l, f.Severity)
				}
			}
		}
		if msg == "" {
			t.Fatalf("expected an abstract-length finding at %d chars; got %v", l, fs)
		}
		if strings.Contains(msg, "-") && strings.Contains(msg, "headroom") {
			t.Errorf("%d chars: headroom must not be reported negatively: %q", l, msg)
		}
		for _, forbidden := range []string{"only -", "of headroom before"} {
			if strings.Contains(msg, forbidden) {
				t.Errorf("%d chars: %q is meaningless at or past the cap: %q", l, forbidden, msg)
			}
		}
	}
}

// AT the cap and PAST it are different states, and lumping them re-stated the
// very overclaim round 1 removed (@codex on #565, second time).
//
// At exactly 2000 the server still accepts an equal-length rewrite — it rejects
// values LONGER than the cap. Saying "any update that does not shorten it is
// rejected" there would push a node toward a supersede-level split it does not
// need. Past the cap the unconditional claim is true, because the value already
// exceeds the limit.
func TestTheCapBoundaryIsItsOwnStateNotLumpedWithOverCap(t *testing.T) {
	msgAt := func(l int) string {
		for _, f := range lintNode(abstractOf(t, "msg:010:02", l), "") {
			if f.Rule == "abstract-length" {
				return f.Message
			}
		}
		t.Fatalf("expected an abstract-length finding at %d", l)
		return ""
	}

	at := msgAt(abstractHardMax)
	if !strings.Contains(at, "equal-length rewrite still works") {
		t.Errorf("at the cap, an equal-length rewrite is valid and the finding must say so: %q", at)
	}
	if strings.Contains(at, "does not shorten") {
		t.Errorf("at the cap, shortening is not the only valid edit: %q", at)
	}

	// PAST the cap the claim is about abstract REPLACEMENTS, not updates — and
	// my own assertion here encoded the broader version until @codex pointed at
	// the schema: UpdateNodeInput preserves omitted fields, and `spec supersede`
	// retires a node by sending only tags and content. So "any update that does
	// not shorten it" said the very remedy the message recommends would itself
	// be rejected.
	over := msgAt(abstractHardMax + 48)
	if !strings.Contains(over, "REPLACES the abstract is rejected") {
		t.Errorf("past the cap the claim must be scoped to abstract replacements: %q", over)
	}
	if !strings.Contains(over, "spec supersede") {
		t.Errorf("the message must say the recommended remedy still works: %q", over)
	}
	if strings.Contains(over, "any update that does not shorten") {
		t.Errorf("the retired overclaim must not come back: %q", over)
	}
	if strings.Contains(over, "equal-length rewrite still works") {
		t.Errorf("past the cap an equal-length rewrite is still rejected: %q", over)
	}
}

// The threshold is STRICTLY less than, and the prose says so (@copilot, #565).
//
// At exactly abstractTightHeadroom the finding does NOT escalate. The wording
// used to be "inside the last 150", which reads as inclusive and disagreed with
// the code at exactly one length — the kind of gap where the docs and the
// behaviour are each defensible alone and only wrong together.
func TestTheEscalationBoundaryIsExclusiveAndSaysSo(t *testing.T) {
	sevAt := func(l int) string {
		for _, f := range lintNode(abstractOf(t, "msg:010:02", l), "") {
			if f.Rule == "abstract-length" {
				return f.Severity
			}
		}
		t.Fatalf("expected an abstract-length finding at %d", l)
		return ""
	}
	if got := sevAt(abstractHardMax - abstractTightHeadroom); got != sevWarning {
		t.Errorf("exactly %d chars of headroom must NOT escalate, got %q", abstractTightHeadroom, got)
	}
	if got := sevAt(abstractHardMax - abstractTightHeadroom + 1); got != sevError {
		t.Errorf("one char inside the boundary must escalate, got %q", got)
	}
}

// "only 1 characters of headroom" is the kind of thing a reader notices and a
// test does not — unless it asserts the exact string at headroom 1 (@copilot).
// The message uses "chars" throughout, which also matches its own opening
// clause ("abstract is 1999 chars"), so there is one vocabulary rather than two.
func TestTheNearCapMessageReadsCorrectlyAtOneCharOfHeadroom(t *testing.T) {
	for _, f := range lintNode(abstractOf(t, "msg:010:02", abstractHardMax-1), "") {
		if f.Rule != "abstract-length" {
			continue
		}
		// BOTH plural spellings are wrong at one, and the first fix only
		// removed one of them (@copilot, twice): "1 characters" became
		// "1 chars". The assertion now names the singular it must be.
		for _, wrong := range []string{"1 characters", "1 chars"} {
			if strings.Contains(f.Message, wrong) {
				t.Errorf("singular headroom must not read as a plural (%q): %q", wrong, f.Message)
			}
		}
		if !strings.Contains(f.Message, "only 1 char of headroom") {
			t.Errorf("expected the singular at headroom 1: %q", f.Message)
		}
		return
	}
	t.Fatalf("expected an abstract-length finding")
}

func TestPlural(t *testing.T) {
	for _, tc := range []struct {
		n    int
		want string
	}{
		{1, "1 char"},
		{0, "0 chars"},
		{78, "78 chars"},
		{300, "300 chars"},
	} {
		if got := plural(tc.n, "char"); got != tc.want {
			t.Errorf("plural(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

// THE HARD CAP BINDS EVERY TIER, including the module and feature headers the
// soft rule deliberately skips (@codex on #565).
//
// `lintNode` returns at `c.Level() < 3` before the rubric, which is right for an
// advisory length bound — a header abstract being long costs retrieval little.
// It is wrong for the WALL: a header abstract a sentence from the cap is exactly
// as unwritable as a rule's, and reporting nothing there leaves the author to
// discover it at write time, which is the whole defect #539 is about.
func TestTheHardCapIsReportedForHeaderTiersToo(t *testing.T) {
	for _, loc := range []string{"cor", "cor:agt", "cor:agt:020"} {
		sn := cleanSpec(t, loc, "Headers")
		abs := strings.Repeat("a", abstractHardMax-10)
		sn.Abstract = &abs
		var found bool
		for _, f := range lintNode(sn, "") {
			if f.Rule == "abstract-length" {
				found = true
				if f.Severity != sevError {
					t.Errorf("%s: a header at the wall must escalate, got %q", loc, f.Severity)
				}
				if !strings.Contains(f.Message, "10 chars of headroom") {
					t.Errorf("%s: the header finding must report headroom: %q", loc, f.Message)
				}
			}
		}
		if !found {
			t.Errorf("%s: a header abstract 10 chars from the cap must be reported", loc)
		}
	}

	// ...and the ADVISORY soft bound still tiers down: a header comfortably past
	// 1600 but far from the cap reports nothing, which is the behaviour the
	// early return was written for and must not change.
	sn := cleanSpec(t, "cor:agt", "Headers")
	abs := strings.Repeat("a", abstractSoftMax+100)
	sn.Abstract = &abs
	for _, f := range lintNode(sn, "") {
		if f.Rule == "abstract-length" {
			t.Errorf("the soft bound must still skip headers: %q", f.Message)
		}
	}
}

// One finding per node, not two: the near-cap check runs above the header
// return and the soft check below it, so a rule-tier node at the wall must not
// collect both.
func TestANodeAtTheWallGetsExactlyOneAbstractLengthFinding(t *testing.T) {
	n := 0
	for _, f := range lintNode(abstractOf(t, "msg:010:02", abstractHardMax-10), "") {
		if f.Rule == "abstract-length" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("expected exactly one abstract-length finding, got %d", n)
	}
}

// The count must match the SERVER's, and it did not, on two axes
// (@codex on #565; verified against hadron-server origin/main 6968543).
//
// normalizeAbstract checks `raw.length > 2000` on the RAW value, BEFORE the
// whitespace-only collapse, and a non-empty value persists untrimmed. So
// trailing whitespace counts — and #539 exists so the reported headroom can be
// trusted, which makes an off-by-the-newline exactly the defect it set out to
// remove.
func TestAbstractLengthCountsWhatTheServerCounts(t *testing.T) {
	lenOf := func(s string) int { return abstractLength(&s) }

	// 1. Trailing whitespace COUNTS. `--abstract-file` preserves the final
	//    newline, so this is the ordinary case, not an exotic one.
	if got := lenOf("abc\n"); got != 4 {
		t.Errorf("a trailing newline counts toward the cap: got %d, want 4", got)
	}
	if got := lenOf("  abc  "); got != 7 {
		t.Errorf("surrounding whitespace counts: got %d, want 7", got)
	}

	// 2. UTF-16 CODE UNITS, because JavaScript's `.length` is. Everything a spec
	//    corpus actually carries is one unit per character, so this only bites
	//    above the BMP — but claiming to match the server and not doing so is
	//    what put the wrong number in front of the reader in the first place.
	if got := lenOf("—→"); got != 2 {
		t.Errorf("BMP punctuation is one unit each: got %d, want 2", got)
	}
	if got := lenOf("😀"); got != 2 {
		t.Errorf("a non-BMP rune is a surrogate PAIR to the server: got %d, want 2", got)
	}

	// And the consequence the issue is about: an abstract that looks like it has
	// headroom, and does not.
	atCap := strings.Repeat("a", abstractHardMax-1) + "\n"
	if got := lenOf(atCap); got != abstractHardMax {
		t.Fatalf("fixture: want exactly the cap, got %d", got)
	}
	sn := cleanSpec(t, "msg:010:02", "Delivery")
	sn.Abstract = &atCap
	var msg string
	for _, f := range lintNode(sn, "") {
		if f.Rule == "abstract-length" {
			msg = f.Message
		}
	}
	if !strings.Contains(msg, "exactly the 2000-char hard cap") {
		t.Errorf("1999 chars plus the newline IS at the cap, and must be reported so: %q", msg)
	}
}

// #335 — the abstract is the corpus's RAG retrieval surface, so a stale one
// answers an agent's question authoritatively and wrongly. The signal was on
// the wire all along and thrown away.
func TestLintAbstractVerification(t *testing.T) {
	t.Run("matching hash is clean", func(t *testing.T) {
		n := cleanSpec(t, "msg:010:02", "W2")
		if fs := lintNode(n, ""); hasRule(fs, "abstract-stale") || hasRule(fs, "abstract-unverified") {
			t.Errorf("a fingerprinted, matching abstract is clean: %v", fs)
		}
	})

	t.Run("body moved since the abstract was written", func(t *testing.T) {
		n := cleanSpec(t, "msg:010:02", "W2")
		moved := *n.Content + "\n\nA paragraph added after the abstract was written.\n"
		n.Content = &moved
		fs := lintNode(n, "")
		if !hasRule(fs, "abstract-stale") {
			t.Fatalf("expected abstract-stale: %v", fs)
		}
		// Both hashes in the message, so the reader can check it by hand
		// rather than taking the tool's word.
		var msg string
		for _, f := range fs {
			if f.Rule == "abstract-stale" {
				msg = f.Message
				if f.Severity != sevWarning {
					t.Errorf("stale is a WARNING — 176 of 264 nodes were stale when measured, "+
						"so an error makes --all permanently red; got %q", f.Severity)
				}
			}
		}
		if !strings.Contains(msg, *n.AbstractOriginHash) || !strings.Contains(msg, contentHash(moved)) {
			t.Errorf("both hashes must be named: %q", msg)
		}
		// And it must not overclaim: a hash says the body MOVED, not that the
		// abstract is wrong.
		if !strings.Contains(msg, "NOT proof") {
			t.Errorf("must not assert the abstract is wrong: %q", msg)
		}
	})

	// The half the ISSUE got wrong. It proposed that a null hash is
	// "pre-spec-032, not a finding" — but the contract was refreshed since:
	// null on a node with BOTH an abstract and content means the abstract was
	// written before the body existed and has never been checked against it,
	// which reads as unverified rather than verified (server #1128).
	t.Run("never fingerprinted is unverified, not clean", func(t *testing.T) {
		n := cleanSpec(t, "msg:010:02", "W2")
		n.AbstractOriginHash = nil
		fs := lintNode(n, "")
		if !hasRule(fs, "abstract-unverified") {
			t.Fatalf("a null hash with both an abstract and a body is UNVERIFIED: %v", fs)
		}
		if hasRule(fs, "abstract-stale") {
			t.Errorf("unverified is not stale — they are different states: %v", fs)
		}
	})

	// Null is clean only when there is nothing to verify.
	t.Run("null is clean with no abstract or no content", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			mutate func(*specNode)
		}{
			{"no abstract", func(n *specNode) { n.Abstract = nil }},
			{"no content", func(n *specNode) { empty := ""; n.Content = &empty }},
		} {
			t.Run(tc.name, func(t *testing.T) {
				n := cleanSpec(t, "msg:010:02", "W2")
				n.AbstractOriginHash = nil
				tc.mutate(&n)
				if fs := lintNode(n, ""); hasRule(fs, "abstract-unverified") || hasRule(fs, "abstract-stale") {
					t.Errorf("nothing to verify here: %v", fs)
				}
			})
		}
	})
}

// The predicate is SHARED with the citation surface, which asks the narrower
// question. Pinned so the two cannot drift on what "current" means.
func TestStaleAbstractIsTheNarrowerQuestion(t *testing.T) {
	n := cleanSpec(t, "msg:010:02", "W2")
	n.AbstractOriginHash = nil
	if abstractVerification(n) != abstractUnverified {
		t.Fatal("precondition: this node is unverified")
	}
	// A citation defect it is not: an abstract that predates the hash is a
	// `spec lint` concern, which is exactly the rule above.
	if staleAbstract(n) {
		t.Error("unverified must not be reported as a stale citation")
	}
}

// PR #587 review, @copilot. A COMPILED body cannot be compared against a
// fingerprint taken over the source, so a template-backed spec would be
// reported stale with nothing changed — and templates are exactly what the
// single-ref read renders. Silence beats a false positive on a warning nobody
// can act on.
func TestAbstractChecksAreSilentOnACompiledBody(t *testing.T) {
	n := cleanSpec(t, "msg:010:02", "W2")
	moved := *n.Content + "\n\nAdded later.\n"
	n.Content = &moved // genuinely stale…
	n.ContentIsRaw = false
	if got := abstractVerification(n); got != abstractUncheckable {
		t.Fatalf("a compiled body is UNCHECKABLE, got %v", got)
	}
	if fs := lintNode(n, ""); hasRule(fs, "abstract-stale") || hasRule(fs, "abstract-unverified") {
		t.Errorf("must not report staleness from a body it cannot compare: %v", fs)
	}
	// …and the same node with a raw body does report it, so the gate is not
	// silently swallowing the whole feature.
	n.ContentIsRaw = true
	if fs := lintNode(n, ""); !hasRule(fs, "abstract-stale") {
		t.Errorf("a RAW body must still be compared: %v", fs)
	}
}

// PR #587 review, @copilot. The server hashes the STORED bytes, so a
// whitespace-only body is fingerprinted and an abstract over it can genuinely
// be unverified. Trimming before the emptiness test classified that as "nothing
// to check" and reported neither warning.
func TestWhitespaceOnlyBodyIsStillContent(t *testing.T) {
	n := cleanSpec(t, "msg:010:02", "W2")
	ws := " \n"
	n.Content = &ws
	n.AbstractOriginHash = nil
	if got := abstractVerification(n); got != abstractUnverified {
		t.Errorf("a whitespace-only body is still content to verify against, got %v", got)
	}
}

// ---- #708: lint reads every spec, whatever its loc ----

// What a lint scan reads: every spec (tag or role) at any loc, plus an untagged
// citation-shaped node so the missing-tag finding still reaches it (#241). A
// spec outside the legacy numbering is no longer dropped (@codex on #710).
func TestLintSelectsEverySpec(t *testing.T) {
	role := api.SpecNodeRole
	for _, c := range []struct {
		name string
		n    api.ListNode
		want bool
	}{
		{"tagged, legacy", api.ListNode{Loc: "msg:010:02", Tags: []string{"spec"}}, true},
		{"tagged, any loc", api.ListNode{Loc: "onboarding:mentor:screens", Tags: []string{"spec"}}, true},
		{"role only, any loc", api.ListNode{Loc: "glossary", Role: &role}, true},
		{"untagged, legacy-shaped (missing-tag finding)", api.ListNode{Loc: "msg:010:03"}, true},
		{"untagged, any other loc (not a spec)", api.ListNode{Loc: "register"}, false},
	} {
		if got := lintSelects(&c.n); got != c.want {
			t.Errorf("%s: lintSelects = %v, want %v", c.name, got, c.want)
		}
	}
	all := []*api.ListNode{{Loc: "onboarding:mentor", Tags: []string{"spec"}}, {Loc: "register"}, nil}
	if got := citationListNodes(all); len(got) != 1 || got[0].Loc != "onboarding:mentor" {
		t.Errorf("citationListNodes = %v, want only the spec outside the numbering", got)
	}
}

// Two nodes at one loc is a defect at any loc, not only a legacy one.
func TestDuplicateLocAtAnyLoc(t *testing.T) {
	n := specNode{Loc: "onboarding:mentor", Name: "onboarding:mentor — M", NodeType: "info", Tags: []string{"spec"}}
	if !hasRuleFor(lintCorpus([]specNode{n, n}, lintMem), "onboarding:mentor", "duplicate-loc") {
		t.Error("a duplicated loc outside the numbering must be reported")
	}
}

// A near-cap abstract on a spec outside the numbering gets the generic split
// remedy, never legacy index or contract guidance built from an empty
// Citation (@copilot on #710).
func TestNearCapAtAnyLocIsNotTierAdvice(t *testing.T) {
	sn := specNode{Loc: "onboarding:mentor", Name: "onboarding:mentor — Mentors", NodeType: "info", Tags: []string{"spec"}}
	abs := strings.Repeat("a", abstractHardMax-10)
	sn.Abstract = &abs
	var msg string
	for _, f := range lintNode(sn, "") {
		if f.Rule == "abstract-length" {
			msg = f.Message
		}
	}
	if msg == "" {
		t.Fatal("the hard cap binds every spec, so a near-cap abstract must be reported")
	}
	if strings.Contains(msg, indexRemedy(Citation{})) || strings.Contains(msg, contractRemedy) {
		t.Errorf("tier advice for a loc with no tier: %q", msg)
	}
	if !strings.Contains(msg, splitRemedy) {
		t.Errorf("want the generic split remedy: %q", msg)
	}
}

// A spec by its governed role but without the tag is a spec (isSpec), so the
// missing tag is a WARNING that says why it matters — the tag-based scans skip
// it — not the "broken" error a non-spec gets (@copilot on #710).
func TestTagSpecFindingKnowsTheRole(t *testing.T) {
	role := api.SpecNodeRole
	find := func(n specNode) *lintFindingDTO {
		for _, f := range lintNode(n, "") {
			if f.Rule == "tag-spec" {
				f := f
				return &f
			}
		}
		return nil
	}
	roleOnly := specNode{Loc: "onboarding:mentor", Name: "onboarding:mentor — M", NodeType: "info", Role: &role}
	if f := find(roleOnly); f == nil || f.Severity != sevWarning || !strings.Contains(f.Message, "skip this spec") {
		t.Errorf("role-only spec: tag-spec = %+v, want a warning explaining the scans skip it", f)
	} else if !strings.Contains(f.Message, "find --match-exactly") {
		t.Errorf("exact find filters by the tag too, so the warning must name it: %q", f.Message)
	} else if strings.Contains(f.Message, "lint") {
		t.Errorf("lint's own scans DO include a role-only spec, so the warning must not name lint: %q", f.Message)
	}
	untagged := specNode{Loc: "msg:010:02", Name: "msg:010:02 — W", NodeType: "info"}
	if f := find(untagged); f == nil || f.Severity != sevError {
		t.Errorf("untagged, no role: tag-spec = %+v, want the error", f)
	}
}

// #708: the legacy tier obligations are gone. A corpus that used to trip every
// one of them — a rule whose feature and module don't exist, no table-of-
// contents edge, a sibling contract it doesn't inherit, a header whose body
// lists no children — reports none, because `spec new <loc>` creates exactly
// such specs on purpose (@codex on #710).
func TestLintCorpusHasNoTierObligations(t *testing.T) {
	orphan := cleanSpec(t, "msg:010:02", "W2") // no msg:010, no msg …
	orphan.OutEdges = nil                      // … and no ToC edge
	nodes := []specNode{
		orphan,
		cleanSpec(t, "cor:acl:010:00", "Provisions"),
		cleanSpec(t, "cor:acl:010:01", "Rule"), // does not inherit :00
		{Loc: "cor:acl", Name: "cor:acl — ACL", NodeType: "info", Tags: []string{"spec"}, Content: ptr("# cor:acl — ACL\n")}, // lists no children
	}
	fs := lintCorpus(nodes, lintMem)
	for _, rule := range []string{"parent-exists", "toc-edge", "inheritance-edge", "index-incomplete"} {
		if hasRule(fs, rule) {
			t.Errorf("%s is a removed tier obligation, but was reported: %v", rule, fs)
		}
	}
}

// removedRubricRules are the old content rubric's findings, retired by
// Holger's ruling on #708 (team chat #1681): the sections a spec needs differ
// by its type, so no one rule is right. None may fire at any loc.
var removedRubricRules = []string{"abstract", "invalidates", "data-version", "scaffold-body", "placeholder-contract"}

// #708: a spec missing everything the old rubric demanded (no abstract, no
// "what invalidates", no data.version, and a `spec new` scaffold body) lints
// with NONE of those findings, at a numbered rule, a flow, a legacy contract
// and a named path alike. The structural checks still fire on the same nodes,
// so the lint is not simply silent.
func TestLintNodeNoRubricAtAnyLoc(t *testing.T) {
	// Three bodies: the scaffold (which carries a "what invalidates" heading, so
	// it would pass that check), an empty one (which would fail it), and the
	// case the ruling came from — a spec written by specs:tasks:write-spec,
	// whose sections are "What stays fixed" / "What can change".
	scaffold, empty := rubricBody(mustCit(t, "msg:010:02"), "W2"), ""
	writeSpec := "## Definition\n\nx\n\n## What stays fixed\n\ny\n\n## What can change\n\nz\n"
	for _, loc := range []string{"msg:010:02", "msg:010:02:01", "msg:010:00", "authoring:rules:naming"} {
		// Two abstracts: none, and the scaffold placeholder — the old rubric
		// flagged both, and a placeholder is what placeholder-contract keyed on.
		placeholder := placeholderAbstractAt(loc, "X")
		for _, abs := range []*string{nil, &placeholder} {
			for _, body := range []*string{&scaffold, &empty, &writeSpec} {
				n := specNode{Loc: loc, Name: loc + " — X", NodeType: "info", Tags: []string{"spec"}, Abstract: abs, Content: body}
				fs := lintNode(n, "")
				for _, gone := range removedRubricRules {
					if hasRule(fs, gone) {
						t.Errorf("%s: the retired rubric rule %q still fired; got %v", loc, gone, fs)
					}
				}
				// Negative controls: structure is still checked at the same loc.
				bad := n
				bad.Name, bad.NodeType, bad.Tags = "wrong", "finding", nil
				got := lintNode(bad, "")
				for _, want := range []string{"name-prefix", "nodetype-info", "tag-spec"} {
					if !hasRule(got, want) {
						t.Errorf("%s: the structural check %q must still fire; got %v", loc, want, got)
					}
				}
			}
		}
	}
}

// @copilot on #728: the length diagnostics apply to an abstract that still
// carries the scaffold placeholder, too. With the rubric's `abstract` rule
// gone, nothing else would report a long one — and the server's cap counts
// every character, placeholder or not.
func TestLintNodeAbstractLengthAppliesToAPlaceholderAbstract(t *testing.T) {
	for _, tc := range []struct {
		n   int
		sev string
	}{{abstractSoftMax + 50, sevWarning}, {abstractHardMax - 10, sevError}} {
		sn := cleanSpec(t, "msg:010:02", "W2")
		abs := abstractPlaceholder + " " + strings.Repeat("a", tc.n-len(abstractPlaceholder)-1)
		sn.Abstract = &abs
		var got string
		for _, f := range lintNode(sn, "") {
			if f.Rule == "abstract-length" {
				got = f.Severity
			}
		}
		if got != tc.sev {
			t.Errorf("a %d-char placeholder abstract: abstract-length severity %q, want %q", tc.n, got, tc.sev)
		}
	}
}
