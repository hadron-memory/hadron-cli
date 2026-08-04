package coding

import "testing"

func intp(i int) *int { return &i }

// Rows come back in the order a reviewer walks the checklist — by seq, then
// loc, with unset seq last — and carry the linter's verdict as their status.
func TestReviewRowsOrderAndStatus(t *testing.T) {
	in := reviewInput{
		Members: map[string]checkNode{
			"review:zeta":  {Loc: "review:zeta", Description: "d"},
			"review:alpha": {Loc: "review:alpha", Description: "d"},
			"review:first": {Loc: "review:first", Description: "d", Seq: intp(1)},
			"review:tenth": {Loc: "review:tenth", Description: "d", Seq: intp(10)},
			"review:bad":   {Loc: "review:bad", Description: "d"},
		},
		Edges: map[string]graphEdge{
			"review:zeta":  {ID: "e1", Label: "Applies when z changes"},
			"review:alpha": {ID: "e2", Label: "Applies when a changes"},
			"review:first": {ID: "e3", Label: "Applies when f changes"},
			"review:tenth": {ID: "e4", Label: "Applies when t changes"},
			"review:bad":   {ID: "e5", Label: "child-of"},
		},
		Unavailable: []string{"review:ghost"},
	}
	rows := reviewRows(in, lintReview(in))

	want := []string{"review:first", "review:tenth", "review:alpha", "review:bad", "review:ghost", "review:zeta"}
	if len(rows) != len(want) {
		t.Fatalf("got %d rows, want %d: %+v", len(rows), len(want), rows)
	}
	for i, w := range want {
		if rows[i].Loc != w {
			t.Errorf("row %d = %q, want %q (seq first, then loc; unset seq last)", i, rows[i].Loc, w)
		}
	}

	status := map[string]string{}
	for _, r := range rows {
		status[r.Loc] = r.Status
	}
	if status["review:bad"] != statusBroken {
		t.Errorf("a check the linter errors on must list as %q, got %q", statusBroken, status["review:bad"])
	}
	if status["review:alpha"] != statusOK {
		t.Errorf("a healthy check must list as %q, got %q", statusOK, status["review:alpha"])
	}
	// The list-vs-read visibility gap: an endpoint that can't be read is part
	// of the answer to "what is in this checklist", not something to drop.
	if status["review:ghost"] != statusUnavailable {
		t.Errorf("an unreadable check must list as %q, got %q", statusUnavailable, status["review:ghost"])
	}

	// The trigger column is the edge label — the text a diff is matched
	// against, which is the whole reason to list.
	for _, r := range rows {
		if r.Loc == "review:alpha" && r.Trigger != "Applies when a changes" {
			t.Errorf("trigger = %q, want the edge label", r.Trigger)
		}
	}

	broken := keepBroken(rows)
	if len(broken) != 2 {
		t.Fatalf("--broken should keep the broken and the unavailable, got %+v", broken)
	}
}

// A check with no edge to the parent is invisible to the reviewer — the
// highest-severity finding — so it must not list as ok.
func TestReviewRowsUnwiredCheckIsBroken(t *testing.T) {
	in := reviewInput{
		Members: map[string]checkNode{"review:orphan": {Loc: "review:orphan", Description: "d"}},
		Edges:   map[string]graphEdge{},
	}
	rows := reviewRows(in, lintReview(in))
	if len(rows) != 1 || rows[0].Status != statusBroken {
		t.Fatalf("an unwired check must list as broken, got %+v", rows)
	}
	if rows[0].Trigger != "" {
		t.Errorf("an unwired check has no trigger, got %q", rows[0].Trigger)
	}
}

func TestRouteRowsStatusAndOrder(t *testing.T) {
	in := preflightInput{
		Routes: []graphEdge{
			{ID: "e1", Label: "to fix a failing build", OtherID: "t1", Other: "ops:ci", MemoryID: "m1"},
			{ID: "e2", Label: "routes-to", OtherID: "t2", Other: "architecture", MemoryID: "m1"},
			{ID: "e3", Label: "to read the dead one", OtherID: "t3", Other: "findings:gone", MemoryID: "m1"},
		},
		Targets: map[string]checkNode{
			"ops:ci":       {Loc: "ops:ci"},
			"architecture": {Loc: "architecture"},
		},
		Unavailable: []string{"findings:gone"},
		HomeMemory:  "m1",
	}
	rows := routeRows(in, lintPreflight(in))

	want := []string{"architecture", "findings:gone", "ops:ci"}
	for i, w := range want {
		if rows[i].Target != w {
			t.Errorf("row %d = %q, want %q (target order, matching the linter's)", i, rows[i].Target, w)
		}
	}
	byTarget := map[string]routeDTO{}
	for _, r := range rows {
		byTarget[r.Target] = r
	}
	if byTarget["findings:gone"].Status != statusBroken {
		t.Errorf("a route whose target can't be read is broken, got %q", byTarget["findings:gone"].Status)
	}
	// A convention violation is a warning, not a broken route: the label is
	// unhelpful but the reader still lands somewhere real.
	if byTarget["architecture"].Status != statusOK {
		t.Errorf("a badly-labelled but live route stays ok, got %q", byTarget["architecture"].Status)
	}
	if byTarget["ops:ci"].EdgeID != "e1" || byTarget["ops:ci"].MemoryID != "m1" {
		t.Errorf("row should carry the edge id and the target's memory, got %+v", byTarget["ops:ci"])
	}
	if got := keepBrokenRoutes(rows); len(got) != 1 || got[0].Target != "findings:gone" {
		t.Errorf("--broken should keep only the dead route, got %+v", got)
	}
}

// A redacted endpoint has no loc to key a finding by; the row must still say
// the route is broken rather than looking clean.
func TestRouteRowsRedactedEndpointIsBroken(t *testing.T) {
	in := preflightInput{
		Routes: []graphEdge{{ID: "e1", Label: "to somewhere", OtherID: "", Other: ""}},
	}
	rows := routeRows(in, lintPreflight(in))
	if len(rows) != 1 || rows[0].Status != statusBroken {
		t.Fatalf("a redacted endpoint must list as broken, got %+v", rows)
	}
}
