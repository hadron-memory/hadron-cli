package spec

import (
	"encoding/json"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The fixtures are Vera's authoritative ones (designs:decision-ledger-grammar §9,
// vendored byte for byte). Today's registry is NOT pinned: only these.

func ledgerFixture(t *testing.T) (string, []ledgerDoc, map[string]bool) {
	t.Helper()
	reg, err := os.ReadFile("testdata/ledger/registry.md")
	if err != nil {
		t.Fatal(err)
	}
	return string(reg), ledgerFixtureDocs(t), ledgerFixtureSpecs(t)
}

var corpusLine = regexp.MustCompile(`^(.+?):(\d+): (.*)$`)

func ledgerFixtureDocs(t *testing.T) []ledgerDoc {
	t.Helper()
	raw, err := os.ReadFile("testdata/ledger/corpus.txt")
	if err != nil {
		t.Fatal(err)
	}
	var docs []ledgerDoc
	for _, l := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		m := corpusLine.FindStringSubmatch(l)
		if m == nil {
			t.Fatalf("bad corpus fixture line %q", l)
		}
		n, _ := strconv.Atoi(m[2])
		// Pad so the fixture's line number is the line within the body.
		docs = append(docs, ledgerDoc{Citation: m[1], Field: "content", Text: strings.Repeat("\n", n-1) + m[3]})
	}
	return docs
}

func ledgerFixtureSpecs(t *testing.T) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile("testdata/ledger/specs.json")
	if err != nil {
		t.Fatal(err)
	}
	var list []struct {
		Citation string `json:"citation"`
		Replaced bool   `json:"replaced"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, s := range list {
		out[s.Citation] = s.Replaced
	}
	return out
}

// keys renders findings of one rule as "ID" or "ID->citation" for exact comparison.
func ledgerKeys(fs []ledgerFinding, rule string) []string {
	var out []string
	for _, f := range fs {
		if f.Rule != rule {
			continue
		}
		k := f.ID
		if f.Citation != "" {
			k += "->" + f.Citation
		}
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func eq(t *testing.T, what string, got, want []string) {
	t.Helper()
	if len(got) != len(want) || strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("%s:\n got  %q\n want %q", what, got, want)
	}
}

// Every rule the fixtures exercise appears with EXACTLY the expected members:
// a positive control per defect, and the negatives (out-of-scope date, closed
// entry, Retired->superseded, two-IDs-joined-by-slash, the cor:dmo:140 codes)
// are absent. "Nothing else" is the second half of each assertion.
func TestLedgerFixtureFindings(t *testing.T) {
	reg, docs, specs := ledgerFixture(t)
	fs := checkLedger(reg, docs, specs)

	eq(t, "cat1 missing-disposition", ledgerKeys(fs, "missing-disposition"),
		[]string{"D-2026-10-01-001", "D-2026-10-01-002"})
	eq(t, "cat2 unresolved", ledgerKeys(fs, "unresolved"), []string{"D-2026-10-01-B->cor:spc:020:99"})
	eq(t, "cat2 superseded", ledgerKeys(fs, "superseded"), []string{"D-2026-10-01-C->cor:spc:020:04"})
	eq(t, "cat3 unregistered-id", ledgerKeys(fs, "unregistered-id"),
		[]string{"D-2026-10-01-007", "D-2026-10-02-005", "D-2026-11-30-001"})
	eq(t, "cat4 pending", ledgerKeys(fs, "pending"), []string{"D-2026-10-02-001"})
	eq(t, "disposition-unknown", ledgerKeys(fs, "disposition-unknown"), []string{"D-2026-10-02-002"})

	// Nothing besides those six rules.
	allowed := map[string]bool{"missing-disposition": true, "unresolved": true, "superseded": true,
		"unregistered-id": true, "pending": true, "disposition-unknown": true}
	for _, f := range fs {
		if !allowed[f.Rule] {
			t.Errorf("unexpected finding %+v", f)
		}
	}
}

// The cor:dmo:140 boundary (Xabat's gate 6627866421): DA, DU-1, DU-12,
// rulings:0061, R218, V51, session D1/D3/D14 and a lowercase anchor are never
// unregistered decisions. Line 19 of the corpus fixture is the negative control.
func TestLedgerBoundaryNegativeControl(t *testing.T) {
	reg, docs, specs := ledgerFixture(t)
	fs := checkLedger(reg, docs, specs)
	for _, f := range fs {
		for _, loc := range f.Locations {
			if loc == "cor:dmo:050:01:content:19" {
				t.Errorf("line 19 (the cor:dmo:140 codes) produced %+v", f)
			}
		}
	}
	// And in isolation: a corpus of nothing but those tokens is clean.
	only := []ledgerDoc{{Citation: "cor:x:010", Field: "content",
		Text: "DA, DU-1, decision DU-12, rulings:0061, R218, V51, session D1, D3 and D14, #d-2026-10-01-001-in-scope"}}
	if got := checkLedger(reg, only, specs); len(ledgerKeys(got, "unregistered-id")) != 0 {
		t.Errorf("boundary tokens reported as unregistered: %+v", got)
	}
}

// Two full IDs joined by a slash are two singles, never a letter item "D".
func TestLedgerTwoIDsJoinedBySlashAreTwoSingles(t *testing.T) {
	reg, _, specs := ledgerFixture(t)
	docs := []ledgerDoc{{Citation: "cor:x:010", Field: "content", Text: "D-2026-10-01-001 / D-2026-10-01-B"}}
	if got := ledgerKeys(checkLedger(reg, docs, specs), "unregistered-id"); len(got) != 0 {
		t.Errorf("got %q, want none", got)
	}
}

// Range cases the issue names: same-date bounds, malformed, descending, letter
// base, and a hyphen/space tolerant slash list. Registered members are
// D-2026-10-02-002..004 and D-2026-10-01-001/002/003 style numbers.
func TestLedgerRangeShorthand(t *testing.T) {
	reg, _, specs := ledgerFixture(t)
	// Only category 3: the fixture registry carries findings of its own.
	run := func(text string) []ledgerFinding {
		var out []ledgerFinding
		for _, f := range checkLedger(reg, []ledgerDoc{{Citation: "cor:x:010", Field: "content", Text: text}}, specs) {
			if f.Category == 3 {
				out = append(out, f)
			}
		}
		return out
	}
	// All three spellings expand inclusively.
	for _, op := range []string{"…", "...", "–"} {
		if got := run("D-2026-10-02-002" + op + "004"); len(got) != 0 {
			t.Errorf("range with %q: %+v", op, got)
		}
		if got := run("D-2026-10-02-002 " + op + " -004"); len(got) != 0 {
			t.Errorf("spaced range with %q: %+v", op, got)
		}
	}
	// One past the registered end is reported, and only that one.
	eq(t, "past end", ledgerKeys(run("D-2026-10-02-002…006"), "unregistered-id"),
		[]string{"D-2026-10-02-005", "D-2026-10-02-006"})
	// Descending is reported, never silently empty.
	eq(t, "descending", ledgerKeys(run("D-2026-10-02-004…002"), "unparsed-shorthand"), []string{""})
	// A range never applies to a letter ID.
	eq(t, "letter base", ledgerKeys(run("D-2026-10-01-A…004"), "unparsed-shorthand"), []string{""})
	// An item must end at a boundary: "-x" after three digits is not an item.
	if got := run("D-2026-10-01-001/007-x"); len(got) != 0 {
		t.Errorf("item with trailing hyphen word: %+v", got)
	}
	// Cross-date: the second bound is a full ID, so these are two singles.
	if got := run("D-2026-10-02-002 … D-2026-10-02-004"); len(got) != 0 {
		t.Errorf("cross-bound: %+v", got)
	}
	// Slash list: spaces and an optional hyphen, letters allowed.
	if got := run("D-2026-10-01-001 / -002 /003"); len(got) != 0 {
		t.Errorf("slash list: %+v", got)
	}
	if got := run("D-2026-10-01-A/B/-C"); len(got) != 0 {
		t.Errorf("letter slash list: %+v", got)
	}
}

// A Retired line whose cited specs are ALL live is a finding (Victor's builder
// note, cli#662 comment 6628458382): a Retired entry expects superseded targets.
// A line that cites a superseded target AND its live successor is the normal
// shape (fixture D-2026-10-01-D) and stays clean.
func TestLedgerRetiredCitingLiveSpec(t *testing.T) {
	reg, _, specs := ledgerFixture(t)
	reg += "\n## D-2026-10-03-001: Retired but its target is live\n- **Status:** Superseded (never built)\n- **Date:** 2026-10-03\n- **Corpus:** Retired — `cor:spc:020:05`\n"
	fs := checkLedger(reg, nil, specs)
	eq(t, "retired-live", ledgerKeys(fs, "retired-live"), []string{"D-2026-10-03-001->cor:spc:020:05"})
	// ...and the fixture's own Retired entry (superseded target) stays clean.
	for _, k := range ledgerKeys(fs, "retired-live") {
		if strings.HasPrefix(k, "D-2026-10-01-D") {
			t.Errorf("Retired -> superseded flagged: %s", k)
		}
	}
	// Superseded target plus live successor: clean. Two live targets: one finding.
	reg3 := reg + "\n## D-2026-10-03-003: ok\n- **Status:** Superseded\n- **Date:** 2026-10-03\n- **Corpus:** Retired — `cor:spc:020:04`, successor `cor:spc:020:05`\n" +
		"\n## D-2026-10-03-004: two live\n- **Status:** Superseded\n- **Date:** 2026-10-03\n- **Corpus:** Retired — `cor:spc:020:01`, `cor:spc:020:05`\n"
	eq(t, "retired mixed", ledgerKeys(checkLedger(reg3, nil, specs), "retired-live"),
		[]string{"D-2026-10-03-001->cor:spc:020:05", "D-2026-10-03-004->cor:spc:020:01, cor:spc:020:05"})
	// A Retired line citing something that does not resolve is still unresolved.
	reg2 := reg + "\n## D-2026-10-03-002: Retired, target gone\n- **Status:** Superseded\n- **Date:** 2026-10-03\n- **Corpus:** Retired — `cor:spc:020:98`\n"
	eq(t, "retired unresolved", ledgerKeys(checkLedger(reg2, nil, specs), "unresolved"),
		[]string{"D-2026-10-01-B->cor:spc:020:99", "D-2026-10-03-002->cor:spc:020:98"})
}

// Status classes and the ID-date scope: an unknown status is reported, never
// guessed; the scope is the ID's date, not the free-text Date field.
func TestLedgerStatusAndScope(t *testing.T) {
	reg, _, specs := ledgerFixture(t)
	for status, wantMissing := range map[string]bool{
		"Decided (active)": true, "**Decided**": true, "Implemented": true, "Implemented in part: x": true,
		"Part": true, "Superseded in part — by X": true, "Partially superseded": true,
		"Superseded": false, "Revisited": false, "Deferred": false,
	} {
		r := reg + "\n## D-2026-10-05-001: probe\n- **Status:** " + status + "\n- **Date:** 2026-10-05\n"
		got := ledgerKeys(checkLedger(r, nil, specs), "missing-disposition")
		has := false
		for _, k := range got {
			if k == "D-2026-10-05-001" {
				has = true
			}
		}
		if has != wantMissing {
			t.Errorf("status %q: missing-disposition=%v, want %v", status, has, wantMissing)
		}
	}
	r := reg + "\n## D-2026-10-05-002: probe\n- **Status:** Frozen\n- **Date:** 2026-10-05\n"
	eq(t, "status-unknown", ledgerKeys(checkLedger(r, nil, specs), "status-unknown"), []string{"D-2026-10-05-002"})
	r = reg + "\n## D-2026-10-05-003: no status at all\n- **Date:** 2026-10-05\n"
	eq(t, "no status", ledgerKeys(checkLedger(r, nil, specs), "status-unknown"), []string{"D-2026-10-05-003"})
	// Boundary day itself is in scope; the day before is not, whatever Date says.
	r = reg + "\n## D-2026-09-22-001: edge\n- **Status:** Decided\n- **Date:** 2026-09-01\n\n## D-2026-09-21-009: before\n- **Status:** Decided\n- **Date:** 2026-10-30\n"
	got := ledgerKeys(checkLedger(r, nil, specs), "missing-disposition")
	if strings.Join(got, ",") != "D-2026-09-22-001,D-2026-10-01-001,D-2026-10-01-002" {
		t.Errorf("scope: %q", got)
	}
}

// Citation shorthand inside a Codified line (fixture D-2026-10-01-A covers the
// clean case; these pin the individual forms).
func TestLedgerCitationShorthand(t *testing.T) {
	_, _, specs := ledgerFixture(t)
	probe := func(corpus string) []ledgerFinding {
		r := "## D-2026-10-06-001: probe\n- **Status:** Decided\n- **Date:** 2026-10-06\n- **Corpus:** " + corpus + "\n"
		return checkLedger(r, nil, specs)
	}
	// Feature anchor + relative appends; rule anchor + relative replaces the last segment.
	if got := probe("Codified — `cor:spc:020`, `:01`, `:02`"); len(got) != 0 {
		t.Errorf("append: %+v", got)
	}
	if got := probe("Codified — `cor:dmo:050:01`/`:03`"); len(got) != 0 {
		t.Errorf("replace: %+v", got)
	}
	// Range keeps width and expands inclusively: :04 is superseded, :05 live.
	eq(t, "range", ledgerKeys(probe("Codified — `cor:spc:020:03`–`:05`"), "superseded"), []string{"D-2026-10-06-001->cor:spc:020:04"})
	eq(t, "ellipsis range", ledgerKeys(probe("Codified — `cor:spc:020:03`…`:05`"), "superseded"), []string{"D-2026-10-06-001->cor:spc:020:04"})
	// Relative range whose start is itself relative.
	if got := probe("Codified — `cor:spc:020` and `:01`–`:03`"); len(got) != 0 {
		t.Errorf("relative range: %+v", got)
	}
	// A relative token with no anchor is reported, never guessed.
	eq(t, "unbound", ledgerKeys(probe("Codified — `:01`"), "citation-unbound"), []string{"D-2026-10-06-001->:01"})
	// Only backticked tokens count: a bare citation in prose is ignored.
	if got := probe("Codified in part — cor:spc:020:99 is prose; `cor:spc:020:01` is the law"); len(got) != 0 {
		t.Errorf("prose citation judged: %+v", got)
	}
}

// Dispositions are matched longest first, on a word boundary.
func TestLedgerDispositions(t *testing.T) {
	_, _, specs := ledgerFixture(t)
	probe := func(corpus string) []ledgerFinding {
		r := "## D-2026-10-06-001: probe\n- **Status:** Decided\n- **Date:** 2026-10-06\n- **Corpus:** " + corpus + "\n"
		return checkLedger(r, nil, specs)
	}
	for _, d := range []string{"Codified in part — `cor:spc:020:01`", "Codified (unminted) — `cor:spc:020:01`",
		"Codified — `cor:spc:020:01`", "Not a contract — process", "Retired — `cor:spc:020:04`"} {
		if got := probe(d); len(got) != 0 {
			t.Errorf("%q: %+v", d, got)
		}
	}
	eq(t, "pending", ledgerKeys(probe("Pending codification — amends `cor:spc:020:99`"), "pending"), []string{"D-2026-10-06-001"})
	// Pending names what is to change; its citations are not checked.
	if got := ledgerKeys(probe("Pending codification — `cor:spc:020:99`"), "unresolved"); len(got) != 0 {
		t.Errorf("pending citations checked: %q", got)
	}
	// Not a contract is not checked either.
	if got := ledgerKeys(probe("Not a contract — see `cor:spc:020:99`"), "unresolved"); len(got) != 0 {
		t.Errorf("not-a-contract citations checked: %q", got)
	}
	// No word boundary, no match: never inferred.
	eq(t, "boundary", ledgerKeys(probe("Codifiedish — `cor:spc:020:01`"), "disposition-unknown"), []string{"D-2026-10-06-001"})
	eq(t, "unknown", ledgerKeys(probe("Mostly codified — x"), "disposition-unknown"), []string{"D-2026-10-06-001"})
}

// Category 3 reports where the citation lives, in `citation:field:line` form,
// and reads abstracts as well as bodies.
func TestLedgerLocationsAndAbstracts(t *testing.T) {
	reg, _, specs := ledgerFixture(t)
	docs := []ledgerDoc{
		{Citation: "cor:a:010", Field: "content", Text: "x\nsee D-2030-01-01-001 here"},
		{Citation: "cor:b:010", Field: "abstract", Text: "D-2030-01-01-001"},
	}
	fs := checkLedger(reg, docs, specs)
	var loc []string
	for _, f := range fs {
		if f.Rule == "unregistered-id" {
			loc = f.Locations
		}
	}
	eq(t, "locations", loc, []string{"cor:a:010:content:2", "cor:b:010:abstract:1"})
}

// A heading that looks like an entry but is not, and a repeated ID, are
// reported rather than crashing or silently merging.
func TestLedgerRegistryShape(t *testing.T) {
	_, _, specs := ledgerFixture(t)
	r := "## D-2026-10-07-1: short suffix\n- **Status:** Decided\n\n## D-2026-10-07-001: ok\n- **Status:** Deferred\n\n## D-2026-10-07-001: again\n- **Status:** Deferred\n"
	fs := checkLedger(r, nil, specs)
	eq(t, "malformed", ledgerKeys(fs, "registry-malformed"), []string{""})
	eq(t, "duplicate", ledgerKeys(fs, "duplicate-id"), []string{"D-2026-10-07-001"})
	// The first occurrence of a field wins; a later repeat is ignored.
	r = "## D-2026-10-07-002: twice\n- **Status:** Decided\n- **Corpus:** Not a contract — x\n- **Corpus:** Mostly codified\n"
	if got := checkLedger(r, nil, specs); len(got) != 0 {
		t.Errorf("repeated field: %+v", got)
	}
}

// A registry with no entries is reported, never "clean": a wrong file would
// otherwise read as a ledger with nothing wrong.
func TestLedgerEmptyRegistryIsNotClean(t *testing.T) {
	for _, reg := range []string{"", "# just a title\n\nno entries\n"} {
		got := checkLedger(reg, nil, nil)
		if len(got) != 1 || got[0].Rule != "registry-malformed" || got[0].Severity != "error" {
			t.Errorf("registry %q: %+v", reg, got)
		}
	}
}

// Clean inputs yield no findings, and the report is deterministic.
func TestLedgerCleanAndDeterministic(t *testing.T) {
	clean := "## D-2026-10-01-001: ok\n- **Status:** Deferred\n"
	if got := checkLedger(clean, nil, nil); len(got) != 0 {
		t.Errorf("clean: %+v", got)
	}
	reg, docs, specs := ledgerFixture(t)
	a, _ := json.Marshal(checkLedger(reg, docs, specs))
	b, _ := json.Marshal(checkLedger(reg, docs, specs))
	if string(a) != string(b) || len(a) < 10 {
		t.Errorf("not deterministic or empty")
	}
}

// #1670 BLIND + LOCAL findings: malformed registry text is reported, never a
// crash, never silently clean.
func TestLedgerMalformedCitationRange(t *testing.T) {
	_, _, specs := ledgerFixture(t)
	probe := func(corpus string) []ledgerFinding {
		r := "## D-2026-10-06-001: probe\n- **Status:** Decided\n- **Date:** 2026-10-06\n- **Corpus:** " + corpus + "\n"
		return checkLedger(r, nil, specs)
	}
	// A bare `cor` with a range bound has no segment to expand: no panic.
	eq(t, "bare anchor range", ledgerKeys(probe("Codified — `cor`–`:02`"), "citation-malformed"), []string{"D-2026-10-06-001->cor"})
	// A descending citation range is reported, not reduced to its start.
	eq(t, "descending", ledgerKeys(probe("Codified — `cor:spc:020:03`–`:01`"), "citation-malformed"), []string{"D-2026-10-06-001->cor:spc:020:03"})
	// An ascending one stays clean of this rule.
	eq(t, "ascending", ledgerKeys(probe("Codified — `cor:spc:020:01`–`:03`"), "citation-malformed"), nil)
}

func TestLedgerIDTokenBoundariesAndMalformedRangeTail(t *testing.T) {
	reg, _, specs := ledgerFixture(t)
	run := func(text string) []ledgerFinding {
		var out []ledgerFinding
		for _, f := range checkLedger(reg, []ledgerDoc{{Citation: "cor:x:010", Field: "content", Text: text}}, specs) {
			if f.Category == 3 {
				out = append(out, f)
			}
		}
		return out
	}
	// A full ID is a whole token: four digits or two letters is no ID, and is reported.
	eq(t, "four digits", ledgerKeys(run("D-2026-10-01-0010"), "unparsed-shorthand"), []string{""})
	eq(t, "two letters", ledgerKeys(run("D-2026-10-01-AB"), "unparsed-shorthand"), []string{""})
	if got := ledgerKeys(run("D-2026-10-01-0010"), "unregistered-id"); len(got) != 0 {
		t.Errorf("truncated ID judged: %q", got)
	}
	// A range tail whose bound is not three digits is reported, not ignored.
	eq(t, "short bound", ledgerKeys(run("D-2026-10-02-002…00"), "unparsed-shorthand"), []string{""})
	eq(t, "long bound", ledgerKeys(run("D-2026-10-02-002–0040"), "unparsed-shorthand"), []string{""})
	// An ID glued to a preceding word character is not that ID.
	if got := run("xD-2026-10-09-099"); len(got) != 0 {
		t.Errorf("glued ID judged: %+v", got)
	}
	// Prose after a dash is still just prose.
	if got := run("D-2026-10-02-002 – see above"); len(got) != 0 {
		t.Errorf("prose after dash: %+v", got)
	}
}
