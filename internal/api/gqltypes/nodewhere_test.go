package gqltypes

import (
	"encoding/json"
	"strings"
	"testing"
)

// The load-bearing contract (#719): an unset optional field must be OMITTED from
// the wire, never serialized as null — the server's leaf validation counts any
// operator key that is not undefined (an explicit null included), so a null would
// trip "a leaf must carry exactly one operator". These are bound (not generated)
// types precisely because genqlient's omitempty on the recursive NodeWhereInput
// is non-deterministic; this test pins the tags so a hand-edit can't regress them.
func TestNodeWhereInputOmitsUnsetFields(t *testing.T) {
	eq := json.RawMessage(`"substack"`)
	leaf := NodeWhereInput{Path: []string{"source"}, Eq: &eq}
	b, err := json.Marshal(leaf)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(b)
	if got != `{"path":["source"],"eq":"substack"}` {
		t.Fatalf("leaf must carry only path+eq, got %s", got)
	}
	for _, banned := range []string{"null", "ne", "lt", "gt", "between", "exists", "contains", "and", "field", "as"} {
		if strings.Contains(got, banned) {
			t.Errorf("unset field %q leaked into the wire: %s", banned, got)
		}
	}
}

// A branch node marshals to just its combinator; the leaf fields stay omitted.
func TestNodeWhereInputBranchOmitsLeafFields(t *testing.T) {
	eq := json.RawMessage(`"x"`)
	tree := NodeWhereInput{And: []*NodeWhereInput{{Path: []string{"a"}, Eq: &eq}}}
	b, _ := json.Marshal(tree)
	if got := string(b); got != `{"and":[{"path":["a"],"eq":"x"}]}` {
		t.Errorf("branch should carry only and[], got %s", got)
	}
}

// exists:false is a real predicate (match nodes lacking the path), so a non-nil
// false pointer must survive — omitempty on a *bool only drops nil, not false.
func TestNodeWhereInputExistsFalseSurvives(t *testing.T) {
	f := false
	b, _ := json.Marshal(NodeWhereInput{Path: []string{"x"}, Exists: &f})
	if got := string(b); got != `{"path":["x"],"exists":false}` {
		t.Errorf("exists:false must reach the wire, got %s", got)
	}
}

// NodePropertySort keeps its required path and omits unset field/as/direction.
func TestNodePropertySortOmitsUnset(t *testing.T) {
	dir := SortDirectionDesc
	as := NodeWhereCastNumber
	b, _ := json.Marshal(NodePropertySort{Path: []string{"rank"}, As: &as, Direction: &dir})
	if got := string(b); got != `{"path":["rank"],"as":"number","direction":"desc"}` {
		t.Errorf("sort should carry path+as+direction only, got %s", got)
	}
}
