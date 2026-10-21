package cmd

import (
	"fmt"
	"strings"
	"testing"
)

func probeRows(n int) []string {
	rows := make([]string, n)
	for i := range rows {
		rows[i] = fmt.Sprintf(`{"score":null,"node":{"id":"p%d","memoryId":"m","loc":"findings:flaky-ci:%03d","name":"P","nodeType":"info","tags":[],"seq":%d,"updatedAt":"2026-06-11T00:00:00Z"}}`, i, i, i+1)
	}
	return rows
}

func probePage(page []string, _ int) string {
	return `{"data":{"findNodes":{"total":null,"degraded":null,"reason":null,"hits":[` + strings.Join(page, ",") + `]}}}`
}

// cli#967 (Gene's P2-2): a failed later page of the overwrite probe must fail
// the import — NOT read as "absent" and report `created` over a node the scan
// never reached.
func TestNodeImportDryRunFailsWhenTheOverwriteProbeFailsOnALaterPage(t *testing.T) {
	gql, rec := pagedGraphQL(t, map[string]*pagedSource{
		"FindNodes": {rows: probeRows(50), serve: 20, wrap: probePage, failAtCall: 2},
	}, nil)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "import", importFile(t, "n.md", governedMd("")), "-m", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"--dry-run", "--json", "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatalf("a failed probe page must fail the import, got success: %s", out.String())
	}
	if strings.Contains(out.String(), `"created"`) {
		t.Errorf("must not report created: %s", out.String())
	}
	if n := len(rec["FindNodes"].Calls); n != 2 {
		t.Errorf("want 2 probe requests (page 1, failing page 2), got %d", n)
	}
}

// Genuine absence, reached by walking every page, stays a plain create.
func TestNodeImportDryRunReportsCreatedWhenTheProbeReachesTheEnd(t *testing.T) {
	gql, _ := pagedGraphQL(t, map[string]*pagedSource{
		"FindNodes": {rows: probeRows(50), serve: 20, wrap: probePage},
	}, nil)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "import", importFile(t, "n.md", governedMd("")), "-m", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"--dry-run", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "created") {
		t.Errorf("absent after a full walk must read as created: %s", out.String())
	}
}
