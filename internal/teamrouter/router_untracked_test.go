package teamrouter

// Ported 1:1 from router.test.ts describe('untracked terminals: unknown is
// not busy (#7398)') — the two-signal discipline's screen fallback class.

import (
	"strings"
	"testing"
)

func setupUntracked(t *testing.T, agent string) (*fakeCLIs, *Config) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"})
	f.addTerminal("eli", agent, "", false) // lastOutputAt null — tracking lost
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{})}
	return f, c
}

//nolint:unparam // later blocks deliver workers other than Eli
func wantDelivered(t *testing.T, out *Outcome, name string) {
	t.Helper()
	if len(out.Nudged) != 1 || out.Nudged[0] != name || !out.Adopted {
		t.Fatalf("outcome = %+v, want nudged [%s] adopted", out, name)
	}
}

//nolint:unparam // later blocks hold workers other than Eli
func wantHeldContaining(t *testing.T, out *Outcome, name string, wants ...string) {
	t.Helper()
	reason := out.Held[name]
	if reason == "" {
		t.Fatalf("worker %s not held; outcome = %+v", name, out)
	}
	for _, w := range wants {
		if !strings.Contains(reason, w) {
			t.Fatalf("hold reason %q missing %q", reason, w)
		}
	}
}

func wantNotHeldContaining(t *testing.T, out *Outcome, name, not string) {
	t.Helper()
	if reason := out.Held[name]; strings.Contains(reason, not) {
		t.Fatalf("hold reason %q must not contain %q", reason, not)
	}
}

func wantNoSend(t *testing.T, f *fakeCLIs) {
	t.Helper()
	if len(f.sent) != 0 {
		t.Fatalf("sent = %v, want none", f.sent)
	}
}

func TestUntrackedScreenProvesIdleStillDelivers(t *testing.T) {
	f, c := setupUntracked(t, "claude")
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{mentions: ip(1), unread: ip(2)})}
	out := f.poll(t, c)
	wantDelivered(t, out, "Eli")
	if len(f.sent) != 1 {
		t.Fatalf("sent = %d, want 1", len(f.sent))
	}
}

func TestUntrackedScreenShowsActivityHoldsUnknownNotBusy(t *testing.T) {
	f, c := setupUntracked(t, "claude")
	f.setScreen("term_eli", screenSpec{tail: withPrompt(claudeIdle, "❯", "mid-turn output")}) // no idle-prompt shape
	out := f.poll(t, c)
	if len(out.Nudged) != 0 {
		t.Fatalf("nudged = %v, want none", out.Nudged)
	}
	wantHeldContaining(t, out, "Eli", "unknown idle state")
	wantNotHeldContaining(t, out, "Eli", "busy for")
	wantNoSend(t, f)
}

// #1924 (#1845): a tui-idle timeout is UNKNOWN on a tracked terminal too —
// the screen decides. A completed turn over an empty composer nudges.
func TestUntrackedTrackedTimeoutCompletedTurnNudges(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"})
	f.addTerminal("eli", "claude", "", true) // tracked; the tui-idle wait times out below
	f.busy["term_eli"] = true
	f.setScreen("term_eli", screenSpec{tail: claudeIdle}) // completed turn, empty composer
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{})}
	out := f.poll(t, c)
	wantDelivered(t, out, "Eli")
	wantHandles(t, f, "term_eli")
}

// #1924 codex shape: a tracked timeout over a Worked-for frame nudges
// (Atilla/Chip, held 2442/877 min as busy).
func TestUntrackedTrackedTimeoutCodexWorkedForNudges(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"})
	f.addTerminal("eli", "codex", "", true)
	f.busy["term_eli"] = true
	f.setScreen("term_eli", screenSpec{tail: codexIdle})
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{})}
	out := f.poll(t, c)
	wantDelivered(t, out, "Eli")
	wantHandles(t, f, "term_eli")
}

// A tracked terminal that times out still HOLDS on a live working
// indicator: the screen vetoes.
func TestUntrackedTrackedTimeoutLiveIndicatorHolds(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"})
	f.addTerminal("eli", "claude", "", true) // tracked
	f.busy["term_eli"] = true
	f.setScreen("term_eli", screenSpec{tail: claudeBusyEmpty})
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{})}
	out := f.poll(t, c)
	if len(out.Nudged) != 0 {
		t.Fatalf("nudged = %v, want none", out.Nudged)
	}
	wantHeldContaining(t, out, "Eli", "a running turn is drawn above the prompt")
	wantNoSend(t, f)
}

func TestUntrackedRecheckAlsoFallsBackToScreen(t *testing.T) {
	f, c := setupUntracked(t, "claude")
	// Gate passes via the screen fallback; the recompute re-runs; the
	// fail-closed recheck also times out for the untracked terminal — the
	// screen still proves the idle prompt, so the send proceeds.
	out := f.poll(t, c)
	wantDelivered(t, out, "Eli")
	waits := 0
	for _, call := range f.calls {
		if strings.HasPrefix(call, "terminal wait") {
			waits++
		}
	}
	if waits < 2 {
		t.Fatalf("waits = %d, want gate + recheck both attempted", waits)
	}
	if f.freshN != 1 {
		t.Fatalf("fresh re-reads = %d, want 1 (the fallback path really exercised it)", f.freshN)
	}
}

func TestUntrackedBusyClaudeEmptyComposerHoldsUnknown(t *testing.T) {
	f, c := setupUntracked(t, "claude")
	f.setScreen("term_eli", screenSpec{tail: claudeBusyEmpty}) // empty box + hint; spinner above
	out := f.poll(t, c)
	if len(out.Nudged) != 0 {
		t.Fatalf("nudged = %v, want none", out.Nudged)
	}
	wantHeldContaining(t, out, "Eli", "running turn")
	wantNotHeldContaining(t, out, "Eli", "busy for")
	wantNoSend(t, f)
	if f.freshN != 0 {
		t.Fatalf("fresh re-reads = %d, want 0 (held at the gate)", f.freshN)
	}
}

func TestUntrackedBusyCodexWorkingLineHolds(t *testing.T) {
	f, c := setupUntracked(t, "codex")
	f.setScreen("term_eli", screenSpec{tail: codexBusyEmpty})
	out := f.poll(t, c)
	if len(out.Nudged) != 0 {
		t.Fatalf("nudged = %v, want none", out.Nudged)
	}
	wantHeldContaining(t, out, "Eli", "running turn")
	wantNoSend(t, f)
}

func TestUntrackedBusyOnlyAfterFreshScan(t *testing.T) {
	f, c := setupUntracked(t, "claude")
	f.setRecheck("term_eli", screenSpec{tail: claudeBusyEmpty}) // it went busy during the recompute
	out := f.poll(t, c)
	if len(out.Nudged) != 0 {
		t.Fatalf("nudged = %v, want none", out.Nudged)
	}
	wantHeldContaining(t, out, "Eli", "busy again after the recompute", "running turn")
	wantNoSend(t, f)
	if f.freshN != 1 {
		t.Fatalf("fresh re-reads = %d, want 1", f.freshN)
	}
}

func TestUntrackedTrackingHealedRestoresOrdinaryHold(t *testing.T) {
	f, c := setupUntracked(t, "claude")
	f.healTracking["term_eli"] = true                           // produced output after the gate: the recheck's list shows it tracked
	f.busyRecheck["term_eli"] = true                            // and its recheck wait times out
	f.setRecheck("term_eli", screenSpec{tail: claudeBusyEmpty}) // #1924: a live indicator, not just the wait
	out := f.poll(t, c)
	if len(out.Nudged) != 0 {
		t.Fatalf("nudged = %v, want none", out.Nudged)
	}
	wantHeldContaining(t, out, "Eli", "busy again after the recompute (a running turn is drawn above the prompt")
	wantNotHeldContaining(t, out, "Eli", "unknown")
	wantNoSend(t, f)
}

func TestUntrackedFreshClaudeLaunchDelivers(t *testing.T) {
	f, c := setupUntracked(t, "claude")
	f.setScreen("term_eli", screenSpec{tail: claudeFresh}) // captured live (Cody, 2026-10-10)
	out := f.poll(t, c)
	wantDelivered(t, out, "Eli")
	if f.freshN != 1 {
		t.Fatalf("fresh re-reads = %d, want 1", f.freshN)
	}
}

func TestUntrackedFreshCodexLaunchDelivers(t *testing.T) {
	f, c := setupUntracked(t, "codex")
	f.setScreen("term_eli", screenSpec{tail: codexFresh}) // captured live (Dara, 2026-10-10)
	out := f.poll(t, c)
	wantDelivered(t, out, "Eli")
}

func TestUntrackedNeitherBusyNorIdleMarkerHoldsUnknown(t *testing.T) {
	f, c := setupUntracked(t, "claude")
	f.setScreen("term_eli", screenSpec{tail: claudeUnknownQuiet}) // empty prompt, quiet, unclassified chrome
	out := f.poll(t, c)
	if len(out.Nudged) != 0 {
		t.Fatalf("nudged = %v, want none", out.Nudged)
	}
	wantHeldContaining(t, out, "Eli", "unknown idle state", "unrecognized chrome")
	wantNoSend(t, f)
}

func TestUntrackedMinimalScreenHoldsNoLivenessEvidence(t *testing.T) {
	f, c := setupUntracked(t, "claude")
	f.setScreen("term_eli", screenSpec{tail: []string{rule, "❯ ", rule, claudeHint}}) // Diego's truncated-tail counterexample
	out := f.poll(t, c)
	if len(out.Nudged) != 0 {
		t.Fatalf("nudged = %v, want none", out.Nudged)
	}
	wantHeldContaining(t, out, "Eli", "unknown idle state", "no positive liveness evidence")
	wantNoSend(t, f)
}

func TestUntrackedHistoricalDoneMarkerDoesNotRescue(t *testing.T) {
	f, c := setupUntracked(t, "claude")
	// Diego's counterexample: old completion text above an unexplained current status line.
	f.setScreen("term_eli", screenSpec{tail: []string{"⏺ Done.", "an unrecognized current status", rule, "❯ ", rule, claudeHint}})
	out := f.poll(t, c)
	if len(out.Nudged) != 0 {
		t.Fatalf("nudged = %v, want none", out.Nudged)
	}
	wantHeldContaining(t, out, "Eli", "unknown idle state", "unrecognized chrome")
	wantNoSend(t, f)
}

func TestUntrackedRealIdleScreenDelivers(t *testing.T) {
	f, c := setupUntracked(t, "claude")
	f.setScreen("term_eli", screenSpec{tail: claudeIdleRealXan}) // captured live (Xan, 2026-10-11)
	out := f.poll(t, c)
	wantDelivered(t, out, "Eli")
}

func TestUntrackedRealIdleScreenUpdateBannerDelivers(t *testing.T) {
	f, c := setupUntracked(t, "claude")
	f.setScreen("term_eli", screenSpec{tail: claudeIdleRealLiam}) // captured live (Liam, 2026-10-11)
	out := f.poll(t, c)
	wantDelivered(t, out, "Eli")
}

func TestUntrackedProseContainingCompactedIsNotBanner(t *testing.T) {
	f, c := setupUntracked(t, "claude")
	// Diego's counterexample: an output-text line mentioning compaction is not the banner.
	f.setScreen("term_eli", screenSpec{tail: []string{"  the router compacted several workers overnight", rule, "❯\u00a0", rule, claudeHint}})
	out := f.poll(t, c)
	if len(out.Nudged) != 0 {
		t.Fatalf("nudged = %v, want none", out.Nudged)
	}
	wantHeldContaining(t, out, "Eli", "unrecognized chrome")
	wantNoSend(t, f)
}

func TestUntrackedCompactedWhileIdleBannerIsPositiveIdle(t *testing.T) {
	f, c := setupUntracked(t, "claude")
	f.setScreen("term_eli", screenSpec{tail: claudeIdleCompacted})
	out := f.poll(t, c)
	wantDelivered(t, out, "Eli")
}
