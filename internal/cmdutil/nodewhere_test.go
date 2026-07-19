package cmdutil

import (
	"encoding/json"
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
