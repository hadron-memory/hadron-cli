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

// citationBatchNode is one spec in the nodeBatch projection `spec citations`
// reads. The body is fixed — staleness is driven by originHash, not by the
// content itself. originHash "" means null (no staleness comparison possible);
// supersededBy "" means no retirement edge.
func citationBatchNode(loc, tags, originHash, supersededBy string) string {
	const content = "body"
	hash := "null"
	if originHash != "" {
		hash = fmt.Sprintf("%q", originHash)
	}
	edges := ""
	if supersededBy != "" {
		edges = fmt.Sprintf(`{"id":"e1","name":"superseded-by","loc":"l","description":null,"isRunnable":false,`+
			`"priority":0,"condition":null,"target":{"id":"t1","loc":%q,"memoryId":"mem1"}}`, supersededBy)
	}
	return fmt.Sprintf(`{"id":"id-%s","memoryId":"mem1","loc":%q,"name":%q,"alias":null,"nodeType":"info",`+
		`"objectType":null,"isRunnable":false,"description":null,"abstract":"An abstract.","abstractOriginHash":%s,`+
		`"tags":%s,"seq":null,"data":null,"properties":null,"content":%q,`+
		`"createdAt":"2026-08-05T00:00:00Z","updatedAt":"2026-08-05T00:00:00Z",`+
		`"outgoingEdges":[%s],"incomingEdges":[]}`,
		loc, loc, loc+" — T", hash, tags, content, edges)
}

func citationBatch(nodes []string, unavailable string) string {
	return `{"data":{"nodeBatch":{"truncated":false,"omitted":[],"unavailable":[` + unavailable + `],
		"nodes":[` + strings.Join(nodes, ",") + `]}}}`
}

// srcTree writes a source tree and returns its root.
func srcTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	return root
}

func TestSpecCitationsClean(t *testing.T) {
	src := srcTree(t, map[string]string{"src/a.ts": "// Spec: msg:010:02\nconst x = 1;\n"})
	gql := fakeGraphQL(t, map[string]string{
		"NodeBatch": citationBatch([]string{citationBatchNode("msg:010:02", `["spec"]`, "", "")}, ""),
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "citations", "-m", specMem, "--src", src, "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("a resolving citation should exit 0, got %v", err)
	}
	if !strings.Contains(out.String(), "1 citation(s) in 1 file(s) resolve") {
		t.Errorf("expected the checked-counts summary, got %q", out.String())
	}
}

// The failure that prompted the command: supersede retires a number and every
// pointer to it silently documents a replaced contract.
func TestSpecCitationsSupersededExits5(t *testing.T) {
	src := srcTree(t, map[string]string{"src/a.ts": "// Spec: msg:010:02 (retired)\n"})
	gql := fakeGraphQL(t, map[string]string{
		"NodeBatch": citationBatch([]string{
			citationBatchNode("msg:010:02", `["spec","superseded"]`, "", "msg:010:03"),
		}, ""),
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "citations", "-m", specMem, "--src", src, "--json", "--server", gql.URL})
	if got := exitcode.FromError(root.Execute()); got != exitcode.Conflict {
		t.Errorf("a superseded citation should exit 5, got %d", got)
	}

	var findings []citationFindingJSON
	if err := json.Unmarshal([]byte(out.String()), &findings); err != nil {
		t.Fatalf("--json must emit an array: %v (%q)", err, out.String())
	}
	if len(findings) != 1 {
		t.Fatalf("want one finding, got %+v", findings)
	}
	fnd := findings[0]
	if fnd.Rule != "superseded" || fnd.Severity != "error" {
		t.Errorf("unexpected finding: %+v", fnd)
	}
	if fnd.Replacement != "msg:010:03" {
		t.Errorf("the finding must carry the replacement, got %q", fnd.Replacement)
	}
	// file:line is what lets CI annotate the pointer rather than the spec.
	if !strings.HasSuffix(fnd.File, filepath.FromSlash("src/a.ts")) || fnd.Line != 1 {
		t.Errorf("finding must locate the occurrence, got %s:%d", fnd.File, fnd.Line)
	}
}

func TestSpecCitationsUnresolvedExits5(t *testing.T) {
	src := srcTree(t, map[string]string{"a.go": "// Spec: msg:010:99\n"})
	gql := fakeGraphQL(t, map[string]string{
		"NodeBatch": citationBatch(nil, `"`+specMem+`::msg:010:99"`),
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "citations", "-m", specMem, "--src", src, "--server", gql.URL})
	if got := exitcode.FromError(root.Execute()); got != exitcode.Conflict {
		t.Errorf("an unresolved citation should exit 5, got %d", got)
	}
	if s := out.String(); !strings.Contains(s, "unresolved") || !strings.Contains(s, "msg:010:99") {
		t.Errorf("expected the unresolved finding, got %q", s)
	}
}

// A ref the server neither returns nor lists as unavailable must not pass as
// healthy — the false-clean this check exists to prevent.
func TestSpecCitationsSilentlyMissingRefIsReported(t *testing.T) {
	src := srcTree(t, map[string]string{"a.go": "// Spec: msg:010:99\n"})
	gql := fakeGraphQL(t, map[string]string{
		"NodeBatch": citationBatch(nil, ""), // neither returned nor unavailable
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "citations", "-m", specMem, "--src", src, "--server", gql.URL})
	if got := exitcode.FromError(root.Execute()); got != exitcode.Conflict {
		t.Errorf("a silently-missing citation should still fail, got %d", got)
	}
}

// Staleness is OFF by default: 174 of 271 live specs trip it — and it measures
// "the body is not the version the abstract was written against", not "the abstract is
// wrong" (d = 0.01 at the rule tier, #352) — so default-on it would bury the two
// rules that name an actually-broken pointer.
func TestSpecCitationsStaleAbstractIsOptIn(t *testing.T) {
	src := srcTree(t, map[string]string{"a.go": "// Spec: msg:010:02\n"})
	responses := map[string]string{
		"NodeBatch": citationBatch([]string{
			citationBatchNode("msg:010:02", `["spec"]`, "deadbeef", ""),
		}, ""),
	}

	gqlOff := fakeGraphQL(t, responses)
	fOff, outOff := testFactory(t)
	rootOff := NewRootCmd(fOff)
	rootOff.SetArgs([]string{"spec", "citations", "-m", specMem, "--src", src, "--server", gqlOff.URL})
	if err := rootOff.Execute(); err != nil {
		t.Fatalf("without --stale-abstracts the citation is healthy, got %v", err)
	}
	if s := outOff.String(); strings.Contains(s, "stale-abstract") {
		t.Errorf("staleness must not be reported by default, got %q", s)
	}

	gql := fakeGraphQL(t, responses)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "citations", "-m", specMem, "--src", src, "--stale-abstracts", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("a warning alone should exit 0, got %v", err)
	}
	if !strings.Contains(out.String(), "stale-abstract") {
		t.Errorf("expected the stale-abstract warning, got %q", out.String())
	}

	gql2 := fakeGraphQL(t, responses)
	f2, _ := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"spec", "citations", "-m", specMem, "--src", src, "--stale-abstracts", "--strict", "--server", gql2.URL})
	if got := exitcode.FromError(root2.Execute()); got != exitcode.Conflict {
		t.Errorf("--strict should promote the warning to an error (exit 5), got %d", got)
	}
}

// A citation repeated across files costs ONE read and reports every occurrence.
func TestSpecCitationsBatchReadsEachCitationOnce(t *testing.T) {
	src := srcTree(t, map[string]string{
		"a.go":   "// Spec: msg:010:02\n",
		"b.go":   "\n\n// Spec: msg:010:02\n",
		"c/d.ts": "// Spec: msg:010:02 and msg:010:03\n",
	})
	gql, captured := captureGraphQL(t, map[string]string{
		"NodeBatch": citationBatch([]string{
			citationBatchNode("msg:010:02", `["spec","superseded"]`, "", "msg:010:04"),
			citationBatchNode("msg:010:03", `["spec"]`, "", ""),
		}, ""),
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "citations", "-m", specMem, "--src", src, "--json", "--server", gql.URL})
	_ = root.Execute()

	var vars struct {
		Refs []string `json:"refs"`
	}
	if err := json.Unmarshal(captured["NodeBatch"], &vars); err != nil {
		t.Fatalf("decoding NodeBatch vars: %v", err)
	}
	if len(vars.Refs) != 2 {
		t.Errorf("three occurrences of two citations must read 2 refs, got %v", vars.Refs)
	}

	var findings []struct {
		File string `json:"file"`
		Line int    `json:"line"`
	}
	if err := json.Unmarshal([]byte(out.String()), &findings); err != nil {
		t.Fatalf("--json must emit an array: %v", err)
	}
	if len(findings) != 3 {
		t.Errorf("every OCCURRENCE of the broken citation is a finding, got %+v", findings)
	}
}

// With nothing to resolve there is nothing to resolve it AGAINST: a repo with
// no pointers must not fail for want of -m, and must not open a connection.
// The server address here is dead, so a request would fail the test.
func TestSpecCitationsNoCitationsNeedsNoMemory(t *testing.T) {
	src := srcTree(t, map[string]string{"a.go": "const x = 1\n"})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "citations", "--src", src, "--server", "http://127.0.0.1:1"})
	if err := root.Execute(); err != nil {
		t.Fatalf("no citations must not require a memory, got %v", err)
	}
	if !strings.Contains(out.String(), "no spec citations found") {
		t.Errorf("expected the nothing-found line, got %q", out.String())
	}

	// --json still emits the array contract, not the prose line.
	f2, out2 := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"spec", "citations", "--src", src, "--json", "--server", "http://127.0.0.1:1"})
	if err := root2.Execute(); err != nil {
		t.Fatalf("--json with no citations should exit 0, got %v", err)
	}
	var findings []citationFindingJSON
	if err := json.Unmarshal([]byte(out2.String()), &findings); err != nil {
		t.Fatalf("--json must emit [] : %v (%q)", err, out2.String())
	}
	if len(findings) != 0 {
		t.Errorf("want an empty array, got %+v", findings)
	}
}

// citationFindingJSON is the decoded --json row.
type citationFindingJSON struct {
	File        string `json:"file"`
	Line        int    `json:"line"`
	Citation    string `json:"citation"`
	Rule        string `json:"rule"`
	Severity    string `json:"severity"`
	Replacement string `json:"replacement"`
}

// Scanning the wrong path must not look like a clean repo.
func TestSpecCitationsNoneFound(t *testing.T) {
	src := srcTree(t, map[string]string{"a.go": "const x = 1\n"})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "citations", "-m", specMem, "--src", src, "--server", "http://127.0.0.1:1"})
	if err := root.Execute(); err != nil {
		t.Fatalf("no citations is not a failure, got %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "no spec citations found") || strings.Contains(s, "✓") {
		t.Errorf("a nothing-scanned run must say so, not print a checkmark: %q", s)
	}
	if !strings.Contains(s, "--loose") {
		t.Errorf("the empty result should point at --loose, got %q", s)
	}
}

func TestSpecCitationsBadSrcIsUsageError(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "citations", "-m", specMem, "--src", filepath.Join(t.TempDir(), "missing"),
		"--server", "http://127.0.0.1:1"})
	if got := exitcode.FromError(root.Execute()); got != exitcode.Usage {
		t.Errorf("a nonexistent --src should be a usage error (2), got %d", got)
	}
}
