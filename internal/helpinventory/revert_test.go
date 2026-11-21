package helpinventory

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

const header = "# Help inventory\n\n| path | aliases | generated | has-examples | child | help-hash | evaluator | result | finding |\n|---|---|---|---|---|---|---|---|---|\n"

func doc(rows ...string) string { return header + strings.Join(rows, "\n") + "\n" }

func row(path, hash, eval, result, finding string) string {
	return fmt.Sprintf("| `%s` |  |  | y | #1 | %s | %s | %s | %s |", path, hash, eval, result, finding)
}

func check(base, head string) []string { return checkLive(base, head, nil) }

// checkLive is check with the live command pages (cli#2699).
func checkLive(base, head string, live map[string]bool) []string {
	b, err := Parse(base)
	if err != nil {
		panic(err)
	}
	h, err := Parse(head)
	if err != nil {
		panic(err)
	}
	return Reverts(b, h, live)
}

// (a) a reverted pass row with the SAME hash fails and names the row.
func TestRevertedPassRowIsNamed2648(t *testing.T) {
	got := check(
		doc(row("node get", "aaa111", "Gil", "pass", "[x](u)")),
		doc(row("node get", "aaa111", "", "unread", "")))
	if len(got) != 1 || !strings.Contains(got[0], "`node get`") || !strings.Contains(got[0], "aaa111") {
		t.Fatalf("want one revert naming the row: %v", got)
	}
}

// (b) a changed hash is the legitimate reset.
func TestChangedHashMayReset2648(t *testing.T) {
	if got := check(doc(row("node get", "aaa111", "Gil", "pass", "[x](u)")), doc(row("node get", "bbb222", "", "unread", ""))); len(got) != 0 {
		t.Fatalf("a changed hash must pass: %v", got)
	}
}

// (c) unread -> pass (a docs-only flip) is allowed.
func TestUnreadToPassIsAllowed2648(t *testing.T) {
	if got := check(doc(row("node get", "aaa111", "", "unread", "")), doc(row("node get", "aaa111", "Gil", "pass", "[x](u)"))); len(got) != 0 {
		t.Fatalf("unread -> pass must pass: %v", got)
	}
}

// A real re-assessment with the same hash (fail -> pass, new finding) is not a revert.
func TestReassessmentIsNotARevert2648(t *testing.T) {
	if got := check(doc(row("a", "h1", "Gil", "fail", "[x](u)")), doc(row("a", "h1", "Gemma", "pass", "[y](v)"))); len(got) != 0 {
		t.Fatalf("fail -> pass must pass: %v", got)
	}
}

// Erasing the evaluator or the finding while the result stays pass is a revert too.
func TestDroppedEvaluatorOrFindingIsARevert2648(t *testing.T) {
	base := doc(row("a", "h1", "Gil", "pass", "[x](u)"), row("b", "h2", "Gil", "pass", "[x](u)"))
	head := doc(row("a", "h1", "", "pass", "[x](u)"), row("b", "h2", "Gil", "pass", ""))
	if got := check(base, head); len(got) != 2 {
		t.Fatalf("want both rows named: %v", got)
	}
}

// (d) a PR merely behind main: measured against the MERGE BASE, main's newer flips
// are absent from both sides, so there is nothing to compare and nothing fails.
func TestBehindMainIsNotARevert2648(t *testing.T) {
	mergeBase := doc(row("a", "h1", "", "unread", ""))
	prHead := doc(row("a", "h1", "", "unread", "")) // main later flipped it to pass; the PR never saw that
	if got := check(mergeBase, prHead); len(got) != 0 {
		t.Fatalf("a PR behind main must not fail: %v", got)
	}
}

// (e) the #2578 shape: many rows put back to unread with unchanged hashes.
func TestMassRevertIsCaught2648(t *testing.T) {
	var b, h []string
	for i := 0; i < 78; i++ {
		p := fmt.Sprintf("cmd%02d", i)
		b = append(b, row(p, "hh"+p, "Gil", "pass", "[x](u)"))
		h = append(h, row(p, "hh"+p, "", "unread", ""))
	}
	b = append(b, row("kept", "k1", "Gil", "pass", "[x](u)"))
	h = append(h, row("kept", "k1", "Gil", "pass", "[x](u)"))
	if got := check(doc(b...), doc(h...)); len(got) != 78 {
		t.Fatalf("want all 78 reverted rows, got %d", len(got))
	}
}

// A row new in the PR, or removed, is the drift check's business, not this one's.
func TestNewAndRemovedRowsAreIgnored2648(t *testing.T) {
	if got := check(doc(row("gone", "g1", "Gil", "pass", "[x](u)")), doc(row("new", "n1", "", "unread", ""))); len(got) != 0 {
		t.Fatalf("%v", got)
	}
}

// Fail closed (blind P2 #1): a short row, a duplicate path and a document with
// no rows are errors, not an empty map that lets every revert pass.
func TestParseFailsClosed2648(t *testing.T) {
	short := "| `a` |  |  | y | #1 | h1 | Gil | pass |\n"
	for name, d := range map[string]string{
		"short row":  header + short,
		"no rows":    header,
		"duplicate":  doc(row("a", "h", "", "unread", ""), row("a", "h", "", "unread", "")),
		"extra col":  header + "| `a` |  |  | y | #1 | h1 | Gil | pass | f | extra |\n",
		"empty text": "",
		"recased":    doc(row("a", "h", "Gil", "Pass", "")),
		"reordered":  strings.Replace(doc(row("a", "h", "", "unread", "")), "| evaluator | result |", "| result | evaluator |", 1),
		"no header":  "| `a` |  |  | y | #1 | h | | unread | |\n",
	} {
		if got, err := Parse(d); err == nil {
			t.Errorf("%s: want an error, got %v", name, got)
		}
	}
}

// The real committed inventory must parse: ties the parser's header, columns and
// result vocabulary to what the generator writes (blind P2 #1).
func TestRealInventoryParses2648(t *testing.T) {
	b, err := os.ReadFile("../../docs/help-inventory.md")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := Parse(string(b))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) < 100 {
		t.Fatalf("only %d rows read from the real inventory", len(rows))
	}
}

// cli#2699 (red first): an assessed row deleted while its command still exists in the live
// command tree fails and names the row and the command; the same deletion for a command
// that is gone passes; an unread row, a changed row and a still-present row are untouched.
func TestDeletedAssessedRowForASurvivingCommandIsNamed2699(t *testing.T) {
	base := doc(row("node get", "aaa111", "Gil", "pass", "[x](u)"), row("node ls", "bbb222", "Gem", "fail", "[y](v)"))
	head := doc(row("node ls", "bbb222", "Gem", "fail", "[y](v)"))
	got := checkLive(base, head, map[string]bool{"node get": true, "node ls": true})
	if len(got) != 1 || !strings.Contains(got[0], "`node get`") || !strings.Contains(got[0], "hadron node get") || !strings.Contains(got[0], "deleted") || !strings.Contains(got[0], "aaa111") {
		t.Fatalf("want one deletion naming the row and the command: %v", got)
	}
	// A fail row is assessed work too.
	got = checkLive(base, doc(row("node get", "aaa111", "Gil", "pass", "[x](u)")), map[string]bool{"node ls": true})
	if len(got) != 1 || !strings.Contains(got[0], "`node ls`") || !strings.Contains(got[0], "fail") {
		t.Fatalf("a deleted fail row for a surviving command must be named: %v", got)
	}
}

func TestDeletedRowForARemovedCommandPasses2699(t *testing.T) {
	base := doc(row("node get", "aaa111", "Gil", "pass", "[x](u)"))
	if got := checkLive(base, doc(row("node ls", "bbb222", "", "unread", "")), map[string]bool{"node ls": true}); len(got) != 0 {
		t.Fatalf("a row deleted together with its command must pass: %v", got)
	}
	// A nil tree means the command set is unknown: the rule is inert, not fail-open noise.
	if got := checkLive(base, doc(row("node ls", "bbb222", "", "unread", "")), nil); len(got) != 0 {
		t.Fatalf("a nil command tree must leave the deletion rule inert: %v", got)
	}
}

// An unread row has no evaluator work to lose: deleting it is not this rule's business.
func TestDeletedUnreadRowIsNotNamed2699(t *testing.T) {
	base := doc(row("node get", "aaa111", "", "unread", ""), row("node ls", "bbb222", "Gem", "pass", "[y](v)"))
	head := doc(row("node ls", "bbb222", "Gem", "pass", "[y](v)"))
	if got := checkLive(base, head, map[string]bool{"node get": true, "node ls": true}); len(got) != 0 {
		t.Fatalf("an unread row has no assessed work: %v", got)
	}
}

// The root page's row is "(root)" and its command is `hadron`.
func TestDeletedRootRowNamesTheRootCommand2699(t *testing.T) {
	got := checkLive(doc(row("(root)", "ccc333", "Gil", "pass", "[x](u)"), row("a", "h", "", "unread", "")), doc(row("a", "h", "", "unread", "")), map[string]bool{"(root)": true})
	if len(got) != 1 || !strings.Contains(got[0], "`(root)`") || !strings.Contains(got[0], "`hadron`") {
		t.Fatalf("want the root row named with the `hadron` command: %v", got)
	}
}

// LivePaths spells the pages the way the inventory does: a group and its children by
// canonical name, the root as "(root)", help and completion as generated pages; a hidden or
// deprecated command, a non-runnable group with nothing listed and an alias are not pages.
func TestLivePathsSpellingMatchesTheInventory2699(t *testing.T) {
	root := &cobra.Command{Use: "hadron"}
	grp := &cobra.Command{Use: "grp", Short: "g"}
	grp.AddCommand(&cobra.Command{Use: "sub", Aliases: []string{"s", "ls"}, Short: "s", Run: func(*cobra.Command, []string) {}})
	grp.AddCommand(&cobra.Command{Use: "gone", Short: "gone", Hidden: true, Run: func(*cobra.Command, []string) {}})
	grp.AddCommand(&cobra.Command{Use: "old", Short: "old", Deprecated: "use sub", Run: func(*cobra.Command, []string) {}})
	empty := &cobra.Command{Use: "empty", Short: "no listed children"}
	empty.AddCommand(&cobra.Command{Use: "h", Short: "h", Hidden: true, Run: func(*cobra.Command, []string) {}})
	root.AddCommand(grp, empty, &cobra.Command{Use: "solo", Short: "solo", Run: func(*cobra.Command, []string) {}})
	got := LivePaths(root)
	want := map[string]bool{"(root)": true, "help": true, "completion": true, "grp": true, "grp sub": true, "solo": true}
	for p := range want {
		if !got[p] {
			t.Errorf("live paths lack %q: %v", p, got)
		}
	}
	for p := range got {
		if !want[p] {
			t.Errorf("live paths carry %q, which the inventory would not list: %v", p, got)
		}
	}
}
