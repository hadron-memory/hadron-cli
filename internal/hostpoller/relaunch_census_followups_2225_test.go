//go:build linux

package hostpoller

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// cli#2225 item 1. A real foreign exclusion happens, then the FINAL authority
// check refuses: the count must stay unpublished. Moving `*skipped = foreign`
// before the final ctx/authority validation must fail this test.
func TestCensusCountUnpublishedWhenFinalAuthorityRefuses2225(t *testing.T) {
	proc, work := t.TempDir(), t.TempDir()
	foreignDir := censusFixture2060(t, proc, work, 41)
	censusFixture2060(t, proc, work, 42)
	if err := os.Remove(filepath.Join(foreignDir, "cwd")); err != nil {
		t.Fatal(err)
	}
	uid := os.Geteuid() + 1000000
	if err := os.WriteFile(filepath.Join(foreignDir, "status"), []byte(fmt.Sprintf("Uid:\t%d\t%d\t%d\t%d\n", uid, uid, uid, uid)), 0600); err != nil {
		t.Fatal(err)
	}
	refusal := errors.New("authority lapsed before completion")

	// Control: with authority held throughout, the same fixture publishes 1.
	held := 0
	if got, err := strictRelaunchCensusCounted(context.Background(), proc, work, func(context.Context) error { return nil }, &held); err != nil || len(got) != 1 || held != 1 {
		t.Fatalf("control: agents=%+v count=%d err=%v", got, held, err)
	}

	calls := 0
	authority := func(context.Context) error {
		calls++
		if calls >= 2 { // the first call is the entry check; the second is the final one
			return refusal
		}
		return nil
	}
	count := 0
	got, err := strictRelaunchCensusCounted(context.Background(), proc, work, authority, &count)
	if !errors.Is(err, refusal) || got != nil {
		t.Fatalf("final authority refusal must refuse the census: agents=%+v err=%v", got, err)
	}
	if calls != 2 {
		t.Fatalf("authority consulted %d times, want the entry and the final check only", calls)
	}
	if count != 0 {
		t.Fatalf("a count escaped a census whose final authority was refused: %d", count)
	}
}

// cli#2225 item 2. The FIFO subprocess budget is stated once, as named
// constants with an explicit acceptance: one in-child wait, plus headroom for
// process start and scheduling on a loaded host, under a hard watchdog. A
// loosening needs this test to be edited with it.
func TestFIFOBudgetAcceptance2225(t *testing.T) {
	if fifoWait2197 != 2*time.Second || fifoChildBudget2197 != 6*time.Second || fifoWatchdog2197 != 10*time.Second {
		t.Fatalf("FIFO budget changed without changing its acceptance: wait=%s budget=%s watchdog=%s", fifoWait2197, fifoChildBudget2197, fifoWatchdog2197)
	}
	if fifoWait2197 >= fifoChildBudget2197 || fifoChildBudget2197 >= fifoWatchdog2197 {
		t.Fatal("a refusal must fit its budget, and the budget must fit under the watchdog")
	}
}

// cli#2225 item 3. The scenario switches act only in a child that was started
// with the child-mode guard. A stray switch in the parent environment must not
// change the ordinary fixture test.
func TestFIFOSwitchesIgnoredWithoutChildGuard2225(t *testing.T) {
	t.Setenv("HADRON_TEST_FIFO_STALL_2197", "1")
	t.Setenv("HADRON_TEST_FIFO_EARLY_REFUSAL_2197", "1")
	ctx, cancel := context.WithTimeout(context.Background(), fifoWatchdog2197)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCensusForeignSkipIdentityAndDisappearance2195$/^stable$", "-test.count=1")
	cmd.Env = os.Environ() // stray switches inherited, no child guard
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("a stray parent switch altered the ordinary fixture test: %v: %s", err, out)
	}
}

// A stray switch in the parent must not leak into a child that runs a
// different scenario: the child gets exactly one scenario.
func TestFIFOChildGetsExactlyOneScenario2225(t *testing.T) {
	t.Setenv("HADRON_TEST_FIFO_EARLY_REFUSAL_2197", "1") // stray; the scenario below is the stall
	out, err := runFIFOChild2197(t, "HADRON_TEST_FIFO_STALL_2197=1")
	if err == nil {
		t.Fatal("stall child unexpectedly succeeded")
	}
	if !strings.Contains(string(out), "FIFO writer did not complete before deadline") || strings.Contains(string(out), "did not open") {
		t.Fatalf("the stray early-refusal switch reached the stall child: %s", out)
	}
}
