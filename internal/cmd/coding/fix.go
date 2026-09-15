package coding

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Khan/genqlient/graphql"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
)

// plannedFix is one edge relabel: promote a check's description into an edge
// label that carries no condition.
type plannedFix struct {
	Loc      string
	EdgeID   string
	OldLabel string
	NewLabel string
}

// planReviewFix selects the mechanical subset of findings that can be repaired
// without a human: an empty or non-condition label on a check whose description
// already states the trigger. Anything else — a broken label on a node whose
// description has no condition either — needs a person and is left alone.
func planReviewFix(in reviewInput, findings []findingDTO) []plannedFix {
	broken := map[string]bool{}
	for _, f := range findings {
		if f.Rule == "label-present" || f.Rule == "label-is-condition" {
			broken[f.Node] = true
		}
	}
	var out []plannedFix
	for loc := range broken {
		e, ok := in.Edges[loc]
		if !ok {
			continue
		}
		trigger := triggerFromDescription(in.Members[loc].Description)
		if trigger == "" {
			continue
		}
		out = append(out, plannedFix{Loc: loc, EdgeID: e.ID, OldLabel: e.Label, NewLabel: trigger})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Loc < out[j].Loc })
	return out
}

// triggerFromDescription extracts an "Applies when …" sentence from a
// description, or "" when it carries no condition. Deliberately conservative:
// it only promotes text that is already spelled as a trigger, so --fix never
// invents a condition.
func triggerFromDescription(desc string) string {
	d := strings.TrimSpace(desc)
	if d == "" {
		return ""
	}
	lower := strings.ToLower(d)
	i := strings.Index(lower, triggerStem)
	if i < 0 {
		return ""
	}
	rest := d[i:]
	if j := sentenceEnd(rest); j > 0 {
		rest = rest[:j]
	}
	rest = strings.TrimSpace(rest)
	if len(strings.TrimSpace(rest[len(triggerStem):])) == 0 {
		return "" // the stem with nothing after it is not a condition either
	}
	if danglesMidClause(rest) {
		// Refuse rather than persist a plausible-but-wrong trigger (#381). This
		// function is already documented as conservative enough never to INVENT
		// a condition; writing a truncated one is the same error with the
		// evidence removed, so it gets the same answer — leave it for a human.
		return ""
	}
	return rest
}

// sentenceEnd returns the offset of the first real sentence end in s, or -1.
//
// The old rule was `strings.IndexAny(rest, ".\n")` under a comment that said
// "first sentence end" — and the two are not the same thing (#381). A period is
// a sentence end only SOMETIMES, and the exceptions are exactly the vocabulary
// review checks are written in: dotted identifiers (`AppLocale.current`), glob
// patterns (`lib/l10n/*.arb`), dotfile names (`.specify`). Three live labels
// were written truncated at one of those, and two of them came out as
// grammatically complete sentences — so nothing in the output said it had been
// cut, and the next lint run reported OK.
//
// A period counts ONLY when it is followed by whitespace or the end of the
// string. A newline always ends it.
//
// That one rule covers every case, including the dotfile one: `.specify` is
// excluded because its period is followed by a LETTER, not because the period
// has a space in front of it. An earlier version also required the period not
// be preceded by whitespace, reasoning about `.specify` from the wrong end —
// redundant, and it broke the degenerate `"Applies when ."`, where a lone
// trailing period really is the end and the existing test says so. Kept as a
// note because the simpler rule looks incomplete until you check it.
func sentenceEnd(s string) int {
	for i, r := range s {
		switch r {
		case '\n':
			return i
		case '.':
			if i+1 >= len(s) || isSpace(rune(s[i+1])) {
				return i
			}
		}
	}
	return -1
}

func isSpace(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\r' }

// danglingWords are function words a real trigger clause never ends on. A label
// ending in one is the tell that the clause was cut mid-thought — the shape
// `…or a` had when `.specify` truncated it.
var danglingWords = map[string]bool{
	"a": true, "an": true, "the": true, "and": true, "or": true, "of": true,
	"in": true, "on": true, "to": true, "with": true, "for": true, "from": true,
	"any": true, "its": true, "that": true, "which": true, "when": true,
	"whose": true, "by": true, "at": true, "as": true, "into": true, "per": true,
}

// danglesMidClause reports whether s ends somewhere a sentence would not.
func danglesMidClause(s string) bool {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return true
	}
	last := strings.ToLower(strings.Trim(fields[len(fields)-1], `,;:("'`+"`"))
	return danglingWords[last]
}

// applyReviewFix writes the planned relabels and returns how many landed.
//
// Each write is a single-edge updateEdge. It must never go through
// updateNode(edges:), which REPLACES a node's whole outgoing edge set and would
// destroy the check's sibling documented-by / relates-to edges — the hazard
// that makes the MCP surface unsafe for this repair (issue #325).
func applyReviewFix(ctx context.Context, client graphql.Client, f *cmdutil.Factory, in reviewInput, findings []findingDTO, yes bool) (int, error) {
	plan := planReviewFix(in, findings)
	if len(plan) == 0 {
		fmt.Fprintln(f.IOStreams.ErrOut, "--fix: nothing mechanically fixable (a broken label needs a description that already states its trigger)")
		return 0, nil
	}
	for _, p := range plan {
		fmt.Fprintf(f.IOStreams.ErrOut, "  %s: %q → %q\n", p.Loc, p.OldLabel, p.NewLabel)
	}
	if err := cmdutil.Confirm(f.IOStreams, yes,
		fmt.Sprintf("Relabel %d edge(s)?", len(plan))); err != nil {
		return 0, err
	}
	applied := 0
	for _, p := range plan {
		label := p.NewLabel
		if _, err := gen.UpdateEdge(ctx, client, p.EdgeID, &label, nil, nil, nil, nil, nil, nil); err != nil {
			return applied, fmt.Errorf("relabelling %s: %w", p.Loc, api.MapError(err))
		}
		applied++
	}
	return applied, nil
}
