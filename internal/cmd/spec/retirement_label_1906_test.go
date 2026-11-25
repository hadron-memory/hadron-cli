package spec

import "testing"

func TestRetirementKindOfAbstract(t *testing.T) {
	str := func(s string) *string { return &s }
	for _, tc := range []struct {
		abstract *string
		want     string
	}{
		{nil, ""},
		{str("An abstract."), ""},
		{str("WITHDRAWN (2026-11-11); do not apply. x"), retirementWithdrawn},
		{str("  RESCINDED (2026-11-11); do not apply. x"), retirementRescinded},
		{str("WITHDRAWN without a date"), ""},
		{str("SUPERSEDED (2026-11-11) by a:b (T); do not apply."), ""},
	} {
		got := ""
		if tc.abstract != nil {
			got = rescindKind(*tc.abstract)
		}
		if got != tc.want {
			t.Errorf("%v: got %q want %q", tc.abstract, got, tc.want)
		}
	}
}

func TestReplacedMarkerPerKind(t *testing.T) {
	by := "x:y"
	for _, tc := range []struct {
		r    replacement
		want string
	}{
		{replacement{}, ""},
		{replacement{Replaced: true, By: &by}, " (replaced by x:y)"},
		{replacement{Replaced: true, By: &by, Kind: retirementWithdrawn}, " (replaced by x:y)"}, // a readable successor wins
		{replacement{Replaced: true, Kind: retirementWithdrawn}, " (withdrawn)"},
		{replacement{Replaced: true, Kind: retirementRescinded}, " (rescinded)"},
		{replacement{Replaced: true}, " (retired)"}, // neither prefix nor usable edge: neutral
	} {
		if got := replacedMarker(tc.r, ""); got != tc.want {
			t.Errorf("%+v: got %q want %q", tc.r, got, tc.want)
		}
	}
}

func TestRetirementWordNamesTheState(t *testing.T) {
	by := "x:y"
	for _, tc := range []struct {
		r    replacement
		want string
	}{
		{replacement{Replaced: true, By: &by}, "replaced"},
		{replacement{Replaced: true, Kind: retirementWithdrawn}, "withdrawn"},
		{replacement{Replaced: true, Kind: retirementRescinded}, "rescinded"},
		{replacement{Replaced: true}, "retired"},
	} {
		if got := tc.r.word(); got != tc.want {
			t.Errorf("%+v: got %q want %q", tc.r, got, tc.want)
		}
	}
}
