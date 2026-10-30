package cmd

import (
	"reflect"
	"strconv"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1238: `node recovery graph --edge <id>` (repeatable) restricts the result
// to those edges by sending recoveryEdges(edgeIds:) (hadron-server#2818).

const c1238Empty = `{"data":{"recoveryEdges":{"total":0,"items":[]}}}`

func TestRecoveryGraphEdgeFlagSendsExactlyTheIDs(t *testing.T) {
	srv, calls := recoveryServer(t, func(c recCall) string { return c1238Empty })
	for name, tc := range map[string]struct {
		args []string
		want []any
	}{
		"two":       {[]string{"--edge", "a", "--edge", "b"}, []any{"a", "b"}},
		"one":       {[]string{"--edge", "a"}, []any{"a"}},
		"with page": {[]string{"--edge", "a", "--limit", "5"}, []any{"a"}},
	} {
		t.Run(name, func(t *testing.T) {
			before := len(calls())
			args := append([]string{"node", "recovery", "graph", "-m", recMemory, "--json"}, tc.args...)
			if _, _, _, err := recRun(t, srv, args...); err != nil {
				t.Fatal(err)
			}
			got := calls()[before:]
			if len(got) != 1 {
				t.Fatalf("requests = %d, want 1", len(got))
			}
			if !reflect.DeepEqual(got[0].vars["edgeIds"], tc.want) {
				t.Errorf("edgeIds = %#v, want %#v", got[0].vars["edgeIds"], tc.want)
			}
		})
	}
}

func TestRecoveryGraphWithoutEdgeFlagSendsNoEdgeIdsKey(t *testing.T) {
	srv, calls := recoveryServer(t, func(c recCall) string { return c1238Empty })
	if _, _, _, err := recRun(t, srv, "node", "recovery", "graph", "-m", recMemory, "--json"); err != nil {
		t.Fatal(err)
	}
	vars := recOnly(t, calls(), "RecoveryEdges")[0].vars
	if _, has := vars["edgeIds"]; has {
		t.Errorf("edgeIds must be omitted without --edge (the server refuses an empty list): %v", vars)
	}
}

func TestRecoveryGraphEdgeLimitIs200AndRefusalsSendNothing(t *testing.T) {
	srv, calls := recoveryServer(t, func(c recCall) string { return c1238Empty })
	ids := func(n int) []string {
		var a []string
		for i := 0; i < n; i++ {
			a = append(a, "--edge", "e"+strconv.Itoa(i))
		}
		return a
	}
	base := []string{"node", "recovery", "graph", "-m", recMemory, "--json"}
	if _, _, code, err := recRun(t, srv, append(base, ids(201)...)...); code != exitcode.Usage || err == nil {
		t.Fatalf("201 --edge: code %d err %v, want usage", code, err)
	}
	if n := len(calls()); n != 0 {
		t.Fatalf("a refused call sent %d requests", n)
	}
	for _, bad := range [][]string{{"--edge", ""}, {"--edge", "  "}} {
		if _, _, code, _ := recRun(t, srv, append(base, bad...)...); code != exitcode.Usage {
			t.Errorf("%q: want usage, got %d", bad, code)
		}
	}
	if n := len(calls()); n != 0 {
		t.Fatalf("empty ids sent %d requests", n)
	}
	if _, _, _, err := recRun(t, srv, append(base, ids(200)...)...); err != nil {
		t.Fatalf("200 --edge must be accepted: %v", err)
	}
	if got := recOnly(t, calls(), "RecoveryEdges")[0].vars["edgeIds"].([]any); len(got) != 200 {
		t.Errorf("sent %d ids, want 200", len(got))
	}
}
