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
	inv, err := discoverHostInventory(context.Background(), []hostPlanWorker{{Worker: "Iris", ID: "id-iris", Repo: repo, Family: "codex"}}, run)
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
	_, err := discoverHostInventory(context.Background(), []hostPlanWorker{{Worker: "Iris", ID: "id-iris", Repo: repo, Family: "codex"}}, run)
	if err != nil {
		t.Fatalf("known unrelated worker binding poisoned inventory: %v", err)
	}
}

func TestHostPlan1635DecisionTable(t *testing.T) {
	for _, name := range []string{"open-terminal", "inspect-agent", "terminal-family", "multiple-worktrees", "multiple-terminals", "recorded-family", "retired", "foreign-app"} {
		t.Run(name, func(t *testing.T) {
			repo := hostRepo1635(t)
			inv := hostInventory{Repos: map[string]string{repo: "common"}, Trees: []hostTree{{repo, "common", &hostBinding{"https://target.example", "id-iris", "app-id", "session"}}}, Terminals: []hostTerminal{}}
			reason, action, wantCode := "", "", 0
			switch name {
			case "open-terminal":
				action = "open-terminal"
			case "inspect-agent":
				inv.Terminals = []hostTerminal{{"t", repo, ""}}
				action = "inspect-agent"
			case "terminal-family":
				inv.Terminals = []hostTerminal{{"t", repo, "claude"}}
				reason = "terminal_family_mismatch"
				wantCode = 5
			case "multiple-worktrees":
				inv.Trees = append(inv.Trees, hostTree{repo + "-other", "common", &hostBinding{"https://target.example", "id-iris", "app-id", "other-session"}})
				reason = "multiple_bound_worktrees"
				wantCode = 5
			case "multiple-terminals":
				inv.Terminals = []hostTerminal{{"a", repo, "codex"}, {"b", repo, "codex"}}
				reason = "multiple_bound_terminals"
				wantCode = 5
			case "recorded-family":
				reason = "recorded_family_mismatch"
				wantCode = 5
			case "retired", "foreign-app":
				wantCode = 2
			}
			calls := []string{}
			env := fakePlan1635(inv, &calls)
			if name == "recorded-family" || name == "retired" || name == "foreign-app" {
				env.Worker = func(context.Context, string, string) (hostWorkerIdentity, error) {
					w := hostWorkerIdentity{ID: "id-iris", AppID: "app-id", Family: "codex"}
					if name == "recorded-family" {
						w.Family = "claude"
					}
					if name == "retired" {
						w.Retired = true
					}
					if name == "foreign-app" {
						w.AppID = "foreign"
					}
					return w, nil
				}
			}
			dto, code, raw := executePlan1635(t, roster1635(t, repo, "Iris"), env)
			if code != wantCode {
				t.Fatalf("code%d want%d: %s", code, wantCode, raw)
			}
			if reason != "" && (len(dto.Conflicts) != 1 || dto.Conflicts[0].Reason != reason || len(dto.Actions) != 0) {
				t.Fatalf("guard/action mismatch: %s", raw)
			}
			if action != "" && (len(dto.Actions) != 1 || dto.Actions[0].Kind != action) {
				t.Fatalf("action mismatch: %s", raw)
			}
			if wantCode == 2 && strings.Contains(strings.Join(calls, ","), "inventory") {
				t.Fatal("refused identity reached host discovery")
			}
		})
	}
}

func TestHostPlan1635DiscoveryScopeExplicit(t *testing.T) {
	repo := hostRepo1635(t)
	calls := []string{}
	_, code, raw := executePlan1635(t, roster1635(t, repo, "Iris"), fakePlan1635(hostInventory{Repos: map[string]string{repo: "common"}}, &calls))
	var dto map[string]any
	if err := json.Unmarshal([]byte(raw), &dto); err != nil {
		t.Fatal(err)
	}
	if code != 0 || dto["discoveryScope"] != "roster-repositories-and-reported-terminals" {
		t.Fatalf("scope of absence not explicit: code%d %s", code, raw)
	}
}

func TestHostPlan1635WorkerRowKeysStrict(t *testing.T) {
	repo := hostRepo1635(t)
	raw, err := os.ReadFile(roster1635(t, repo, "Iris"))
	if err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []string{`"worker":"Iris","extra":true`, `"Worker":"Iris"`, `"worker":"Iris","worker":"Dara"`} {
		file := filepath.Join(t.TempDir(), "roster.json")
		if err := os.WriteFile(file, []byte(strings.Replace(string(raw), `"worker":"Iris"`, mutation, 1)), 0600); err != nil {
			t.Fatal(err)
		}
		calls := []string{}
		_, code, out := executePlan1635(t, file, fakePlan1635(hostInventory{}, &calls))
		if code != 2 || len(calls) != 0 || out != "" {
			t.Fatalf("malformed worker key admitted: code%d calls%v %s", code, calls, out)
		}
	}
}
