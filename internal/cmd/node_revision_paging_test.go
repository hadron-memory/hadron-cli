package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// revisionsPageResp answers nodeRevisionsPage with n rows, the real total, and
// whether more follow (cli#1339).
func revisionsPageResp(n, total int, hasMore bool) string {
	rows := make([]string, n)
	for i := range rows {
		rows[i] = fmt.Sprintf(`{"id":"v%d","nodeId":"n1","loc":"findings:x","name":"X","description":null,"tags":[],"createdAt":"2026-06-11T00:00:00Z","editedBy":null,"editedByInfo":null,"editedByUser":null,"revLabel":null,"changes":[]}`, i)
	}
	return fmt.Sprintf(`{"data":{"nodeRevisionsPage":{"total":%d,"hasMore":%v,"items":[%s]}}}`, total, hasMore, strings.Join(rows, ","))
}

func runRevisionList(t *testing.T, resp string, args ...string) (vars map[string]any, stdout, stderr string) {
	t.Helper()
	gql, captured := captureGraphQL(t, map[string]string{"NodeRevisionsPage": resp})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"node", "revision", "list", "n1", "--server", gql.URL}, args...))
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	_ = json.Unmarshal(captured["NodeRevisionsPage"], &vars)
	return vars, out.String(), f.IOStreams.ErrOut.(*strings.Builder).String()
}

// cli#1339: the real total and the next offset, on stderr.
func TestNodeRevisionListShowsRealTotalAndNextOffset(t *testing.T) {
	vars, _, errOut := runRevisionList(t, revisionsPageResp(20, 57, true))
	if vars["limit"] != float64(20) {
		t.Fatalf("default limit sent as %v, want 20", vars["limit"])
	}
	if _, ok := vars["offset"]; !ok || vars["offset"] != float64(0) {
		// offset 0 is omitted (omitempty) or zero; either way it is the first page.
		if ok {
			t.Fatalf("offset sent as %v", vars["offset"])
		}
	}
	for _, want := range []string{"showing 1-20 of 57", "more: --offset 20"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr missing %q: %q", want, errOut)
		}
	}
}

func TestNodeRevisionListLastPageHasNoMoreCue(t *testing.T) {
	_, _, errOut := runRevisionList(t, revisionsPageResp(7, 27, false), "--offset", "20")
	if !strings.Contains(errOut, "showing 21-27 of 27") || strings.Contains(errOut, "more:") {
		t.Errorf("last page: %q", errOut)
	}
}

// A full page of exactly the total is the whole history: no "more" cue, which the
// unpaged read could never prove.
func TestNodeRevisionListFullPageThatEndsTheHistory(t *testing.T) {
	_, _, errOut := runRevisionList(t, revisionsPageResp(20, 20, false))
	if strings.Contains(errOut, "more:") {
		t.Errorf("20 of 20 is the whole history, got %q", errOut)
	}
}

func TestNodeRevisionListOffsetAndLimitReachTheWire(t *testing.T) {
	vars, _, _ := runRevisionList(t, revisionsPageResp(5, 30, true), "--limit", "5", "--offset", "10")
	if vars["limit"] != float64(5) || vars["offset"] != float64(10) {
		t.Fatalf("vars %v", vars)
	}
}

func TestNodeRevisionListLimitZeroCountsOnly(t *testing.T) {
	vars, stdout, errOut := runRevisionList(t, revisionsPageResp(0, 12, true), "--limit", "0", "--json")
	if l, ok := vars["limit"]; ok && l != float64(0) {
		t.Fatalf("limit %v", l)
	}
	if strings.TrimSpace(stdout) != "[]" || !strings.Contains(errOut, "total 12") {
		t.Errorf("count-only: stdout %q stderr %q", stdout, errOut)
	}
}

func TestNodeRevisionListRejectsNegativeOffset(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{"NodeRevisionsPage": revisionsPageResp(0, 0, false)})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "revision", "list", "n1", "--offset", "-1", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("expected a usage error")
	}
	if len(captured) != 0 {
		t.Error("a negative --offset must not reach the server")
	}
}

// The cue must not change the JSON shape: still a bare array, cue on stderr.
func TestNodeRevisionListJSONStaysABareArray(t *testing.T) {
	_, stdout, errOut := runRevisionList(t, revisionsPageResp(20, 40, true), "--json")
	var rows []map[string]any
	if err := json.Unmarshal([]byte(stdout), &rows); err != nil || len(rows) != 20 {
		t.Fatalf("stdout must be a bare 20-element array: %v\n%.200s", err, stdout)
	}
	if !strings.Contains(errOut, "more: --offset 20") {
		t.Errorf("cue must still reach stderr in --json mode, got %q", errOut)
	}
}
