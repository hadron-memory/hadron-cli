package teamrouter

// Ported 1:1 from router.test.ts describe('identity-flap fallback: the
// screen resolves what tracking lost') — #1823. Orca's per-terminal tracking
// degrades mid-life: agentIdentity reads null on a terminal that is running
// its agent right now, the same degradation family as the null lastOutputAt
// the #7398 gate already survives.

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func setupFlap(t *testing.T) (*fakeCLIs, *Config) { //nolint:unparam // the null agent IS the flap regime; later blocks vary it
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"})
	f.addTerminal("eli", "", "", true)
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{})}
	return f, c
}

func TestFlapNullIdentityClaudeScreenStillNudges(t *testing.T) {
	f, c := setupFlap(t)
	out := f.poll(t, c)
	if len(out.Nudged) != 1 || out.Nudged[0] != "Eli" {
		t.Fatalf("nudged = %v, want [Eli]", out.Nudged)
	}
	if len(f.sent) != 1 || f.sent[0].handle != "term_eli" {
		t.Fatalf("sent = %v, want term_eli", f.sent)
	}
	if !containsStr(f.logs, "resolved by screen") {
		t.Errorf("missing the resolved-by-screen log")
	}
}

func containsStr(xs []string, needle string) bool {
	for _, x := range xs {
		if strings.Contains(x, needle) {
			return true
		}
	}
	return false
}

func TestFlapCodexScreenResolvesAsCodex(t *testing.T) {
	f, c := setupFlap(t)
	f.setScreen("term_eli", screenSpec{tail: codexIdle})
	out := f.poll(t, c)
	if len(out.Nudged) != 1 || out.Nudged[0] != "Eli" {
		t.Fatalf("nudged = %v, want [Eli] (the family flows to the gates)", out.Nudged)
	}
}

func TestFlapPlainShellScreenNeverResolvedByFallback(t *testing.T) {
	f, c := setupFlap(t)
	f.setScreen("term_eli", screenSpec{tail: []string{"orca@orca1:~/ws/eli$ "}})
	out := f.poll(t, c)
	if out.Held["Eli"] != "no live agent terminal in its worktree" {
		t.Fatalf("hold = %q, want the no-terminal reason", out.Held["Eli"])
	}
	wantNoSend(t, f)
}

// Blind review P2 (#1823, Anne): a plain shell in the bound worktree `cat`s a
// captured Claude idle screen and returns to its own prompt. The box and the
// hint are HISTORY, not a live composer — the whole counterexample that
// passed both post-recompute gates when the status check read only the FIRST
// nonblank line below the closing rule.
func TestFlapShellPrintingCapturedClaudeScreenHolds(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"})
	f.addTerminal("eli", "", "term_eli", false) // identity lost, untracked: the fallback's regime
	f.setScreen("term_eli", screenSpec{tail: append(append([]string{}, claudeIdle...), "orca@orca1:~/ws/eli$ ")})
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{})}
	out := f.poll(t, c)
	if len(out.Nudged) != 0 {
		t.Fatalf("nudged = %v, want none", out.Nudged)
	}
	wantNoSend(t, f)
	if !strings.Contains(out.Held["Eli"], "screen shows text in the prompt") {
		t.Fatalf("hold = %q, want the prompt-text refusal", out.Held["Eli"])
	}
}

func TestFlapWrongFamilyGuessCaughtOneGateLater(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"})
	f.addTerminal("eli", "", "term_eli", false)
	// Classifies 'claude' on the banner line; there is no Claude composer box,
	// so the family-specific empty-prompt gate must refuse what it resolved.
	f.setScreen("term_eli", screenSpec{tail: []string{"  Claude Code v2.1.296 — a captured banner line", "  ordinary output", "orca@orca1:~/ws/eli$ "}})
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{})}
	out := f.poll(t, c)
	if len(out.Nudged) != 0 {
		t.Fatalf("nudged = %v, want none", out.Nudged)
	}
	wantNoSend(t, f)
	if !strings.Contains(out.Held["Eli"], "screen shows text in the prompt") {
		t.Fatalf("hold = %q, want the prompt-text refusal", out.Held["Eli"])
	}
}

func TestFlapCapturedCodexScreenWithShellPromptHolds(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"})
	f.addTerminal("eli", "", "term_eli", false)
	f.setScreen("term_eli", screenSpec{tail: append(append([]string{}, codexIdle...), "orca@orca1:~/ws/eli$ ")})
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{})}
	out := f.poll(t, c)
	if len(out.Nudged) != 0 {
		t.Fatalf("nudged = %v, want none", out.Nudged)
	}
	wantNoSend(t, f)
	if !strings.Contains(out.Held["Eli"], "screen shows text in the prompt") {
		t.Fatalf("hold = %q, want the prompt-text refusal", out.Held["Eli"])
	}
}

func TestFlapUnreadableScreenHolds(t *testing.T) {
	f, c := setupFlap(t)
	f.setScreen("term_eli", screenSpec{tail: claudeIdle, unreadable: true})
	out := f.poll(t, c)
	if out.Held["Eli"] != "no live agent terminal in its worktree" {
		t.Fatalf("hold = %q, want the no-terminal reason", out.Held["Eli"])
	}
	wantNoSend(t, f)
}

func TestFlapBothFamiliesOnScreenHoldsRatherThanGuess(t *testing.T) {
	f, c := setupFlap(t)
	f.setScreen("term_eli", screenSpec{tail: append(append([]string{}, claudeIdle...), "› Ask Codex to do anything")})
	out := f.poll(t, c)
	if out.Held["Eli"] != "no live agent terminal in its worktree" {
		t.Fatalf("hold = %q, want the no-terminal reason", out.Held["Eli"])
	}
	wantNoSend(t, f)
}

func TestFlapTwoScreenVerifiedTerminalsHoldAmbiguous(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"})
	f.addTerminal("eli", "", "term_a", true)
	f.addTerminal("eli", "", "term_b", true)
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{})}
	out := f.poll(t, c)
	if out.Held["Eli"] != "no live agent terminal in its worktree" {
		t.Fatalf("hold = %q, want the no-terminal reason", out.Held["Eli"])
	}
	wantNoSend(t, f)
}

func TestFlapVerifiedIdentityResolvesWithoutFallback(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"})
	f.addTerminal("eli", "claude", "term_eli", true)
	delete(f.screens, "term_eli") // identity path must not need a screen
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{})}
	// The gates then read the screen; serve it back only for the gate reads.
	f.setScreen("term_eli", screenSpec{tail: claudeIdle})
	out := f.poll(t, c)
	if len(out.Nudged) != 1 || out.Nudged[0] != "Eli" {
		t.Fatalf("nudged = %v, want [Eli]", out.Nudged)
	}
	if containsStr(f.logs, "resolved by screen") {
		t.Errorf("the identity path must not log the fallback")
	}
	// Resolution itself must not read: the FIRST screen read belongs to the
	// gates, so it strictly follows the first idle wait (blind P3, #1823 —
	// the absent fallback log alone could not tell a resolve-time read apart
	// from a gate read).
	firstWait, firstRead := -1, -1
	for i, call := range f.calls {
		if firstWait == -1 && strings.HasPrefix(call, "terminal wait") {
			firstWait = i
		}
		if firstRead == -1 && strings.HasPrefix(call, "terminal read") {
			firstRead = i
		}
	}
	if firstWait < 0 || firstRead <= firstWait {
		t.Fatalf("first wait=%d must precede first read=%d", firstWait, firstRead)
	}
}

func TestFlapResolveTerminalVerifiedIdentityNeverReadsScreen(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"})
	f.addTerminal("eli", "claude", "term_eli", true)
	trap := func(ctx context.Context, argv []string) RunResult {
		if len(argv) >= 2 && strings.Join(argv[0:2], " ") == "terminal read" {
			t.Fatal("resolution read the screen")
		}
		return f.orca(ctx, argv)
	}
	entry := RosterEntry{Worker: "w-eli", Name: "Eli", Worktree: filepath.Join(root, "workspaces", "eli")}
	handle, family, how, err := ResolveTerminal(entry, f.terminals, c, trap)
	if err != nil {
		t.Fatalf("resolveTerminal: %v", err)
	}
	if handle != "term_eli" || family != "claude" || how != "identity" {
		t.Fatalf("resolve = (%q, %q, %q), want (term_eli, claude, identity)", handle, family, how)
	}
}
