package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func revisionsResp(n int) string {
	rows := make([]string, n)
	for i := range rows {
		rows[i] = fmt.Sprintf(`{"id":"v%d","nodeId":"n1","loc":"findings:x","name":"X","description":null,"tags":[],"createdAt":"2026-06-11T00:00:00Z","editedBy":null,"editedByInfo":null,"editedByUser":null,"revLabel":null,"changes":[]}`, i)
	}
	return `{"data":{"nodeRevisions":[` + strings.Join(rows, ",") + `]}}`
}

func runRevisionList(t *testing.T, resp string, args ...string) (limit *int, stdout, stderr string) {
	t.Helper()
	gql, captured := captureGraphQL(t, map[string]string{"NodeRevisions": resp})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"node", "revision", "list", "n1", "--server", gql.URL}, args...))
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Limit *int `json:"limit"`
	}
	_ = json.Unmarshal(captured["NodeRevisions"], &vars)
	return vars.Limit, out.String(), f.IOStreams.ErrOut.(*strings.Builder).String()
}

// A full default page cannot prove the history ended: say so, on stderr.
func TestNodeRevisionListFullDefaultPageSaysMoreMayExist(t *testing.T) {
	limit, _, errOut := runRevisionList(t, revisionsResp(20))
	if limit == nil || *limit != 20 {
		t.Fatalf("default limit sent as %v, want 20", limit)
	}
	for _, want := range []string{"first 20", "more may exist"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr missing %q: %q", want, errOut)
		}
	}
}

func TestNodeRevisionListShortPageHasNoCue(t *testing.T) {
	_, _, errOut := runRevisionList(t, revisionsResp(19))
	if errOut != "" {
		t.Errorf("19 of 20 rows is the whole history; got a cue %q", errOut)
	}
}

// An explicit --limit is the user's own page size: respected, and no cue.
func TestNodeRevisionListExplicitLimitIsRespectedWithoutCue(t *testing.T) {
	limit, _, errOut := runRevisionList(t, revisionsResp(50), "--limit", "50")
	if limit == nil || *limit != 50 {
		t.Fatalf("explicit limit sent as %v, want 50", limit)
	}
	if errOut != "" {
		t.Errorf("explicit --limit needs no cue, got %q", errOut)
	}
}

// The cue must not change the JSON shape: still a bare array, cue on stderr.
func TestNodeRevisionListJSONStaysABareArray(t *testing.T) {
	_, stdout, errOut := runRevisionList(t, revisionsResp(20), "--json")
	var rows []map[string]any
	if err := json.Unmarshal([]byte(stdout), &rows); err != nil || len(rows) != 20 {
		t.Fatalf("stdout must be a bare 20-element array: %v\n%.200s", err, stdout)
	}
	if !strings.Contains(errOut, "more may exist") {
		t.Errorf("cue must still reach stderr in --json mode, got %q", errOut)
	}
}
