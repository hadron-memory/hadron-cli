package spec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Khan/genqlient/graphql"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
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

// Operation choice is independent of the citation value. The public backlinks
// command separately rejects an empty citation before creating its client.
func TestReferenceExplicitOperation1768(t *testing.T) {
	for _, tc := range []struct {
		name, loc, operationName, field string
		operation                       referenceOperation
	}{
		{"empty backlinks stays backlinks", "", "SpecBacklinksPage", "specBacklinksPage", referenceBacklinks},
		{"unresolved ignores citation", "unused", "SpecUnresolvedReferencesPage", "specUnresolvedReferencesPage", referenceUnresolved},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var req struct {
					OperationName string         `json:"operationName"`
					Variables     map[string]any `json:"variables"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
					return
				}
				if req.OperationName != tc.operationName || req.Variables["memoryRef"] != "fixture-memory" || req.Variables["limit"] != float64(20) || req.Variables["offset"] != float64(0) {
					t.Errorf("wrong operation/variables: %+v", req)
				}
				if tc.operation == referenceBacklinks {
					if loc, ok := req.Variables["loc"]; !ok || loc != tc.loc {
						t.Errorf("loc=%v present=%t", loc, ok)
					}
				} else if _, ok := req.Variables["loc"]; ok {
					t.Error("unresolved gained a citation variable")
				}
				fmt.Fprintf(w, `{"data":{"%s":{"items":[],"total":0,"hasMore":false}}}`, tc.field)
			}))
			defer srv.Close()
			rows, err := readReferences(context.Background(), graphql.NewClient(srv.URL, srv.Client()), "fixture-memory", tc.operation, tc.loc)
			if err != nil || rows == nil || len(rows) != 0 || calls.Load() != 1 {
				t.Fatalf("rows=%v calls=%d err=%v", rows, calls.Load(), err)
			}
		})
	}
}
