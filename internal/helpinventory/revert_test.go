package helpinventory

import (
	"fmt"
	"strings"
	"testing"
)

const header = "# Help inventory\n\n| path | aliases | generated | has-examples | child | help-hash | evaluator | result | finding |\n|---|---|---|---|---|---|---|---|---|\n"

func doc(rows ...string) string { return header + strings.Join(rows, "\n") + "\n" }

func row(path, hash, eval, result, finding string) string {
	return fmt.Sprintf("| `%s` |  |  | y | #1 | %s | %s | %s | %s |", path, hash, eval, result, finding)
}

func check(base, head string) []string { return Reverts(Parse(base), Parse(head)) }

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

// A short row (fewer than the nine columns) is skipped, not a panic.
func TestShortRowDoesNotPanic2648(t *testing.T) {
	short := "| `a` |  |  | y | #1 | h1 | Gil | pass |\n"
	if got := Parse(header + short); len(got) != 0 {
		t.Fatalf("an 8-column row is not a row: %v", got)
	}
}
