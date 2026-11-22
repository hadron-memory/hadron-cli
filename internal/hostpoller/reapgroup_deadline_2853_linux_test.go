//go:build linux

package hostpoller

import (
	"os"
	"testing"
	"time"
)

// cli#2853: a host that churns short-lived processes voids every pass of the /proc scan
// (each listed-then-vanished pid voids its pass, by the safety rule of cli#2042), and a
// bound of 32 PASSES gave up on a group that was in fact empty, so a real stop read as
// unconfirmed. Ruling (Holger, 2026-11-21): the scan runs until a WALL-CLOCK deadline instead
// of a pass count, a stop still claims the group empty only after one clean pass, and an
// unconfirmed stop is reported with its why code. These tests drive the scan with a modelled
// /proc and a modelled clock: no sleeps, no real churn.

// withScanClock installs a modelled clock that advances `step` on every /proc listing (the
// scan's unit of work) and sets the deadline, restoring both afterwards. It returns a
// function reading the modelled time elapsed since install.
func withScanClock(t *testing.T, step, deadline time.Duration) func() time.Duration {
	t.Helper()
	oldClock, oldDeadline, oldPause, oldDir := scanClock, groupScanDeadline, scanPause, readProcDir
	t.Cleanup(func() { scanClock, groupScanDeadline, scanPause, readProcDir = oldClock, oldDeadline, oldPause, oldDir })
	scanPause = 0
	base := time.Unix(1_700_000_000, 0)
	var elapsed time.Duration
	scanClock = func() time.Time { return base.Add(elapsed) }
	groupScanDeadline = deadline
	list := readProcDir
	readProcDir = func(path string) ([]os.DirEntry, error) {
		elapsed += step
		return list(path)
	}
	return func() time.Duration { return elapsed }
}

// A host whose churn voids far more than the old 32 passes, but whose pass is fast, still
// settles on the first clean pass inside the deadline: the bound is time, not a count.
// (Red at a bound of 32 passes.)
func TestAScanKeepsGoingPastThirtyTwoVoidPassesWhileInsideTheDeadline2853(t *testing.T) {
	const voidPasses = 100
	f := churnHost(3)
	f.install(t)
	withFreshVanisher(f, voidPasses)
	withScanClock(t, time.Millisecond, time.Second) // 100 void passes take 100ms of 1s
	members, why := groupMembership(churnPgid, churnPgid)
	if members {
		t.Fatalf("answered members (%s) after %d listings; a clean pass exists at %d, well inside the deadline", why, f.lists, voidPasses+1)
	}
	if f.lists != voidPasses+1 {
		t.Fatalf("must settle on the first clean pass: %d listings, want %d", f.lists, voidPasses+1)
	}
}

// Endless churn ends at the DEADLINE, as members with the void pass's code, not at a pass
// count: every pass is void, the scan stops once the modelled clock passes the deadline.
// (Red at a bound of 32 passes: it gives up after 32 listings, 32ms in.)
func TestEndlessChurnEndsAtTheDeadlineNotAtAPassCount2853(t *testing.T) {
	f := churnHost(3)
	f.install(t)
	withFreshVanisher(f, 1<<30)
	elapsed := withScanClock(t, 10*time.Millisecond, time.Second)
	members, why := groupMembership(churnPgid, churnPgid)
	if !members || why != whyUnseenPid {
		t.Fatalf("a /proc that never holds still proves nothing: want members/unseen-pid, got %v/%s", members, why)
	}
	if elapsed() < time.Second {
		t.Fatalf("gave up before the deadline: %s elapsed of %s after %d listings", elapsed(), time.Second, f.lists)
	}
	if f.lists < 100 || f.lists > 101 {
		t.Fatalf("one listing per 10ms up to 1s: want about 100 listings, got %d", f.lists)
	}
}

// A live member is proof at once, whatever the deadline: the scan does not wait out the
// clock to say members, and does not mistake a deadline for an empty group.
func TestALiveMemberAnswersAtOnceAndAnExpiredDeadlineNeverReadsAsEmpty2853(t *testing.T) {
	f := churnHost(1)
	f.stats[churnMember] = churnStat{churnPgid, 'S', 1} // a running member
	f.install(t)
	withScanClock(t, 10*time.Millisecond, time.Second)
	members, why := groupMembership(churnPgid, churnPgid)
	if !members || why != whyLiveMember || f.lists != 1 {
		t.Fatalf("a running member answers on the first pass: members=%v why=%s lists=%d", members, why, f.lists)
	}
	// A deadline already past before the first pass still runs one pass, and a clean one is
	// still believed: the deadline bounds the retries, never the evidence.
	g := churnHost(0)
	g.stats[churnMember] = churnStat{churnPgid, 'Z', 1}
	g.install(t)
	withScanClock(t, time.Hour, time.Nanosecond)
	if m, w := groupMembership(churnPgid, churnPgid); !m || w != whyLateFork {
		// pass 0 reads the Z member for the first time: void (late-fork), and the deadline
		// is already past, so the answer is members with that code, never empty.
		t.Fatalf("an expired deadline after a void pass is members, never empty: %v/%s", m, w)
	}
}
