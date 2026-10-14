package teamrouter

// Ported 1:1 from router.test.ts describe('loadConfig — liveness
// configuration (#1826)') and describe('#1845 — the terminal pin: a roster
// row may name its MAIN terminal').

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func writeConfig(t *testing.T, root string, over map[string]any) string {
	t.Helper()
	cfgMap := map[string]any{
		"app": "a", "channel": "c", "stateDir": filepath.Join(root, "s"),
		"roster": []any{map[string]string{"worker": "w", "name": "E", "worktree": "/x"}},
	}
	for k, v := range over {
		cfgMap[k] = v
	}
	blob, err := json.Marshal(cfgMap)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "c.json")
	if err := os.WriteFile(path, blob, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadConfigLivenessDefaultsAndAccepts(t *testing.T) {
	root := t.TempDir()
	c, err := LoadConfig(writeConfig(t, root, nil))
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if c.CoordinatorWatchMin != 20 {
		t.Errorf("default coordinatorWatchMinutes = %v, want 20", c.CoordinatorWatchMin)
	}
	c, err = LoadConfig(writeConfig(t, root, map[string]any{"coordinatorWatchMinutes": 5.5}))
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if c.CoordinatorWatchMin != 5.5 {
		t.Errorf("coordinatorWatchMinutes = %v, want 5.5", c.CoordinatorWatchMin)
	}
}

func TestLoadConfigLivenessRejectsBadNumbers(t *testing.T) {
	root := t.TempDir()
	for _, bad := range []any{0, -3, "abc", nil} {
		if _, err := LoadConfig(writeConfig(t, root, map[string]any{"coordinatorWatchMinutes": bad})); err == nil {
			t.Errorf("coordinatorWatchMinutes %v must be refused", bad)
		}
	}
}

func TestLoadConfigRosterFlagsMustBeBooleans(t *testing.T) {
	root := t.TempDir()
	entry := func(over map[string]any) []any {
		m := map[string]any{"worker": "w", "name": "E", "worktree": "/x"}
		for k, v := range over {
			m[k] = v
		}
		return []any{m}
	}
	c, err := LoadConfig(writeConfig(t, root, map[string]any{"roster": entry(map[string]any{"coordinator": true})}))
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if !c.Roster[0].Coordinator {
		t.Error("coordinator flag must survive the load")
	}
	if _, err := LoadConfig(writeConfig(t, root, map[string]any{"roster": entry(map[string]any{"coordinator": "yes"})})); err == nil {
		t.Error("a string coordinator flag must be refused")
	}
	if _, err := LoadConfig(writeConfig(t, root, map[string]any{"roster": entry(map[string]any{"lifecycle": 1})})); err == nil {
		t.Error("a numeric lifecycle flag must be refused")
	}
}

// The pin resolves FIRST, before identity and the screen, so it survives the
// agentIdentity flap and the two-family ambiguity — the Atilla case: five
// busy-misread holds in one day on a terminal whose identity was lost, where
// every screen fallback failed on completed-turn chrome (Yuri #10736).

func pinnedCfg(t *testing.T, root, handle string) *Config {
	t.Helper()
	c := cfgEntries(root, [2]string{"w-eli", "Eli"})
	c.Roster[0].Terminal = handle
	return c
}

func TestPinResolvesTwoAgentWorktree(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	f.addTerminal("eli", "claude", "", true)
	// A codex review spawn in the SAME worktree: two agent identities, one
	// worktree — the identity resolver holds 'ambiguous' without a pin, and
	// the screen fallback would too (both families present = hold).
	now := int64(1700000000)
	f.terminals = append(f.terminals, Terminal{Handle: "term_spawn", WorktreePath: f.terminals[len(f.terminals)-1].WorktreePath,
		AgentIdentity: strPtr("codex"), Connected: true, Writable: true, LastOutputAt: &now})
	f.setScreen("term_spawn", screenSpec{tail: codexIdle})
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{mentions: ip(1), unread: ip(3)})}

	unpinned := cfgEntries(root, [2]string{"w-eli", "Eli"})
	out := f.poll(t, unpinned)
	if !regexp.MustCompile(`ambiguous`).MatchString(out.Held["Eli"]) {
		t.Fatalf("unpinned hold = %q, want ambiguous", out.Held["Eli"])
	}
	wantNoSend(t, f)

	out = f.poll(t, pinnedCfg(t, root, "term_eli"))
	if len(out.Nudged) != 1 || out.Nudged[0] != "Eli" {
		t.Fatalf("nudged = %v, want [Eli] through the pin", out.Nudged)
	}
	wantHandles(t, f, "term_eli")
}

func strPtr(v string) *string { return &v }

func TestPinForeignHandleFallsThroughStale(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	f.addTerminal("eli", "", "", true)
	f.addTerminal("other", "claude", "", true) // exists, but not in Eli's worktree
	// A plain shell screen: the fallback refuses shells, so the stale pin
	// surfaces in the hold reason instead of being silently rescued.
	f.setScreen("term_eli", screenSpec{tail: []string{"orca@orca1:~/ws/eli$ "}})
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{mentions: ip(1), unread: ip(3)})}
	out := f.poll(t, pinnedCfg(t, root, "term_other"))
	if len(out.Nudged) != 0 {
		t.Fatalf("nudged = %v, want none", out.Nudged)
	}
	wantNoSend(t, f)
	if !regexp.MustCompile(`pinned handle is stale`).MatchString(out.Held["Eli"]) {
		t.Fatalf("hold = %q, want the stale-pin reason", out.Held["Eli"])
	}
}

func TestPinAndHealthyIdentityAgree(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	f.addTerminal("eli", "codex", "", true)
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{mentions: ip(1), unread: ip(3)})}
	c := pinnedCfg(t, root, "term_eli")
	out := f.poll(t, c)
	if len(out.Nudged) != 1 || out.Nudged[0] != "Eli" {
		t.Fatalf("nudged = %v, want [Eli]", out.Nudged)
	}
	wantHandles(t, f, "term_eli")
	// The identity flows as the family for the idle gates.
	handle, family, how, err := ResolveTerminal(c.Roster[0], f.terminals, c, f.orca)
	if err != nil {
		t.Fatalf("resolveTerminal: %v", err)
	}
	if handle != "term_eli" || how != "pin" || family != "codex" {
		t.Fatalf("resolve = (%q, %q, %q), want (term_eli, codex, pin)", handle, family, how)
	}
}

func TestPinOnIdentityLostUntrackedDerivesFamilyFromScreen(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	f.addTerminal("eli", "", "term_eli", false) // flap AND untracked (#7398 regime)
	f.setScreen("term_eli", screenSpec{tail: codexIdle})
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{mentions: ip(1), unread: ip(3)})}
	out := f.poll(t, pinnedCfg(t, root, "term_eli"))
	if len(out.Nudged) != 1 || out.Nudged[0] != "Eli" {
		t.Fatalf("nudged = %v, want [Eli]", out.Nudged)
	}
	wantHandles(t, f, "term_eli")
}

func TestPinNeverBypassesGuardChain(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	f.addTerminal("eli", "codex", "", true)
	f.busy["term_eli"] = true
	f.setScreen("term_eli", screenSpec{tail: claudeBusyEmpty}) // #1924: a busy hold needs the live indicator on screen
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{mentions: ip(1), unread: ip(3)})}
	out := f.poll(t, pinnedCfg(t, root, "term_eli"))
	if len(out.Nudged) != 0 {
		t.Fatalf("nudged = %v, want none (a busy pinned terminal holds)", out.Nudged)
	}
	wantNoSend(t, f)
	if out.Held["Eli"] == "" {
		t.Fatal("the pin must still produce a hold reason")
	}
}

func TestLoadConfigPinValidation(t *testing.T) {
	root := t.TempDir()
	write := func(roster any) string {
		return writeConfig(t, root, map[string]any{"roster": roster})
	}
	if _, err := LoadConfig(write([]any{map[string]any{"worker": "w", "name": "E", "worktree": "/x", "terminal": 7}})); err == nil || !regexp.MustCompile(`"terminal" must be a non-empty`).MatchString(err.Error()) {
		t.Fatalf("err = %v, want the terminal-type refusal", err)
	}
	if _, err := LoadConfig(write([]any{
		map[string]any{"worker": "w1", "name": "E", "worktree": "/x", "terminal": "term_t"},
		map[string]any{"worker": "w2", "name": "F", "worktree": "/y", "terminal": "term_t"},
	})); err == nil || !regexp.MustCompile(`pins one terminal for two workers`).MatchString(err.Error()) {
		t.Fatalf("err = %v, want the one-terminal-two-workers refusal", err)
	}
}

func TestPinUnpinnedRowUntouched(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	f.addTerminal("eli", "claude", "", true)
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{mentions: ip(1), unread: ip(3)})}
	c := pinnedCfg(t, root, "") // no terminal field
	out := f.poll(t, c)
	if len(out.Nudged) != 1 || out.Nudged[0] != "Eli" {
		t.Fatalf("nudged = %v, want [Eli]", out.Nudged)
	}
	_, _, how, err := ResolveTerminal(c.Roster[0], f.terminals, c, f.orca)
	if err != nil {
		t.Fatalf("resolveTerminal: %v", err)
	}
	if how != "identity" {
		t.Fatalf("resolve via = %q, want identity (resolution order unchanged)", how)
	}
}
