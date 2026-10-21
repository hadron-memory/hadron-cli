package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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

// The tree importer's --on-conflict skip resolves a colliding node through the
// same listing scan: a failed later page must fail the import, not leave the
// node "unresolved" and carry on (cli#967, Gene's P2-2).
func TestNodeImportTreeSkipFailsWhenTheExistingNodeScanFails(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "docs")
	writeTree(t, dir)
	findCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string          `json:"operationName"`
			Variables     json.RawMessage `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		switch body.OperationName {
		case "CreateNode":
			var v struct {
				Input createNodeInput `json:"input"`
			}
			_ = json.Unmarshal(body.Variables, &v)
			if v.Input.Loc == "docs:how-to:setup" {
				_, _ = w.Write([]byte(`{"errors":[{"message":"exists","extensions":{"code":"NodeLocConflictError"}}]}`))
				return
			}
			fmt.Fprintf(w, `{"data":{"createNode":{"id":"nid:%s","memoryId":"mem1","loc":%q,"name":%q,"nodeType":null,"tags":[],"seq":null,"isRunnable":null,"updatedAt":"2026-01-01T00:00:00Z"}}}`,
				v.Input.Loc, v.Input.Loc, v.Input.Name)
		case "FindNodes":
			findCalls++
			if findCalls == 2 {
				_, _ = w.Write([]byte(`{"errors":[{"message":"boom","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`))
				return
			}
			_, _ = w.Write([]byte(probePage(probeRows(20), 0)))
		default:
			t.Errorf("unexpected op %q", body.OperationName)
		}
	}))
	t.Cleanup(srv.Close)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "import", "-r", dir, "-m", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "--on-conflict", "skip", "--json", "--server", srv.URL})
	if err := root.Execute(); err == nil {
		t.Fatalf("a failed existing-node scan must fail the import, got success: %.300s", out.String())
	}
}
