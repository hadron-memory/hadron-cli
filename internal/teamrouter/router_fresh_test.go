package teamrouter

// Ported 1:1 from router.test.ts describe('fresh counts at send time
// (#1616)') — the tokenless re-read before each send, draft refutation and
// the post-recompute rechecks. A null lastRelevantSeq on a fresh row (TS)
// is modeled as 0: "no mention left" is what the drop decision reads.

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func (f *fakeCLIs) pollAt(t *testing.T, c *Config, now func() int64) *Outcome {
	t.Helper()
	out, err := PollOnce(t.Context(), c, f, f.orca, func(l string) { f.logs = append(f.logs, l) }, now)
	if err != nil {
		t.Fatalf("pollOnce: %v", err)
	}
	return out
}

func TestFreshDropsNudgeWhenWorkerReadWhileWaiting(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"})
	f.addTerminal("eli", "claude", "", true)
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{mentions: ip(2), unread: ip(5), relevant: ip(110)})}
	// The fresh scan reads later: Eli read everything (mentions 0, unread 0).
	fresh := worker("w-eli", "Eli", workerOpts{mentions: ip(0), unread: ip(0), relevant: ip(0)})
	f.fresh = []AttentionItem{fresh}
	out := f.poll(t, c)
	if len(out.Nudged) != 0 || len(out.Dropped) != 1 || out.Dropped[0] != "Eli" || !out.Adopted {
		t.Fatalf("outcome = %+v, want dropped [Eli] adopted", out)
	}
	wantNoSend(t, f)
	if d := DescribeOutcome(out); !strings.Contains(d, "dropped (read while waiting): Eli") {
		t.Fatalf("describeOutcome = %q, want the drop line", d)
	}
}

func TestFreshDroppedFrontierRecordedCovered(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"}, [2]string{"w-baloo", "Baloo"})
	f.addTerminal("eli", "claude", "", true)
	f.addTerminal("baloo", "claude", "", true)
	f.busy["term_baloo"] = true
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{relevant: ip(110)}), worker("w-baloo", "Baloo", workerOpts{})}
	f.fresh = []AttentionItem{worker("w-baloo", "Baloo", workerOpts{})} // Eli read its chat; Baloo still has its mention
	first := f.poll(t, c)
	if len(first.Dropped) != 1 || first.Dropped[0] != "Eli" {
		t.Fatalf("first = %+v, want Eli dropped", first)
	}
	if first.Held["Baloo"] == "" {
		t.Fatal("Baloo must be held")
	}
	st := NewState(c.StateDir)
	ledger := st.Ledger()
	if got := ledger.Nudged["w-eli"].RelevantSeq; got != 110 {
		t.Fatalf("dropped frontier recorded = %d, want 110", got)
	}
	f.fresh = nil // from here the fresh scan mirrors the poll rows
	second := f.poll(t, c)
	if !contains(second.Covered, "Eli") {
		t.Fatalf("second = %+v, want Eli covered by the ledger", second)
	}
	if len(second.Nudged) != 0 {
		t.Fatalf("Baloo is still busy: nudged = %v", second.Nudged)
	}
	if f.freshN != 1 {
		t.Fatalf("fresh scans = %d, want 1 (Eli's drop consumed the only one)", f.freshN)
	}
}

func contains(xs []string, v string) bool { //nolint:unparam // later blocks cover workers other than Eli
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func TestFreshSendsFreshCountsAndRecordsFreshFrontier(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"}, [2]string{"w-baloo", "Baloo"})
	f.addTerminal("eli", "claude", "", true)
	f.addTerminal("baloo", "claude", "", true)
	f.busy["term_baloo"] = true // hold the token so the ledger's base survives to be read
	f.attention.workers = []AttentionItem{
		worker("w-eli", "Eli", workerOpts{mentions: ip(2), unread: ip(5), relevant: ip(110), head: ip(120)}),
		worker("w-baloo", "Baloo", workerOpts{}),
	}
	f.fresh = []AttentionItem{worker("w-eli", "Eli", workerOpts{mentions: ip(3), unread: ip(9), relevant: ip(118), head: ip(121)})}
	out := f.poll(t, c)
	if len(out.Nudged) != 1 || out.Nudged[0] != "Eli" {
		t.Fatalf("outcome = %+v, want nudged [Eli]", out)
	}
	if out.Held["Baloo"] == "" {
		t.Fatal("Baloo must be held")
	}
	if len(f.sent) != 1 {
		t.Fatalf("sent = %d, want 1", len(f.sent))
	}
	for _, want := range []string{"9 relevant unread (3 mentions", "unread #100–#121", "latest mention #118"} {
		if !strings.Contains(f.sent[0].text, want) {
			t.Errorf("nudge missing %q", want)
		}
	}
	if strings.Contains(f.sent[0].text, "5 relevant unread") {
		t.Errorf("stale poll numbers leaked into the nudge")
	}
	if got := NewState(c.StateDir).Ledger().Nudged["w-eli"].RelevantSeq; got != 118 {
		t.Errorf("ledger frontier = %d, want the fresh 118", got)
	}
	if tok := adoptedToken(t, c); tok != "" {
		t.Errorf("held poll adopted %q; the fresh scan's token is never adopted", tok)
	}
}

func TestFreshFailedScanFailsPollTokenUnchanged(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"})
	f.addTerminal("eli", "claude", "", true)
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{})}
	f.freshOk = false
	if _, err := PollOnce(t.Context(), c, f, f.orca, func(string) {}, nil); err == nil {
		t.Fatal("a failed fresh scan must fail the poll")
	}
	wantNoSend(t, f)
	if tok := adoptedToken(t, c); tok != "" {
		t.Fatalf("token = %q, want none", tok)
	}
}

func TestFreshStaleDraftRefutedByEmptyClaudeComposer(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"})
	f.addTerminal("eli", "claude", "", true)
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{mentions: ip(1), unread: ip(2), relevant: ip(110)})}
	// Orca's draft carries text no client shows; the rendered box is bare and
	// the agents hint is up — Claude draws typed text on the ❯ line, so this
	// draft cannot be pending input.
	draft := "mark #1625 ready, do not wait"
	f.setScreen("term_eli", screenSpec{tail: claudeIdle, draft: &draft})
	out := f.poll(t, c)
	wantDelivered(t, out, "Eli")
	if len(f.sent) != 1 {
		t.Fatalf("sent = %d, want 1", len(f.sent))
	}
	if !strings.Contains(strings.Join(f.logs, "\n"), "Eli: stale Orca draft discarded") {
		t.Errorf("missing the stale-discard log line")
	}
	if strings.Contains(strings.Join(f.logs, "\n"), "mark #1625") {
		t.Errorf("the draft's text must never be logged")
	}
}

func TestFreshStaleDiscardLogsOncePerToken(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"}, [2]string{"w-baloo", "Baloo"})
	f.addTerminal("eli", "claude", "", true)
	f.addTerminal("baloo", "claude", "", true)
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{relevant: ip(110)}), worker("w-baloo", "Baloo", workerOpts{relevant: ip(115)})}
	stale := "anything stale"
	typed := "typed words"
	f.setScreen("term_eli", screenSpec{tail: claudeIdle, draft: &stale})
	// Baloo's box RENDERS text — the draft is corroborated, not refuted.
	f.setScreen("term_baloo", screenSpec{tail: withPrompt(claudeIdle, "❯", "typed words"), draft: &typed})
	first := f.poll(t, c)
	if len(first.Nudged) != 1 || first.Nudged[0] != "Eli" {
		t.Fatalf("first = %+v, want nudged [Eli]", first)
	}
	if !strings.Contains(first.Held["Baloo"], "text in the composer draft") {
		t.Fatalf("Baloo hold = %q, want the draft pin", first.Held["Baloo"])
	}
	if n := countLines(f.logs, "stale Orca draft discarded"); n != 1 {
		t.Fatalf("stale-discard lines = %d, want 1", n)
	}
	f.attention.workers = []AttentionItem{worker("w-baloo", "Baloo", workerOpts{relevant: ip(120)})}
	f.poll(t, c)
	if n := countLines(f.logs, "stale Orca draft discarded"); n != 1 { // once per token
		t.Fatalf("stale-discard lines = %d, want 1 (once per token)", n)
	}
}

func countLines(logs []string, needle string) int {
	n := 0
	for _, l := range logs {
		if strings.Contains(l, needle) {
			n++
		}
	}
	return n
}

func TestFreshCodexDraftStaysAuthoritative(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"})
	f.addTerminal("eli", "codex", "", true)
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{mentions: ip(1), unread: ip(2), relevant: ip(110)})}
	draft := "pending codex text"
	f.setScreen("term_eli", screenSpec{tail: codexIdle, draft: &draft})
	out := f.poll(t, c)
	if len(out.Nudged) != 0 {
		t.Fatalf("nudged = %v, want none", out.Nudged)
	}
	wantHeldContaining(t, out, "Eli", "text in the composer draft")
	wantNoSend(t, f)
}

func TestFreshLogsLongHoldAtMostEvery30Min(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"})
	f.addTerminal("eli", "claude", "", true)
	f.busy["term_eli"] = true
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{})}
	base := time.Now().UnixMilli()
	now := func() int64 { return base }
	f.pollAt(t, c, now) // held: busy
	st := NewState(c.StateDir)
	ledger := st.Ledger()
	ledger.HeldSince["w-eli"] = base - 41*60_000 // backdate the hold
	if err := st.SaveLedger(ledger); err != nil {
		t.Fatal(err)
	}
	f.logs = nil
	later := now // router.ts polls on the wall clock right after the backdate: 41 min, not 42
	f.pollAt(t, c, later)
	if !regexp.MustCompile(`Eli held 4[01] min with mentions pending \(busy`).MatchString(strings.Join(f.logs, "\n")) {
		t.Fatalf("missing the long-hold line; logs = %v", f.logs)
	}
	f.logs = nil
	f.pollAt(t, c, later) // within 30 min of the last long-hold line: silent
	if regexp.MustCompile(`held \d+ min`).MatchString(strings.Join(f.logs, "\n")) {
		t.Fatalf("long-hold line repeated too soon; logs = %v", f.logs)
	}
}

func TestFreshReprovesPromptEmptyAfterRecompute(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"})
	f.addTerminal("eli", "claude", "", true)
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{mentions: ip(1), unread: ip(2), relevant: ip(110)})}
	// The recompute's round trip reopened the #5473 window; a human started
	// typing in it — and Claude RENDERS the text, so the draft is corroborated.
	typed := "Holger typing here"
	f.setRecheck("term_eli", screenSpec{tail: withPrompt(claudeIdle, "❯", "Holger typing here"), draft: &typed})
	out := f.poll(t, c)
	if len(out.Nudged) != 0 {
		t.Fatalf("nudged = %v, want none", out.Nudged)
	}
	wantHeldContaining(t, out, "Eli", "prompt not empty on recheck")
	wantNoSend(t, f)
	if tok := adoptedToken(t, c); tok != "" {
		t.Fatalf("token = %q, want none", tok)
	}
	if strings.Contains(strings.Join(f.logs, "\n"), "Holger typing here") {
		t.Errorf("the hold reason must never quote the typed text")
	}
}

func TestFreshReprovesIdleAfterRecompute(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"})
	f.addTerminal("eli", "claude", "", true)
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{mentions: ip(1), unread: ip(2), relevant: ip(110)})}
	f.busyRecheck["term_eli"] = true // the agent went busy during the fresh scan
	out := f.poll(t, c)
	if len(out.Nudged) != 0 {
		t.Fatalf("nudged = %v, want none", out.Nudged)
	}
	wantHeldContaining(t, out, "Eli", "busy again after the recompute")
	wantNoSend(t, f)
	if tok := adoptedToken(t, c); tok != "" {
		t.Fatalf("token = %q, want none", tok)
	}
}

// Charles's QA probe (receipt on the PR), pinned per Xan's ruling #6495: the
// fake ignores --timeout-ms, so without this a full busy-wait at the recheck
// — the exact behavior Atilla's second P2 killed — survives the whole suite.
func TestFreshGateWaitsFullBusyWaitRecheckBounded(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"})
	c.BusyWaitMS = 90_000
	f.addTerminal("eli", "claude", "", true)
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{})}
	out := f.poll(t, c)
	if len(out.Nudged) != 1 || out.Nudged[0] != "Eli" {
		t.Fatalf("outcome = %+v, want nudged [Eli]", out)
	}
	var waitFlags []int
	for _, call := range f.calls {
		if !strings.HasPrefix(call, "terminal wait") {
			continue
		}
		parts := strings.Fields(call)
		for i, p := range parts {
			if p == "--timeout-ms" && i+1 < len(parts) {
				ms, err := strconv.Atoi(parts[i+1])
				if err != nil {
					t.Fatalf("bad --timeout-ms %q", parts[i+1])
				}
				waitFlags = append(waitFlags, ms)
			}
		}
	}
	if len(waitFlags) != 2 {
		t.Fatalf("gate + recheck waits = %v, want 2", waitFlags)
	}
	if waitFlags[0] != 90_000 {
		t.Errorf("gate wait = %d ms, want the full busyWaitMs", waitFlags[0])
	}
	if waitFlags[1] > 1_000 {
		t.Errorf("recheck wait = %d ms, want a short bounded timeout", waitFlags[1])
	}
}

func TestFreshAdoptionCarriesNudgeBeyondTokenHorizon(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"}, [2]string{"w-baloo", "Baloo"})
	f.addTerminal("eli", "claude", "", true)
	f.addTerminal("baloo", "claude", "", true)
	// Main scan: Eli's mention at 110, Baloo's at 115. Fresh scan, reading
	// later: Eli's has moved to 125; Baloo's is unchanged.
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{relevant: ip(110)}), worker("w-baloo", "Baloo", workerOpts{relevant: ip(115)})}
	f.fresh = []AttentionItem{worker("w-eli", "Eli", workerOpts{relevant: ip(125)}), worker("w-baloo", "Baloo", workerOpts{relevant: ip(115)})}
	first := f.poll(t, c)
	if len(first.Nudged) != 2 || first.Nudged[0] != "Eli" || first.Nudged[1] != "Baloo" || !first.Adopted {
		t.Fatalf("first = %+v, want nudged [Eli Baloo] adopted", first)
	}
	if !strings.Contains(f.sent[0].text, "latest mention #125") {
		t.Errorf("nudge must carry the fresh frontier")
	}
	// Only Eli's record outlives the adopted token (125 above its 110
	// horizon); Baloo's 115 equals its horizon, so the token covers it.
	led := NewState(c.StateDir).Ledger().Nudged
	if len(led) != 1 || led["w-eli"].RelevantSeq != 125 {
		t.Fatalf("carried records = %+v, want only w-eli@125", led)
	}
	// The server lists Eli again next poll: 125 postdates the token's horizon.
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{relevant: ip(125)})}
	f.fresh = nil
	second := f.poll(t, c)
	if len(second.Covered) != 1 || second.Covered[0] != "Eli" || len(second.Nudged) != 0 {
		t.Fatalf("second = %+v, want Eli covered, none nudged", second)
	}
	if len(f.sent) != 2 { // no re-send: Eli answered from the carried record
		t.Fatalf("sends = %d, want 2 (no re-send)", len(f.sent))
	}
}
