package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// A hit whose envelope lives in `data` and whose `properties` is null — the
// shape of every chat message, and the shape #602/#603 were measured against.
// It also carries an abstract, so a test can tell the --long output apart from
// the projection output.
const searchProjEnvelope = `{"data":{"findNodes":{"total":null,"degraded":null,"reason":null,"hits":[
	{"score":0.8,"vector":null,"node":{"id":"n1","memoryId":"mem1","loc":"chats:team:messages:001-abc-holger",
		"name":"Message from holger","nodeType":"chat-message","tags":[],
		"description":null,"abstract":"AN-ABSTRACT-MARKER","updatedAt":"2026-06-11T00:00:00Z",
		"properties":null,"data":{"authorName":"holger","sessionId":"s1"}}}
]}}}`

type searchProjVars struct {
	WithProperties *bool `json:"withProperties"`
	WithData       *bool `json:"withData"`
}

func searchProjectionVars(t *testing.T, captured map[string]json.RawMessage) searchProjVars {
	t.Helper()
	raw, ok := captured["SearchNodes"]
	if !ok {
		t.Fatalf("no SearchNodes call captured")
	}
	var v searchProjVars
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decode variables: %v", err)
	}
	if v.WithProperties == nil || v.WithData == nil {
		t.Fatalf("both toggles must always be sent (Boolean!), got %s", raw)
	}
	return v
}

func runSearchProj(t *testing.T, args ...string) (map[string]json.RawMessage, string) {
	t.Helper()
	gql, captured := captureGraphQL(t, map[string]string{"SearchNodes": searchProjEnvelope})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{"search", "authorName"}, args...), "--server", gql.URL))
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	return captured, out.String()
}

func searchHit(t *testing.T, payload string) map[string]json.RawMessage {
	t.Helper()
	var result struct {
		Hits []map[string]json.RawMessage `json:"hits"`
	}
	if err := json.Unmarshal([]byte(payload), &result); err != nil {
		t.Fatalf("decode --json: %v\n%s", err, payload)
	}
	if len(result.Hits) == 0 {
		t.Fatalf("expected a hit, got none:\n%s", payload)
	}
	return result.Hits[0]
}

// The default search is unchanged, on the wire and in the payload: it asks for
// neither column and carries neither key. The flags are additive, so an agent
// parsing today's `search --json` sees nothing new until it opts in.
func TestSearchWithoutProjectionOmitsBothColumns(t *testing.T) {
	captured, out := runSearchProj(t, "--json")
	vars := searchProjectionVars(t, captured)
	if *vars.WithProperties || *vars.WithData {
		t.Errorf("an unflagged search must request neither column, got properties=%v data=%v",
			*vars.WithProperties, *vars.WithData)
	}
	hit := searchHit(t, out)
	for _, key := range []string{"data", "properties"} {
		if _, present := hit[key]; present {
			t.Errorf("unprojected hit must not carry %q: %s", key, out)
		}
	}
}

// Each flag selects its own column and NOT its neighbour. This is what catches
// the two booleans being transposed on the way to the generated client — they
// are adjacent and same-typed, so swapping them compiles silently.
func TestSearchProjectionFlagsSelectIndependently(t *testing.T) {
	t.Run("--with-data", func(t *testing.T) {
		captured, out := runSearchProj(t, "--with-data", "--json")
		vars := searchProjectionVars(t, captured)
		if !*vars.WithData || *vars.WithProperties {
			t.Errorf("--with-data must select data alone, got properties=%v data=%v",
				*vars.WithProperties, *vars.WithData)
		}
		hit := searchHit(t, out)
		if _, present := hit["properties"]; present {
			t.Errorf("properties was not requested and must be absent: %s", out)
		}
		if c := compact(t, hit["data"]); c != `{"authorName":"holger","sessionId":"s1"}` {
			t.Errorf("data must arrive verbatim, got %s", c)
		}
	})
	t.Run("--with-properties", func(t *testing.T) {
		captured, out := runSearchProj(t, "--with-properties", "--json")
		vars := searchProjectionVars(t, captured)
		if !*vars.WithProperties || *vars.WithData {
			t.Errorf("--with-properties must select properties alone, got properties=%v data=%v",
				*vars.WithProperties, *vars.WithData)
		}
		if _, present := searchHit(t, out)["data"]; present {
			t.Errorf("data was not requested and must be absent: %s", out)
		}
	})
}

// A requested column that is null on the node keeps its key, carrying the JSON
// null — an ABSENT key already means "you did not ask for it", and collapsing
// the two is the ambiguity the projection exists to remove.
func TestSearchRequestedNullColumnKeepsItsKey(t *testing.T) {
	_, out := runSearchProj(t, "--with-properties", "--with-data", "--json")
	hit := searchHit(t, out)
	got, present := hit["properties"]
	if !present {
		t.Fatalf("a REQUESTED column must keep its key even when null: %s", out)
	}
	if string(got) != "null" {
		t.Errorf("properties is null on this node and must render as null, got %s", got)
	}
	if c := compact(t, hit["data"]); c != `{"authorName":"holger","sessionId":"s1"}` {
		t.Errorf("data must still carry its value, got %s", c)
	}
}

// Text output: either projection flag switches the table for a per-hit block,
// and shows both columns whole.
func TestSearchProjectedTextShowsTheColumns(t *testing.T) {
	_, out := runSearchProj(t, "--with-properties", "--with-data")
	for _, want := range []string{
		"chats:team:messages:001-abc-holger",
		`data: {"authorName":"holger","sessionId":"s1"}`,
		"properties: null",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("projected text missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "SCORE") && strings.Contains(out, "NAME") {
		t.Errorf("projection must replace the table header, not print it too:\n%s", out)
	}
}

// The deliberate half: --with-data must NOT start printing abstracts. Both
// flags reach the same block renderer, but they ask for different things, and a
// flag that quietly turns on a neighbour's output is the "output shape changed
// because of an unrelated flag" surprise this change declined elsewhere.
func TestSearchProjectionDoesNotImplyLong(t *testing.T) {
	_, projected := runSearchProj(t, "--with-data")
	if strings.Contains(projected, "AN-ABSTRACT-MARKER") {
		t.Errorf("--with-data must not print abstracts; that is --long's job:\n%s", projected)
	}
	// Two-directional: --long DOES print it, so the absence above is the gate
	// and not a fixture that never carried an abstract.
	_, long := runSearchProj(t, "--long")
	if !strings.Contains(long, "AN-ABSTRACT-MARKER") {
		t.Fatalf("--long must still print the abstract, or this test proves nothing:\n%s", long)
	}
	// And --long alone must not start projecting columns, the other direction.
	if strings.Contains(long, "data: {") {
		t.Errorf("--long must not project columns:\n%s", long)
	}
	// Together: both.
	_, both := runSearchProj(t, "--long", "--with-data")
	if !strings.Contains(both, "AN-ABSTRACT-MARKER") || !strings.Contains(both, `data: {"authorName"`) {
		t.Errorf("--long --with-data must show both:\n%s", both)
	}
}
