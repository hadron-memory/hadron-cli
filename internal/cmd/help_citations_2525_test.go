package cmd

import (
	"flag"
	"os"
	"regexp"
	"sort"
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
	citationRE2525 = regexp.MustCompile(`cor:[a-z]+:[0-9]+`)
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

// isRevisionHash2525 decides whether a hex run is a revision hash. It is not when it
// lacks a digit or a letter (a date, a number, a word), when it is an obvious
// placeholder, when it is exactly 32 characters (an entity id, which the examples
// use throughout), when it is a segment of a UUID or session id (a neighbouring
// dash), or when it is a truncated id (followed by an ellipsis).
func isRevisionHash2525(h, before, after string) bool {
	if !strings.ContainsAny(h, "0123456789") || !strings.ContainsAny(h, "abcdef") || placeholderHash2525(h) || len(h) == 32 {
		return false
	}
	if strings.HasSuffix(before, "-") || strings.HasPrefix(after, "-") || strings.HasPrefix(after, "…") {
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
			if isRevisionHash2525(h, rest[:m[2]], rest[m[3]:]) {
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

var updateExceptions2525 = flag.Bool("update-citation-exceptions", false, "rewrite testdata/help_citation_exceptions_2525.txt from the current help")

const exceptionsFile2525 = "testdata/help_citation_exceptions_2525.txt"

func loadExceptions2525(t *testing.T) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(exceptionsFile2525)
	if err != nil {
		t.Fatalf("read %s: %v (regenerate with -update-citation-exceptions)", exceptionsFile2525, err)
	}
	m := map[string]bool{}
	for _, l := range strings.Split(string(raw), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			if i := strings.Index(l, "  # "); i >= 0 {
				l = strings.TrimSpace(l[:i])
			}
			m[l] = true
		}
	}
	return m
}

// topGroups2525 lists every visible top-level command, sorted.
func topGroups2525(t *testing.T) []string {
	t.Helper()
	f, _ := testFactory(t)
	var names []string
	for _, c := range NewRootCmd(f).Commands() {
		if !c.Hidden && c.Name() != "help" && c.Name() != "completion" {
			names = append(names, c.Name())
		}
	}
	sort.Strings(names)
	return names
}

func TestHelpWalkCoversEveryTopLevelGroup2525(t *testing.T) {
	groups := topGroups2525(t)
	for _, want := range []string{"app", "agent", "team", "secret", "spec", "node", "email"} {
		if i := sort.SearchStrings(groups, want); i >= len(groups) || groups[i] != want {
			t.Errorf("group %q is not walked: %v", want, groups)
		}
	}
}

func TestHelpCarriesNoNewCitationsNumbersOrHashes2525(t *testing.T) {
	groups := topGroups2525(t)
	allowed := loadExceptions2525(t)
	seen := map[string]bool{}
	var rewritten []string
	reasons := map[string]string{}
	for _, group := range groups {
		pages := helpTree2508(t, group)
		names := make([]string, 0, len(pages))
		for n := range pages {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, name := range names {
			hits := bannedHits2525(pages[name])
			if len(hits) > 0 {
				rewritten = append(rewritten, name)
				kinds := map[string]bool{}
				for _, h := range hits {
					kinds[h.kind] = true
				}
				var ks []string
				for k := range kinds {
					ks = append(ks, k)
				}
				sort.Strings(ks)
				reasons[name] = strings.Join(ks, ", ")
			}
			seen[name] = true
			t.Run(name, func(t *testing.T) {
				if *updateExceptions2525 {
					return
				}
				switch {
				case len(hits) > 0 && !allowed[name]:
					t.Errorf("%s --help carries %s %q in %q; help carries no citations, issue numbers or revision hashes (state a server floor as a date)", name, hits[0].kind, hits[0].text, hits[0].line)
				case len(hits) == 0 && allowed[name]:
					t.Errorf("%s --help is clean: remove it from %s (the list only shrinks)", name, exceptionsFile2525)
				}
			})
		}
	}
	if *updateExceptions2525 {
		sort.Strings(rewritten)
		body := "# Help pages that still carry a spec citation or an issue number (revision hashes are\n" +
			"# banned outright and are not listed)\n" +
			"# (cli#2525). A ratchet: a page may leave this list and never join it; a listed page\n" +
			"# that is clean fails until it is removed. Regenerate with\n" +
			"#   go test ./internal/cmd -run TestHelpCarriesNoNewCitationsNumbersOrHashes2525 -update-citation-exceptions\n" +
			"# Each line: the page, then (after two spaces and a #) what it still carries.\n" +
			"# The scrub is tracked in https://github.com/hadron-memory/hadron-cli/issues/2811, by group.\n"
		for _, name := range rewritten {
			body += name + "  # " + reasons[name] + "\n"
		}
		if err := os.WriteFile(exceptionsFile2525, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	for name := range allowed {
		if !seen[name] {
			t.Errorf("%s is listed in %s but is not a help page", name, exceptionsFile2525)
		}
	}
}

// Revision hashes are scrubbed everywhere (not ratcheted): a server floor is a date.
func TestHelpCarriesNoRevisionHashAnywhere2525(t *testing.T) {
	for _, group := range topGroups2525(t) {
		pages := helpTree2508(t, group)
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
