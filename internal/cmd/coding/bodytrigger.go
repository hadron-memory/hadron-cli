package coding

import (
	"regexp"
	"strconv"
	"strings"
)

// Quoting the body's trigger paragraph in a label finding (#331).
//
// When a check's edge label is empty or isn't a condition, the text that
// *should* be in the label is usually sitting a few lines away in the node
// body. Measured across the 90 review checks in the four memories that have
// them, 57 state their scope in the body under one of two conventions:
//
//	> **Scope.** Adding or renaming a GraphQL argument that identifies …
//	**Applies when** a parent component reads a child's `bind:`-bound value …
//
// The finding quotes that text so whoever fixes it has the source material
// without opening each node.
//
// It is deliberately NOT fed to --fix. Those paragraphs run a median of 238
// characters (max 567) against a median healthy edge label of 84, so promoting
// one verbatim would produce a label 3x too long — and, since the edge loc is
// slugified from the name, an enormous derived loc with it. Condensing a scope
// paragraph into a trigger is a judgement call the linter hands over rather
// than makes.
var (
	reScopeMarker   = regexp.MustCompile(`(?i)^\s*>?\s*\*\*Scope\.?\*\*\s*(.*)$`)
	reAppliesMarker = regexp.MustCompile(`(?i)^\s*\*\*Applies when\*\*\s*(.*)$`)
)

// triggerQuoteLimit is how much of the paragraph a finding shows by default.
// A little above the p90 healthy label (142 chars), so what's displayed is
// roughly the size of the label being asked for. --suggest prints it whole.
const triggerQuoteLimit = 160

// bodyTrigger returns the scope/trigger paragraph a check's body states, and
// whether one was found. The paragraph is flattened to a single line.
//
// Both conventions wrap across lines: the blockquote form continues on
// following `>` lines, the inline form on following non-blank lines. Either
// ends at a blank line.
func bodyTrigger(content string) (string, bool) {
	lines := strings.Split(content, "\n")
	for i, ln := range lines {
		if m := reScopeMarker.FindStringSubmatch(ln); m != nil {
			// The marker's `>` is optional, so how the paragraph continues has
			// to follow the line actually matched — assuming blockquote would
			// silently drop the continuation of a plain `**Scope.**` paragraph.
			// Every Scope marker in the live memories is blockquoted today, so
			// this is a latent case rather than an observed one.
			quoted := strings.HasPrefix(strings.TrimSpace(ln), ">")
			return joinParagraph(m[1], lines[i+1:], quoted)
		}
		if m := reAppliesMarker.FindStringSubmatch(ln); m != nil {
			// Keep the marker: "Applies when X" already reads as the trigger,
			// whereas a Scope paragraph describes it.
			text, ok := joinParagraph(m[1], lines[i+1:], false)
			if !ok {
				return "", false
			}
			return "Applies when " + text, true
		}
	}
	return "", false
}

// joinParagraph flattens a marker line's remainder plus its continuation lines
// into one whitespace-normalised string. quoted selects blockquote
// continuation (lines starting `>`) over plain continuation (any non-blank).
func joinParagraph(first string, rest []string, quoted bool) (string, bool) {
	parts := []string{first}
	for _, ln := range rest {
		t := strings.TrimSpace(ln)
		if t == "" {
			break
		}
		if quoted {
			if !strings.HasPrefix(t, ">") {
				break
			}
			t = strings.TrimSpace(strings.TrimPrefix(t, ">"))
			if t == "" {
				break
			}
		} else if strings.HasPrefix(t, "#") || strings.HasPrefix(t, ">") {
			break // a new heading or blockquote ends the paragraph
		}
		parts = append(parts, t)
	}
	out := strings.Join(strings.Fields(strings.Join(parts, " ")), " ")
	if out == "" {
		return "", false
	}
	return out, true
}

// truncateRunes shortens s to at most n runes, backing off to a word boundary
// when one is near the limit so the quote doesn't end mid-word.
//
// Both the search and the threshold are in RUNES. strings.LastIndexAny would
// return a byte offset, which over-truncates multi-byte text: a CJK trigger
// whose only space sits at rune 50 has byte offset ~150, which passes a
// "3/4 of 160" test and cuts 160 runes down to 50.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	cut := r[:n]
	for i := len(cut) - 1; i > n*3/4; i-- {
		if cut[i] == ' ' || cut[i] == '\t' {
			cut = cut[:i]
			break
		}
	}
	return strings.TrimRight(string(cut), " \t,;:.") + "…"
}

// triggerHint is the sentence appended to a label finding when the body states
// a scope. Returns "" when it doesn't, so a finding gains nothing rather than
// carrying a "couldn't find one" line — a third of checks have no such
// paragraph, and that noise would land on every one of them.
func triggerHint(content string, full bool) string {
	text, ok := bodyTrigger(content)
	if !ok {
		return ""
	}
	if !full {
		text = truncateRunes(text, triggerQuoteLimit)
	}
	return " — the body states its scope, condense it into the label: " + strconv.Quote(text)
}
