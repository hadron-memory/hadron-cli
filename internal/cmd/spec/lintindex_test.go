package spec

import (
	"strings"
	"testing"
)

// Fixtures here are taken from the SHAPES the live corpus actually writes, not
// from a model of them. That distinction is the reason #605's first attempt was
// wrong: its unit tests all passed while the check was reporting 72/72 against
// hrn:mem:hadronmemory.com:specs, because the fixtures were written from the
// same mistaken model as the matcher. Each index-body form below is copied from
// a real node and named with it.

// indexNode builds an index-tier parent carrying body.
func indexNode(loc, body, updated string) specNode {
	return specNode{Loc: loc, Content: &body, UpdatedAt: updated, ContentIsRaw: true, NodeType: "info"}
}

// childNode builds a child of an index, with no body of its own.
func childNode(loc, updated string) specNode {
	return specNode{Loc: loc, UpdatedAt: updated, ContentIsRaw: true, NodeType: "info"}
}

const (
	beforeTS = "2026-01-01T00:00:00Z"
	afterTS  = "2026-06-01T00:00:00Z"
)

func indexFindingFor(fs []lintFindingDTO, loc string) *lintFindingDTO {
	for i := range fs {
		if fs[i].Citation == loc && fs[i].Rule == ruleIndexIncomplete {
			return &fs[i]
		}
	}
	return nil
}

// A module index that says it has no features, while two exist. This is
// `cor:agt` as it stood on 2026-09-17 — the live case that proved the rule.
func TestIndexIncompleteFiresOnUncitedChildren(t *testing.T) {
	body := "# cor:agt — Agents\n\nAgent domain contracts. Reserved root — no features yet.\n\n" +
		"Entity shapes live in `cor:dmo:050`; behavioral rules will land here.\n"
	fs := indexIncompleteFindings([]specNode{
		indexNode("cor:agt", body, beforeTS),
		childNode("cor:agt:010", afterTS),
		childNode("cor:agt:020", afterTS),
	})
	f := indexFindingFor(fs, "cor:agt")
	if f == nil {
		t.Fatalf("expected an index-incomplete finding on cor:agt; got %v", fs)
	}
	if f.Severity != sevWarning {
		t.Errorf("severity = %q, want %q", f.Severity, sevWarning)
	}
	for _, want := range []string{"cor:agt:010", "cor:agt:020", "2 of its 2 children"} {
		if !strings.Contains(f.Message, want) {
			t.Errorf("message missing %q: %s", want, f.Message)
		}
	}
}

// THE MATCHER, BOTH DIRECTIONS IN ONE TABLE.
//
// Deliberately one table rather than an "accepts" test beside a "rejects" test:
// this rule is permissive on purpose, so the rows that must still FIRE are the
// ones at risk of lagging behind a widening — which is exactly how a check goes
// quiet (`review:an-exemption-you-cannot-enumerate-is-a-hiding-place`, where the
// false-negative rows arrived two rounds after the false-positive ones, in a
// separate test). Every accepted spelling is copied from a live corpus node and
// named with it.
func TestIndexIncompleteCitationForms(t *testing.T) {
	for _, tc := range []struct {
		name, parent, child, body string
		cited                     bool
	}{
		// ---- must NOT fire: the three spellings the corpus writes ----
		{
			// THE REGRESSION GUARD THIS RULE EXISTS BEHIND. The module tier
			// links by URN, so the citation's only appearance is preceded by a
			// colon. A leading word-boundary guard rejects it, taking module-tier
			// coverage from 76/81 to 12/81 and warning on every compliant index
			// in the corpus. Copied from `cor:acl`.
			name: "full citation inside a URN link target", cited: true,
			parent: "cor:acl", child: "cor:acl:010",
			body: "## Features\n\n- **[010 Memory access](hrn:node:hadronmemory.com:specs:cor:acl:010)** — who may read a memory.\n",
		},
		{
			// Copied from `cor:acl:100`. Without this form the rule warned three
			// times on an index that routes to all four of its children.
			name: "colon-leaf form", cited: true,
			parent: "cor:acl:100", child: "cor:acl:100:01",
			body: "## Rules under this feature\n\n- **`:01` Who may impersonate** — org ADMIN/OWNER only.\n",
		},
		{
			// What @Vera's `cor:agt:020` rewrite adopted to buy back the
			// characters that closed its abstract-length error.
			name: "relative last-two-atom form", cited: true,
			parent: "cor:agt:020", child: "cor:agt:020:00",
			body: "## Rules\n\n- (020:00) General provisions.\n",
		},
		{
			// A withdrawal correctly recorded must not warn. Needs no special
			// case: the loc is still in the string.
			name: "struck citation for a superseded child", cited: true,
			parent: "cor:agt:020", child: "cor:agt:020:06",
			body: "## Rules\n\n- ~~[`cor:agt:020:06`](hrn:node:hadronmemory.com:specs:cor:agt:020:06)~~ — **superseded** (rescinded 2026-08-14)\n",
		},
		{
			// The deliberate false negative, pinned so it is a known price
			// rather than a surprise: this does not strip code, because a partial
			// Markdown parser is a hiding place.
			name: "citation inside a fenced example still counts", cited: true,
			parent: "cor:acl", child: "cor:acl:010",
			body: "An example:\n\n```sh\nhadron spec get cor:acl:010\n```\n",
		},

		// ---- must STILL fire: a longer citation never satisfies a shorter ----
		{
			name: "grandchild does not credit child", cited: false,
			parent: "cor:acl", child: "cor:acl:010",
			body: "See [`cor:acl:010:01`](hrn:node:hadronmemory.com:specs:cor:acl:010:01) for the detail.\n",
		},
		{
			name: "other module's rule does not credit this one", cited: false,
			parent: "cor:agt:020", child: "cor:agt:020:09",
			body: "Compare `cor:oth:020:09`, which governs the other surface.\n",
		},
		{
			// The bare leaf adds zero coverage on the live corpus, and at the
			// rule tier it is two digits, which prose produces by accident.
			// Pinned so widening it is a decision rather than a tidy-up.
			name: "bare leaf atom is not a citation", cited: false,
			parent: "cor:agt:020", child: "cor:agt:020:09",
			body: "## Rules\n\n- 09 — the ninth rule, named but not cited.\n",
		},
		{
			// The digit before `:09` in `cor:api:060:09` is what rejects it.
			name: "colon-leaf not satisfied by a longer citation", cited: false,
			parent: "cor:acl:100", child: "cor:acl:100:09",
			body: "Compare `cor:api:060:09`, a different feature's ninth rule.\n",
		},
		{
			// @codex on #611: the relaxed lead guard that lets a URN link
			// through also lets a PRODUCT atom through, so a flat corpus's
			// `msg:010` was satisfied by a product-rooted `cor:msg:010` — a
			// different corpus's node certifying this one's index.
			name: "flat child not satisfied by a product-rooted citation", cited: false,
			parent: "msg", child: "msg:010",
			body: "## Features\n\nSee `cor:msg:010` in the platform corpus for the adjacent rule.\n",
		},
		{
			// The same guard must not cost the flat corpus its own URN links:
			// there the atom before the citation is the MEMORY slug, which does
			// not extend it into a valid citation.
			name: "flat child still cited through a URN link target", cited: true,
			parent: "msg", child: "msg:010",
			body: "## Features\n\n- [**010 W-series**](hrn:node:micromentor.org:platform-specs:msg:010) — the W rules.\n",
		},
		{
			// Same at rule depth, where the flat form has more atoms to be a
			// suffix of.
			name: "flat rule not satisfied by a product-rooted rule", cited: false,
			parent: "msg:010", child: "msg:010:02",
			body: "Compare `cor:msg:010:02`, which is a different corpus.\n",
		},
		{
			// A colon with NOTHING before it cannot make the match a tail, so
			// the tail check must not reject it. Pins the degenerate branch,
			// which a mutation otherwise walks straight through.
			name: "leading colon with no atom before it is not a tail", cited: true,
			parent: "cor:acl", child: "cor:acl:010",
			body: ":cor:acl:010 — written with a stray leading colon.\n",
		},
		{
			// @codex on #611, second round: the preceding atom must be taken
			// WHOLE. Scanning back over citation characters only stops at the
			// hyphen and tests `msg:msg:010`, which parses — rejecting a
			// citation that is plainly there, in a memory whose slug merely
			// ends in three letters.
			name: "hyphenated memory slug does not make a tail", cited: true,
			parent: "msg", child: "msg:010",
			body: "- [**010 W-series**](hrn:node:acme.com:platform-msg:msg:010) — the W rules.\n",
		},
		{
			// The same for a dot, which a slug may also carry.
			name: "dotted slug does not make a tail", cited: true,
			parent: "msg", child: "msg:010",
			body: "See hrn:node:acme.com:v1.msg:msg:010 for the rule.\n",
		},
		{
			name: "an index naming none of its children", cited: false,
			parent: "cor:agt", child: "cor:agt:020",
			body: "# cor:agt — Agents\n\nAgent domain contracts. Reserved root — no features yet.\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs := indexIncompleteFindings([]specNode{
				indexNode(tc.parent, tc.body, beforeTS),
				childNode(tc.child, afterTS),
			})
			f := indexFindingFor(fs, tc.parent)
			if tc.cited && f != nil {
				t.Fatalf("%s must count as cited; got %s", tc.child, f.Message)
			}
			if !tc.cited {
				if f == nil {
					t.Fatalf("expected %s to be reported uncited; got no finding", tc.child)
				}
				if !strings.Contains(f.Message, tc.child) {
					t.Errorf("message should name %s: %s", tc.child, f.Message)
				}
			}
		})
	}
}

// Scope, stated through the CHILD's level — the only unambiguous side, since a
// lone atom parses as a flat module and collides with a bare product root.
func TestIndexIncompleteScope(t *testing.T) {
	for _, tc := range []struct {
		name, parent, child string
		want                bool
	}{
		{"module tier is checked", "cor:acl", "cor:acl:010", true},
		{"feature tier is checked", "cor:acl:010", "cor:acl:010:01", true},
		{"flat-corpus module tier is checked", "msg", "msg:010", true},
		// The product root cites 0 of its 17 modules and is a different shape —
		// @Vera's to decide, not this rule's to warn on yet.
		{"product root is out of scope", "cor", "cor:acl", false},
		// One such parent exists corpus-wide, with one child; a one-item list is
		// not a convention.
		{"rule tier is out of scope", "cor:aut:030:01", "cor:aut:030:01:01", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs := indexIncompleteFindings([]specNode{
				indexNode(tc.parent, "# "+tc.parent+"\n\nNo children named here.\n", beforeTS),
				childNode(tc.child, afterTS),
			})
			if got := indexFindingFor(fs, tc.parent) != nil; got != tc.want {
				t.Errorf("finding on %s = %v, want %v (findings: %v)", tc.parent, got, tc.want, fs)
			}
		})
	}
}

// A contract is excluded as a PARENT and required as a CHILD, and the asymmetry
// is deliberate rather than an oversight (@copilot on #611 read it as one and
// asked for contracts to be dropped from `indexedChild` too).
//
// A contract indexes nothing — hence the parent exclusion. But it is indexed:
// the corpus lists it as the first entry of its parent's list, `- [`cor:agt:020:00`]
// (…) — **General provisions.**`, and `cor:acl` opens its Features list with
// `- **[000 General provisions](…cor:acl:000)**`. Measured over
// hrn:mem:hadronmemory.com:specs: **29 of 29** contract children are cited by
// their parent. Dropping them would exempt the one entry every sibling inherits
// from, on a rule about reachability.
func TestIndexIncompleteRequiresContractChildren(t *testing.T) {
	// A module whose Features list names the regular child but omits the
	// contract — the shape the exclusion would have made invisible.
	body := "# cor:acl — Access control\n\n## Features\n\n" +
		"- **[010 Memory access](hrn:node:hadronmemory.com:specs:cor:acl:010)** — who may read a memory.\n"
	fs := indexIncompleteFindings([]specNode{
		indexNode("cor:acl", body, beforeTS),
		childNode("cor:acl:000", afterTS), // the contract, uncited
		childNode("cor:acl:010", afterTS), // cited
	})
	f := indexFindingFor(fs, "cor:acl")
	if f == nil {
		t.Fatal("a contract child omitted from the index is a finding")
	}
	if !strings.Contains(f.Message, "cor:acl:000") {
		t.Errorf("the uncited contract must be named: %s", f.Message)
	}
	if strings.Contains(f.Message, "cor:acl:010") {
		t.Errorf("the cited regular child must not be reported: %s", f.Message)
	}
	// And the same index with the contract listed is clean — the pairing
	// @copilot asked for, so the rule is pinned in both directions.
	withContract := "# cor:acl — Access control\n\n## Features\n\n" +
		"- **[000 General provisions](hrn:node:hadronmemory.com:specs:cor:acl:000)** — the shared predicates.\n" +
		"- **[010 Memory access](hrn:node:hadronmemory.com:specs:cor:acl:010)** — who may read a memory.\n"
	clean := indexIncompleteFindings([]specNode{
		indexNode("cor:acl", withContract, beforeTS),
		childNode("cor:acl:000", afterTS),
		childNode("cor:acl:010", afterTS),
	})
	if f := indexFindingFor(clean, "cor:acl"); f != nil {
		t.Errorf("an index listing its contract is complete; got %s", f.Message)
	}
}

// A general-provisions contract is inherited by its siblings rather than
// indexing anything, so "your index omits a child" is not a claim about it —
// the exclusion @codex and @copilot arrived at independently on #609.
func TestIndexIncompleteSkipsContractParents(t *testing.T) {
	for _, parent := range []string{"cor:acl:000", "cor:acl:010:00"} {
		child := parent + ":01"
		if parent == "cor:acl:000" {
			child = "cor:acl:000:01"
		}
		fs := indexIncompleteFindings([]specNode{
			indexNode(parent, "# "+parent+"\n\nShared provisions.\n", beforeTS),
			childNode(child, afterTS),
		})
		if f := indexFindingFor(fs, parent); f != nil {
			t.Errorf("contract %s must not be told to route: %s", parent, f.Message)
		}
	}
}

// The two drift classes read differently to an author: one says the list fell
// behind, the other says the list was touched and the child left out.
func TestIndexIncompleteSplitsDriftClasses(t *testing.T) {
	body := "# cor:api:060 — Run task by name\n\nNo index list yet.\n"
	fs := indexIncompleteFindings([]specNode{
		indexNode("cor:api:060", body, "2026-05-01T00:00:00Z"),
		childNode("cor:api:060:01", "2026-01-01T00:00:00Z"), // predates the index
		childNode("cor:api:060:05", "2026-09-01T00:00:00Z"), // postdates it
	})
	f := indexFindingFor(fs, "cor:api:060")
	if f == nil {
		t.Fatal("expected a finding")
	}
	if !strings.Contains(f.Message, "cor:api:060:05 (last written after this index)") {
		t.Errorf("child-newer class not reported: %s", f.Message)
	}
	if !strings.Contains(f.Message, "cor:api:060:01 (already there when this index was last written)") {
		t.Errorf("parent-newer class not reported: %s", f.Message)
	}
	// The order is a NODE-wide timestamp, so it may not be stated as a claim
	// about the index list having been edited (@copilot on #611).
	if !strings.Contains(f.Message, "read the order as a lead") {
		t.Errorf("the order must be labelled as a lead: %s", f.Message)
	}
	for _, overclaim := range []string{"the list was touched", "left out", "drifted behind"} {
		if strings.Contains(f.Message, overclaim) {
			t.Errorf("node-wide updatedAt cannot support %q: %s", overclaim, f.Message)
		}
	}
}

// An exact tie is not evidence of order. `updatedAt` has millisecond
// resolution, so two writes can share one — and this used to fall through to
// the sharper "already there when this index was last written" claim
// (@copilot on #611).
func TestIndexIncompleteEqualTimestampsClaimNoOrder(t *testing.T) {
	const same = "2026-05-01T00:00:00Z"
	fs := indexIncompleteFindings([]specNode{
		indexNode("cor:api:060", "# cor:api:060\n\nNo list.\n", same),
		childNode("cor:api:060:01", same),
	})
	f := indexFindingFor(fs, "cor:api:060")
	if f == nil {
		t.Fatal("expected a finding")
	}
	if !strings.Contains(f.Message, "cor:api:060:01 (write order unknown)") {
		t.Errorf("a tie must claim no order: %s", f.Message)
	}
	if strings.Contains(f.Message, "read the order as a lead") {
		t.Errorf("no order was reported, so there is no lead to caveat: %s", f.Message)
	}
}

// "1 of its 1 children" (@copilot on #611). The `plural` helper cannot serve
// this one — it appends an "s" and would say "childs".
func TestIndexIncompleteSingularChildGrammar(t *testing.T) {
	fs := indexIncompleteFindings([]specNode{
		indexNode("cor:api:060", "# cor:api:060\n\nNo list.\n", beforeTS),
		childNode("cor:api:060:01", afterTS),
	})
	f := indexFindingFor(fs, "cor:api:060")
	if f == nil {
		t.Fatal("expected a finding")
	}
	if !strings.Contains(f.Message, "does not cite 1 of its 1 child —") {
		t.Errorf("singular grammar: %s", f.Message)
	}
	if strings.Contains(f.Message, "childs") {
		t.Errorf("plural() must not be used for this noun: %s", f.Message)
	}
}

// The remedy must name every spelling the matcher accepts, or an author reads
// their existing colon-leaf entry as not counting (@copilot on #611, 3 votes).
func TestIndexIncompleteRemedyNamesEveryAcceptedForm(t *testing.T) {
	fs := indexIncompleteFindings([]specNode{
		indexNode("cor:agt:020", "# cor:agt:020\n\nNo list.\n", beforeTS),
		childNode("cor:agt:020:09", afterTS),
	})
	f := indexFindingFor(fs, "cor:agt:020")
	if f == nil {
		t.Fatal("expected a finding")
	}
	// One assertion per accepted form, so widening the matcher without
	// widening the remedy fails here.
	for _, form := range []string{"`cor:agt:020:09`", "`020:09`", "`:09`", "struck"} {
		if !strings.Contains(f.Message, form) {
			t.Errorf("remedy omits the %s form: %s", form, f.Message)
		}
	}
}

// An unparseable or missing timestamp must not be guessed into a class. Silence
// on the class beats reporting the drift backwards.
func TestIndexIncompleteUndatedChildMakesNoClaim(t *testing.T) {
	fs := indexIncompleteFindings([]specNode{
		indexNode("cor:api:060", "# cor:api:060\n\nNo list.\n", ""),
		childNode("cor:api:060:01", ""),
	})
	f := indexFindingFor(fs, "cor:api:060")
	if f == nil {
		t.Fatal("expected a finding")
	}
	for _, forbidden := range []string{"drifted behind", "left out"} {
		if strings.Contains(f.Message, forbidden) {
			t.Errorf("must not claim a drift class without timestamps: %s", f.Message)
		}
	}
	if !strings.Contains(f.Message, "cor:api:060:01") {
		t.Errorf("the loc list is the useful part and must still be there: %s", f.Message)
	}
}

// Timestamps are compared as instants, not lexically: an offset spelling sorts
// the wrong way as a string while naming the same instant.
func TestIndexIncompleteComparesInstantsNotStrings(t *testing.T) {
	// 2026-05-01T01:00:00+02:00 is 2026-04-30T23:00:00Z — BEFORE the index,
	// though it sorts after it as a string.
	fs := indexIncompleteFindings([]specNode{
		indexNode("cor:api:060", "# cor:api:060\n\nNo list.\n", "2026-05-01T00:00:00Z"),
		childNode("cor:api:060:01", "2026-05-01T01:00:00+02:00"),
	})
	f := indexFindingFor(fs, "cor:api:060")
	if f == nil {
		t.Fatal("expected a finding")
	}
	if !strings.Contains(f.Message, "already there when this index was last written") {
		t.Errorf("an offset timestamp must be compared as an instant: %s", f.Message)
	}
}

// A node the corpus listed but could not read has no body to judge and no
// citation worth trusting; `unavailable` already reports it as an error.
func TestIndexIncompleteSkipsUnavailableNodes(t *testing.T) {
	fs := indexIncompleteFindings([]specNode{
		{Loc: "cor:acl", Unavailable: true},
		childNode("cor:acl:010", afterTS),
	})
	if len(fs) != 0 {
		t.Errorf("an unreadable index must not also be reported incomplete: %v", fs)
	}
}

// An empty index has cited nothing, which is the finding — not a crash and not
// a silent pass.
func TestIndexIncompleteEmptyBodyIsIncomplete(t *testing.T) {
	fs := indexIncompleteFindings([]specNode{
		{Loc: "cor:acl", UpdatedAt: beforeTS, ContentIsRaw: true},
		childNode("cor:acl:010", afterTS),
	})
	if indexFindingFor(fs, "cor:acl") == nil {
		t.Errorf("an index with no body cites nothing; expected a finding, got %v", fs)
	}
}

// A parent absent from the scan is `parent-exists`'s finding, not this one's —
// otherwise a scoped run reports the same gap twice under two names.
func TestIndexIncompleteSilentWhenParentIsNotInScope(t *testing.T) {
	fs := indexIncompleteFindings([]specNode{childNode("cor:acl:010", afterTS)})
	if len(fs) != 0 {
		t.Errorf("a missing parent belongs to parent-exists: %v", fs)
	}
}

// Deterministic output: findings come back in citation order however the scan
// happened to return the nodes.
func TestIndexIncompleteIsOrdered(t *testing.T) {
	var nodes []specNode
	for _, loc := range []string{"cor:sec", "cor:acl", "cor:int"} {
		nodes = append(nodes, indexNode(loc, "# "+loc+"\n\nNo list.\n", beforeTS), childNode(loc+":010", afterTS))
	}
	fs := indexIncompleteFindings(nodes)
	var got []string
	for _, f := range fs {
		got = append(got, f.Citation)
	}
	want := []string{"cor:acl", "cor:int", "cor:sec"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("findings order = %v, want %v", got, want)
	}
}

// The rule reaches a corpus lint, and is not raised by a single-node one (which
// cannot see a node's children).
func TestIndexIncompleteRunsInCorpusLintOnly(t *testing.T) {
	parent := indexNode("cor:acl", "# cor:acl — Access control\n\nNo features listed.\n", beforeTS)
	if fs := lintNode(parent, ""); hasRule(fs, ruleIndexIncomplete) {
		t.Errorf("a single-node lint cannot see children: %v", fs)
	}
	fs := lintCorpus([]specNode{parent, childNode("cor:acl:010", afterTS)}, "", lintMem)
	if !hasRuleFor(fs, "cor:acl", ruleIndexIncomplete) {
		t.Errorf("expected index-incomplete from lintCorpus; got %v", fs)
	}
}
