package teamrouter

// Ported 1:1 from router.test.ts describe('failures leave the token alone')
// — send refusals, failed polls, and the single-writer lock suite (L1–L3,
// C1–C4, @codex P2 #1490, Chip's QA race).

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func lockDirOf(c *Config) string { return filepath.Join(c.StateDir, ".writer-lock") }

func writeOwner(t *testing.T, c *Config, rec any) {
	t.Helper()
	if err := os.MkdirAll(lockDirOf(c), 0o755); err != nil {
		t.Fatal(err)
	}
	var blob []byte
	switch v := rec.(type) {
	case string:
		blob = []byte(v)
	default:
		blob, _ = json.Marshal(v)
	}
	if err := os.WriteFile(filepath.Join(lockDirOf(c), "owner"), blob, 0o644); err != nil {
		t.Fatal(err)
	}
}

// bootTime approximates the system boot time the way os.uptime() does in the
// TS suite (procfs on linux, a process-lifetime fallback elsewhere — the
// lock engine compares boot equality, not precision).
func bootTime() int64 {
	if data, err := os.ReadFile("/proc/uptime"); err == nil {
		if fields := strings.Fields(string(data)); len(fields) > 0 {
			if upSec, err := strconv.ParseFloat(fields[0], 64); err == nil {
				return time.Now().Add(-time.Duration(upSec * float64(time.Second))).UnixMilli()
			}
		}
	}
	return time.Now().UnixMilli()
}

func TestFailuresRejectedSendHoldsToken(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"})
	f.addTerminal("eli", "claude", "", true)
	f.reject["term_eli"] = true
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{})}
	out := f.poll(t, c)
	if len(out.Held) != 1 || out.Held["Eli"] == "" {
		t.Fatalf("held = %v, want only Eli", out.Held)
	}
	if tok := adoptedToken(t, c); tok != "" {
		t.Fatalf("token = %q, want none (not recorded as delivered)", tok)
	}
	delete(f.reject, "term_eli")
	out = f.poll(t, c)
	if len(out.Nudged) != 1 || out.Nudged[0] != "Eli" {
		t.Fatalf("nudged = %v, want [Eli] once the send works", out.Nudged)
	}
}

func TestFailuresFailedAttentionPollChangesNothing(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"})
	f.addTerminal("eli", "claude", "", true)
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{})}
	f.poll(t, c)
	f.attentionOk = false
	if _, err := PollOnce(t.Context(), c, f, f.orca, func(string) {}, nil); err == nil {
		t.Fatal("a failed attention poll must throw")
	}
	if tok := adoptedToken(t, c); tok != token1 {
		t.Fatalf("token = %q, want the previously adopted %q", tok, token1)
	}
}

func TestLocksSecondConcurrentRunRefused(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"})
	f.addTerminal("eli", "claude", "", true)
	writeOwner(t, c, strconv.Itoa(os.Getpid()))
	if _, err := PollOnce(t.Context(), c, f, f.orca, func(string) {}, nil); err == nil {
		t.Fatal("a live owner's lock must refuse the run")
	}
}

func TestLocksForeignUserLiveLockNeverReclaimed(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"})
	f.addTerminal("eli", "claude", "", true)
	writeOwner(t, c, "1") // launchd/init: alive, not ours (kill → EPERM)
	if _, err := PollOnce(t.Context(), c, f, f.orca, func(string) {}, nil); err == nil {
		t.Fatal("another user's live lock must never be reclaimed")
	}
}

func TestLocksL1PreRebootLockReclaimed(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"})
	f.addTerminal("eli", "claude", "", true)
	writeOwner(t, c, map[string]any{"pid": os.Getpid(), "boot": 0, "startedAt": time.Now().UnixMilli()})
	out := f.poll(t, c)
	if !out.Adopted {
		t.Fatal("a pre-reboot lock is reclaimed, even if its pid is alive now (@codex P2, #1490)")
	}
}

func TestLocksL2ReusedPidReclaimed(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"})
	f.addTerminal("eli", "claude", "", true)
	writeOwner(t, c, map[string]any{"pid": os.Getpid(), "boot": bootTime(), "startedAt": 0})
	out := f.poll(t, c)
	if !out.Adopted {
		t.Fatal("a reused pid (started after the lock) is not the owner: reclaimed")
	}
}

func TestLocksL3LiveOwnerNeverDisplaced(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"})
	f.addTerminal("eli", "claude", "", true)
	rec := map[string]any{"pid": os.Getpid(), "boot": bootTime(), "startedAt": time.Now().UnixMilli()}
	writeOwner(t, c, rec)
	if _, err := PollOnce(t.Context(), c, f, f.orca, func(string) {}, nil); err == nil {
		t.Fatal("a genuinely live owner is never displaced")
	}
	blob, err := os.ReadFile(filepath.Join(lockDirOf(c), "owner"))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(blob, &got); err != nil {
		t.Fatal(err)
	}
	if int(got["pid"].(float64)) != os.Getpid() {
		t.Fatalf("the live record was modified: %s", blob)
	}
}

func TestLocksC1StaleJudgementCannotBreakLiveLock(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"})
	f.addTerminal("eli", "claude", "", true)
	writeOwner(t, c, "999999") // stale: judged dead by both racers
	if verdict, err := BreakStaleLock(lockDirOf(c), "999999"); err != nil || verdict != "retry" {
		t.Fatalf("racer A break = (%q, %v), want retry", verdict, err)
	}
	writeOwner(t, c, "A-live-record") // ...and takes the lock
	if verdict, err := BreakStaleLock(lockDirOf(c), "999999"); err != nil || verdict != "busy" {
		t.Fatalf("racer B break = (%q, %v), want busy (same outdated judgement)", verdict, err)
	}
	blob, err := os.ReadFile(filepath.Join(lockDirOf(c), "owner"))
	if err != nil || string(blob) != "A-live-record" {
		t.Fatalf("the live lock must be restored, got %q (%v)", blob, err)
	}
	assertNoTombs(t, c)
}

func assertNoTombs(t *testing.T, c *Config) {
	t.Helper()
	entries, err := os.ReadDir(c.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tomb-") {
			t.Fatalf("unexpected tomb %s", e.Name())
		}
	}
}

func TestLocksC2C3StaleBreaksCleanlyBrokenReadsFree(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"})
	f.addTerminal("eli", "claude", "", true)
	writeOwner(t, c, "999999")
	if verdict, err := BreakStaleLock(lockDirOf(c), "999999"); err != nil || verdict != "retry" {
		t.Fatalf("stale break = (%q, %v), want retry", verdict, err)
	}
	if _, err := os.Stat(lockDirOf(c)); !os.IsNotExist(err) {
		t.Fatalf("the broken lock dir must be gone, stat err = %v", err)
	}
	assertNoTombs(t, c)
	if verdict, err := BreakStaleLock(lockDirOf(c), "999999"); err != nil || verdict != "retry" {
		t.Fatalf("already-broken = (%q, %v), want retry (reads as free)", verdict, err)
	}
}

// lockSwapServer wraps the fake Server so the first attention poll swaps the
// lock's owner mid-poll (C4: the run loses its lock).
type lockSwapServer struct {
	*fakeCLIs
	c    *Config
	done bool
}

func (w *lockSwapServer) AttentionPage(ctx context.Context, req AttentionRequest) (AttentionPage, error) {
	if !w.done {
		w.done = true
		writeOwner(w.t, w.c, "someone-else")
	}
	return w.fakeCLIs.AttentionPage(ctx, req)
}

func TestLocksC4LostLockStopsBeforeSendOrAdoption(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"})
	f.addTerminal("eli", "claude", "", true)
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{})}
	wrapped := &lockSwapServer{fakeCLIs: f, c: c}
	_, err := PollOnce(t.Context(), c, wrapped, f.orca, func(string) {}, nil)
	if err == nil || !strings.Contains(err.Error(), "lost the writer lock") {
		t.Fatalf("err = %v, want the lost-lock refusal", err)
	}
	wantNoSend(t, f)
	if tok := adoptedToken(t, c); tok != "" {
		t.Fatalf("token = %q, want none", tok)
	}
	blob, err := os.ReadFile(filepath.Join(lockDirOf(c), "owner"))
	if err != nil || string(blob) != "someone-else" {
		t.Fatalf("the other lock was deleted or altered: %q (%v)", blob, err)
	}
}

func TestLocksDeadProcessLockReclaimed(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"})
	f.addTerminal("eli", "claude", "", true)
	if err := os.MkdirAll(filepath.Join(c.StateDir, ".writer-lock"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(c.StateDir, ".writer-lock", "owner"), []byte("999999"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := f.poll(t, c)
	if !out.Adopted {
		t.Fatal("a dead process's lock is reclaimed (@codex P2, #1490)")
	}
	if _, err := os.Stat(filepath.Join(c.StateDir, ".writer-lock")); !os.IsNotExist(err) {
		t.Fatalf("the reclaimed lock must be gone, stat err = %v", err)
	}
}

func TestLocksFreshLockNoOwnerRespected(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"})
	f.addTerminal("eli", "claude", "", true)
	lock := filepath.Join(c.StateDir, ".writer-lock")
	if err := os.MkdirAll(lock, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := PollOnce(t.Context(), c, f, f.orca, func(string) {}, nil); err == nil {
		t.Fatal("a fresh lock with no owner record yet is respected")
	}
	old := time.Now().Add(-5 * time.Minute)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	out := f.poll(t, c)
	if !out.Adopted {
		t.Fatal("an old ownerless lock is reclaimed")
	}
}
