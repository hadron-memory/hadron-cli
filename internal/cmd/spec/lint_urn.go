package spec

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	urnlib "github.com/hadron-memory/urn-lib-go"
)

// A worked example is the one part of a spec a reader EXECUTES, so when prose
// and example disagree the example wins in practice and the prose rots unread
// (@tove, #527). The two sit tens of lines apart in a long node and nobody
// diffs a spec against itself — so this rule puts each example's decomposition
// on screen, beside the prose, and lets the author adjudicate.
//
// ADVISORY, never a gate. The lint cannot know which reading an author
// intended, so failing a corpus run on one would be a guard asserting a
// classification it has not computed.
//
// WHAT IT REFUSES TO ANSWER is the load-bearing half. `urnlib.SplitNodeUrn`
// hard-codes "the memory is the first two segments" for BOTH grammars, but a
// v1 `::` chain uses the last-segment rule — they agree at three segments and
// diverge from the fourth on, so the library is wrong about exactly the deep
// chains this rule was filed to examine. Re-measured against urn-lib-go
// v0.0.13 and the running server on 2026-09-20; both readings unchanged. See
// findings:splitnodeurn-disagrees-with-the-server-on-deep-chains.
//
// Decomposing those here would print a confident wrong answer about the very
// node #527 was filed for — the failure mode this rule exists to catch, one
// level up. So a deep chain is REPORTED AS UNDECIDABLE and the reader is sent
// to the oracle that does know.

// urnLiteralRE matches a node-URN-shaped literal by SHAPE, which is the only
// safe test: `SplitNodeUrn("cor:urn:010:04")` returns memory "cor:urn", loc
// "010:04" and a nil error, so a bare spec citation decomposes cleanly and
// silently. Anything keyed on "it parsed" would report every citation in the
// corpus as a URN example.
//
// Two admitted shapes: a `hrn:`/`urn:` scheme prefix naming the node type, or
// a scheme-less `::` chain, which no citation ever contains.
var urnLiteralRE = regexp.MustCompile(
	`\b(?:(?:hrn|urn):node:[A-Za-z0-9._:-]+)|(?:\b[A-Za-z0-9._-]+(?:::[A-Za-z0-9._:-]+){2,})`)

// urnExampleFinding is one literal's verdict.
type urnExampleFinding struct {
	literal string
	// decomposed is false when the CLI cannot honestly say where the memory
	// ends — a deep v1 chain. It is NOT an error: the example may be perfect.
	decomposed bool
	memory     string
	loc        string
	why        string
}

// scanURNExamples finds every node-URN-shaped literal in a spec body and
// decomposes the ones the CLI can be right about.
//
// Deduplicated and sorted: a spec that shows one URN five times is making one
// claim, and five identical advisory lines would train a reader to skim them.
func scanURNExamples(body string) []urnExampleFinding {
	seen := map[string]bool{}
	var out []urnExampleFinding
	for _, m := range urnLiteralRE.FindAllString(body, -1) {
		lit := strings.Trim(m, ".,;:)")
		if lit == "" || seen[lit] {
			continue
		}
		seen[lit] = true
		out = append(out, classifyURNLiteral(lit))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].literal < out[j].literal })
	return out
}

// v1ChainSegments counts the `::`-separated segments of a literal's PATH — the
// part after any scheme prefix. Zero when the literal carries no `::` at all.
//
// This is a shape measurement, not a decomposition: it says how many atoms the
// author wrote, never where the memory ends. That distinction is why this does
// not violate #239/#693's "the CLI does not hand-roll URN decomposition" — it
// is counting separators to decide whether to ASK, not answering.
func v1ChainSegments(literal string) int {
	path := literal
	for _, p := range []string{"hrn:node:", "urn:node:"} {
		path = strings.TrimPrefix(path, p)
	}
	if !strings.Contains(path, "::") {
		return 0
	}
	return len(strings.Split(path, "::"))
}

func classifyURNLiteral(lit string) urnExampleFinding {
	f := urnExampleFinding{literal: lit}
	segs := v1ChainSegments(lit)

	// A deep v1 chain: the one case where the library and the server disagree.
	// Four is where they part, measured — not a guess at a safe margin.
	if segs >= 4 {
		f.why = fmt.Sprintf("deep v1 \"::\" chain (%d segments): urn-lib reads the memory as the first two "+
			"segments while the server reads it as all but the last, so the CLI cannot say where this one "+
			"ends. Ask the platform — `hadron_get_node %s` names the memory it tried", segs, lit)
		return f
	}

	parts, err := urnlib.SplitNodeUrn(lit)
	if err != nil {
		// An example nobody can execute is a defect on its own terms (#527) —
		// but still reported, not thrown: this rule never fails a corpus run.
		f.why = "does not decompose as a node URN: " + err.Error()
		return f
	}
	f.decomposed, f.memory, f.loc = true, parts.MemoryURN, parts.Loc
	return f
}

// lintURNExamples renders the findings as advisory lint lines.
//
// SIBLING CITATIONS ARE NOT EXAMPLES, and filtering them is what makes this
// rule readable. Measured against the live corpus before the filter: 400
// findings, of which 396 were `hrn:node:<this-memory>:<sibling>` cross-links —
// a spec citing the spec next door. Four were the deep chains #527 is about,
// and they were buried under a hundred times their number in noise.
//
// That is worse than not shipping: a nudge that is usually irrelevant trains a
// reader to skim the rule, which is the opposite of putting a disagreement
// where they cannot miss it. A literal addressing the memory being linted is a
// REFERENCE; an example is the one pointing somewhere else.
//
// The undecidable ones are never filtered — their memory is precisely what the
// CLI cannot determine, so excluding them would require the guess this rule
// exists to refuse.
func lintURNExamples(body, memURN string) []struct{ Rule, Severity, Message string } {
	var out []struct{ Rule, Severity, Message string }
	self := canonicalMemoryURN(memURN)
	for _, f := range scanURNExamples(body) {
		if f.decomposed && self != "" && canonicalMemoryURN(f.memory) == self {
			continue
		}
		var msg string
		if f.decomposed {
			msg = fmt.Sprintf("example %s → memory %s, loc %s — check this against what the prose says",
				f.literal, f.memory, f.loc)
		} else {
			msg = fmt.Sprintf("example %s → %s", f.literal, f.why)
		}
		out = append(out, struct{ Rule, Severity, Message string }{"urn-example", sevInfo, msg})
	}
	return out
}
