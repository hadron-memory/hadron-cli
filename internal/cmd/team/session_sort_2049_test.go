package team

import (
	"slices"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// sortFixture2049 covers every branch of the comparator with one expected order:
// distinct instants, equal instants written in different offsets (an Id
// tie-break), an offset whose text order opposes its instant order, the year-0000
// and year-0001 boundaries, and two unparsable timestamps whose text order
// opposes their Id order.
func sortFixture2049() (rows []gen.TeamSessionFields, want []string) {
	at := func(id, startedAt string) gen.TeamSessionFields {
		return gen.TeamSessionFields{Id: id, StartedAt: startedAt}
	}
	rows = []gen.TeamSessionFields{
		at("s7", "2026-08-11T09:30:00Z"),
		at("s5", "2026-08-11T09:00:00Z"),
		at("s6", "2026-08-11T10:00:00+01:00"), // the same instant as s5
		at("sa", "2026-08-11T05:00:00Z"),
		at("s8", "2026-08-11T09:00:00+05:00"), // 04:00Z: later text than sa, earlier instant
		at("s3", "0001-01-01T00:00:00Z"),
		at("s4", "0000-01-01T00:00:00Z"),
		at("s9", "aa-bad"),
		at("s1", "zz-bad"),
	}
	return rows, []string{"s7", "s6", "s5", "sa", "s8", "s3", "s4", "s9", "s1"}
}

func ids2049(rows []gen.TeamSessionFields) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Id
	}
	return out
}

// Every ordering of the fixture sorts to the same result: the comparator is a
// total order on one basis, not a pairwise mix that depends on the input order.
func TestSortNewestFirstIsOrderIndependent(t *testing.T) {
	rows, want := sortFixture2049()
	n := 0
	var permute func(k int)
	permute = func(k int) {
		if k == 1 {
			cp := slices.Clone(rows)
			sortNewestFirst(cp)
			if got := ids2049(cp); !slices.Equal(got, want) {
				t.Fatalf("input %v sorted to %v, want %v", ids2049(rows), got, want)
			}
			n++
			return
		}
		for i := 0; i < k; i++ {
			permute(k - 1)
			if k%2 == 0 {
				rows[i], rows[k-1] = rows[k-1], rows[i]
			} else {
				rows[0], rows[k-1] = rows[k-1], rows[0]
			}
		}
	}
	permute(len(rows))
	if n != 362880 {
		t.Fatalf("covered %d orderings, want all 9! = 362880", n)
	}
}

// The boundaries and ties named one by one, so a failure says which branch broke.
func TestSortNewestFirstBranches(t *testing.T) {
	order := func(rows ...gen.TeamSessionFields) []string {
		sortNewestFirst(rows)
		return ids2049(rows)
	}
	at := func(id, s string) gen.TeamSessionFields { return gen.TeamSessionFields{Id: id, StartedAt: s} }
	for name, tc := range map[string]struct {
		rows []gen.TeamSessionFields
		want []string
	}{
		"equal startedAt: Id descending":              {[]gen.TeamSessionFields{at("a", "2026-08-11T09:00:00Z"), at("b", "2026-08-11T09:00:00Z")}, []string{"b", "a"}},
		"equal instant, different offsets: Id desc":   {[]gen.TeamSessionFields{at("a", "2026-08-11T10:00:00+01:00"), at("b", "2026-08-11T09:00:00Z")}, []string{"b", "a"}},
		"offset text opposes instant: instant wins":   {[]gen.TeamSessionFields{at("a", "2026-08-11T09:00:00+05:00"), at("b", "2026-08-11T05:00:00Z")}, []string{"b", "a"}},
		"unparsable sorts after any parsable":         {[]gen.TeamSessionFields{at("z", "garbage"), at("a", "0000-01-01T00:00:00Z")}, []string{"a", "z"}},
		"year 0001 before year 0000, both parsable":   {[]gen.TeamSessionFields{at("a", "0000-01-01T00:00:00Z"), at("b", "0001-01-01T00:00:00Z")}, []string{"b", "a"}},
		"unparsable pair: Id, not text":               {[]gen.TeamSessionFields{at("s1", "zz-bad"), at("s9", "aa-bad")}, []string{"s9", "s1"}},
		"empty startedAt is unparsable, sorts last":   {[]gen.TeamSessionFields{at("z", ""), at("a", "2026-08-11T09:00:00Z")}, []string{"a", "z"}},
		"year 0001 row still sorts before unparsable": {[]gen.TeamSessionFields{at("a", "0001-01-01T00:00:00Z"), at("z", "bad")}, []string{"a", "z"}},
		"fractional seconds compare as instants":      {[]gen.TeamSessionFields{at("a", "2026-08-11T09:00:00.100Z"), at("b", "2026-08-11T09:00:00.200Z")}, []string{"b", "a"}},
		"single row is returned as is":                {[]gen.TeamSessionFields{at("a", "x")}, []string{"a"}},
		"already newest first stays":                  {[]gen.TeamSessionFields{at("b", "2026-08-12T00:00:00Z"), at("a", "2026-08-11T00:00:00Z")}, []string{"b", "a"}},
		"parsable rows never interleave unparsable":   {[]gen.TeamSessionFields{at("a", "bad"), at("b", "2026-08-11T09:00:00Z"), at("c", "bad2"), at("d", "2026-08-10T09:00:00Z")}, []string{"b", "d", "c", "a"}},
	} {
		if got := order(tc.rows...); !slices.Equal(got, tc.want) {
			t.Errorf("%s: got %v, want %v", name, got, tc.want)
		}
	}
}
