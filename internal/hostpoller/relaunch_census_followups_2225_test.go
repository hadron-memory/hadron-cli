//go:build linux

package hostpoller

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

	// Control: with authority held throughout, the same fixture publishes 1, and
	// the number of authority checks it makes (the entry check, the final check
	// and any intermediate ones) is measured here rather than assumed.
	held, total := 0, 0
	if got, err := strictRelaunchCensusCounted(context.Background(), proc, work, func(context.Context) error { total++; return nil }, &held); err != nil || len(got) != 1 || held != 1 {
		t.Fatalf("control: agents=%+v count=%d err=%v", got, held, err)
	}
	if total < 2 {
		t.Fatalf("the census must check authority at entry and again at the end; it checked %d time(s)", total)
	}

	// The refusal lands on the LAST authority check, whatever that number is, so
	// a legitimate extra intermediate check moves it instead of breaking the pin.
	calls := 0
	authority := func(context.Context) error {
		calls++
		if calls == total {
			return refusal
		}
		return nil
	}
	count := 0
	got, err := strictRelaunchCensusCounted(context.Background(), proc, work, authority, &count)
	if !errors.Is(err, refusal) || got != nil {
		t.Fatalf("final authority refusal must refuse the census: agents=%+v err=%v", got, err)
	}
	if calls != total {
		t.Fatalf("the refusal must come from the final authority check: consulted %d times, want %d", calls, total)
	}
	if count != 0 {
		t.Fatalf("a count escaped a census whose final authority was refused: %d", count)
	}
}

// cli#2225 item 2. The FIFO subprocess budget is stated once, as named
// constants, and this test pins only their ORDERING (cli#2384 F3: the durations
// are no longer restated here, so retuning one is a one-place edit): one in-child
// wait, plus headroom for process start and scheduling on a loaded host, under a
// hard watchdog.
func TestFIFOBudgetAcceptance2225(t *testing.T) {
	if fifoWait2197 <= 0 {
		t.Fatalf("the in-child wait must be positive: %s", fifoWait2197)
	}
	if fifoWait2197 >= fifoChildBudget2197 || fifoChildBudget2197 >= fifoWatchdog2197 {
		t.Fatalf("a refusal must fit its budget, and the budget must fit under the watchdog: wait=%s budget=%s watchdog=%s", fifoWait2197, fifoChildBudget2197, fifoWatchdog2197)
	}
}

// cli#2225 item 3. The scenario switches act only in a child that was started
// with the child-mode guard. A stray switch in the parent environment must not
// change the ordinary fixture test.
func TestFIFOSwitchesIgnoredWithoutChildGuard2225(t *testing.T) {
	// A stray OUTER child guard would turn the switches below on and make this
	// test pass or fail for the wrong reason: refuse to run in that environment.
	if v, set := os.LookupEnv("HADRON_TEST_FIFO_CHILD_2197"); set {
		t.Fatalf("HADRON_TEST_FIFO_CHILD_2197=%q is set in the parent; unset it, this test needs the child guard absent", v)
	}
	t.Setenv("HADRON_TEST_FIFO_STALL_2197", "1")
	t.Setenv("HADRON_TEST_FIFO_EARLY_REFUSAL_2197", "1")
	ctx, cancel := context.WithTimeout(context.Background(), fifoWatchdog2197)
	defer cancel()
	// Stray switches inherited, no child guard.
	cmd := fifoChildCmd2197(ctx, os.Environ())
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
