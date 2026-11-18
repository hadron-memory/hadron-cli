//go:build !windows

package team

// cli#2342 — fixture-runner sturdiness. Two red pins, one per gate item:
//
//  1. Named arming: the recheck variants must arm by PURPOSE, not by ordinal
//     call count, so one orca call inserted anywhere before the sweep cannot
//     silently re-point every variant. The pin inserts a `terminal list`
//     (an operator listing terminals while the sweep is idle) before a sweep
//     whose recheck is armed to fail, and requires the same receipt the
//     un-inserted TestHealth2027FailingRecheckListDeclinesAndReportsIt gets.
//  2. A safe default orca runner under test: a title-pass test that forgets
//     the stubbing must not run a real `orca` from PATH — the default runner
//     answers an inert, record-only world.

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
)

// Item 1: the inserted list must not re-point the armed recheck variant. On
// an ordinal fake, the inserted call becomes list #1, the sweep's own first
// listing lands on #3 and answers the failing recheck: the sweep then sees
// no terminals at all, plans no fix, and the receipt loses the
// close-recheck-failed skip this pin requires.
func TestHealth2027InsertedOrcaCallDoesNotRepointRecheckArming2342(t *testing.T) {
	w := health2027Setup(t)
	w.setTerminals(t, []health2027Term{
		{Handle: "term_agent", Tab: "tab-agent", Screen: []string{"✻ Compiling…", "  go build ./..."}},
		{Handle: "term_bare", Tab: "tab-bare", Screen: []string{"orca@orca1:~$"}},
	})
	w.failRecheckList(t)
	// One orca call inserted before the sweep runs.
	out, err := exec.Command(filepath.Join(w.dir, "fake-orca"), "terminal", "list", "--json").Output()
	if err != nil {
		t.Fatalf("the inserted call failed: %v", err)
	}
	if !strings.Contains(string(out), "term_bare") {
		t.Fatalf("the inserted call saw the wrong world: %s", out)
	}
	dto, _, err := w.run(t, false, "--dry-run=false")
	if err != nil {
		t.Fatalf("sweep failed: %v", err)
	}
	if fix := health2027Find(dto.Fixes, func(f health2027Fix) bool { return f.Action == "close-tab" }); fix != nil {
		t.Fatalf("an unverifiable tab was acted on: %+v", dto.Fixes)
	}
	skip := health2027Find(dto.Skips, func(s health2027Skip) bool { return s.Code == "close-recheck-failed" })
	if skip == nil || skip.Worker != "w1" {
		t.Fatalf("the recheck did not decline at the close (the variant was re-pointed): %+v", dto.Skips)
	}
	if _, closes := w.records(); closes != "" {
		t.Fatalf("an unverifiable tab was closed: %q", closes)
	}
}

// Item 2: with no stubbing at all, the default runner under test must answer
// an inert world — the title pass completes, renames nothing, and no real
// `orca` is exec'd from PATH. On an exec'ing default this fails: with no orca
// installed the pass errors out, and with one installed it reaches a real
// binary.
func TestHealthTitlesDefaultOrcaRunnerIsInert2342(t *testing.T) {
	oldBound := healthBoundWorker
	healthBoundWorker = func(string) string { return "w1" }
	t.Cleanup(func() { healthBoundWorker = oldBound })

	renames, err := healthTitles(context.Background(), &hostpoller.Config{}, map[string]string{"w1": "Iris"}, false)
	if err != nil {
		t.Fatalf("the default runner must be inert, not fail the pass: %v", err)
	}
	if len(renames) != 0 {
		t.Fatalf("an inert runner must list nothing to rename: %+v", renames)
	}
}
