package teamrouter

// Ported 1:1 from router.test.ts describe('coordinator/liveness rule
// (#1826)') — the idle-watch rule, including the #1829 local-seat cycle-1
// pins. liveness.json is read and seeded directly: the durable file is the
// contract, not an API.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

const ago21min = 21 * 60_000

func mcfgEntries(t *testing.T, root string, entry [3]string, watchMinutes float64) *Config { //nolint:unparam // TS varies N via loadConfig; the poll helper stays at 20
	t.Helper()
	c := cfgEntries(root)
	c.CoordinatorWatchMin = watchMinutes
	c.Roster = []RosterEntry{{
		Worker: entry[0], Name: entry[1],
		Worktree:    filepath.Join(root, "workspaces", strings.ToLower(entry[1])),
		Coordinator: entry[2] == "coordinator", Lifecycle: entry[2] == "lifecycle"}}
	return c
}

func seedLiveness(t *testing.T, c *Config, workers map[string]any) {
	t.Helper()
	if err := os.MkdirAll(c.StateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	blob, err := json.Marshal(workers)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(c.StateDir, "liveness.json"), blob, 0o644); err != nil {
		t.Fatal(err)
	}
}

func readLiveness(t *testing.T, c *Config) map[string]map[string]any {
	t.Helper()
	blob, err := os.ReadFile(filepath.Join(c.StateDir, "liveness.json"))
	if err != nil {
		t.Fatalf("read liveness.json: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(blob, &raw); err != nil {
		t.Fatalf("parse liveness.json: %v", err)
	}
	out := map[string]map[string]any{}
	for k, v := range raw {
		if m, ok := v.(map[string]any); ok {
			out[k] = m
		} else {
			out[k] = map[string]any{"_corrupt": v}
		}
	}
	return out
}

func idleRec(handle string, idleSince int64, covered *int) map[string]any {
	rec := map[string]any{"handle": handle, "idleSince": idleSince}
	if covered != nil {
		rec["coveredFrontier"] = *covered
	}
	return rec
}

func coveredPtr(v int) *int { return &v }

func TestLivenessIdleFlaggedWorkerWokenThroughGuardChain(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := mcfgEntries(t, root, [3]string{"w-ada", "Ada", "coordinator"}, 20)
	f.addTerminal("ada", "claude", "", true)
	f.attention.workers = nil // attention sees nothing relevant: the suppressed-row gap (#1826 delta 1)
	f.cursors["w-ada"] = 100  // watermark 100 < head 120, no mention anywhere in the span
	seedLiveness(t, c, map[string]any{"w-ada": idleRec("term_ada", time.Now().UnixMilli()-ago21min, nil)})
	out := f.poll(t, c)
	if len(out.Liveness) != 1 || out.Liveness[0] != "Ada" {
		t.Fatalf("liveness = %v, want [Ada]", out.Liveness)
	}
	if len(out.Nudged) != 0 || !out.Adopted {
		t.Fatalf("outcome = %+v, want no mention nudges, adopted", out)
	}
	if len(f.sent) != 1 || f.sent[0].handle != "term_ada" {
		t.Fatalf("sent = %v, want one to term_ada", f.sent)
	}
	text := f.sent[0].text
	for _, want := range []string{"liveness", "idle-watch rule", "#100", "#120", "--since 100", "sinceSeq=100", "mark read", "as of send"} {
		if !strings.Contains(text, want) {
			t.Errorf("liveness text missing %q:\n%s", want, text)
		}
	}
	if n := strings.Count(text, "mark read"); n != 1 { // once, not twice (Taylor #9748)
		t.Errorf("'mark read' appears %d times, want 1", n)
	}
	if strings.Contains(text, "relevant unread") {
		t.Error("the liveness text never claims counts")
	}
	if regexp.MustCompile(`\d+ mention`).MatchString(text) {
		t.Error("identifying the rule is fine; a count claim never is")
	}
	// The full guard chain ran, in order, with probe / gate / recheck waits.
	var waitFlags []int
	waitIdx, readIdx, sendIdx := -1, -1, -1
	for i, call := range f.calls {
		switch {
		case strings.HasPrefix(call, "terminal wait"):
			if waitIdx == -1 {
				waitIdx = i
			}
			parts := strings.Fields(call)
			for j, p := range parts {
				if p == "--timeout-ms" && j+1 < len(parts) {
					ms, err := strconv.Atoi(parts[j+1])
					if err != nil {
						t.Fatalf("bad --timeout-ms %q", parts[j+1])
					}
					waitFlags = append(waitFlags, ms)
				}
			}
		case strings.HasPrefix(call, "terminal read") && readIdx == -1:
			readIdx = i
		case strings.HasPrefix(call, "terminal send") && sendIdx == -1:
			sendIdx = i
		}
	}
	if waitIdx >= readIdx || readIdx >= sendIdx {
		t.Fatalf("verbs out of order: wait=%d read=%d send=%d", waitIdx, readIdx, sendIdx)
	}
	if len(waitFlags) != 3 {
		t.Fatalf("waits = %v, want probe + gate + recheck", waitFlags)
	}
	if waitFlags[0] > 1_000 || waitFlags[2] > 1_000 {
		t.Errorf("probe/recheck waits = %v, want short bounded", waitFlags)
	}
	if waitFlags[1] != c.BusyWaitMS {
		t.Errorf("gate wait = %d, want the full busyWaitMs %d", waitFlags[1], c.BusyWaitMS)
	}
	// Persisted at once: frontier = the head at send, clock restarted at the send.
	rec := readLiveness(t, c)["w-ada"]
	if rec["coveredFrontier"].(float64) != 120 {
		t.Errorf("coveredFrontier = %v, want 120", rec["coveredFrontier"])
	}
	if rec["idleSince"].(float64) < float64(time.Now().UnixMilli()-60_000) {
		t.Errorf("idleSince = %v, want restarted at the send", rec["idleSince"])
	}
}

func TestLivenessNonFlaggedWorkerGetsNothing(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := mcfgEntries(t, root, [3]string{"w-eli", "Eli", ""}, 20)
	f.addTerminal("eli", "claude", "", true)
	f.attention.workers = nil
	f.cursors["w-eli"] = 100
	seedLiveness(t, c, map[string]any{"w-eli": idleRec("term_eli", time.Now().UnixMilli()-ago21min, nil)})
	out := f.poll(t, c)
	if len(out.Liveness) != 0 || len(f.sent) != 0 || !out.Adopted {
		t.Fatalf("outcome = %+v, sent = %v: liveness is opt-in per roster flag", out, f.sent)
	}
}

func TestLivenessFiresOnlyStrictlyPastN(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := mcfgEntries(t, root, [3]string{"w-ada", "Ada", "coordinator"}, 20) // N = 20 min
	f.addTerminal("ada", "claude", "", true)
	f.cursors["w-ada"] = 100
	frozen := time.Now().UnixMilli()
	freeze := func() int64 { return frozen }
	seedLiveness(t, c, map[string]any{"w-ada": idleRec("term_ada", frozen-20*60_000, nil)})
	if out := f.pollAt(t, c, freeze); len(out.Liveness) != 0 { // exactly N: never fires
		t.Fatalf("liveness = %v at exactly N", out.Liveness)
	}
	seedLiveness(t, c, map[string]any{"w-ada": idleRec("term_ada", frozen-20*60_000-1, nil)})
	if out := f.pollAt(t, c, freeze); len(out.Liveness) != 1 || out.Liveness[0] != "Ada" {
		t.Fatalf("liveness = %v one tick past N, want [Ada]", out.Liveness)
	}
}

func TestLivenessBusyAtGateHoldsAndResetsClock(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := mcfgEntries(t, root, [3]string{"w-ada", "Ada", "coordinator"}, 20)
	f.addTerminal("ada", "claude", "", true)
	f.cursors["w-ada"] = 100
	seedLiveness(t, c, map[string]any{"w-ada": idleRec("term_ada", time.Now().UnixMilli()-ago21min, nil)})
	f.busyRecheck["term_ada"] = true                            // idle at the probe, busy at the full gate
	f.setRecheck("term_ada", screenSpec{tail: claudeBusyEmpty}) // #1924: busy needs the live indicator
	out := f.poll(t, c)
	if len(out.Liveness) != 0 {
		t.Fatalf("liveness = %v, want none", out.Liveness)
	}
	wantHeldContaining(t, out, "Ada", "liveness", "a running turn is drawn above the prompt")
	if out.Adopted {
		t.Error("a held liveness nudge holds the token like a held mention nudge")
	}
	wantNoSend(t, f)
	if _, has := readLiveness(t, c)["w-ada"]["idleSince"]; has {
		t.Error("the clock was not reset")
	}
}

func TestLivenessDraftAtGateHoldsAndResetsClock(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := mcfgEntries(t, root, [3]string{"w-ada", "Ada", "coordinator"}, 20)
	f.addTerminal("ada", "claude", "", true)
	f.cursors["w-ada"] = 100
	seedLiveness(t, c, map[string]any{"w-ada": idleRec("term_ada", time.Now().UnixMilli()-ago21min, nil)})
	typed := "typed mid-watch"
	f.setRecheck("term_ada", screenSpec{tail: withPrompt(claudeIdle, "❯", "typed mid-watch"), draft: &typed})
	out := f.poll(t, c)
	wantHeldContaining(t, out, "Ada", "liveness", "prompt not empty")
	wantNoSend(t, f)
	if _, has := readLiveness(t, c)["w-ada"]["idleSince"]; has {
		t.Error("the clock was not reset")
	}
	if strings.Contains(strings.Join(f.logs, "\n"), "typed mid-watch") {
		t.Error("the reason never quotes the text")
	}
}

func TestLivenessUnreadableAtGateHolds(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := mcfgEntries(t, root, [3]string{"w-ada", "Ada", "coordinator"}, 20)
	f.addTerminal("ada", "claude", "", true)
	f.cursors["w-ada"] = 100
	seedLiveness(t, c, map[string]any{"w-ada": idleRec("term_ada", time.Now().UnixMilli()-ago21min, nil)})
	f.setRecheck("term_ada", screenSpec{unreadable: true})
	out := f.poll(t, c)
	wantHeldContaining(t, out, "Ada", "liveness", "screen unreadable")
	wantNoSend(t, f)
}

func TestLivenessAmbiguousAtSendHolds(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := mcfgEntries(t, root, [3]string{"w-ada", "Ada", "coordinator"}, 20)
	f.addTerminal("ada", "claude", "", true)
	f.cursors["w-ada"] = 100
	seedLiveness(t, c, map[string]any{"w-ada": idleRec("term_ada", time.Now().UnixMilli()-ago21min, nil)})
	f.hideFromList = 2 // the probe's list sees it; the chain's fresh list does not
	out := f.poll(t, c)
	wantHeldContaining(t, out, "Ada", "liveness", "no live agent terminal")
	wantNoSend(t, f)
	if _, has := readLiveness(t, c)["w-ada"]["idleSince"]; has {
		t.Error("the clock was not reset")
	}
}

func TestLivenessClockSurvivesTokenAdoption(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := mcfgEntries(t, root, [3]string{"w-ada", "Ada", "coordinator"}, 20)
	c2 := mcfgEntries(t, root, [3]string{"w-eli", "Eli", ""}, 20)
	c.Roster = append(c.Roster, c2.Roster...)
	f.addTerminal("ada", "claude", "", true)
	f.addTerminal("eli", "claude", "", true)
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{})} // a mention nudge adopts the token
	f.cursors["w-ada"] = 100
	seeded := time.Now().UnixMilli() - 5*60_000 // behind, but only 5 idle minutes — not yet eligible
	seedLiveness(t, c, map[string]any{"w-ada": idleRec("term_ada", seeded, nil)})
	out := f.poll(t, c)
	if len(out.Nudged) != 1 || out.Nudged[0] != "Eli" || len(out.Liveness) != 0 || !out.Adopted {
		t.Fatalf("outcome = %+v", out)
	}
	if got := readLiveness(t, c)["w-ada"]["idleSince"].(float64); int64(got) != seeded { // EXACTLY untouched by adoption
		t.Fatalf("idleSince = %v, want %d untouched", got, seeded)
	}
}

func TestLivenessClockSurvivesRestartMidWait(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := mcfgEntries(t, root, [3]string{"w-ada", "Ada", "coordinator"}, 20)
	f.addTerminal("ada", "claude", "", true)
	f.cursors["w-ada"] = 100
	seeded := time.Now().UnixMilli() - 19*60_000 // one minute short of N
	seedLiveness(t, c, map[string]any{"w-ada": idleRec("term_ada", seeded, nil)})
	if out := f.poll(t, c); len(out.Liveness) != 0 { // 19 < 20
		t.Fatalf("liveness = %v", out.Liveness)
	}
	if got := readLiveness(t, c)["w-ada"]["idleSince"].(float64); int64(got) != seeded { // a poll is a restart: same episode
		t.Fatalf("idleSince = %v, want %d", got, seeded)
	}
	if out := f.poll(t, c); len(out.Liveness) != 0 {
		t.Fatalf("liveness = %v", out.Liveness)
	}
	if got := readLiveness(t, c)["w-ada"]["idleSince"].(float64); int64(got) != seeded { // still the same episode
		t.Fatalf("idleSince = %v, want %d (not restarted)", got, seeded)
	}
}

func TestLivenessBusyProbeResetsClockKeepsFrontier(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := mcfgEntries(t, root, [3]string{"w-ada", "Ada", "coordinator"}, 20)
	f.addTerminal("ada", "claude", "", true)
	f.busy["term_ada"] = true
	f.setScreen("term_ada", screenSpec{tail: codexBusyEmpty}) // #1924: a busy hold needs the live indicator on screen
	f.cursors["w-ada"] = 100
	seedLiveness(t, c, map[string]any{"w-ada": idleRec("term_ada", time.Now().UnixMilli()-ago21min, coveredPtr(90))})
	out := f.poll(t, c)
	if len(out.Liveness) != 0 {
		t.Fatalf("liveness = %v", out.Liveness)
	}
	if len(out.Held) != 0 { // a probe reset is silent — no delivery was attempted
		t.Fatalf("held = %v, want none", out.Held)
	}
	wantNoSend(t, f)
	rec := readLiveness(t, c)["w-ada"]
	if _, has := rec["idleSince"]; has {
		t.Error("the clock was not reset")
	}
	if rec["coveredFrontier"].(float64) != 90 { // the delivered frontier still dedupes
		t.Errorf("coveredFrontier = %v, want 90 preserved", rec["coveredFrontier"])
	}
	delete(f.busy, "term_ada")
	f.setScreen("term_ada", screenSpec{tail: claudeIdle}) // the turn finished: the probe can prove idle again (family-matched)
	if out := f.poll(t, c); len(out.Liveness) != 0 {      // clock restarted at the idle poll — N more minutes needed
		t.Fatalf("liveness = %v too early", out.Liveness)
	}
	if _, has := readLiveness(t, c)["w-ada"]["idleSince"]; !has {
		t.Error("the restarted clock must be recorded")
	}
}

func TestLivenessHalfTypedPromptAtProbeResetsSilently(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := mcfgEntries(t, root, [3]string{"w-ada", "Ada", "coordinator"}, 20)
	f.addTerminal("ada", "claude", "", true)
	typed := "Holger typing here"
	f.setScreen("term_ada", screenSpec{tail: withPrompt(claudeIdle, "❯", "Holger typing here"), draft: &typed})
	f.cursors["w-ada"] = 100
	seedLiveness(t, c, map[string]any{"w-ada": idleRec("term_ada", time.Now().UnixMilli()-ago21min, nil)})
	out := f.poll(t, c)
	if len(out.Liveness) != 0 || len(out.Held) != 0 {
		t.Fatalf("outcome = %+v, want a silent reset", out)
	}
	wantNoSend(t, f)
	if _, has := readLiveness(t, c)["w-ada"]["idleSince"]; has {
		t.Error("the clock was not reset")
	}
}

func TestLivenessAmbiguousProbeResetsSilently(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := mcfgEntries(t, root, [3]string{"w-ada", "Ada", "coordinator"}, 20)
	f.addTerminal("ada", "claude", "term_a", true)
	f.addTerminal("ada", "codex", "term_b", true)
	f.cursors["w-ada"] = 100
	seedLiveness(t, c, map[string]any{"w-ada": idleRec("term_a", time.Now().UnixMilli()-ago21min, nil)})
	out := f.poll(t, c)
	if len(out.Liveness) != 0 || len(out.Held) != 0 { // the watch never resolved a terminal; nothing was owed a send
		t.Fatalf("outcome = %+v, want a silent reset", out)
	}
	wantNoSend(t, f)
	if _, has := readLiveness(t, c)["w-ada"]["idleSince"]; has {
		t.Error("the clock was not reset")
	}
}

func TestLivenessUnchangedBacklogNeverRenudges(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := mcfgEntries(t, root, [3]string{"w-ada", "Ada", "coordinator"}, 20)
	f.addTerminal("ada", "claude", "", true)
	f.cursors["w-ada"] = 100
	seedLiveness(t, c, map[string]any{"w-ada": idleRec("term_ada", time.Now().UnixMilli()-ago21min, nil)})
	if out := f.poll(t, c); len(out.Liveness) != 1 || out.Liveness[0] != "Ada" { // delivered at head 120
		t.Fatalf("liveness = %v", out.Liveness)
	}
	// Twenty more idle minutes pass, but the head has not moved past the frontier.
	seedLiveness(t, c, map[string]any{"w-ada": idleRec("term_ada", time.Now().UnixMilli()-ago21min, coveredPtr(120))})
	if out := f.poll(t, c); len(out.Liveness) != 0 {
		t.Fatalf("liveness = %v, want none (frontier covered)", out.Liveness)
	}
	if len(f.sent) != 1 {
		t.Fatalf("sends = %d, want 1", len(f.sent))
	}
	// New content past the frontier: eligible again once idle > N measured from the send.
	f.head = 130
	seedLiveness(t, c, map[string]any{"w-ada": idleRec("term_ada", time.Now().UnixMilli()-ago21min, coveredPtr(120))})
	if out := f.poll(t, c); len(out.Liveness) != 1 || out.Liveness[0] != "Ada" {
		t.Fatalf("liveness = %v, want re-armed [Ada]", out.Liveness)
	}
	if len(f.sent) != 2 || !strings.Contains(f.sent[1].text, "#130") {
		t.Fatalf("second send = %v, want the new head named", f.sent)
	}
}

func TestLivenessMentionFirstCollapseSkipsLivenessSend(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := mcfgEntries(t, root, [3]string{"w-ada", "Ada", "coordinator"}, 20)
	f.addTerminal("ada", "claude", "", true)
	f.attention.workers = []AttentionItem{worker("w-ada", "Ada", workerOpts{mentions: ip(1), unread: ip(4)})}
	f.cursors["w-ada"] = 100
	seedLiveness(t, c, map[string]any{"w-ada": idleRec("term_ada", time.Now().UnixMilli()-ago21min, nil)})
	out := f.poll(t, c)
	if len(out.Nudged) != 1 || out.Nudged[0] != "Ada" || len(out.Liveness) != 0 {
		t.Fatalf("outcome = %+v", out)
	}
	if len(f.sent) != 1 { // exactly one send — the mention nudge
		t.Fatalf("sends = %d, want 1", len(f.sent))
	}
	if !strings.Contains(f.sent[0].text, "relevant unread (1 mention") { // the mention shape, not the liveness text
		t.Fatalf("text = %q, want the mention shape", f.sent[0].text)
	}
	rec := readLiveness(t, c)["w-ada"]
	if rec["idleSince"].(float64) < float64(time.Now().UnixMilli()-60_000) { // clock restarted at the send
		t.Errorf("idleSince = %v, want restarted", rec["idleSince"])
	}
	if _, has := rec["coveredFrontier"]; has { // no liveness frontier claimed
		t.Error("a mention nudge claims no liveness frontier")
	}
}

func TestLivenessHeldMentionRestartsClock(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := mcfgEntries(t, root, [3]string{"w-ada", "Ada", "coordinator"}, 20)
	f.addTerminal("ada", "claude", "", true)
	f.busy["term_ada"] = true
	f.setScreen("term_ada", screenSpec{tail: codexBusyEmpty}) // #1924: a busy hold needs the live indicator on screen
	f.attention.workers = []AttentionItem{worker("w-ada", "Ada", workerOpts{})}
	f.cursors["w-ada"] = 100
	seedLiveness(t, c, map[string]any{"w-ada": idleRec("term_ada", time.Now().UnixMilli()-ago21min, nil)})
	out := f.poll(t, c)
	if out.Held["Ada"] == "" { // the mention hold
		t.Fatal("the mention hold must fire")
	}
	if len(out.Liveness) != 0 {
		t.Fatalf("liveness = %v, want none", out.Liveness)
	}
	if _, has := readLiveness(t, c)["w-ada"]["idleSince"]; has {
		t.Error("a held mention is non-idle evidence: the clock restarts")
	}
}

func TestLivenessReadWhileWaitingDropsNudge(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := mcfgEntries(t, root, [3]string{"w-ada", "Ada", "coordinator"}, 20)
	f.addTerminal("ada", "claude", "", true)
	f.cursors["w-ada"] = 100
	seedLiveness(t, c, map[string]any{"w-ada": idleRec("term_ada", time.Now().UnixMilli()-ago21min, nil)})
	f.freshCursors["w-ada"] = 120 // the worker caught up while the gates ran
	out := f.poll(t, c)
	if len(out.Liveness) != 0 || len(out.LivenessDropped) != 1 || out.LivenessDropped[0] != "Ada" {
		t.Fatalf("outcome = %+v, want liveness dropped [Ada]", out)
	}
	if !strings.Contains(DescribeOutcome(out), "liveness dropped") {
		t.Error("describeOutcome must name the liveness drop")
	}
	wantNoSend(t, f)
	rec := readLiveness(t, c)["w-ada"]
	if _, has := rec["idleSince"]; has { // reset — the worker demonstrably acted
		t.Error("the clock was not reset")
	}
	if _, has := rec["coveredFrontier"]; has {
		t.Error("no frontier claimed on a drop")
	}
}

func TestLivenessHeldTokenNeitherRestartsNorResets(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := mcfgEntries(t, root, [3]string{"w-ada", "Ada", "coordinator"}, 20)
	c2 := mcfgEntries(t, root, [3]string{"w-baloo", "Baloo", ""}, 20)
	c.Roster = append(c.Roster, c2.Roster...)
	f.addTerminal("ada", "claude", "", true)
	f.addTerminal("baloo", "claude", "", true)
	f.busy["term_baloo"] = true
	f.setScreen("term_baloo", screenSpec{tail: claudeBusyEmpty}) // #1924: a busy hold needs the live indicator on screen
	f.attention.workers = []AttentionItem{worker("w-baloo", "Baloo", workerOpts{})}
	f.cursors["w-ada"] = 100
	seeded := time.Now().UnixMilli() - 10*60_000
	seedLiveness(t, c, map[string]any{"w-ada": idleRec("term_ada", seeded, nil)})
	first := f.poll(t, c)
	if first.Adopted { // Baloo's mention hold holds the token
		t.Fatal("the held poll must not adopt")
	}
	if len(first.Liveness) != 0 { // 10 idle minutes < N — clock pending
		t.Fatalf("liveness = %v", first.Liveness)
	}
	if got := readLiveness(t, c)["w-ada"]["idleSince"].(float64); int64(got) != seeded { // untouched by the held poll
		t.Fatalf("idleSince = %v, want %d untouched", got, seeded)
	}
	delete(f.busy, "term_baloo")
	seedLiveness(t, c, map[string]any{"w-ada": idleRec("term_ada", time.Now().UnixMilli()-ago21min, nil)})
	second := f.poll(t, c)
	if len(second.Nudged) != 1 || second.Nudged[0] != "Baloo" || len(second.Liveness) != 1 || second.Liveness[0] != "Ada" || !second.Adopted {
		t.Fatalf("second = %+v, want Baloo nudged and Ada liveness-woken", second)
	}
}

// failAfterArming fails every ChannelHead call once the wrapped cursor reads
// for the named worker have happened — the TS suite armed a 502 on the
// recompute's head query via run().
type failAfterArming struct {
	*fakeCLIs
	c       *Config
	armedBy string
	armed   bool
}

func (w *failAfterArming) ReadCursor(ctx context.Context, workerID string) (CursorResult, error) {
	if workerID == w.armedBy {
		w.armed = true
	}
	return w.fakeCLIs.ReadCursor(ctx, workerID)
}

func (w *failAfterArming) ChannelHead(ctx context.Context, channelRef string) (int, error) {
	if w.armed {
		return 0, fmt.Errorf("gateway 502")
	}
	return w.fakeCLIs.ChannelHead(ctx, channelRef)
}

func TestLivenessDeliveredNudgePersistedAtOnce(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := mcfgEntries(t, root, [3]string{"w-ada", "Ada", "coordinator"}, 20)
	c.Roster = append(c.Roster, RosterEntry{Worker: "w-yuri", Name: "Yuri", Lifecycle: true,
		Worktree: filepath.Join(root, "workspaces", "yuri")})
	f.addTerminal("ada", "claude", "", true)
	f.addTerminal("yuri", "claude", "", true)
	f.cursors["w-ada"] = 100
	f.cursors["w-yuri"] = 100
	seedLiveness(t, c, map[string]any{
		"w-ada":  idleRec("term_ada", time.Now().UnixMilli()-ago21min, nil),
		"w-yuri": idleRec("term_yuri", time.Now().UnixMilli()-ago21min, nil),
	})
	// Ada delivers; Yuri's recompute then fails hard — a failed fresh check fails the poll.
	wrapped := &failAfterArming{fakeCLIs: f, c: c, armedBy: "w-yuri"}
	if _, err := PollOnce(t.Context(), c, wrapped, f.orca, func(string) {}, nil); err == nil {
		t.Fatal("the armed recompute failure must fail the poll")
	}
	wantHandles(t, f, "term_ada")
	if readLiveness(t, c)["w-ada"]["coveredFrontier"].(float64) != 120 { // persisted at once
		t.Fatal("Ada's frontier must persist before Yuri's failure")
	}
	out := f.poll(t, c)
	if len(out.Liveness) != 1 || out.Liveness[0] != "Yuri" { // Yuri delivers on the retry
		t.Fatalf("liveness = %v, want [Yuri]", out.Liveness)
	}
	wantHandles(t, f, "term_ada", "term_yuri")
	n := 0
	for _, s := range f.sent {
		if s.handle == "term_ada" {
			n++
		}
	}
	if n != 1 { // Ada NOT re-nudged
		t.Fatalf("term_ada sent %d times, want 1", n)
	}
}

func TestLivenessFailClosedNullCursor(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := mcfgEntries(t, root, [3]string{"w-ada", "Ada", "coordinator"}, 20)
	f.addTerminal("ada", "claude", "", true)
	f.cursors["w-ada"] = 100
	seeded := time.Now().UnixMilli() - ago21min
	seedLiveness(t, c, map[string]any{"w-ada": idleRec("term_ada", seeded, nil)})
	f.cursorNulls["w-ada"] = true // channelReadState: null — unknown, not zero
	out := f.poll(t, c)
	if len(out.Liveness) != 0 || len(f.sent) != 0 || !out.Adopted {
		t.Fatalf("outcome = %+v: only the candidate is suppressed, the poll is healthy", out)
	}
	if !containsStr(f.logs, "liveness") { // the reason logged
		t.Error("the suppressed-candidate reason must be logged")
	}
	f.logs = nil
	f.poll(t, c)
	if containsStr(f.logs, "liveness") { // once per reason, not per poll
		t.Error("the reason logged again")
	}
	if got := readLiveness(t, c)["w-ada"]["idleSince"].(float64); int64(got) != seeded { // the clock kept aging through the outage
		t.Fatalf("idleSince = %v, want %d", got, seeded)
	}
	delete(f.cursorNulls, "w-ada")
	if out := f.poll(t, c); len(out.Liveness) != 1 || out.Liveness[0] != "Ada" { // fires on recovery — the episode never restarted
		t.Fatalf("liveness = %v", out.Liveness)
	}
}

func TestLivenessFailClosedHeadQuery(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := mcfgEntries(t, root, [3]string{"w-ada", "Ada", "coordinator"}, 20)
	f.addTerminal("ada", "claude", "", true)
	f.cursors["w-ada"] = 100
	seedLiveness(t, c, map[string]any{"w-ada": idleRec("term_ada", time.Now().UnixMilli()-ago21min, nil)})
	f.headOk = false
	if out := f.poll(t, c); len(out.Liveness) != 0 {
		t.Fatalf("liveness = %v, want none", out.Liveness)
	}
	wantNoSend(t, f)
	if out := f.poll(t, c); !out.Adopted { // still a healthy poll
		t.Fatal("a failed head query suppresses the candidate, not the poll")
	}
}

func TestLivenessUntrackedFlaggedTerminalStillWakes(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := mcfgEntries(t, root, [3]string{"w-ada", "Ada", "coordinator"}, 20)
	f.addTerminal("ada", "claude", "", false) // lastOutputAt null — tracking lost
	f.cursors["w-ada"] = 100
	seedLiveness(t, c, map[string]any{"w-ada": idleRec("term_ada", time.Now().UnixMilli()-ago21min, nil)})
	if out := f.poll(t, c); len(out.Liveness) != 1 || out.Liveness[0] != "Ada" {
		t.Fatalf("liveness = %v", out.Liveness)
	}
}

func TestLivenessReplacedTerminalRestartsClock(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := mcfgEntries(t, root, [3]string{"w-ada", "Ada", "coordinator"}, 20)
	f.addTerminal("ada", "claude", "term_new", true)
	f.cursors["w-ada"] = 100
	seedLiveness(t, c, map[string]any{"w-ada": idleRec("term_old", time.Now().UnixMilli()-ago21min, nil)})
	out := f.poll(t, c)
	if len(out.Liveness) != 0 { // handle mismatch ⇒ fresh clock on the new terminal
		t.Fatalf("liveness = %v", out.Liveness)
	}
	rec := readLiveness(t, c)["w-ada"]
	if rec["handle"] != "term_new" {
		t.Errorf("handle = %v, want term_new", rec["handle"])
	}
	if rec["idleSince"].(float64) < float64(time.Now().UnixMilli()-60_000) {
		t.Errorf("idleSince = %v, want a fresh clock", rec["idleSince"])
	}
}

func TestLivenessRemovingFlagPrunesRecords(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := mcfgEntries(t, root, [3]string{"w-ada", "Ada", "coordinator"}, 20)
	f.addTerminal("ada", "claude", "", true)
	f.cursors["w-ada"] = 100
	seedLiveness(t, c, map[string]any{
		"w-ada":  idleRec("term_ada", time.Now().UnixMilli()-ago21min, nil),
		"w-gone": idleRec("x", 1, nil),
	})
	unflagged := mcfgEntries(t, root, [3]string{"w-ada", "Ada", ""}, 20) // same stateDir
	f.poll(t, unflagged)
	blob, err := os.ReadFile(filepath.Join(unflagged.StateDir, "liveness.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(blob)) != "{}" { // pruned: no flagged workers ⇒ no records
		t.Fatalf("liveness.json = %s, want {}", blob)
	}
}

// ── #1829 local-seat cycle 1 (Xylon #9736) ─────────────────────────────

func TestLivenessP11ErrorsWithDataNeverLive(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := mcfgEntries(t, root, [3]string{"w-ada", "Ada", "coordinator"}, 20)
	f.addTerminal("ada", "claude", "", true)
	f.cursors["w-ada"] = 100
	seeded := time.Now().UnixMilli() - ago21min
	seedLiveness(t, c, map[string]any{"w-ada": idleRec("term_ada", seeded, nil)})
	f.cursorErrors["w-ada"] = true // partial data WITH an error rides the same integer
	out := f.poll(t, c)
	if len(out.Liveness) != 0 {
		t.Fatalf("liveness = %v, want none", out.Liveness)
	}
	wantNoSend(t, f)
	if got := readLiveness(t, c)["w-ada"]["idleSince"].(float64); int64(got) != seeded { // fail-closed, not a reset
		t.Fatalf("idleSince = %v, want %d kept", got, seeded)
	}
	delete(f.cursorErrors, "w-ada")
	if out := f.poll(t, c); len(out.Liveness) != 1 || out.Liveness[0] != "Ada" { // fires on recovery
		t.Fatalf("liveness = %v", out.Liveness)
	}
}

func TestLivenessP11NegativeCursorNoCandidate(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := mcfgEntries(t, root, [3]string{"w-ada", "Ada", "coordinator"}, 20)
	f.addTerminal("ada", "claude", "", true)
	f.cursors["w-ada"] = -5 // parsed, integer, nonsense
	seedLiveness(t, c, map[string]any{"w-ada": idleRec("term_ada", time.Now().UnixMilli()-ago21min, nil)})
	if out := f.poll(t, c); len(out.Liveness) != 0 {
		t.Fatalf("liveness = %v", out.Liveness)
	}
	wantNoSend(t, f)
}

func TestLivenessP11NonZeroExitFailsRecompute(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := mcfgEntries(t, root, [3]string{"w-ada", "Ada", "coordinator"}, 20)
	f.addTerminal("ada", "claude", "", true)
	f.cursors["w-ada"] = 100
	seedLiveness(t, c, map[string]any{"w-ada": idleRec("term_ada", time.Now().UnixMilli()-ago21min, nil)})
	// Candidacy passes on the clean read; the recompute's head query then fails.
	wrapped := &failAfterArming{fakeCLIs: f, c: c, armedBy: "w-ada"}
	if _, err := PollOnce(t.Context(), c, wrapped, f.orca, func(string) {}, nil); err == nil {
		t.Fatal("the failed recompute must fail the poll")
	}
	wantNoSend(t, f)
}

func TestLivenessP22CorruptRecordNeverSuppresses(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := mcfgEntries(t, root, [3]string{"w-ada", "Ada", "coordinator"}, 20)
	f.addTerminal("ada", "claude", "", true)
	f.cursors["w-ada"] = 100
	// A huge frontier (above any real seq) and a string handle: both drop on
	// read, so the worker cannot look covered forever (fail-closed direction).
	seedLiveness(t, c, map[string]any{"w-ada": map[string]any{
		"handle": "term_ada", "idleSince": time.Now().UnixMilli() - ago21min, "coveredFrontier": 9007199254740991}})
	if out := f.poll(t, c); len(out.Liveness) != 1 || out.Liveness[0] != "Ada" {
		t.Fatalf("liveness = %v", out.Liveness)
	}
	if readLiveness(t, c)["w-ada"]["coveredFrontier"].(float64) != 120 { // the honest frontier the send recorded
		t.Fatal("the corrupt frontier must be replaced by the honest one")
	}
}

func TestLivenessP22CorruptTopLevelReadsFresh(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := mcfgEntries(t, root, [3]string{"w-ada", "Ada", "coordinator"}, 20)
	c2 := mcfgEntries(t, root, [3]string{"w-baloo", "Baloo", "coordinator"}, 20)
	c.Roster = append(c.Roster, c2.Roster...)
	f.addTerminal("ada", "claude", "", true)
	f.addTerminal("baloo", "claude", "", true)
	f.cursors["w-ada"] = 100
	if err := os.MkdirAll(c.StateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(c.StateDir, "liveness.json"), []byte("null"), 0o644); err != nil { // top-level null once crashed the reader
		t.Fatal(err)
	}
	if out := f.poll(t, c); len(out.Liveness) != 0 { // fresh clocks, no throw
		t.Fatalf("liveness = %v", out.Liveness)
	}
	if _, has := readLiveness(t, c)["w-ada"]["idleSince"]; !has {
		t.Fatal("a fresh clock must be recorded")
	}
	seedLiveness(t, c, map[string]any{"w-ada": 5, "w-baloo": []string{"x"}}) // non-object records
	out := f.poll(t, c)
	if len(out.Liveness) != 0 || len(out.Held) != 0 {
		t.Fatalf("outcome = %+v", out)
	}
	rec := readLiveness(t, c)["w-ada"]
	if rec["idleSince"].(float64) < float64(time.Now().UnixMilli()-60_000) { // re-seeded by the probe, not carried
		t.Error("the corrupt record was carried instead of re-seeded")
	}
}

func TestLivenessP24TerminalReplacedBetweenProbeAndChain(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := mcfgEntries(t, root, [3]string{"w-ada", "Ada", "coordinator"}, 20)
	f.addTerminal("ada", "claude", "", true)
	f.cursors["w-ada"] = 100
	seedLiveness(t, c, map[string]any{"w-ada": idleRec("term_ada", time.Now().UnixMilli()-ago21min, nil)})
	f.renameFromList = 2 // the probe's list shows term_ada; the chain's shows term_ada_r
	out := f.poll(t, c)
	if len(out.Liveness) != 0 || len(out.Held) != 0 { // a reset, not a delivery failure
		t.Fatalf("outcome = %+v", out)
	}
	wantNoSend(t, f)
	if _, has := readLiveness(t, c)["w-ada"]["idleSince"]; has {
		t.Error("the clock was not reset")
	}
}

func TestLivenessP25FirstRecordFromMentionCollapseCarriesHandle(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := mcfgEntries(t, root, [3]string{"w-ada", "Ada", "coordinator"}, 20)
	f.addTerminal("ada", "claude", "", true)
	f.attention.workers = []AttentionItem{worker("w-ada", "Ada", workerOpts{mentions: ip(1), unread: ip(4)})}
	f.cursors["w-ada"] = 100
	out := f.poll(t, c) // no seeded record: the collapse write is its first
	if len(out.Nudged) != 1 || out.Nudged[0] != "Ada" {
		t.Fatalf("nudged = %v", out.Nudged)
	}
	born := readLiveness(t, c)["w-ada"]
	if born["handle"] != "term_ada" {
		t.Fatalf("handle = %v, want term_ada", born["handle"])
	}
	at := born["idleSince"].(float64)
	if at < float64(time.Now().UnixMilli()-60_000) {
		t.Fatalf("idleSince = %v, want now", at)
	}
	f.attention.workers = nil // nothing new; the clock must survive the next probe
	f.poll(t, c)
	if got := readLiveness(t, c)["w-ada"]["idleSince"].(float64); got != at { // NOT restarted by a handle mismatch
		t.Fatalf("idleSince = %v, want %v", got, at)
	}
}

func TestLivenessP27WatchMinutesTypeValidated(t *testing.T) {
	root := t.TempDir()
	// 1e307 × 60_000 overflows to Infinity — the class whose comparison against
	// elapsed time silently disables delivery. (A finite-but-huge window stays
	// legal: coherent config, equivalent to the rule off.)
	for _, bad := range []any{true, "20", 1e307, []any{20}} {
		if _, err := LoadConfig(writeConfig(t, root, map[string]any{"coordinatorWatchMinutes": bad})); err == nil {
			t.Errorf("coordinatorWatchMinutes %v must be refused", bad)
		}
	}
}
