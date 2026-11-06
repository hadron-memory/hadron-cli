package orca

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestHostPlan1635ForeignBindingApp(t *testing.T) {
	repo := hostRepo1635(t)
	inv := hostInventory{Repos: map[string]string{repo: "common"}, Trees: []hostTree{{repo, "common", &hostBinding{"https://target.example", "id-iris", "foreign-app", "session"}}}, Terminals: []hostTerminal{}}
	calls := []string{}
	dto, code, raw := executePlan1635(t, roster1635(t, repo, "Iris"), fakePlan1635(inv, &calls))
	if len(dto.Workers) != 1 || code != 5 || len(dto.Conflicts) != 1 || dto.Conflicts[0].Reason != "binding_app_mismatch" || len(dto.Actions) != 0 || len(dto.Workers[0].Worktrees) != 0 {
		t.Fatalf("foreign binding admitted: code%d %s", code, raw)
	}
}

func TestHostPlan1635PrunableWorktreeDoesNotPoisonInventory(t *testing.T) {
	repo := hostRepo1635(t)
	missing := filepath.Join(t.TempDir(), "removed-worktree")
	base := hostDiscoveryRunner1635(repo)
	run := func(ctx context.Context, bin string, args []string) ([]byte, error) {
		if bin == "git" && reflect.DeepEqual(args, []string{"-C", repo, "worktree", "list", "--porcelain", "-z"}) {
			return []byte("worktree " + repo + "\x00HEAD abc\x00\x00worktree " + missing + "\x00HEAD def\x00prunable gitdir file points to non-existent location\x00\x00"), nil
		}
		return base(ctx, bin, args)
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
	run := hostDiscoveryRunner1635(repo)
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
	for _, tc := range []struct{ name, mutation string }{{"unknown-key", `"worker":"Iris","extra":true`}, {"case-mismatched-key", `"Worker":"Iris"`}, {"duplicate-key", `"worker":"Iris","worker":"Dara"`}} {
		t.Run(tc.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "roster.json")
			if err := os.WriteFile(file, []byte(strings.Replace(string(raw), `"worker":"Iris"`, tc.mutation, 1)), 0600); err != nil {
				t.Fatal(err)
			}
			calls := []string{}
			_, code, out := executePlan1635(t, file, fakePlan1635(hostInventory{}, &calls))
			if code != 2 || len(calls) != 0 || out != "" {
				t.Fatalf("malformed worker key admitted: code%d calls%v %s", code, calls, out)
			}
		})
	}
}

func TestHostPlan1635CommandPassesResolvedIdentityToDiscovery(t *testing.T) {
	repo := hostRepo1635(t)
	binding := `{"server":"https://target.example","workerId":"server-generated-47","appId":"app-id","sessionId":"recorded-session"}`
	if err := os.WriteFile(filepath.Join(repo, ".git", bindingFileName), []byte(binding), 0600); err != nil {
		t.Fatal(err)
	}
	run := hostDiscoveryRunner1635(repo)
	calls := []string{}
	env := fakePlan1635(hostInventory{}, &calls)
	env.Worker = func(context.Context, string, string) (hostWorkerIdentity, error) {
		return hostWorkerIdentity{ID: "server-generated-47", AppID: "app-id", Family: "codex"}, nil
	}
	env.Inventory = func(ctx context.Context, rows []hostPlanWorker) (hostInventory, error) {
		if len(rows) != 1 || rows[0].ID != "server-generated-47" || rows[0].Repo != repo || rows[0].Worker != "Iris" || rows[0].Family != "codex" {
			t.Fatalf("resolved discovery inputs lost: %+v", rows)
		}
		return discoverHostInventory(ctx, rows, run)
	}
	dto, code, raw := executePlan1635(t, roster1635(t, repo, "Iris"), env)
	if code != 0 || !dto.Complete || len(dto.Workers) != 1 || len(dto.Workers[0].Sessions) != 1 || dto.Workers[0].Sessions[0] != "recorded-session" || len(dto.Actions) != 1 || dto.Actions[0].Kind != "open-terminal" {
		t.Fatalf("bound worker falsely absent: code%d %s", code, raw)
	}
}

func hostDiscoveryRunner1635(repos ...string) hostReadRunner {
	return func(_ context.Context, bin string, args []string) ([]byte, error) {
		if bin == "orca" && reflect.DeepEqual(args, []string{"terminal", "list", "--json"}) {
			return []byte(`{"ok":true,"result":{"terminals":[]}}`), nil
		}
		if bin != "git" || len(args) < 3 || args[0] != "-C" {
			return nil, fmt.Errorf("unexpected fixture argv %s %q", bin, args)
		}
		repo := ""
		for _, candidate := range repos {
			if args[1] == candidate {
				repo = candidate
			}
		}
		if repo == "" {
			return nil, fmt.Errorf("unexpected fixture repo %q", args[1])
		}
		switch {
		case reflect.DeepEqual(args[2:], []string{"worktree", "list", "--porcelain", "-z"}):
			return []byte("worktree " + repo + "\x00HEAD abc\x00\x00"), nil
		case reflect.DeepEqual(args[2:], []string{"rev-parse", "--show-toplevel"}):
			return []byte(repo + "\n"), nil
		case reflect.DeepEqual(args[2:], []string{"rev-parse", "--path-format=absolute", "--git-common-dir"}), reflect.DeepEqual(args[2:], []string{"rev-parse", "--absolute-git-dir"}):
			return []byte(filepath.Join(repo, ".git") + "\n"), nil
		default:
			return nil, fmt.Errorf("unexpected fixture argv %s %q", bin, args)
		}
	}
}

func TestHostPlan1635IncompleteRequestedBindingNeverAllClear(t *testing.T) {
	complete := map[string]string{"server": "https://target.example", "workerId": "id-iris", "appId": "app-id", "sessionId": "s"}
	for _, field := range []string{"complete-control", "appId", "sessionId", "workerId", "missing-workerId", "missing-appId", "missing-sessionId", "invalid-json"} {
		t.Run(field, func(t *testing.T) {
			fields := map[string]string{}
			for k, v := range complete {
				fields[k] = v
			}
			if strings.HasPrefix(field, "missing-") {
				delete(fields, strings.TrimPrefix(field, "missing-"))
			} else if field != "complete-control" {
				fields[field] = ""
			}
			raw, _ := json.Marshal(fields)
			if field == "invalid-json" {
				raw = []byte("{")
			}
			repo := hostRepo1635(t)
			healthy := hostRepo1635(t)
			if err := os.WriteFile(filepath.Join(repo, ".git", bindingFileName), raw, 0600); err != nil {
				t.Fatal(err)
			}
			good := `{"server":"https://target.example","workerId":"id-dara","appId":"app-id","sessionId":"healthy"}`
			if err := os.WriteFile(filepath.Join(healthy, ".git", bindingFileName), []byte(good), 0600); err != nil {
				t.Fatal(err)
			}
			roster := hostRoster{1, "hrn:app:acme.com:team", []hostRosterWorker{{"Iris", repo, "codex"}, {"Dara", healthy, "codex"}}}
			bytes, _ := json.Marshal(roster)
			file := filepath.Join(t.TempDir(), "roster.json")
			if err := os.WriteFile(file, bytes, 0600); err != nil {
				t.Fatal(err)
			}
			calls := []string{}
			env := fakePlan1635(hostInventory{}, &calls)
			env.Inventory = func(ctx context.Context, rows []hostPlanWorker) (hostInventory, error) {
				return discoverHostInventory(ctx, rows, hostDiscoveryRunner1635(repo, healthy))
			}
			dto, code, out := executePlan1635(t, file, env)
			if field == "complete-control" {
				if code != 0 || !dto.Complete || len(dto.Workers) != 2 || len(dto.Actions) != 2 || len(dto.Workers[0].Worktrees) != 1 || len(dto.Workers[1].Worktrees) != 1 {
					t.Fatalf("complete binding control: code%d %s", code, out)
				}
				return
			}
			if code != 1 || dto.Complete || len(dto.Workers) != 2 || len(dto.Unavailable) != 1 || dto.Unavailable[0].Reason != "git_inventory_unavailable" || len(dto.Actions) != 0 {
				t.Fatalf("whole-plan unknown binding: code%d %s", code, out)
			}
		})
	}
}

func TestHostPlan1635UnknownTerminalPathIsIncomplete(t *testing.T) {
	for _, tc := range []struct{ name, entry string }{{"empty", `{"handle":"t","worktreePath":""}`}, {"absent", `{"handle":"t"}`}, {"null", `{"handle":"t","worktreePath":null}`}} {
		t.Run(tc.name, func(t *testing.T) {
			repo := hostRepo1635(t)
			calls := []string{}
			env := fakePlan1635(hostInventory{}, &calls)
			env.Inventory = func(ctx context.Context, rows []hostPlanWorker) (hostInventory, error) {
				return discoverHostInventory(ctx, rows, func(_ context.Context, bin string, args []string) ([]byte, error) {
					if bin != "orca" || !reflect.DeepEqual(args, []string{"terminal", "list", "--json"}) {
						t.Fatalf("unexpected call %s %q", bin, args)
					}
					return []byte(`{"ok":true,"result":{"terminals":[` + tc.entry + `]}}`), nil
				})
			}
			dto, code, raw := executePlan1635(t, roster1635(t, repo, "Iris"), env)
			if code != 1 || dto.Complete || len(dto.Unavailable) != 1 || dto.Unavailable[0].Reason != "terminal_path_unresolved" || len(dto.Actions) != 0 {
				t.Fatalf("unknown terminal path: code%d %s", code, raw)
			}
		})
	}
}

func TestHostDiscoveryFixtureRejectsUnexpectedArgv1729(t *testing.T) {
	repo := hostRepo1635(t)
	run := hostDiscoveryRunner1635(repo)
	for _, tc := range []struct {
		bin  string
		args []string
	}{{"git", nil}, {"git", []string{"-C"}}, {"git", []string{"-C", "/foreign", "rev-parse", "--show-toplevel"}}, {"orca", []string{"terminal", "list"}}, {"shell", nil}, {"git", []string{"-C", repo, "rev-parse", "--show-toplevel", "extra"}}, {"git", []string{"-C", repo, "status"}}, {"git", []string{"-C", repo}}} {
		if _, err := run(context.Background(), tc.bin, tc.args); err == nil {
			t.Fatalf("fixture accepted %s %q", tc.bin, tc.args)
		}
	}
}
