package teamrouter

// Ported 1:1 from router.test.ts describe('resolution never guesses') —
// mapping a roster row to its agent terminal, including the worktree's
// worker binding (the CLI's hadron-team-session.json behind the .git file).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// bind fakes a worktree's worker binding: the gitdir holds the CLI's
// hadron-team-session.json, the worktree's .git file points at it.
func bindWorker(t *testing.T, root, worktree, workerID string) {
	t.Helper()
	gitdir := filepath.Join(root, "gitdirs", worktree)
	if err := os.MkdirAll(gitdir, 0o755); err != nil {
		t.Fatal(err)
	}
	blob, err := json.Marshal(map[string]string{"workerId": workerID})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitdir, "hadron-team-session.json"), blob, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "workspaces", worktree, ".git"), []byte("gitdir: "+gitdir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolveNoAgentTerminalHoldsAndReportsOnce(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"})
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{})}
	f.poll(t, c)
	f.poll(t, c)
	wantNoSend(t, f)
	if tok := adoptedToken(t, c); tok != "" {
		t.Fatalf("token = %q, want none", tok)
	}
	if len(f.logs) != 1 { // reported once, not every poll
		t.Fatalf("logs = %v, want exactly one", f.logs)
	}
	if !strings.Contains(f.logs[0], "no live agent terminal") {
		t.Fatalf("log = %q, want the no-terminal report", f.logs[0])
	}
}

func TestResolveTwoAgentTerminalsAmbiguous(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"})
	f.addTerminal("eli", "claude", "term_a", true)
	f.addTerminal("eli", "codex", "term_b", true)
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{})}
	out := f.poll(t, c)
	if !regexp.MustCompile(`ambiguous`).MatchString(out.Held["Eli"]) {
		t.Fatalf("hold = %q, want ambiguous", out.Held["Eli"])
	}
	wantNoSend(t, f)
}

func TestResolvePlainShellTerminalIsNotAnAgent(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"})
	f.addTerminal("eli", "claude", "", true)
	f.addTerminal("eli", "", "term_shell", true) // agentIdentity null
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{})}
	out := f.poll(t, c)
	if len(out.Nudged) != 1 || out.Nudged[0] != "Eli" {
		t.Fatalf("nudged = %v, want [Eli]", out.Nudged)
	}
}

func TestResolveWorktreeBoundToAnotherWorkerRefused(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"})
	f.addTerminal("eli", "claude", "", true)
	bindWorker(t, root, "eli", "w-someone-else")
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{})}
	out := f.poll(t, c)
	if !regexp.MustCompile(`bound to another worker`).MatchString(out.Held["Eli"]) {
		t.Fatalf("hold = %q, want the wrong-worker refusal", out.Held["Eli"])
	}
	wantNoSend(t, f)
}

func TestResolveMatchingBindingAccepted(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"})
	f.addTerminal("eli", "claude", "", true)
	bindWorker(t, root, "eli", "w-eli")
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{})}
	out := f.poll(t, c)
	if len(out.Nudged) != 1 || out.Nudged[0] != "Eli" {
		t.Fatalf("nudged = %v, want [Eli]", out.Nudged)
	}
}
