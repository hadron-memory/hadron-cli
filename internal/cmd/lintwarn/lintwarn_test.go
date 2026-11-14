package lintwarn

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

func raw(total int, next string, n int) *Raw {
	r := &Raw{Total: total, Items: make([]*Warning, n)}
	for i := range r.Items {
		r.Items[i] = &Warning{Kind: "BROKEN_INCLUDE", Position: i, Text: "t"}
	}
	if next != "" {
		r.NextCursor = &next
	}
	return r
}

func always() bool { return true }

func script(pages ...*Raw) Fetch {
	i := 0
	return func(context.Context, *string) (*Raw, error) {
		if i >= len(pages) {
			return nil, errors.New("past the script")
		}
		p := pages[i]
		i++
		return p, nil
	}
}

func TestDrainFollowsCursorsToTheEnd(t *testing.T) {
	p := Drain(context.Background(), always, script(raw(45, "a", 20), raw(45, "b", 20), raw(45, "", 5)))
	if p == nil || len(p.Items) != 45 || p.Total != 45 || p.NextCursor != nil {
		t.Fatalf("page = %+v", p)
	}
}

func TestDrainEmptyIsAnAnswerWithItemsNotNil(t *testing.T) {
	p := Drain(context.Background(), always, script(raw(0, "", 0)))
	if p == nil || p.Items == nil || len(p.Items) != 0 {
		t.Fatalf("an empty answer keeps items [] (not nil): %+v", p)
	}
}

// Each guard fails closed: never a partial page, never a clean "no warnings".
func TestDrainFailsClosed(t *testing.T) {
	cases := map[string]Fetch{
		"null page":                script(nil),
		"nil items":                script(&Raw{Total: 0}),
		"nil element":              script(&Raw{Total: 1, Items: []*Warning{nil}}),
		"negative total":           script(raw(-1, "", 0)),
		"total drifts":             script(raw(5, "a", 2), raw(6, "", 4)),
		"short final page":         script(raw(5, "", 2)),
		"more items than total":    script(raw(1, "", 2)),
		"empty cursor":             script(raw(5, "x", 2), &Raw{Total: 5, Items: []*Warning{{}}, NextCursor: ptr("")}),
		"repeated cursor":          script(raw(9, "a", 2), raw(9, "a", 2)),
		"empty page with a cursor": script(raw(5, "a", 2), raw(5, "b", 0)),
		"fetch error":              script(raw(5, "a", 2)),
		"error from the fetch":     func(context.Context, *string) (*Raw, error) { return nil, errors.New("boom") },
	}
	for name, f := range cases {
		if p := Drain(context.Background(), always, f); p != nil {
			t.Errorf("%s: want unavailable (nil), got %+v", name, p)
		}
	}
}

func ptr(s string) *string { return &s }

func TestDrainStopsWhenTheBudgetIsSpent(t *testing.T) {
	n := 0
	allow := func() bool { n++; return n <= 2 }
	if p := Drain(context.Background(), allow, script(raw(9, "a", 3), raw(9, "b", 3), raw(9, "", 3))); p != nil {
		t.Fatalf("a drain the budget cuts short is unavailable, never partial: %+v", p)
	}
}

func TestBudgetRequestCapAndDeadline(t *testing.T) {
	b := NewBudget(context.Background())
	defer b.Close()
	for i := 0; i < MaxRequests; i++ {
		if !b.Allow() {
			t.Fatalf("request %d refused before the cap", i+1)
		}
	}
	if b.Allow() {
		t.Fatal("the 1001st request must be refused")
	}
	parent, cancel := context.WithCancel(context.Background())
	late := NewBudget(parent)
	defer late.Close()
	cancel()
	if late.Allow() {
		t.Error("a spent deadline refuses every further request")
	}
	if dl, ok := NewBudget(context.Background()).Ctx().Deadline(); !ok || time.Until(dl) > ReadTimeout {
		t.Errorf("the budget's deadline is %v from the start, got %v", ReadTimeout, dl)
	}
}

func hist(ids []string, lint map[string]*gen.LintWarningPageFields) []HistItem {
	items := make([]HistItem, len(ids))
	for i, id := range ids {
		items[i] = HistItem{ID: id, Lint: lint[id]}
	}
	return items
}

func fields(total int, next string, n int) *gen.LintWarningPageFields {
	f := &gen.LintWarningPageFields{Total: total}
	for i := 0; i < n; i++ {
		f.Items = append(f.Items, &gen.LintWarningPageFieldsItemsLintWarning{Kind: "BROKEN_INCLUDE", Position: i, Text: "t"})
	}
	if next != "" {
		f.NextCursor = &next
	}
	return f
}

func TestHistoryLintWholeOrNilPerRecord(t *testing.T) {
	b := NewBudget(context.Background())
	defer b.Close()
	ids := []string{"r1", "r2", "r3"}
	first := func(context.Context) (int, []HistItem, error) {
		return 9, hist([]string{"r1", "OTHER", "r3"}, map[string]*gen.LintWarningPageFields{
			"r1": fields(1, "", 1), "OTHER": fields(1, "", 1), "r3": fields(3, "c1", 2)}), nil
	}
	var offsets []int
	at := func(_ context.Context, off int, after *string) (int, []HistItem, error) {
		offsets = append(offsets, off)
		return 9, hist([]string{"r3"}, map[string]*gen.LintWarningPageFields{"r3": fields(3, "", 1)}), nil
	}
	pages := ReadHistoryLint(b, ids, 4, 9, first, at)
	if pages[0] == nil || len(pages[0].Items) != 1 {
		t.Errorf("a whole inline record keeps its page: %+v", pages[0])
	}
	if pages[1] != nil {
		t.Errorf("item 2 carries another record's id: it must be nil, never that lint: %+v", pages[1])
	}
	if pages[2] == nil || len(pages[2].Items) != 3 || len(offsets) != 1 || offsets[0] != 4+2 {
		t.Errorf("a record with more pages is re-read alone at shownOffset+i: pages=%+v offsets=%v", pages[2], offsets)
	}
}

func TestHistoryLintFailsClosedOnShifts(t *testing.T) {
	ids := []string{"r1"}
	ok := func(context.Context) (int, []HistItem, error) {
		return 5, hist(ids, map[string]*gen.LintWarningPageFields{"r1": fields(3, "c1", 2)}), nil
	}
	cases := map[string]func(context.Context, int, *string) (int, []HistItem, error){
		"history total shifts": func(context.Context, int, *string) (int, []HistItem, error) {
			return 6, hist(ids, map[string]*gen.LintWarningPageFields{"r1": fields(3, "", 1)}), nil
		},
		"a different record at the offset": func(context.Context, int, *string) (int, []HistItem, error) {
			return 5, hist([]string{"r9"}, map[string]*gen.LintWarningPageFields{"r9": fields(3, "", 1)}), nil
		},
		"empty page": func(context.Context, int, *string) (int, []HistItem, error) { return 5, nil, nil },
		"lint total drifts": func(context.Context, int, *string) (int, []HistItem, error) {
			return 5, hist(ids, map[string]*gen.LintWarningPageFields{"r1": fields(4, "", 2)}), nil
		},
		"error": func(context.Context, int, *string) (int, []HistItem, error) { return 0, nil, errors.New("x") },
	}
	for name, at := range cases {
		b := NewBudget(context.Background())
		if p := ReadHistoryLint(b, ids, 0, 5, ok, at); p[0] != nil {
			t.Errorf("%s: want nil, got %+v", name, p[0])
		}
		b.Close()
	}
	b := NewBudget(context.Background())
	defer b.Close()
	shifted := func(context.Context) (int, []HistItem, error) {
		return 6, hist(ids, map[string]*gen.LintWarningPageFields{"r1": fields(1, "", 1)}), nil
	}
	if p := ReadHistoryLint(b, ids, 0, 5, shifted, nil); p[0] != nil {
		t.Errorf("the history total shifting between the shown page and the lint read makes every record nil: %+v", p[0])
	}
}

func TestLineNamesTheLocatorAndAddsNothing(t *testing.T) {
	field := "trailer"
	if got := Line(Warning{Kind: "WORKER_LINK_MISSING", Position: 0, Text: "x", WorkerField: &field}, false); got != "  WORKER_LINK_MISSING trailer: x" {
		t.Errorf("worker line = %q", got)
	}
	if got := Line(Warning{Kind: "BROKEN_INCLUDE", Position: 3, Text: "x"}, true); got != "  BROKEN_INCLUDE occurrence #3: x" {
		t.Errorf("compiled line = %q", got)
	}
	urn, p, n := "hrn:node:a:b:c", 2, 5
	if got := Line(Warning{Kind: "PINNED_NEWER_APPROVED", Text: "x", TargetURN: &urn, PinnedRevision: &p, NewerRevision: &n}, true); !strings.Contains(got, "(r2 → r5)") || !strings.HasSuffix(got, "→ hrn:node:a:b:c") {
		t.Errorf("optional fields print only when returned: %q", got)
	}
	if strings.Contains(Line(Warning{Kind: "BROKEN_INCLUDE", Text: "x"}, true), "→") {
		t.Error("a broken include shows its position only (cor:dmo:150:05)")
	}
}
