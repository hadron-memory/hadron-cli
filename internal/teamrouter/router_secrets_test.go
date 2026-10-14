package teamrouter

// Ported 1:1 from router.test.ts describe('nothing secret leaks').

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestSecretsTokenNeverAppearsAnywhere(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"}, [2]string{"w-baloo", "Baloo"})
	f.addTerminal("eli", "claude", "", true)
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{}), worker("w-baloo", "Baloo", workerOpts{})}
	out := f.poll(t, c)
	rep, err := Status(c)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	statusBlob, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	statusText := string(statusBlob)
	ledgerBlob, _ := os.ReadFile(filepath.Join(c.StateDir, "ledger.json"))
	var sentBlob strings.Builder
	for _, s := range f.sent {
		sentBlob.WriteString(s.handle)
		sentBlob.WriteString(s.text)
	}
	logsBlob, _ := json.Marshal(f.logs)
	blob := DescribeOutcome(out) + string(logsBlob) + sentBlob.String() + statusText + string(ledgerBlob)
	if strings.Contains(blob, "SECRET") {
		t.Fatalf("the token leaked into output, logs, nudges, status or the ledger")
	}
}

func TestSecretsStateIsPrivate(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"})
	f.addTerminal("eli", "claude", "", true)
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{})}
	f.poll(t, c)
	if mode := modeOf(t, c.StateDir); mode != 0o700 {
		t.Fatalf("state dir mode = %o, want 700", mode)
	}
	if mode := modeOf(t, filepath.Join(c.StateDir, "adopted.token")); mode != 0o600 {
		t.Fatalf("token mode = %o, want 600", mode)
	}
}

func modeOf(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Mode().Perm()
}

func TestSecretsConfigRefusesIncompleteOrDuplicateRoster(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "c.json")
	base := map[string]any{"app": "a", "channel": "c", "stateDir": filepath.Join(root, "s")}
	write := func(roster any) {
		t.Helper()
		base["roster"] = roster
		blob, _ := json.Marshal(base)
		if err := os.WriteFile(path, blob, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustRefuse := func(why string) {
		t.Helper()
		if _, err := LoadConfig(path); err == nil {
			t.Fatalf("config must refuse (%s)", why)
		}
	}

	write([]any{map[string]string{"worker": "w", "name": "E"}}) // no worktree
	mustRefuse("incomplete roster entry")

	entry := map[string]string{"worker": "w", "name": "E", "worktree": "/x"}
	write([]any{entry, entry}) // exact duplicate
	mustRefuse("duplicate roster entry")

	write([]any{entry, map[string]string{"worker": "w2", "name": "F", "worktree": "/x"}}) // two workers on one worktree (@codex P2, #1490)
	if _, err := LoadConfig(path); err == nil || !regexp.MustCompile(`one worktree to two workers`).MatchString(err.Error()) {
		t.Fatalf("err = %v, want the one-worktree-two-workers refusal", err)
	}
}
