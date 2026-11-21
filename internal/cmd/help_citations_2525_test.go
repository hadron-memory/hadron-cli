package cmd

import (
	"flag"
	"fmt"
	"github.com/spf13/cobra"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// cli#2525 (the deferred P3s of #2524): the #2508 guard, widened.
//
//   - F1: the walk covers EVERY top-level group, not only app and agent. Pages
//     that still carry citations are listed in testdata/help_citation_exceptions_2525.txt,
//     a ratchet: a page may leave the list, never join it, and a listed page that
//     is clean is itself a failure, so the list only shrinks.
//   - F2/L2: a revision hash is 7 to 40 lowercase hex characters between
//     non-alphanumerics holding at least one digit and one letter (so dates, plain
//     numbers and words like "deadbeef" are not hashes); an issue number is
//     #<digits>, one digit included.
//   - L2: pages are walked in sorted order, one t.Run each.
//   - L3: a hit is found per line, so a hash that ends its line is reported with
//     the line, never an empty example.

type helpHit2525 struct{ kind, text, line string }

var (
	citationRE2525 = regexp.MustCompile(`cor:[a-z]+(?::[0-9]+)+`)
	issueRE2525    = regexp.MustCompile(`#[0-9]+`)
	hashRE2525     = regexp.MustCompile(`(?:^|[^0-9A-Za-z])([0-9a-f]{7,40})(?:$|[^0-9A-Za-z])`)
)

// placeholderHash2525 reports whether a hex run is an obvious placeholder (the
// ascending 0123456789abcdef pattern, or the ids the help pages use as sample
// values), which is not a revision.
func placeholderHash2525(h string) bool {
	const asc = "0123456789abcdef0123456789abcdef0123456789abcdef"
	return strings.Contains(asc, h) || h == "01a1f0c2d3e4f5a6b7c8d9e0f1a2b3c4" || h == "01a1d71218bb"
}

// revisionContextRE2525 marks a line that says the token is a revision: a server floor,
// a commit, a build. On such a line the id-shaped exemptions below do not apply. ("revision"
// is not one: this product has node revisions and --expected-revision.)
var revisionContextRE2525 = regexp.MustCompile(`(?i)\b(server|commit|sha|hash|build|built|merged|release)\b`)

// isRevisionHash2525 decides whether a hex run is a revision hash. It is not when it lacks
// a digit or a letter (a date, a number, a word) or is an obvious placeholder. Three shapes
// look like ids rather than revisions and are exempt UNLESS the line says the token is a
// revision (revisionContextRE2525): exactly 32 characters (an entity id, which the examples
// use throughout), a segment of a UUID or session id (a neighbouring dash), and a truncated
// id (followed by an ellipsis).
//
// Limits, stated: this is a heuristic on text. An example id that sits on a line containing
// one of those context words is reported (fix the example or reword the line), and a
// revision written with no context word on a line with a neighbouring dash, an ellipsis or
// exactly 32 characters is not (the hash class has no such exemptions for the other 7 to 40
// character shapes).
func isRevisionHash2525(h, before, after, line string) bool {
	if !strings.ContainsAny(h, "0123456789") || !strings.ContainsAny(h, "abcdef") || placeholderHash2525(h) {
		return false
	}
	idShaped := len(h) == 32 || strings.HasSuffix(before, "-") || strings.HasPrefix(after, "-") || strings.HasPrefix(after, "…")
	if idShaped && !revisionContextRE2525.MatchString(line) {
		return false
	}
	return true
}

// bannedHits2525 returns every banned thing on a help page, line by line.
func bannedHits2525(help string) []helpHit2525 {
	var hits []helpHit2525
	for _, l := range strings.Split(help, "\n") {
		for _, m := range citationRE2525.FindAllString(l, -1) {
			hits = append(hits, helpHit2525{"citation", m, strings.TrimSpace(l)})
		}
		for _, m := range issueRE2525.FindAllString(l, -1) {
			hits = append(hits, helpHit2525{"issue", m, strings.TrimSpace(l)})
		}
		// The boundary is consumed, so scan from each match's end to catch
		// two hashes separated by one character.
		rest := l
		for {
			m := hashRE2525.FindStringSubmatchIndex(rest)
			if m == nil {
				break
			}
			h := rest[m[2]:m[3]]
			if isRevisionHash2525(h, rest[:m[2]], rest[m[3]:], l) {
				hits = append(hits, helpHit2525{"hash", h, strings.TrimSpace(l)})
			}
			rest = rest[m[3]:]
		}
	}
	return hits
}

func TestBanRegexesCatchTheShapesThePolicyBans2525(t *testing.T) {
	for _, tc := range []struct {
		name, in string
		want     string // the kind that must be found ("" = nothing)
	}{
		{"8 hex", "needs server 17ef5905 or later", "hash"},
		{"7 hex", "needs server 27671b4 or later", "hash"},
		{"10 hex", "needs server 27671b4a1c or later", "hash"},
		{"12 hex", "needs server 27671b4a1c2d or later", "hash"},
		{"40 hex", "commit 27671b4a1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f here", "hash"},
		{"hash ends the line", "needs server 27671b4b", "hash"},
		{"hash starts the line", "27671b4b is the revision", "hash"},
		{"single digit issue", "see #7 for more", "issue"},
		{"two digit issue", "see #42", "issue"},
		{"citation", "cor:dmo:050:12", "citation"},
		{"date is fine", "needs server 2026-11-03 or later", ""},
		{"plain number is fine", "up to 12345678 rows", ""},
		{"no digit is not a hash", "a deadbeef value", ""},
		{"no letter is not a hash", "id 1234567890", ""},
		{"placeholder sha is fine", "--head 0123456789abcdef0123456789abcdef01234567", ""},
		// Dara's #2812 P2: the id-shaped exemptions (exactly 32 characters, a neighbouring dash)
		// do not apply where the line says the token is a revision.
		{"32-char git abbreviation as a revision", "needs server 27671b4a1c2d3e4f5a6b7c8d9e0f1a2b or later", "hash"},
		{"hyphen-adjacent revision", "needs hadron-server 27671b4b-ab12cd34 or later", "hash"},
		{"revision before a dash", "built from 27671b4b-dirty", "hash"},
		{"a 32-char entity id in an example is fine", "hadron asset get 01a189a840f4717c98af75cf4d7aed09", ""},
		{"a uuid segment in an example is fine", "hadron connection flow 7f3c1a90-0000-4000-8000-000000000000", ""},
		{"a truncated id in an example is fine", "--session 01a0a78c…", ""},
		{"longer than a hash is fine", "token 27671b4a1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hits := bannedHits2525(tc.in)
			switch {
			case tc.want == "" && len(hits) != 0:
				t.Fatalf("%q must pass, got %+v", tc.in, hits)
			case tc.want != "" && (len(hits) == 0 || hits[0].kind != tc.want || hits[0].text == "" || hits[0].line == ""):
				t.Fatalf("%q must report a %s with its text and line, got %+v", tc.in, tc.want, hits)
			}
		})
	}
}

var updateExceptions2525 = flag.Bool("update-citation-exceptions", false, "shrink testdata/help_citation_exceptions_2525.txt to what the help still carries (never adds)")

const exceptionsFile2525 = "testdata/help_citation_exceptions_2525.txt"

// refCounts2525 counts the spec citations and issue numbers on one page, keyed "<kind> <text>".
type refCounts2525 map[string]int

// baseline2525 is the ratchet: per page, each reference it may still carry and how many times.
type baseline2525 map[string]refCounts2525

// pageRefs2525 counts a page's citations and issue numbers (revision hashes are not ratcheted:
// they are banned outright, TestHelpCarriesNoRevisionHashAnywhere2525).
func pageRefs2525(help string) refCounts2525 {
	c := refCounts2525{}
	for _, h := range bannedHits2525(help) {
		if h.kind != "hash" {
			c[h.kind+" "+h.text]++
		}
	}
	return c
}

// pageRatchetErrors2525 compares one page with its baseline entry. A page may carry fewer of a
// reference than its baseline allows (and the baseline must then be shrunk), never more, and
// never a reference the baseline does not list: that holds for a page already on the list and
// for a page that is not.
func pageRatchetErrors2525(page string, actual, base refCounts2525) []string {
	var errs []string
	for _, k := range sortedKeys2525(actual) {
		if actual[k] > base[k] {
			errs = append(errs, fmt.Sprintf("%s --help carries %d x %s, baseline allows %d: help carries no new citations or issue numbers (state a server floor as a date)", page, actual[k], k, base[k]))
		}
	}
	for _, k := range sortedKeys2525(base) {
		if actual[k] < base[k] {
			errs = append(errs, fmt.Sprintf("%s --help carries %d x %s, baseline lists %d: shrink %s (the list only shrinks; run the test with -update-citation-exceptions)", page, actual[k], k, base[k], exceptionsFile2525))
		}
	}
	return errs
}

func sortedKeys2525(m refCounts2525) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// shrink2525 returns the baseline reduced to what the pages still carry. It refuses to add:
// a reference above its baseline (or on an unlisted page) is an error, never a new entry.
func shrink2525(actual, base baseline2525) (baseline2525, []string) {
	out := baseline2525{}
	var errs []string
	pages := map[string]bool{}
	for p := range actual {
		pages[p] = true
	}
	for p := range base {
		pages[p] = true
	}
	names := make([]string, 0, len(pages))
	for p := range pages {
		names = append(names, p)
	}
	sort.Strings(names)
	for _, p := range names {
		for _, k := range sortedKeys2525(actual[p]) {
			if actual[p][k] > base[p][k] {
				errs = append(errs, fmt.Sprintf("%s --help carries %d x %s above its baseline (%d): the list cannot be regenerated to admit a new reference; remove the reference", p, actual[p][k], k, base[p][k]))
			}
		}
		for _, k := range sortedKeys2525(base[p]) {
			if n := min(actual[p][k], base[p][k]); n > 0 {
				if out[p] == nil {
					out[p] = refCounts2525{}
				}
				out[p][k] = n
			}
		}
	}
	return out, errs
}

func formatBaseline2525(b baseline2525) string {
	var sb strings.Builder
	sb.WriteString("# Help pages that still carry a spec citation or an issue number, and exactly which\n" +
		"# references and how many (revision hashes are banned outright and are not listed).\n" +
		"# cli#2525. A ratchet per reference: a page may carry fewer of a reference, never more,\n" +
		"# and never one that is not listed; a listed reference the page no longer carries must\n" +
		"# be removed. Shrink it with\n" +
		"#   go test ./internal/cmd -run TestHelpCarriesNoNewCitationsNumbersOrHashes2525 -update-citation-exceptions\n" +
		"# which never adds. Line: <page>  # <kind> <reference> x<count> | ...\n" +
		"# The scrub is tracked in https://github.com/hadron-memory/hadron-cli/issues/2811, by group.\n")
	pages := make([]string, 0, len(b))
	for p := range b {
		pages = append(pages, p)
	}
	sort.Strings(pages)
	for _, p := range pages {
		var items []string
		for _, k := range sortedKeys2525(b[p]) {
			items = append(items, fmt.Sprintf("%s x%d", k, b[p][k]))
		}
		sb.WriteString(p + "  # " + strings.Join(items, " | ") + "\n")
	}
	return sb.String()
}

func parseBaseline2525(raw string) (baseline2525, error) {
	b := baseline2525{}
	for i, l := range strings.Split(raw, "\n") {
		n := i + 1
		if l = strings.TrimSpace(l); l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		page, refs, ok := strings.Cut(l, "  # ")
		page = strings.TrimSpace(page)
		if !ok {
			return nil, fmt.Errorf("line %d: %q has no references: <page>  # <kind> <reference> x<count>", n, l)
		}
		if _, dup := b[page]; dup {
			return nil, fmt.Errorf("line %d: page %q is listed twice", n, page)
		}
		c := refCounts2525{}
		for _, item := range strings.Split(refs, " | ") {
			f := strings.Fields(item)
			if len(f) != 3 || !strings.HasPrefix(f[2], "x") {
				return nil, fmt.Errorf("line %d: item %q is not <kind> <reference> x<count>", n, item)
			}
			cnt, err := strconv.Atoi(f[2][1:])
			if err != nil || cnt < 1 {
				return nil, fmt.Errorf("line %d: bad count in %q", n, item)
			}
			key := f[0] + " " + f[1]
			if _, dup := c[key]; dup {
				return nil, fmt.Errorf("line %d: reference %q is listed twice on %q", n, key, page)
			}
			c[key] = cnt
		}
		b[page] = c
	}
	return b, nil
}

func loadBaseline2525(t *testing.T) baseline2525 {
	t.Helper()
	raw, err := os.ReadFile(exceptionsFile2525)
	if err != nil {
		t.Fatalf("read %s: %v", exceptionsFile2525, err)
	}
	b, err := parseBaseline2525(string(raw))
	if err != nil {
		t.Fatalf("%s: %v", exceptionsFile2525, err)
	}
	return b
}

// helpPagesInScope2525 renders every help page the guards cover, keyed by page name: the ROOT
// page ("hadron --help") and, for every command directly under the root INCLUDING the generated
// help and completion commands, that command's page and every visible page below it.
func helpPagesInScope2525(t *testing.T) map[string]string {
	t.Helper()
	pages := map[string]string{}
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"--help"})
	root.SetOut(out)
	root.SetErr(out)
	if err := root.Execute(); err != nil {
		t.Fatalf("hadron --help: %v", err)
	}
	pages["hadron"] = out.String()
	// Enumerate on the SAME root that was just executed: cobra adds its generated help and
	// completion commands while executing, so this is where they exist.
	var tops []string
	for _, c := range root.Commands() {
		if !c.Hidden {
			tops = append(tops, c.Name())
		}
	}
	sort.Strings(tops)
	for _, group := range tops {
		for n, h := range helpTree2508(t, group) {
			pages[n] = h
		}
	}
	return pages
}

// George's/Dara's #2812 P3: the walked page set is compared with the visible command TREE, not with
// a list of names: every visible command (generated help and completion included, with their
// visible descendants) has a page, and every page is a command, the root, or the root itself.
func TestHelpWalkCoversTheWholeVisibleCommandTree2525(t *testing.T) {
	pages := helpPagesInScope2525(t)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"--help"})
	root.SetOut(out)
	root.SetErr(out)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"hadron": true}
	var walk func(prefix []string, c *cobra.Command)
	walk = func(prefix []string, c *cobra.Command) {
		for _, sub := range c.Commands() {
			if sub.Hidden || (len(prefix) > 0 && sub.Name() == "help") {
				continue
			}
			path := append(append([]string{}, prefix...), sub.Name())
			want[strings.Join(path, " ")] = true
			walk(path, sub)
		}
	}
	walk(nil, root)
	for name := range want {
		if _, ok := pages[name]; !ok {
			t.Errorf("visible command %q has no page in the walked set", name)
		}
	}
	for name := range pages {
		if !want[name] {
			t.Errorf("walked page %q is not a visible command", name)
		}
	}
}

func TestHelpCarriesNoNewCitationsNumbersOrHashes2525(t *testing.T) {
	base := loadBaseline2525(t)
	actual := baseline2525{}
	seen := map[string]bool{}
	pages := helpPagesInScope2525(t)
	names := make([]string, 0, len(pages))
	for n := range pages {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		refs := pageRefs2525(pages[name])
		if len(refs) > 0 {
			actual[name] = refs
		}
		seen[name] = true
		t.Run(name, func(t *testing.T) {
			if *updateExceptions2525 {
				return
			}
			for _, e := range pageRatchetErrors2525(name, refs, base[name]) {
				t.Error(e)
			}
		})
	}
	if *updateExceptions2525 {
		next, errs := shrink2525(actual, base)
		for _, e := range errs {
			t.Error(e)
		}
		if len(errs) > 0 {
			return
		}
		if err := os.WriteFile(exceptionsFile2525, []byte(formatBaseline2525(next)), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	for name := range base {
		if !seen[name] {
			t.Errorf("%s is listed in %s but is not a help page", name, exceptionsFile2525)
		}
	}
}

// Dara's #2812 P2 (F1): the ratchet is per reference, not per page. A new reference on a page that
// is already listed, and any reference on a page that is not, both fail; a reference may only
// be removed, and regenerating the list can shrink it but never admit a new reference.
func TestCitationRatchetIsPerReference2525(t *testing.T) {
	base := baseline2525{"team task": {"citation cor:agt:040:13": 1, "issue #1463": 2}}
	cases := []struct {
		name    string
		page    string
		actual  refCounts2525
		wantErr int
	}{
		{"unchanged page passes", "team task", refCounts2525{"citation cor:agt:040:13": 1, "issue #1463": 2}, 0},
		{"a new issue number on an exempt page", "team task", refCounts2525{"citation cor:agt:040:13": 1, "issue #1463": 2, "issue #9": 1}, 1},
		{"one more of a listed reference", "team task", refCounts2525{"citation cor:agt:040:13": 1, "issue #1463": 3}, 1},
		{"a new citation of a listed kind", "team task", refCounts2525{"citation cor:agt:040:13": 1, "citation cor:agt:040:14": 1, "issue #1463": 2}, 1},
		{"a hit on a page that is not listed", "memory ls", refCounts2525{"issue #5": 1}, 1},
		{"a removed reference must shrink the list", "team task", refCounts2525{"citation cor:agt:040:13": 1, "issue #1463": 1}, 1},
		{"a clean listed page must shrink the list", "team task", refCounts2525{}, 2},
		{"a clean unlisted page passes", "memory ls", refCounts2525{}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := pageRatchetErrors2525(tc.page, tc.actual, base[tc.page]); len(got) != tc.wantErr {
				t.Errorf("errors = %v, want %d", got, tc.wantErr)
			}
		})
	}
	// Regeneration shrinks and refuses to add.
	shrunk, errs := shrink2525(baseline2525{"team task": {"citation cor:agt:040:13": 1, "issue #1463": 1}}, base)
	if len(errs) != 0 || shrunk["team task"]["issue #1463"] != 1 || shrunk["team task"]["citation cor:agt:040:13"] != 1 {
		t.Errorf("shrink: %v %v", shrunk, errs)
	}
	if _, errs := shrink2525(baseline2525{"team task": {"issue #9": 1}}, base); len(errs) != 1 {
		t.Errorf("a new reference on a listed page must not be admitted by regeneration: %v", errs)
	}
	if _, errs := shrink2525(baseline2525{"memory ls": {"issue #5": 1}}, base); len(errs) != 1 {
		t.Errorf("a hit on a new page must not be admitted by regeneration: %v", errs)
	}
	if gone, _ := shrink2525(baseline2525{}, base); len(gone) != 0 {
		t.Errorf("a page that no longer carries anything leaves the list: %v", gone)
	}
	// The file format round-trips.
	back, err := parseBaseline2525(formatBaseline2525(base))
	if err != nil || back["team task"]["issue #1463"] != 2 || back["team task"]["citation cor:agt:040:13"] != 1 {
		t.Errorf("format/parse round trip: %v %v", back, err)
	}
}

// Revision hashes are scrubbed everywhere (not ratcheted): a server floor is a date.
func TestHelpCarriesNoRevisionHashAnywhere2525(t *testing.T) {
	pages := helpPagesInScope2525(t)
	names := make([]string, 0, len(pages))
	for n := range pages {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		for _, h := range bannedHits2525(pages[name]) {
			if h.kind == "hash" {
				t.Errorf("%s --help carries the revision hash %q in %q: state a server floor as a date", name, h.text, h.line)
			}
		}
	}
}

// cli#2525 F4: `app agent list` lost its server floor when the revision hash was
// scrubbed ("Requires a server with AppAgent.installMethod..." named no version at
// all). It states the release date again, so a reader can tell an older server (every
// INSTALL and IMPORTER cell is refused, exit 2) from one that withholds the answer for them.
func TestAppAgentListHelpStatesItsServerFloor2525(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "agent", "list", "--help"})
	root.SetOut(out)
	root.SetErr(out)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	help := strings.Join(strings.Fields(out.String()), " ")
	// Joost's #2812 P2: an older server REFUSES the fields (schema skew, exit 2, no roster:
	// TestAppAgentProvenanceSchemaSkew1682); "?" is only withheld provenance on a server
	// that has them. The page must say the first with the date and never the second.
	if !strings.Contains(help, "A server before 2026-11-05 refuses the install method and importer fields (exit 2)") {
		t.Errorf("app agent list --help must state the refusal and the server floor as a date:\n%s", out.String())
	}
	if strings.Contains(help, "older server answers") {
		t.Errorf("app agent list --help must not say an older server answers \"?\" (it refuses):\n%s", out.String())
	}
}

// Dara's #2812 P2 (F4): a deployment date does not show a server has a change (an operator
// can deploy an older build after the cutoff), and a floor is the date the change was MERGED
// into the server. server-info says so and asks for the deployed revision.
func TestServerInfoHelpExplainsWhatAFloorDateMeans2525(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"server-info", "--help"})
	root.SetOut(out)
	root.SetErr(out)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	help := strings.Join(strings.Fields(out.String()), " ")
	for _, want := range []string{"the date its change was merged into the server", "which revision is deployed"} {
		if !strings.Contains(help, want) {
			t.Errorf("server-info --help must say %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(help, "last updated") {
		t.Errorf("a deployment date does not establish compatibility; do not send the reader to it:\n%s", out.String())
	}
}

// George's #2812 P2 (F1): a citation is matched WHOLE. The old pattern stopped at the first
// numeric segment, so cor:agt:050:14 and cor:agt:050:99 were both "cor:agt:050" and a
// replacement inside the same prefix left the ratchet count unchanged.
func TestCitationsAreCountedWhole2525(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"see cor:dmo:050:12 for the rule", "cor:dmo:050:12"},
		{"see cor:agt:040:13.", "cor:agt:040:13"},
		{"cor:api:240", "cor:api:240"},
		{"(cor:acl:140:05)", "cor:acl:140:05"},
	} {
		hits := bannedHits2525(tc.in)
		if len(hits) != 1 || hits[0].kind != "citation" || hits[0].text != tc.want {
			t.Errorf("%q: want the citation %q whole, got %+v", tc.in, tc.want, hits)
		}
	}
	a, b := pageRefs2525("rule cor:agt:050:14"), pageRefs2525("rule cor:agt:050:99")
	if len(pageRatchetErrors2525("team x", b, a)) == 0 {
		t.Error("replacing cor:agt:050:14 with cor:agt:050:99 must fail the ratchet")
	}
}

// George's #2812 P2 (F2): the guard walks the ROOT page too, and every generated page under it
// (help, completion), not only the user commands' pages.
func TestHelpPagesInScopeIncludeTheRootAndGeneratedPages2525(t *testing.T) {
	pages := helpPagesInScope2525(t)
	for _, want := range []string{"hadron", "help", "completion", "app", "team", "secret client revoke"} {
		if _, ok := pages[want]; !ok {
			t.Errorf("page %q is not in the walked set", want)
		}
	}
	if !strings.Contains(pages["hadron"], "Available Commands") {
		t.Errorf("the root page must be the rendered root help: %.80q", pages["hadron"])
	}
}

// George's #2812 P3 (F6): a baseline with a duplicate page, a duplicate reference or a malformed
// line is refused with a line number, never silently overwritten.
func TestBaselineParserRejectsDuplicatesAndMalformedLines2525(t *testing.T) {
	for _, tc := range []struct{ name, in, wantLine string }{
		{"duplicate page", "a b  # issue #1 x1\na b  # issue #2 x1\n", "line 2"},
		{"duplicate reference", "a b  # issue #1 x1 | issue #1 x2\n", "line 1"},
		{"no reference list", "a b\n", "line 1"},
		{"bad count", "a b  # issue #1 xZ\n", "line 1"},
		{"zero count", "a b  # issue #1 x0\n", "line 1"},
		{"missing count", "a b  # issue #1\n", "line 1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseBaseline2525(tc.in)
			if err == nil || !strings.Contains(err.Error(), tc.wantLine) {
				t.Errorf("want an error naming %s, got %v", tc.wantLine, err)
			}
		})
	}
}
