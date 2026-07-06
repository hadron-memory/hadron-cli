package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const searchReplaceDryJSON = `{"data":{"searchReplaceInNodes":{
	"nodesScanned":2,"nodesChanged":1,"totalReplacements":3,"dryRun":true,
	"results":[{"nodeId":"n1","loc":"findings:flaky-ci","memoryId":"mem1","replacements":3,
		"fields":[{"field":"content","matches":2},{"field":"tags","matches":1}]}]}}}`

const searchReplaceRealJSON = `{"data":{"searchReplaceInNodes":{
	"nodesScanned":2,"nodesChanged":1,"totalReplacements":3,"dryRun":false,
	"results":[{"nodeId":"n1","loc":"findings:flaky-ci","memoryId":"mem1","replacements":3,
		"fields":[{"field":"content","matches":2},{"field":"tags","matches":1}]}]}}}`

// A wide dry-run preview: many nodes would change. Used to exercise the
// --max-nodes ceiling and the --yes count-before-write signal (#126).
const searchReplaceWideDryJSON = `{"data":{"searchReplaceInNodes":{
	"nodesScanned":50,"nodesChanged":42,"totalReplacements":99,"dryRun":true,
	"results":[]}}}`

// A zero-match preview (server echoes dryRun=true for the probe).
const searchReplaceZeroDryJSON = `{"data":{"searchReplaceInNodes":{
	"nodesScanned":5,"nodesChanged":0,"totalReplacements":0,"dryRun":true,
	"results":[]}}}`

func TestReplaceDryRun(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"SearchReplaceInNodes": searchReplaceDryJSON,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{
		"replace", "text", "cat", "dog",
		"-m", "acme.com::kb", "--field", "content", "--field", "tags",
		"--dry-run", "--server", gql.URL,
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	s := out.String()
	if !strings.Contains(s, "Would replace 3 occurrence(s)") || !strings.Contains(s, "dry run") {
		t.Errorf("unexpected dry-run header: %s", s)
	}
	if !strings.Contains(s, "content×2") {
		t.Errorf("expected per-field breakdown in output: %s", s)
	}

	var vars struct {
		Input struct {
			OldText   string   `json:"oldText"`
			NewText   string   `json:"newText"`
			Fields    []string `json:"fields"`
			MemoryIds []string `json:"memoryIds"`
			DryRun    *bool    `json:"dryRun"`
		} `json:"input"`
	}
	if err := json.Unmarshal(captured["SearchReplaceInNodes"], &vars); err != nil {
		t.Fatalf("unmarshal vars: %v", err)
	}
	if vars.Input.OldText != "cat" || vars.Input.NewText != "dog" {
		t.Errorf("positional old/new not forwarded: %+v", vars.Input)
	}
	if len(vars.Input.Fields) != 2 || vars.Input.Fields[0] != "content" || vars.Input.Fields[1] != "tags" {
		t.Errorf("--field should map to fields enum: %v", vars.Input.Fields)
	}
	if len(vars.Input.MemoryIds) != 1 || vars.Input.MemoryIds[0] != "acme.com::kb" {
		t.Errorf("-m should map to memoryIds: %v", vars.Input.MemoryIds)
	}
	if vars.Input.DryRun == nil || !*vars.Input.DryRun {
		t.Errorf("--dry-run should send dryRun=true: %v", vars.Input.DryRun)
	}
}

// #88: --reason forwards to the input so the edit's rationale lands in version
// history. Uses --dry-run to avoid the confirm prompt; reason is on the input
// either way.
func TestReplaceForwardsReason(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"SearchReplaceInNodes": searchReplaceDryJSON,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{
		"replace", "text", "cat", "dog",
		"-m", "acme.com::kb", "--field", "content",
		"--reason", "rename per spec", "--dry-run", "--server", gql.URL,
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Input struct {
			Reason string `json:"reason"`
		} `json:"input"`
	}
	if err := json.Unmarshal(captured["SearchReplaceInNodes"], &vars); err != nil {
		t.Fatalf("unmarshal vars: %v", err)
	}
	if vars.Input.Reason != "rename per spec" {
		t.Errorf("--reason should forward to input.reason, got %q", vars.Input.Reason)
	}
}

// A real write with --yes skips the preview/confirm and sends dryRun=false.
func TestReplaceWithYesWrites(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"SearchReplaceInNodes": searchReplaceRealJSON,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{
		"replace", "text", "cat", "dog",
		"-m", "acme.com::kb", "--field", "content", "--yes", "--server", gql.URL,
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.HasPrefix(out.String(), "Replaced 3 occurrence(s)") {
		t.Errorf("expected a real-write header, got: %s", out.String())
	}
	var vars struct {
		Input struct {
			DryRun *bool `json:"dryRun"`
		} `json:"input"`
	}
	_ = json.Unmarshal(captured["SearchReplaceInNodes"], &vars)
	if vars.Input.DryRun == nil || *vars.Input.DryRun {
		t.Errorf("--yes write should send dryRun=false: %v", vars.Input.DryRun)
	}
}

// #126: even with --yes, the affected count is surfaced on stderr before the
// write, so a whole-memory blast radius is never invisible.
func TestReplaceWithYesShowsCountOnStderr(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"SearchReplaceInNodes": searchReplaceRealJSON,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{
		"replace", "text", "cat", "dog",
		"-m", "acme.com::kb", "--field", "content", "--yes", "--server", gql.URL,
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if errStr := f.IOStreams.ErrOut.(*strings.Builder).String(); !strings.Contains(errStr, "Replacing 3 occurrence(s) across 1 node(s) (--yes)") {
		t.Errorf("expected a pre-write count on stderr, got: %q", errStr)
	}
}

// #126: --max-nodes refuses a write whose preview exceeds the ceiling, BEFORE
// the real (dryRun=false) write is issued.
func TestReplaceMaxNodesRefusesWideWrite(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"SearchReplaceInNodes": searchReplaceWideDryJSON,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{
		"replace", "text", "cat", "dog",
		"-m", "acme.com::kb", "--field", "content", "--yes", "--max-nodes", "10", "--server", gql.URL,
	})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "max-nodes") {
		t.Fatalf("expected a --max-nodes refusal, got: %v", err)
	}
	// The only call made must be the dry-run preview — no real write.
	var vars struct {
		Input struct {
			DryRun *bool `json:"dryRun"`
		} `json:"input"`
	}
	if err := json.Unmarshal(captured["SearchReplaceInNodes"], &vars); err != nil {
		t.Fatalf("unmarshal vars: %v", err)
	}
	if vars.Input.DryRun == nil || !*vars.Input.DryRun {
		t.Errorf("only the dry-run preview should have run, last call dryRun=%v", vars.Input.DryRun)
	}
}

// #126 review: prove the --yes path previews (dryRun=true) THEN writes
// (dryRun=false), in that order. captureGraphQL keeps only the last call, so a
// recording handler is used here to assert the full two-call sequence.
func TestReplaceWithYesPreviewsThenWrites(t *testing.T) {
	var dryRuns []bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Variables struct {
				Input struct {
					DryRun *bool `json:"dryRun"`
				} `json:"input"`
			} `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		dryRuns = append(dryRuns, body.Variables.Input.DryRun != nil && *body.Variables.Input.DryRun)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(searchReplaceRealJSON))
	}))
	defer srv.Close()

	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{
		"replace", "text", "cat", "dog",
		"-m", "acme.com::kb", "--field", "content", "--yes", "--server", srv.URL,
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(dryRuns) != 2 || dryRuns[0] != true || dryRuns[1] != false {
		t.Errorf("--yes must preview then write; got dryRun sequence %v, want [true false]", dryRuns)
	}
}

// #126 review: a real --yes run that matches nothing must report dryRun=false
// (a no-op real run), not the preview's dryRun=true, so --json stays honest.
func TestReplaceYesZeroMatchesReportsRealRun(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"SearchReplaceInNodes": searchReplaceZeroDryJSON,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{
		"replace", "text", "cat", "dog",
		"-m", "acme.com::kb", "--field", "content", "--yes", "--json", "--server", gql.URL,
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var dto struct {
		DryRun bool `json:"dryRun"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, out.String())
	}
	if dto.DryRun {
		t.Errorf("a real --yes run with zero matches must report dryRun=false, got %s", out.String())
	}
}

// #126 review: a negative --max-nodes is a usage error (fires before any
// network round-trip), not a silently-disabled ceiling.
func TestReplaceRejectsNegativeMaxNodes(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{
		"replace", "text", "cat", "dog",
		"-m", "acme.com::kb", "--field", "content", "--yes", "--max-nodes", "-1", "--server", "http://127.0.0.1:1",
	})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "max-nodes") {
		t.Fatalf("expected a negative --max-nodes usage error, got: %v", err)
	}
}

// A real write without --yes in non-interactive mode is refused before any
// GraphQL call (agents must pass --yes or --dry-run).
func TestReplaceRefusesWriteWithoutYesNonInteractive(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{
		"replace", "text", "cat", "dog",
		"-m", "acme.com::kb", "--field", "content", "--server", "http://127.0.0.1:1",
	})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Errorf("expected a non-interactive --yes refusal, got: %v", err)
	}
}

func TestReplacePrefixRequiresMemory(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{
		"replace", "text", "x", "y",
		"--node", "acme.com::kb:findings:flaky-ci", "--prefix", "findings:",
		"--field", "content", "--server", "http://127.0.0.1:1",
	})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "prefix") {
		t.Errorf("expected a --prefix-requires-memory usage error, got: %v", err)
	}
}

func TestReplaceNeedsSelection(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{
		"replace", "text", "x", "y", "--field", "content", "--server", "http://127.0.0.1:1",
	})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "--node or --memory") {
		t.Errorf("expected a selection-required usage error, got: %v", err)
	}
}

func TestReplaceRejectsUnknownField(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{
		"replace", "text", "x", "y", "-m", "acme.com::kb", "--field", "bogus", "--server", "http://127.0.0.1:1",
	})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "unknown --field") {
		t.Errorf("expected an unknown-field usage error, got: %v", err)
	}
}
