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
// on into a longer one, and preceded by a character the mode permits.
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
		if wholeToken(s, start+len(tok)) && leadingOK(s, start, lead) {
			return true
		}
		i = start + 1
	}
	return false
}

func leadingOK(s string, start int, lead leadMode) bool {
	if start == 0 {
		return true
	}
	switch c := s[start-1]; {
	case c >= '0' && c <= '9', c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '_', c == '-':
		return false
	case c == ':' || c == '/' || c == '.':
		return lead == leadIdentifier
	}
	return true
}

// editOrder is how a missing child's timestamp sits against its index's.
type editOrder int

const (
	// editUnknown — one of the two timestamps is missing or unparseable, so the
	// drift class is not claimed. Silence beats guessing which way it went.
	editUnknown editOrder = iota
	// editedAfter — the child changed after the index was last written: the list
	// has simply drifted behind.
	editedAfter
	// editedBefore — the index was written AFTER the child and still omits it:
	// somebody touched the list and did not add it. The sharper class.
	editedBefore
)

// childEditedAfter classifies the drift, which is @Vera's third convention
// detail and the one that changes what an author does about it.
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
	if ct.After(pt) {
		return editedAfter
	}
	return editedBefore
}

// indexIncompleteMessage renders the finding: the counts, the uncited locs
// grouped by drift class, and one remedy.
//
// The two classes are named because they mean different things to the author —
// "the list has drifted behind" versus "the list was rewritten and still omits
// this" — but the locs come first in both, since the list is what gets acted on.
func indexIncompleteMessage(missing, total int, childNewer, parentNewer, undated []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "body does not cite %d of its %d children — an index routes to its children by citing them, so an uncited child is unreachable from its own parent.",
		missing, total)
	for _, g := range []struct {
		locs []string
		why  string
	}{
		{childNewer, "edited since this index was last written, so the list has drifted behind"},
		{parentNewer, "already existed when this index was last written, so the list was touched and they were left out"},
		{undated, "uncited"},
	} {
		if len(g.locs) > 0 {
			fmt.Fprintf(&b, " %s (%s).", strings.Join(g.locs, ", "), g.why)
		}
	}
	b.WriteString(" Add one entry per child to the index list, citing the child's loc — the full citation, or the last two atoms; a struck entry for a superseded child counts as cited.")
	return b.String()
}
