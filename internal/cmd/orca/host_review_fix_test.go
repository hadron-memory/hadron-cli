package orca

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHostPlan1635ForeignBindingApp(t *testing.T) {
	repo := hostRepo1635(t)
	inv := hostInventory{Repos: map[string]string{repo: "common"}, Trees: []hostTree{{repo, "common", &hostBinding{"https://target.example", "id-iris", "foreign-app", "session"}}}, Terminals: []hostTerminal{}}
	calls := []string{}
	dto, code, raw := executePlan1635(t, roster1635(t, repo, "Iris"), fakePlan1635(inv, &calls))
	if code != 5 || len(dto.Conflicts) != 1 || dto.Conflicts[0].Reason != "binding_app_mismatch" || len(dto.Actions) != 0 || len(dto.Workers[0].Worktrees) != 0 {
		t.Fatalf("foreign binding admitted: code%d %s", code, raw)
	}
}

func TestHostPlan1635PrunableWorktreeDoesNotPoisonInventory(t *testing.T) {
	repo := hostRepo1635(t)
	missing := filepath.Join(t.TempDir(), "removed-worktree")
	run := func(_ context.Context, bin string, args []string) ([]byte, error) {
		if bin == "orca" {
			return []byte(`{"ok":true,"result":{"terminals":[]}}`), nil
		}
		if strings.Join(args[2:], " ") == "worktree list --porcelain -z" {
			return []byte("worktree " + repo + "\x00HEAD abc\x00\x00worktree " + missing + "\x00HEAD def\x00prunable gitdir file points to non-existent location\x00\x00"), nil
		}
		if args[len(args)-1] == "--show-toplevel" {
			return []byte(repo + "\n"), nil
		}
		return []byte(filepath.Join(repo, ".git") + "\n"), nil
	}
	inv, err := discoverHostInventory(context.Background(), []hostRosterWorker{{Worker: "Iris", Repo: repo, Family: "codex"}}, run)
	if err != nil {
		t.Fatalf("explicit prunable absent worktree poisoned inventory: %v", err)
	}
	if len(inv.Trees) != 1 || inv.Trees[0].Path != repo {
		t.Fatalf("unexpected inventory: %+v", inv)
	}
}

func TestHostPlan1635UnrelatedLegacyBindingDoesNotPoisonInventory(t *testing.T) {
	repo := hostRepo1635(t)
	raw, _ := json.Marshal(map[string]string{"workerId": "unrelated-worker", "sessionId": "old-session"})
	if err := os.WriteFile(filepath.Join(repo, ".git", bindingFileName), raw, 0600); err != nil {
		t.Fatal(err)
	}
	run := func(_ context.Context, bin string, args []string) ([]byte, error) {
		if bin == "orca" {
			return []byte(`{"ok":true,"result":{"terminals":[]}}`), nil
		}
		if strings.Join(args[2:], " ") == "worktree list --porcelain -z" {
			return []byte("worktree " + repo + "\x00HEAD abc\x00\x00"), nil
		}
		if args[len(args)-1] == "--show-toplevel" {
			return []byte(repo + "\n"), nil
		}
		return []byte(filepath.Join(repo, ".git") + "\n"), nil
	}
	_, err := discoverHostInventory(context.Background(), []hostRosterWorker{{Worker: "Iris", Repo: repo, Family: "codex"}}, run)
	if err != nil {
		t.Fatalf("known unrelated worker binding poisoned inventory: %v", err)
	}
}
