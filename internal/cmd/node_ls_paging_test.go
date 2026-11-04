package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func lsNodeRows() []string {
	rows := make([]string, 45)
	for i := range rows {
		rows[i] = fmt.Sprintf(`{"score":null,"node":{"id":"n%d","memoryId":"mem1","loc":"c:%03d","name":"N%d","nodeType":"info","tags":[],"seq":%d,"updatedAt":"2026-06-11T00:00:00Z"}}`, i, i, i, i+1)
	}
	return rows
}

// findNodesNullTotal renders a page the way the live server does: total NULL.
func findNodesNullTotal(page []string, _ int) string {
	return `{"data":{"findNodes":{"total":null,"degraded":null,"reason":null,"hits":[` + strings.Join(page, ",") + `]}}}`
}

func runNodeLs(t *testing.T, url string, args ...string) (locs []string, err error) {
	t.Helper()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"node", "ls", "--memory", "acme.com::kb", "--json", "--server", url}, args...))
	err = root.Execute()
	if err == nil {
		var rows []struct {
			Loc string `json:"loc"`
		}
		if jerr := json.Unmarshal([]byte(out.String()), &rows); jerr != nil {
			t.Fatalf("not a JSON array: %v\n%s", jerr, out.String())
		}
		for _, r := range rows {
			locs = append(locs, r.Loc)
		}
	}
	return locs, err
}

// cli#967: with no --limit/--offset `node ls` lists everything, even when the
// server clamps every page well below the size asked for and reports no total.
func TestNodeLsDrainsPastAClampedPage(t *testing.T) {
	gql, rec := pagedGraphQL(t, map[string]*pagedSource{
		"FindNodes": {rows: lsNodeRows(), serve: 20, wrap: findNodesNullTotal},
	}, nil)
	locs, err := runNodeLs(t, gql.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(locs) != 45 {
		t.Fatalf("want 45 nodes, got %d (silent truncation)", len(locs))
	}
	for i, l := range locs {
		if l != fmt.Sprintf("c:%03d", i) {
			t.Fatalf("row %d is %s: order lost or duplicated", i, l)
		}
	}
	if n := len(rec["FindNodes"].Calls); n != 4 {
		t.Errorf("want 4 requests (20+20+5+empty, total unknown), got %d", n)
	}
}

func TestNodeLsExplicitLimitOrOffsetIsOneRequest(t *testing.T) {
	for _, args := range [][]string{{"--limit", "5"}, {"--offset", "3"}, {"--limit", "5", "--offset", "2"}, {"--offset", "0"}, {"--limit", "0"}, {"--offset=0", "--limit=0"}} {
		gql, rec := pagedGraphQL(t, map[string]*pagedSource{
			"FindNodes": {rows: lsNodeRows(), serve: 20, wrap: findNodesNullTotal},
		}, nil)
		locs, err := runNodeLs(t, gql.URL, args...)
		if err != nil {
			t.Fatal(err)
		}
		if len(rec["FindNodes"].Calls) != 1 {
			t.Errorf("%v: want one request, got %d", args, len(rec["FindNodes"].Calls))
		}
		if len(locs) == 0 || len(locs) > 20 {
			t.Errorf("%v: unexpected page of %d (an explicit flag, even 0, is one page)", args, len(locs))
		}
	}
}

func TestNodeLsLaterPageErrorFailsTheListing(t *testing.T) {
	gql, _ := pagedGraphQL(t, map[string]*pagedSource{
		"FindNodes": {rows: lsNodeRows(), serve: 20, wrap: findNodesNullTotal, failAtCall: 2},
	}, nil)
	locs, err := runNodeLs(t, gql.URL)
	if err == nil {
		t.Fatalf("a failed page 2 must not read as a complete list (got %d nodes)", len(locs))
	}
}

// --seq-gt keeps draining under its own offsets and applies the filter after.
func TestNodeLsSeqGtDrainsPastAClampedPage(t *testing.T) {
	gql, _ := pagedGraphQL(t, map[string]*pagedSource{
		"FindNodes": {rows: lsNodeRows(), serve: 20, wrap: findNodesNullTotal},
	}, nil)
	locs, err := runNodeLs(t, gql.URL, "--seq-gt", "30")
	if err != nil {
		t.Fatal(err)
	}
	if len(locs) != 15 {
		t.Fatalf("want 15 nodes with seq > 30, got %d", len(locs))
	}
}
