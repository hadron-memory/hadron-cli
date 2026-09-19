package spec

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// `index-incomplete` — an index-tier spec must CITE each of its children.
//
// This is the second half of #605, and the surface it checks is the one the
// issue did NOT start on. Both readings originally proposed were about the
// ABSTRACT; @Vera measured the corpus and ruled them out, for one reason rather
// than three: an abstract is the RAG retrieval surface, and `abstract-length`
// ERRORS on it at the server's 2000-char cap. A citation string carries no
// meaning to an embedding and spends characters against a cap the same linter
// already polices, so requiring one per child would push abstracts toward the
// wall its neighbouring rule fires on. A signal that fights the rule it ships
// beside is not adoptable at any compliance rate.
//
// The convention the corpus actually has is TWO-LAYER, and it was not written
// down anywhere until this rule: the ABSTRACT routes by DESCRIBING subjects, for
// retrieval; the BODY indexes by CITING children, one entry per child. The
// second is what this checks, because it is the one that is exactly decidable —
// a loc string is present or it is not — and a fuzzy completeness check fails in
// the expensive direction, certifying an index as complete. No check beats a
// check that says "complete" when it is not.
//
// WHY IT EARNS ITS PLACE: `cor:agt:020` was abstract-complete (12/12) and
// body-incomplete (7/12) on the day it was held up as the worked example of a
// correct index — the only such node in the corpus. Neither abstract-side
// reading would have caught it. Only this one does.
const ruleIndexIncomplete = "index-incomplete"

// indexIncompleteFindings reports index-tier specs whose body omits a child's
// citation. One finding per PARENT, listing the uncited children: the loc list
// is the actionable part, and one finding per missing child would report a
// single unwritten index list a dozen times.
//
// Corpus-only by nature — it is a statement about a node's children, which a
// single-node lint cannot see. A scoped run (`--prefix`, `--module`) checks only
// the children inside the scan, so it under-reports rather than inventing gaps
// for children the scan deliberately omitted.
func indexIncompleteFindings(nodes []specNode) []lintFindingDTO {
	byLoc := make(map[string]*specNode, len(nodes))
	for i := range nodes {
		n := &nodes[i]
		// An unreadable node has no body to check and no citation worth
		// trusting; `unavailable` already reports it as an error.
		if n.Unavailable {
			continue
		}
		if _, dup := byLoc[n.Loc]; dup {
			continue // `duplicate-loc` owns this; pick one and move on
		}
		byLoc[n.Loc] = n
	}

	locs := make([]string, 0, len(byLoc))
	for loc := range byLoc {
		locs = append(locs, loc)
	}
	sort.Strings(locs)

	// Children grouped under their parent's loc, in citation order.
	kids := map[string][]*specNode{}
	parents := []string{}
	for _, loc := range locs {
		c, err := ParseCitation(loc)
		if err != nil || !indexedChild(c) {
			continue
		}
		p, ok := c.Parent()
		if !ok {
			continue
		}
		pLoc := p.Format()
		if _, seen := kids[pLoc]; !seen {
			parents = append(parents, pLoc)
		}
		kids[pLoc] = append(kids[pLoc], byLoc[loc])
	}
	sort.Strings(parents)

	out := []lintFindingDTO{}
	for _, pLoc := range parents {
		parent := byLoc[pLoc]
		if parent == nil {
			continue // `parent-exists` owns a dangling parent
		}
		pc, err := ParseCitation(pLoc)
		// A general-provisions CONTRACT is excluded for the same reason it is
		// excluded from indexTier (@codex + @copilot on #609): it is inherited
		// by its siblings rather than indexing anything, so "your index omits a
		// child" is not a claim about it. Zero contracts have children in the
		// live corpus, so this guard removes no findings today — it is here so
		// the first one that does is not told to route.
		if err != nil || pc.IsContract() {
			continue
		}
		body := ""
		if parent.Content != nil {
			body = *parent.Content
		}
		var childNewer, parentNewer, undated []string
		for _, kid := range kids[pLoc] {
			if bodyCitesChild(body, kid.Loc) {
				continue
			}
			switch childEditedAfter(kid, parent) {
			case editedAfter:
				childNewer = append(childNewer, kid.Loc)
			case editedBefore:
				parentNewer = append(parentNewer, kid.Loc)
			default:
				undated = append(undated, kid.Loc)
			}
		}
		total := len(kids[pLoc])
		missing := len(childNewer) + len(parentNewer) + len(undated)
		if missing == 0 {
			continue
		}
		out = append(out, lintFindingDTO{
			Citation: pLoc,
			Rule:     ruleIndexIncomplete,
			Severity: sevWarning,
			Message:  indexIncompleteMessage(missing, total, childNewer, parentNewer, undated),
		})
	}
	return out
}

// indexedChild reports whether this citation is a child whose PARENT is expected
// to index it — which is decided by the CHILD's level, not the parent's.
//
// That indirection is load-bearing rather than fussy. `ParseCitation` reads a
// lone atom as a flat module, so a bare product root (`cor`) and a real module
// (`cor:agt`) both arrive at level 1 and cannot be told apart from the parse
// alone — the ambiguity @codex found in `indexRemedy` on #609. A child's level
// has no such collision: a level-2 child means a module parent, a level-3 child
// means a feature parent, and nothing else can produce either.
//
// In scope: the module tier (feature children) and the feature tier (rule
// children) — @Vera's scope, where the index-list convention actually lives.
//
// Out, deliberately:
//   - the PRODUCT ROOT (level-1 children). It cites 0 of its 17 modules in the
//     live corpus and is a different shape; hers to decide, not this rule's to
//     warn on yet.
//   - the RULE tier (level-4 flow children). The corpus has exactly one such
//     parent, with one child, and a one-item list is not a convention.
func indexedChild(c Citation) bool { return c.Level() == 2 || c.Level() == 3 }

// bodyCitesChild reports whether an index body cites this child, in either form
// the corpus uses.
//
// THE MATCHER IS THE WHOLE RULE, and getting it wrong is not a near-miss: a
// too-narrow one reports a compliant index as empty, which is how both @Vera's
// probe and my first run produced a false zero on `cor:agt:020` — a node that
// cites all twelve of its children. Measured against
// hrn:mem:hadronmemory.com:specs, the two accepted forms cover 272 of 293
// child-edges; the 21 that remain are real gaps spread over 10 parents.
//
// Three forms, with deliberately different boundary rules:
//
//  1. THE FULL CITATION (`cor:agt:020:09`), matched after any non-identifier
//     character — including `:` and `/`. That looseness is the corpus's own
//     convention, not a concession: the module tier links by URN, writing
//     `[**010 Memory access**](hrn:node:hadronmemory.com:specs:cor:acl:010)`,
//     where the citation's only appearance is INSIDE the link target, preceded
//     by a colon. Requiring a word boundary in front collapsed module-tier
//     coverage from 76/81 to 12/81 — the same false zero one level up.
//
//  2. THE RELATIVE LAST-TWO-ATOM FORM (`020:09`), matched only at a word
//     boundary. This one needs the strict guard precisely because it is short:
//     without it, a cross-reference to `cor:oth:020:09` would satisfy an index
//     for `cor:agt:020:09` — a different module's rule certifying this one's.
//
//  3. THE COLON-LEAF FORM (`:09`), same strict guard. `cor:acl:100` writes its
//     whole index this way — "- **`:01` Who may impersonate** — …" — and it is
//     the form that nearly cost this rule its credibility: with only the first
//     two, the check warned three times on an index that routes to all four of
//     its children, on the one node whose residual finding I read by eye. The
//     strict guard is what makes it safe, and it is load-bearing here rather
//     than defensive: the character before `:01` in `cor:api:060:01` is a digit,
//     so a full citation of ANOTHER feature's rule 01 cannot satisfy this one.
//
// The BARE last atom (`020`, `09`) is NOT accepted, and that is measured rather
// than assumed: it adds zero coverage over the three forms above, because every
// node that writes a bare number also links the full citation. Accepting it
// would buy nothing and cost the check its precision — at the feature tier the
// bare atom is two digits, which prose produces by accident.
//
// WHAT THIS DELIBERATELY DOES NOT CHECK: that the citation sits in an index
// LIST. A child named once in passing prose counts as cited, as does one inside
// a fenced example — this does not strip code, because a partial parser is a
// hiding place that grows a case per review round (#547's lesson). That is the
// price of being exactly decidable, and it is the price @Vera chose — a
// predicate that judges whether a mention "is an index entry" is the fuzzy check
// her ruling rejected.
//
// WHICH FAILURE IS SILENT, stated because this rule INVERTS the usual answer.
// For a detector over content integrity the missed detection is the dangerous
// one, so ambiguity should resolve toward firing. Here it resolves toward NOT
// firing, and the asymmetry is measured rather than assumed: the strict matcher
// reports 71 gaps across 11 parents on a corpus its own specs engineer had just
// finished clearing, while this one reports none. A warning-tier convention rule
// that is wrong on compliant nodes is not a noisy rule, it is a DEAD one —
// #605 exists because 72 findings on a cleared corpus is "a rule enforced by
// advice nobody should take". A missed gap costs one unlisted child until the
// next edit; a false positive costs the rule.
//
// What keeps that safe is that the permissiveness is ENUMERABLE — three literal
// spellings, each pinned in TestIndexIncompleteCitationForms, not a grammar this
// function half-implements. Both directions live in that one table, so neither
// can lag the other.
//
// A STRUCK citation needs no special case, which is the happy consequence of
// matching the string rather than parsing the markup: the corpus records a
// retired child in place, `~~[`cor:agt:020:06`](…)~~ — **superseded**`, and the
// loc is still there. A withdrawal correctly recorded must not warn.
func bodyCitesChild(body, childLoc string) bool {
	if body == "" {
		return false
	}
	if containsToken(body, childLoc, leadIdentifier) {
		return true
	}
	for _, rel := range relativeCitations(childLoc) {
		if containsToken(body, rel, leadCitation) {
			return true
		}
	}
	return false
}

// relativeCitations returns the shorthands an index uses once its own citation
// has established the prefix: the child's last two atoms (`020:09`) and its leaf
// behind a colon (`:09`). Empty for a citation with fewer than two atoms, which
// cannot be a child anyway.
func relativeCitations(loc string) []string {
	atoms := strings.Split(loc, ":")
	if len(atoms) < 2 {
		return nil
	}
	return []string{
		strings.Join(atoms[len(atoms)-2:], ":"),
		":" + atoms[len(atoms)-1],
	}
}

// leadMode is how strict the character BEFORE a match must be.
type leadMode int

const (
	// leadIdentifier rejects only a character that would make the match part of
	// a longer identifier. `:` and `/` are allowed through, so a citation inside
	// an `hrn:node:…` URN or a URL path counts — which is how the corpus links.
	leadIdentifier leadMode = iota
	// leadCitation additionally rejects `:`, `/`, `.` and `-`, so a short
	// relative form cannot be satisfied by the tail of a longer citation.
	leadCitation
)

// containsToken reports whether tok occurs in s as a whole token: never running
// on into a longer one, never the TAIL of a longer one, and preceded by a
// character the mode permits.
//
// The trailing test is `wholeToken`, shared with the source scanner, so the two
// surfaces cannot drift on where a citation ends. It is what keeps a parent that
// links only the GRANDCHILD `cor:acl:010:01` from being credited with citing the
// child `cor:acl:010`.
func containsToken(s, tok string, lead leadMode) bool {
	for i := 0; i+len(tok) <= len(s); {
		j := strings.Index(s[i:], tok)
		if j < 0 {
			return false
		}
		start := i + j
		if wholeToken(s, start+len(tok)) && leadingOK(s, start, tok, lead) {
			return true
		}
		i = start + 1
	}
	return false
}

func leadingOK(s string, start int, tok string, lead leadMode) bool {
	if start == 0 {
		return true
	}
	switch c := s[start-1]; {
	case c >= '0' && c <= '9', c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '_', c == '-':
		return false
	case c == ':':
		// A colon in front is how the corpus LINKS — the citation's only
		// appearance is often inside an `hrn:node:…:specs:cor:acl:010` target —
		// so leadIdentifier has to let it through. But the same colon is also how
		// a longer CITATION is spelled, and there the match is somebody else's
		// tail: `cor:msg:010` must not satisfy a flat corpus's `msg:010`
		// (@codex on #611). Only the grammar can tell those apart.
		return lead == leadIdentifier && !isTailOfLongerCitation(s, start, tok)
	case c == '/' || c == '.':
		return lead == leadIdentifier
	}
	return true
}

// isTailOfLongerCitation reports whether the match at start is the suffix of a
// longer VALID citation — which is decided by `ParseCitation` rather than by
// re-implementing the grammar here, so the two cannot drift.
//
// Only a FLAT citation can be one: the grammar is
// `[<product>:]<module>[:<feature>[:<rule>[:<flow>]]]`, so prefixing an atom to
// an already product-rooted citation always overruns it. That is why this costs
// nothing on the live corpus, which is product-rooted throughout — it closes the
// hole for the flat corpora (`msg:010:02`) that the same linter serves.
//
// THE PRECEDING ATOM IS TAKEN WHOLE, hyphens and dots included, and that is the
// correctness of this function rather than a detail. The atom before the colon
// is usually a URN's MEMORY SLUG, and a slug may contain them: scanning back
// over citation characters only stops at the hyphen in
// `hrn:node:acme.com:platform-msg:msg:010` and tests `msg:msg:010`, which parses
// — so a legitimate citation in a memory whose slug merely ENDS in three letters
// is rejected and warned on (@codex on #611, second round, against exactly the
// imprecision the first version had named too narrowly).
//
// ONE IMPRECISION SURVIVES, and it is now the whole of it: a memory slug that is
// itself exactly three lowercase letters (`hrn:node:acme.com:abc:msg:010`)
// parses as a product, and a flat citation under it is rejected. That direction
// is a false WARNING, not a false clean, and it needs the corpus to be flat AND
// the memory to be named in three letters. The alternative — trusting the colon
// — is the false clean this function exists for.
func isTailOfLongerCitation(s string, start int, tok string) bool {
	atomEnd := start - 1 // the ':' itself
	atomStart := atomEnd
	for atomStart > 0 && isSlugAtomByte(s[atomStart-1]) {
		atomStart--
	}
	if atomStart == atomEnd {
		return false // nothing but the colon in front
	}
	_, err := ParseCitation(s[atomStart:atomEnd] + ":" + tok)
	return err == nil
}

// isSlugAtomByte reports whether c can occur INSIDE the atom before a colon —
// a citation atom, or a URN/URL label, which also admits `-` and `.`. Wider than
// a citation atom on purpose: the point is to recover the preceding atom whole,
// so that a slug is tested as a slug and fails to parse as a product.
func isSlugAtomByte(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' ||
		c == '-' || c == '.' || c == '_'
}

// editOrder is how a missing child's timestamp sits against its index's.
type editOrder int

const (
	// editUnknown — the order is not claimed. Either timestamp missing or
	// unparseable, and ALSO an exact tie: `updatedAt` has one-millisecond
	// resolution and two writes in the same millisecond say nothing about which
	// came first. This used to fall through to editedBefore and make the sharper
	// claim on no evidence (@copilot on #611).
	editUnknown editOrder = iota
	// editedAfter — the child was last written after the index.
	editedAfter
	// editedBefore — the child already existed when the index was last written.
	editedBefore
)

// childEditedAfter orders a missing child against its index by last write.
//
// WHAT THIS CAN AND CANNOT SUPPORT, because the message is only allowed to claim
// the first (@copilot on #611). `updatedAt` is a NODE-wide mutation timestamp: an
// abstract rewrite, a tag, a data patch or an edge moves it without touching the
// body. So the order is real evidence about when each NODE was last written, and
// it is NOT evidence that the index list itself was reviewed and the child left
// out of it. The earlier wording said exactly that, and it outran what a
// node-wide timestamp can carry.
//
// A body-specific timestamp would support the stronger claim, and none is on the
// wire — `NodeRevision` would have to be read per node, which is a query per
// child on a corpus-wide lint. The order is worth printing as a hint; the
// message now says which it is.
//
// Timestamps are compared as PARSED INSTANTS rather than lexically. Both sides
// arrive as RFC3339 strings and the live corpus writes them in UTC, where a
// lexical compare happens to agree — but it agrees by accident, and an offset
// spelling (`+02:00`) would silently invert the classification rather than fail.
func childEditedAfter(child, parent *specNode) editOrder {
	ct, cerr := time.Parse(time.RFC3339, child.UpdatedAt)
	pt, perr := time.Parse(time.RFC3339, parent.UpdatedAt)
	if cerr != nil || perr != nil {
		return editUnknown
	}
	switch {
	case ct.After(pt):
		return editedAfter
	case pt.After(ct):
		return editedBefore
	default:
		return editUnknown
	}
}

// children renders a child count with its noun. Not `plural`, which appends an
// "s" and would say "1 of its 1 childs" — the irregular plural is exactly the
// case that helper cannot serve (@copilot on #611, on the "1 of its 1 children"
// the first version printed).
func children(n int) string {
	if n == 1 {
		return "1 child"
	}
	return fmt.Sprintf("%d children", n)
}

// indexIncompleteMessage renders the finding: the counts, the uncited locs
// grouped by write order, and one remedy.
//
// The locs come first in every group, since the list is what gets acted on. The
// order is offered as a lead and labelled as one — see childEditedAfter for why
// it cannot be stated as a claim about the index list itself.
func indexIncompleteMessage(missing, total int, childNewer, parentNewer, undated []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "body does not cite %d of its %s — an index routes to its children by citing them, so an uncited child is unreachable from its own parent.",
		missing, children(total))
	for _, g := range []struct {
		locs []string
		why  string
	}{
		{childNewer, "last written after this index"},
		{parentNewer, "already there when this index was last written"},
		{undated, "write order unknown"},
	} {
		if len(g.locs) > 0 {
			fmt.Fprintf(&b, " %s (%s).", strings.Join(g.locs, ", "), g.why)
		}
	}
	if len(childNewer) > 0 || len(parentNewer) > 0 {
		// Named as a lead rather than left to read as a finding: `updatedAt`
		// moves on ANY field, so it cannot say the index list was rewritten.
		b.WriteString(" Those are whole-node timestamps, so read the order as a lead, not as proof the index itself was edited.")
	}
	b.WriteString(" Add one entry per child to the index list citing the child's loc — the full citation (`cor:agt:020:09`), the last two atoms (`020:09`), or the colon-leaf (`:09`) all count, as does a struck entry for a superseded child.")
	return b.String()
}
