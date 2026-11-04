package spec

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// The decision ledger (cli#662): a read-only cross-check of the hadron-concept
// decision registry against the spec corpus. The grammar is Vera's
// (hrn:node:hadronmemory.com:hadron-dev-team-shared:designs:decision-ledger-grammar);
// this file is the pure half — no I/O — so the fixtures drive it directly.
//
// It reads ONLY registry IDs of the form D-YYYY-MM-DD-<NNN|X>. Platform decision
// codes (DA, DU-1; cor:dmo:140:02), ruling numbers and R-codes are never
// registry IDs and are never reported (Xabat's gate 6627866421).

const (
	ledgerRuleMissingDisposition = "missing-disposition" // category 1
	ledgerRuleUnresolved         = "unresolved"          // category 2
	ledgerRuleSuperseded         = "superseded"          // category 2
	ledgerRuleRetiredLive        = "retired-live"        // category 2
	ledgerRuleCitationUnbound    = "citation-unbound"    // category 2
	ledgerRuleCitationMalformed  = "citation-malformed"  // category 2
	ledgerRuleUnregisteredID     = "unregistered-id"     // category 3
	ledgerRuleUnparsedShorthand  = "unparsed-shorthand"  // category 3
	ledgerRulePending            = "pending"             // category 4
	ledgerRuleStatusUnknown      = "status-unknown"      // report
	ledgerRuleDispositionUnknown = "disposition-unknown" // report
	ledgerRuleRegistryMalformed  = "registry-malformed"  // report
	ledgerRuleDuplicateID        = "duplicate-id"        // report
	ledgerRuleUnreadableSpec     = "unreadable-spec"     // report (command level)

	// ledgerScopeFrom is the first day a decided entry must carry a Corpus line
	// (the registry header: "from 2026-09-22"). The date counted is the one in
	// the ID, which is immutable; the Date field is free text.
	ledgerScopeFrom = "2026-09-22"
)

// ledgerDoc is one searchable text of a spec: its body or abstract.
type ledgerDoc struct {
	Citation string
	Field    string // "content" | "abstract"
	Text     string
}

// ledgerFinding is one line of the ledger report (also the --json shape).
type ledgerFinding struct {
	// Category is 1-4 as in the issue; 0 is a report line about the input
	// itself (an unknown status, an unparseable shorthand, an unreadable spec).
	Category  int      `json:"category"`
	Rule      string   `json:"rule"`
	Severity  string   `json:"severity"` // error | info
	ID        string   `json:"id,omitempty"`
	Citation  string   `json:"citation,omitempty"`
	Locations []string `json:"locations"`
	Message   string   `json:"message"`
}

const (
	ledgerError = "error"
	ledgerInfo  = "info"
)

const ledgerIDPattern = `D-\d{4}-\d{2}-\d{2}-(?:\d{3}|[A-Z])`

var (
	ledgerIDRe      = regexp.MustCompile(ledgerIDPattern)
	ledgerHeadingRe = regexp.MustCompile(`^## (` + ledgerIDPattern + `)(?:[:\s]|$)`)
	ledgerFieldRe   = regexp.MustCompile(`^- \*\*([A-Za-z ]+):\*\* ?(.*)$`)
	// A backticked full citation or a relative `:NN`, optionally ended by a
	// range bound: `X`–`:NN` or `X`…`:NN`.
	ledgerCiteRe = regexp.MustCompile("`(cor(?::[a-z]{3})?(?::\\d{2,3})*|:\\d{2,3})`(?:\\s*[–…]\\s*`(:\\d{2,3})`)?")
)

// Longest first: "Codified" must not shadow "Codified in part".
var ledgerDispositions = []string{
	"Codified in part", "Codified (unminted)", "Codified", "Not a contract", "Pending codification", "Retired",
}

type ledgerEntry struct {
	ID, Status, Corpus string
	hasCorpus          bool
}

// checkLedger evaluates the registry text against the corpus. specs maps every
// spec citation to whether it is replaced (superseded). The result is sorted
// and deterministic.
func checkLedger(registry string, docs []ledgerDoc, specs map[string]bool) []ledgerFinding {
	var out []ledgerFinding
	add := func(f ledgerFinding) {
		if f.Locations == nil {
			f.Locations = []string{}
		}
		out = append(out, f)
	}

	entries, order, shape := parseLedgerRegistry(registry)
	for _, f := range shape {
		add(f)
	}
	// A file with no entry at all (wrong path, wrong file) must not read as a
	// clean ledger: with nothing to check, "✓ agree" would be a false all-clear.
	if len(entries) == 0 {
		add(ledgerFinding{Rule: ledgerRuleRegistryMalformed, Severity: ledgerError,
			Message: "the registry holds no `## D-YYYY-MM-DD-…` entry; is --registry the decisions.md file?"})
	}

	for _, id := range order {
		e := entries[id]
		class := ledgerStatusClass(e.Status)
		if class == "unknown" {
			add(ledgerFinding{Rule: ledgerRuleStatusUnknown, Severity: ledgerError, ID: id,
				Message: fmt.Sprintf("status %q is not one the ledger recognises; reported, not guessed", truncateRunes(e.Status, 60))})
		}
		if !e.hasCorpus {
			if class == "decided" && id[2:12] >= ledgerScopeFrom {
				add(ledgerFinding{Category: 1, Rule: ledgerRuleMissingDisposition, Severity: ledgerError, ID: id,
					Message: "decided on or after " + ledgerScopeFrom + " with no Corpus: disposition"})
			}
			continue
		}
		disp, rest := splitLedgerDisposition(e.Corpus)
		if disp == "" {
			add(ledgerFinding{Rule: ledgerRuleDispositionUnknown, Severity: ledgerError, ID: id,
				Message: fmt.Sprintf("Corpus: %q starts with no known disposition; reported, not guessed", truncateRunes(e.Corpus, 60))})
			continue
		}
		switch disp {
		case "Pending codification":
			add(ledgerFinding{Category: 4, Rule: ledgerRulePending, Severity: ledgerInfo, ID: id, Message: rest})
		case "Not a contract":
			// A reason, not a pointer: nothing to resolve.
		default: // Codified*, Retired
			var live, retired []string // Retired only: resolved targets by state
			for _, c := range expandLedgerCitations(rest) {
				if c.malformed {
					add(ledgerFinding{Category: 2, Rule: ledgerRuleCitationMalformed, Severity: ledgerError, ID: id, Citation: c.text,
						Message: "a citation range that cannot be expanded (no numeric last segment, or descending); not guessed"})
					continue
				}
				if c.unbound {
					add(ledgerFinding{Category: 2, Rule: ledgerRuleCitationUnbound, Severity: ledgerError, ID: id, Citation: c.text,
						Message: "a relative citation with no full citation before it; not guessed"})
					continue
				}
				replaced, ok := specs[c.text]
				switch {
				case !ok:
					add(ledgerFinding{Category: 2, Rule: ledgerRuleUnresolved, Severity: ledgerError, ID: id, Citation: c.text,
						Message: disp + " cites a spec that does not resolve"})
				case disp == "Retired":
					if replaced {
						retired = append(retired, c.text)
					} else {
						live = append(live, c.text)
					}
				case replaced:
					add(ledgerFinding{Category: 2, Rule: ledgerRuleSuperseded, Severity: ledgerError, ID: id, Citation: c.text,
						Message: disp + " cites a superseded spec"})
				}
			}
			// A Retired line expects a superseded target. One that names the
			// retired spec AND its live successor is the normal shape; one whose
			// every resolvable target is live retires nothing.
			if disp == "Retired" && len(live) > 0 && len(retired) == 0 {
				add(ledgerFinding{Category: 2, Rule: ledgerRuleRetiredLive, Severity: ledgerError, ID: id,
					Citation: strings.Join(dedupSorted(live), ", "),
					Message:  "Retired cites only specs that are still live; a retired decision's target is expected to be superseded"})
			}
		}
	}

	// Category 3: decision references in the corpus that no registry entry has.
	unregistered := map[string][]string{}
	unparsed := map[string][]string{}
	for _, d := range docs {
		for i, line := range strings.Split(d.Text, "\n") {
			line = strings.TrimSuffix(line, "\r")
			where := fmt.Sprintf("%s:%s:%d", d.Citation, d.Field, i+1)
			ids, bad := scanLedgerIDs(line)
			for _, id := range ids {
				if _, ok := entries[id]; !ok {
					unregistered[id] = append(unregistered[id], where)
				}
			}
			for _, b := range bad {
				unparsed[b] = append(unparsed[b], where)
			}
		}
	}
	for id, locs := range unregistered {
		add(ledgerFinding{Category: 3, Rule: ledgerRuleUnregisteredID, Severity: ledgerError, ID: id, Locations: dedupSorted(locs),
			Message: "cited in the corpus but not a registry entry"})
	}
	for text, locs := range unparsed {
		add(ledgerFinding{Category: 3, Rule: ledgerRuleUnparsedShorthand, Severity: ledgerError, Locations: dedupSorted(locs),
			Message: fmt.Sprintf("shorthand %q cannot be expanded; reported, not guessed", text)})
	}

	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Category != b.Category {
			return a.Category < b.Category
		}
		if a.Rule != b.Rule {
			return a.Rule < b.Rule
		}
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		if a.Citation != b.Citation {
			return a.Citation < b.Citation
		}
		return a.Message < b.Message
	})
	return out
}

func dedupSorted(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// parseLedgerRegistry reads the entries. A line "## D-…" that is not a valid
// heading is reported, not skipped: a malformed ID would otherwise hide an entry.
func parseLedgerRegistry(registry string) (map[string]*ledgerEntry, []string, []ledgerFinding) {
	entries := map[string]*ledgerEntry{}
	var order []string
	var shape []ledgerFinding
	var cur *ledgerEntry
	seenField := map[string]bool{}
	for _, line := range strings.Split(registry, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.HasPrefix(line, "## ") {
			cur = nil
			if m := ledgerHeadingRe.FindStringSubmatch(line); m != nil {
				id := m[1]
				if _, dup := entries[id]; dup {
					shape = append(shape, ledgerFinding{Rule: ledgerRuleDuplicateID, Severity: ledgerError, ID: id,
						Message: "two registry entries share this ID; the first is used"})
				} else {
					cur = &ledgerEntry{ID: id}
					entries[id] = cur
					order = append(order, id)
					seenField = map[string]bool{}
				}
			} else if strings.HasPrefix(line, "## D-") {
				shape = append(shape, ledgerFinding{Rule: ledgerRuleRegistryMalformed, Severity: ledgerError,
					Message: fmt.Sprintf("heading %q is not D-YYYY-MM-DD-<three digits|one capital letter>", truncateRunes(line, 60))})
			}
			continue
		}
		if cur == nil {
			continue
		}
		m := ledgerFieldRe.FindStringSubmatch(line)
		if m == nil || seenField[m[1]] {
			continue // first occurrence wins; indented continuations never match
		}
		seenField[m[1]] = true
		switch m[1] {
		case "Status":
			cur.Status = strings.TrimSpace(m[2])
		case "Corpus":
			cur.Corpus = strings.TrimSpace(m[2])
			cur.hasCorpus = true
		}
	}
	sort.Strings(order)
	return entries, order, shape
}

var (
	ledgerDecidedRe = regexp.MustCompile(`^(?:Decided|Implemented|Part|Superseded in part|Partially superseded)\b`)
	ledgerClosedRe  = regexp.MustCompile(`^(?:Superseded|Revisited|Deferred)\b`)
)

// ledgerStatusClass is decided | closed | unknown (grammar §2).
func ledgerStatusClass(status string) string {
	s := strings.TrimSpace(strings.TrimLeft(status, "*"))
	switch {
	case ledgerDecidedRe.MatchString(s):
		return "decided"
	case ledgerClosedRe.MatchString(s):
		return "closed"
	}
	return "unknown"
}

// splitLedgerDisposition returns the longest known disposition the value starts
// with, on a word boundary, and the text after it without its separator.
func splitLedgerDisposition(v string) (string, string) {
	for _, d := range ledgerDispositions {
		if !strings.HasPrefix(v, d) {
			continue
		}
		rest := v[len(d):]
		if rest != "" {
			r := []rune(rest)[0]
			if r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
				continue
			}
		}
		rest = strings.TrimSpace(rest)
		rest = strings.TrimSpace(strings.TrimLeft(rest, "—–-:"))
		return d, rest
	}
	return "", ""
}

type ledgerCite struct {
	text      string
	unbound   bool
	malformed bool // a range that cannot expand: no numeric last segment, or descending
}

// expandLedgerCitations resolves the backticked citations of a Corpus line
// (grammar §5). A full citation becomes the anchor; a relative `:NN` appends to
// a feature anchor (two segments below cor) and replaces the last segment of a
// deeper one; a range expands the last segment, keeping its width. Relative
// tokens never move the anchor.
func expandLedgerCitations(rest string) []ledgerCite {
	var out []ledgerCite
	anchor := ""
	for _, m := range ledgerCiteRe.FindAllStringSubmatch(rest, -1) {
		tok, hi := m[1], m[2]
		if strings.HasPrefix(tok, ":") {
			if anchor == "" {
				out = append(out, ledgerCite{text: tok, unbound: true})
				continue
			}
			if strings.Count(anchor, ":") <= 2 {
				tok = anchor + tok
			} else {
				tok = anchor[:strings.LastIndex(anchor, ":")] + tok
			}
		} else {
			anchor = tok
		}
		if hi == "" {
			out = append(out, ledgerCite{text: tok})
			continue
		}
		i := strings.LastIndex(tok, ":")
		if i < 0 {
			out = append(out, ledgerCite{text: tok, malformed: true})
			continue
		}
		parent, lo := tok[:i], tok[i+1:]
		a, errA := strconv.Atoi(lo)
		b, errB := strconv.Atoi(hi[1:])
		if errA != nil || errB != nil || b < a {
			out = append(out, ledgerCite{text: tok, malformed: true})
			continue
		}
		for n := a; n <= b; n++ {
			out = append(out, ledgerCite{text: fmt.Sprintf("%s:%0*d", parent, len(lo), n)})
		}
	}
	return out
}

// scanLedgerIDs returns every registry ID a line cites, with slash lists and
// ranges expanded (grammar §6), plus any shorthand it cannot expand. Matching
// is case-sensitive, so a lowercase anchor is never an ID.
func scanLedgerIDs(line string) (ids, bad []string) {
	pos := 0
	for _, loc := range ledgerIDRe.FindAllStringIndex(line, -1) {
		if loc[0] < pos {
			continue // swallowed by the previous ID's tail
		}
		// An ID is a whole token: a longer run of digits or letters around the
		// match is not that ID (D-…-0010, D-…-AB), and is reported, not trimmed.
		if loc[0] > 0 && isIDWordByte(line[loc[0]-1]) {
			continue
		}
		if loc[1] < len(line) && isIDWordByte(line[loc[1]]) {
			end := loc[1]
			for end < len(line) && isIDWordByte(line[end]) {
				end++
			}
			bad = append(bad, line[loc[0]:end])
			pos = end
			continue
		}
		base := line[loc[0]:loc[1]]
		ids = append(ids, base)
		prefix := base[:strings.LastIndex(base, "-")+1]
		end := loc[1]

		if items, next := ledgerSlashTail(line, end); len(items) > 0 {
			for _, it := range items {
				ids = append(ids, prefix+it)
			}
			pos = next
			continue
		}
		if hi, next, state := ledgerRangeTail(line, end, base[len(base)-1] >= 'A' && base[len(base)-1] <= 'Z'); state == rangeMalformed {
			bad = append(bad, strings.TrimSpace(line[loc[0]:next]))
			pos = next
		} else if state == rangeOK {
			text := strings.TrimSpace(line[loc[0]:next])
			lo, err := strconv.Atoi(base[len(base)-3:])
			switch {
			case err != nil || !isDigits(base[len(base)-3:]):
				bad = append(bad, text) // a range never applies to a letter ID
			case hi < lo:
				bad = append(bad, text)
			default:
				for n := lo + 1; n <= hi; n++ {
					ids = append(ids, fmt.Sprintf("%s%03d", prefix, n))
				}
			}
			pos = next
		}
	}
	return ids, bad
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

func skipSpaces(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return i
}

func isWordOrHyphen(b byte) bool {
	return b == '-' || b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// ledgerSlashTail reads "/003", "/ -004", "/B" ... after an ID. An item is three
// digits or ONE capital letter not followed by a word character or hyphen, so
// the D of a second full ID ("… / D-2026-…") is never an item.
func ledgerSlashTail(s string, i int) (items []string, next int) {
	next = i
	for {
		q := skipSpaces(s, next)
		if q >= len(s) || s[q] != '/' {
			return items, next
		}
		q = skipSpaces(s, q+1)
		if q < len(s) && s[q] == '-' {
			q++
		}
		n := 0
		switch {
		case q+3 <= len(s) && isDigits(s[q:q+3]):
			n = 3
		case q < len(s) && s[q] >= 'A' && s[q] <= 'Z':
			n = 1
		default:
			return items, next
		}
		if q+n < len(s) && isWordOrHyphen(s[q+n]) {
			return items, next
		}
		items = append(items, s[q:q+n])
		next = q + n
	}
}

const (
	rangeNone = iota
	rangeOK
	rangeMalformed // a range marker followed by digits that are not a three-digit bound
)

func isIDWordByte(b byte) bool {
	return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// ledgerRangeTail reads "…008", "...008", "–004", "… 010" after an ID.
//
// An ellipsis promises a bound: with no usable one (and no second full ID, the
// cross-date form) it is malformed. A dash is also ordinary prose, so only a
// digit run, or, after a letter ID, a letter directly against the dash
// ("A–C"), is read as a bound; "A – A note" stays prose.
func ledgerRangeTail(s string, i int, letterBase bool) (hi, next, state int) {
	q := skipSpaces(s, i)
	ellipsis := false
	switch {
	case strings.HasPrefix(s[q:], "…"):
		q += len("…")
		ellipsis = true
	case strings.HasPrefix(s[q:], "..."):
		q += 3
		ellipsis = true
	case strings.HasPrefix(s[q:], "–"):
		q += len("–")
	default:
		return 0, 0, rangeNone
	}
	afterMarker := q
	q = skipSpaces(s, q)
	if q < len(s) && s[q] == '-' {
		q++
	}
	if loc := ledgerIDRe.FindStringIndex(s[q:]); ellipsis && loc != nil && loc[0] == 0 {
		return 0, 0, rangeNone // "A … D-…": a cross-date bound is two IDs
	}
	end := q
	for end < len(s) && isIDWordByte(s[end]) {
		end++
	}
	digit := q < len(s) && s[q] >= '0' && s[q] <= '9'
	letter := q < len(s) && s[q] >= 'A' && s[q] <= 'Z'
	switch {
	case letterBase && letter && (ellipsis || q == afterMarker):
		return 0, end, rangeMalformed
	case !digit && ellipsis:
		return 0, max(end, afterMarker), rangeMalformed
	case !digit:
		return 0, 0, rangeNone
	}
	if end-q != 3 || !isDigits(s[q:end]) {
		return 0, end, rangeMalformed
	}
	n, _ := strconv.Atoi(s[q:end])
	return n, end, rangeOK
}
