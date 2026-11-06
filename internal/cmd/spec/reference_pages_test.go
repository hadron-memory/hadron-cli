package spec

import (
	"errors"
	"reflect"
	"testing"
)

func TestDrainReferencesMetadata1554(t *testing.T) {
	i := func(x int) *int { return &x }
	b := func(x bool) *bool { return &x }
	row := referenceDTO{SourceLoc: "a", TargetLoc: "b"}
	p := func(n, total int, more bool) *referencePage {
		v := make([]referenceDTO, n)
		for k := range v {
			v[k] = row
		}
		return &referencePage{v, i(total), b(more)}
	}
	for _, tt := range []struct {
		name  string
		pages []*referencePage
		fail  int
		want  int
	}{
		{"empty", []*referencePage{p(0, 0, false)}, -1, 0},
		{"duplicates and short page", []*referencePage{p(1, 3, true), p(2, 3, false)}, -1, 3},
		{"exact multiple", []*referencePage{p(20, 40, true), p(20, 40, false)}, -1, 40},
		{"null page", []*referencePage{nil}, -1, -1},
		{"null items", []*referencePage{{total: i(0), more: b(false)}}, -1, -1},
		{"missing total", []*referencePage{{items: []referenceDTO{}, more: b(false)}}, -1, -1},
		{"missing hasMore", []*referencePage{{items: []referenceDTO{}, total: i(0)}}, -1, -1},
		{"negative", []*referencePage{p(0, -1, false)}, -1, -1},
		{"empty hasMore", []*referencePage{p(0, 1, true)}, -1, -1},
		{"too many", []*referencePage{p(2, 1, false)}, -1, -1},
		{"early terminal", []*referencePage{p(1, 2, false)}, -1, -1},
		{"hasMore after total", []*referencePage{p(1, 1, true)}, -1, -1},
		{"total drift", []*referencePage{p(1, 2, true), p(1, 3, true)}, -1, -1},
		{"later failure", []*referencePage{p(1, 2, true), nil}, 1, -1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			offset := 0
			got, err := drainReferences(func(limit, off int) (*referencePage, error) {
				if limit != 20 || off != offset {
					t.Fatalf("request limit=%d off=%d expected=%d", limit, off, offset)
				}
				if calls >= len(tt.pages) {
					t.Fatal("unexpected extra page")
					return nil, nil
				}
				k := calls
				calls++
				if k == tt.fail {
					return nil, errors.New("lost answer")
				}
				v := tt.pages[k]
				if v != nil {
					offset += len(v.items)
				}
				return v, nil
			})
			if tt.want < 0 {
				if err == nil || got != nil {
					t.Fatalf("failed read must discard all: got=%v err=%v", got, err)
				}
				return
			}
			if err != nil || len(got) != tt.want {
				t.Fatalf("got=%v err=%v", got, err)
			}
			if got == nil {
				t.Fatal("empty must be []")
			}
			for _, r := range got {
				if !reflect.DeepEqual(r, row) {
					t.Fatal(r)
				}
			}
		})
	}
}

func TestDrainReferenceOrder1554(t *testing.T) {
	total := 4
	more := true
	done := false
	rows := []referenceDTO{{SourceLoc: "z"}, {SourceLoc: "a"}, {SourceLoc: "a"}, {SourceLoc: "b"}}
	calls := 0
	got, err := drainReferences(func(limit, offset int) (*referencePage, error) {
		calls++
		if calls == 1 {
			return &referencePage{rows[:2], &total, &more}, nil
		}
		if calls == 2 {
			return &referencePage{rows[2:], &total, &done}, nil
		}
		t.Fatal("extra page")
		return nil, nil
	})
	if err != nil || !reflect.DeepEqual(got, rows) {
		t.Fatalf("order/multiplicity got=%v err=%v", got, err)
	}
}
