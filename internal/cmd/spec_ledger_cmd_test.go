package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// `hadron spec ledger` (cli#662) end to end against a fake server. The grammar
// itself is pinned in internal/cmd/spec over Vera's fixtures; these pin the
// wiring: what is read from the server, what is read from the file, the exit
// codes and the output contract.

const ledgerRegistry = `# Decisions

## D-2026-10-01-001: no disposition
- **Status:** Decided (active)

## D-2026-10-01-002: cites a live spec
- **Status:** Decided (active)
- **Corpus:** Codified — ` + "`cor:spc:020:01`" + `

## D-2026-10-01-003: cites a replaced spec
- **Status:** Decided (active)
- **Corpus:** Codified — ` + "`cor:spc:020:02`" + `

## D-2026-10-01-004: queue
- **Status:** Decided (active)
- **Corpus:** Pending codification — drafts needed
`

func ledgerNode(loc, tags, content, abstract string) string {
	return fmt.Sprintf(`{"id":"id-%s","memoryId":"mem1","loc":%q,"name":%q,"alias":null,"nodeType":"info",`+
		`"objectType":null,"isRunnable":false,"description":null,"abstract":%s,"abstractOriginHash":null,`+
		`"tags":%s,"seq":null,"data":null,"properties":null,"content":%s,`+
		`"createdAt":"2026-08-05T00:00:00Z","updatedAt":"2026-08-05T00:00:00Z","outgoingEdges":[],"incomingEdges":[]}`,
		loc, loc, loc+" — T", jsonOrNull(abstract), tags, jsonOrNull(content))
}

func jsonOrNull(s string) string {
	if s == "" {
		return "null"
	}
	b, _ := json.Marshal(s)
	return string(b)
}

func ledgerRun(t *testing.T, registry string, listed []string, batch string, args ...string) (string, int) {
	t.Helper()
	dir := t.TempDir()
	reg := filepath.Join(dir, "decisions.md")
	if err := os.WriteFile(reg, []byte(registry), 0o644); err != nil {
		t.Fatal(err)
	}
	gql := fakeGraphQL(t, map[string]string{
		"FindNodes": `{"data":{"nodes":[` + strings.Join(listed, ",") + `]}}`,
		"NodeBatch": batch,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"spec", "ledger", "-m", specMem, "--registry", reg, "--server", gql.URL}, args...))
	err := root.Execute()
	return out.String(), exitCodeFor(err)
}

func ledgerCorpus() ([]string, string) {
	listed := []string{
		specNodeList("cor:spc:020:01", `["spec"]`),
		specNodeList("cor:spc:020:02", `["spec","superseded"]`),
		specNodeList("cor:agt:010:01", `["spec"]`),
	}
	nodes := []string{
		ledgerNode("cor:spc:020:01", `["spec"]`, "rule text", ""),
		ledgerNode("cor:spc:020:02", `["spec","superseded"]`, "old", ""),
		// A body and an abstract that each cite an unregistered decision.
		ledgerNode("cor:agt:010:01", `["spec"]`, "line one\nsee D-2030-01-01-001 and D-2030-01-01-002…003", "abstract cites D-2030-01-01-001"),
	}
	return listed, citationBatch(nodes, "")
}

func TestSpecLedgerReportsAllFourCategories(t *testing.T) {
	listed, batch := ledgerCorpus()
	out, code := ledgerRun(t, ledgerRegistry, listed, batch, "--json")
	if code != exitcode.Conflict {
		t.Fatalf("exit = %d, want %d\n%s", code, exitcode.Conflict, out)
	}
	var got []struct {
		Category  int      `json:"category"`
		Rule      string   `json:"rule"`
		ID        string   `json:"id"`
		Citation  string   `json:"citation"`
		Locations []string `json:"locations"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	seen := map[string]bool{}
	for _, f := range got {
		seen[fmt.Sprintf("%d %s %s %s", f.Category, f.Rule, f.ID, f.Citation)] = true
	}
	for _, want := range []string{
		"1 missing-disposition D-2026-10-01-001 ",
		"2 superseded D-2026-10-01-003 cor:spc:020:02",
		"3 unregistered-id D-2030-01-01-001 ",
		"3 unregistered-id D-2030-01-01-002 ",
		"3 unregistered-id D-2030-01-01-003 ",
		"4 pending D-2026-10-01-004 ",
	} {
		if !seen[want] {
			t.Errorf("missing %q in %v", want, seen)
		}
	}
	if len(got) != 6 {
		t.Errorf("want exactly 6 findings, got %d: %v", len(got), seen)
	}
	// The ID cited in both a body and an abstract lists both places, and the
	// body location carries a line number.
	for _, f := range got {
		if f.ID == "D-2030-01-01-001" {
			if strings.Join(f.Locations, ",") != "cor:agt:010:01:abstract:1,cor:agt:010:01:content:2" {
				t.Errorf("locations = %v", f.Locations)
			}
		}
	}
}

// Pending entries are a work queue, not a failure: with nothing else wrong the
// command exits 0 and still lists them.
func TestSpecLedgerPendingAloneExitsZero(t *testing.T) {
	reg := "## D-2026-10-01-004: queue\n- **Status:** Decided (active)\n- **Corpus:** Pending codification — drafts needed\n"
	listed := []string{specNodeList("cor:spc:020:01", `["spec"]`)}
	batch := citationBatch([]string{ledgerNode("cor:spc:020:01", `["spec"]`, "x", "")}, "")
	out, code := ledgerRun(t, reg, listed, batch)
	if code != 0 {
		t.Fatalf("exit = %d\n%s", code, out)
	}
	if !strings.Contains(out, "D-2026-10-01-004") || !strings.Contains(out, "Pending codification") {
		t.Errorf("queue not listed:\n%s", out)
	}
}

func TestSpecLedgerCleanSaysSo(t *testing.T) {
	reg := "## D-2026-10-01-002: ok\n- **Status:** Decided (active)\n- **Corpus:** Codified — `cor:spc:020:01`\n"
	listed := []string{specNodeList("cor:spc:020:01", `["spec"]`)}
	batch := citationBatch([]string{ledgerNode("cor:spc:020:01", `["spec"]`, "x", "")}, "")
	out, code := ledgerRun(t, reg, listed, batch)
	if code != 0 || !strings.Contains(out, "✓") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	jsonOut, _ := ledgerRun(t, reg, listed, batch, "--json")
	if strings.TrimSpace(jsonOut) != "[]" {
		t.Errorf("clean --json must be [] not null: %q", jsonOut)
	}
}

// A listed spec nodeBatch refuses is a gap in category 3 and is surfaced, not
// dropped — the run must not read cleaner than it is.
func TestSpecLedgerUnreadableSpecIsReported(t *testing.T) {
	reg := "## D-2026-10-01-002: ok\n- **Status:** Decided (active)\n- **Corpus:** Codified — `cor:spc:020:01`\n"
	listed := []string{specNodeList("cor:spc:020:01", `["spec"]`), specNodeList("cor:spc:020:09", `["spec"]`)}
	batch := citationBatch([]string{ledgerNode("cor:spc:020:01", `["spec"]`, "x", "")}, `"id-cor:spc:020:09"`)
	out, code := ledgerRun(t, reg, listed, batch, "--json")
	if code != exitcode.Conflict || !strings.Contains(out, `"unreadable-spec"`) || !strings.Contains(out, "cor:spc:020:09") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

func TestSpecLedgerUsageAndEmptyCorpus(t *testing.T) {
	// No such registry file: a usage error that names the flag.
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "ledger", "-m", specMem, "--registry", filepath.Join(t.TempDir(), "nope.md"), "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if exitCodeFor(err) != exitcode.Usage || !strings.Contains(err.Error(), "--registry") {
		t.Errorf("missing registry: code %d err %v", exitCodeFor(err), err)
	}
	// A corpus with no specs would make every citation "unresolved"; it is a
	// wrong memory, not a finding.
	_, code := ledgerRun(t, ledgerRegistry, nil, citationBatch(nil, ""))
	if code != exitcode.NotFound {
		t.Errorf("empty corpus: exit %d, want %d", code, exitcode.NotFound)
	}
}
