package cmdutil

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func TestParseNodeWhere(t *testing.T) {
	t.Run("empty is nil", func(t *testing.T) {
		w, err := ParseNodeWhere("  ")
		if err != nil || w != nil {
			t.Fatalf("blank --where should be (nil, nil), got (%v, %v)", w, err)
		}
	})

	t.Run("valid leaf", func(t *testing.T) {
		w, err := ParseNodeWhere(`{"path":["source"],"eq":"substack"}`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(w.Path) != 1 || w.Path[0] != "source" || string(w.Eq) != `"substack"` {
			t.Errorf("leaf not parsed: %+v", w)
		}
	})

	t.Run("explicit null operand survives", func(t *testing.T) {
		w, err := ParseNodeWhere(`{"path":["archivedAt"],"eq":null}`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if string(w.Eq) != "null" {
			t.Errorf("explicit eq:null must be preserved, got %q", string(w.Eq))
		}
	})

	// The reviewer case: concatenated JSON must not slip through dec.More().
	for _, bad := range []string{
		`{"path":["x"],`,                // truncated
		`{"path":["x"],"nope":1}`,       // unknown field
		`{"path":["x"],"eq":1} {}`,      // trailing object
		`{"path":["x"],"eq":1} garbage`, // trailing garbage
		`not json`,
	} {
		t.Run("usage error: "+bad, func(t *testing.T) {
			_, err := ParseNodeWhere(bad)
			if err == nil || exitcode.FromError(err) != exitcode.Usage {
				t.Errorf("%q should be a usage error, got %v", bad, err)
			}
		})
	}
}

func TestParseNodePropertySort(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		s, err := ParseNodePropertySort(`{"path":["rank"],"as":"number","direction":"desc"}`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(s.Path) != 1 || s.Path[0] != "rank" || s.As == nil || *s.As != "number" {
			t.Errorf("sort not parsed: %+v", s)
		}
	})

	// path is required — missing or empty must be a client-side usage error, not a
	// server GraphQL validation failure.
	for _, bad := range []string{
		`{}`,                  // missing path
		`{"path":[]}`,         // explicit empty path
		`{"path":["r"]} {}`,   // trailing object
		`{"direction":"asc"}`, // path omitted
	} {
		t.Run("usage error: "+bad, func(t *testing.T) {
			_, err := ParseNodePropertySort(bad)
			if err == nil || exitcode.FromError(err) != exitcode.Usage {
				t.Errorf("%q should be a usage error, got %v", bad, err)
			}
		})
	}
}

// Guard: ParseNodeWhere output round-trips to the exact wire bytes we expect
// (omit-vs-null preserved end to end through the helper).
func TestParseNodeWhereWireShape(t *testing.T) {
	w, err := ParseNodeWhere(`{"and":[{"path":["a"],"eq":"x"},{"path":["b"],"exists":false}]}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	b, _ := json.Marshal(w)
	if got := string(b); got != `{"and":[{"path":["a"],"eq":"x"},{"path":["b"],"exists":false}]}` {
		t.Errorf("wire shape drifted: %s", got)
	}
}

// WhereDefaultColumnNote fires on exactly one shape (#603): a predicate was
// given, the result was EMPTY, and no leaf anywhere in the tree named a column.
// Each case below is driven through ParseNodeWhere rather than a hand-built
// struct, so the test exercises the same tree a real `--where` produces —
// including which branch keys the parser populates.
func TestWhereDefaultColumnNote(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		matched int
		want    bool
	}{
		// The reported case: the example from the old help text, verbatim.
		{"bare leaf, no hits", `{"path":["identity"],"exists":true}`, 0, true},
		// Hits mean the predicate worked; there is nothing to warn about.
		{"bare leaf, predicate matched", `{"path":["identity"],"exists":true}`, 3, false},
		// The author has met "field" — warning them is noise, even though the
		// column they named is the default one.
		{"explicit properties", `{"field":"properties","path":["rank"],"eq":"1"}`, 0, false},
		{"explicit data", `{"field":"data","path":["identity"],"exists":true}`, 0, false},
		// The walk must reach every branch key. `not` is the one a hand-rolled
		// recursion forgets, because it holds a single node rather than a slice.
		{"field under and", `{"and":[{"path":["a"],"eq":"x"},{"field":"data","path":["b"],"exists":true}]}`, 0, false},
		{"field under or", `{"or":[{"path":["a"],"eq":"x"},{"field":"data","path":["b"],"exists":true}]}`, 0, false},
		{"field under not", `{"not":{"field":"data","path":["b"],"exists":true}}`, 0, false},
		{"field nested two deep", `{"and":[{"or":[{"field":"data","path":["b"],"exists":true}]}]}`, 0, false},
		// A whole tree of bare leaves is still a caller who has not met "field".
		{"all bare under and", `{"and":[{"path":["a"],"eq":"x"},{"path":["b"],"exists":true}]}`, 0, true},
		{"all bare under not", `{"not":{"path":["b"],"exists":true}}`, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, err := ParseNodeWhere(tc.raw)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			note := WhereDefaultColumnNote(w, &tc.matched)
			if got := note != ""; got != tc.want {
				t.Errorf("note=%v, want %v (note=%q)", got, tc.want, note)
			}
			// The note must name both the column it searched and the remedy —
			// a note saying only "no results" would be the silent zero with
			// extra words.
			if tc.want {
				for _, must := range []string{"properties", `"field":"data"`} {
					if !strings.Contains(note, must) {
						t.Errorf("note omits %q: %s", must, note)
					}
				}
			}
		})
	}
}

// No --where at all must never produce the note: an empty listing with no
// predicate is just an empty listing, and a column hint there would be noise on
// every bare `node ls` that happens to match nothing.
func TestWhereDefaultColumnNoteSilentWithoutPredicate(t *testing.T) {
	zero := 0
	if note := WhereDefaultColumnNote(nil, &zero); note != "" {
		t.Errorf("no predicate must produce no note, got %q", note)
	}
}

// An UNKNOWN match count must stay silent (@codex, PR #604). The server's
// `total` is nullable, and a note is only worth printing when we know the
// predicate matched nothing — guessing produces a confident wrong answer about
// someone's data, which is the failure this note exists to prevent.
func TestWhereDefaultColumnNoteSilentWhenTotalUnknown(t *testing.T) {
	w, err := ParseNodeWhere(`{"path":["identity"],"exists":true}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if note := WhereDefaultColumnNote(w, nil); note != "" {
		t.Errorf("an unknown total must produce no note, got %q", note)
	}
	// Two-directional: the same predicate with a KNOWN zero does warn, so the
	// silence above is the nil and not a detector that never fires.
	zero := 0
	if note := WhereDefaultColumnNote(w, &zero); note == "" {
		t.Error("a known zero must still warn — otherwise this test proves nothing")
	}
}
