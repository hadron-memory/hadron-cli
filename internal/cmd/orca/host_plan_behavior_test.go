package orca

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func hostRepo1635(t *testing.T) string {
	t.Helper()
	p := t.TempDir()
	if err := os.Mkdir(filepath.Join(p, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}
func roster1635(t *testing.T, repo string, workers ...string) string {
	t.Helper()
	rows := []hostRosterWorker{}
	for _, w := range workers {
		rows = append(rows, hostRosterWorker{w, repo, "codex"})
	}
	raw, err := json.Marshal(hostRoster{1, "hrn:app:acme.com:team", rows})
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "roster.json")
	if err := os.WriteFile(p, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return p
}
func executePlan1635(t *testing.T, file string, env hostPlanEnv) (hostPlanDTO, int, string) {
	t.Helper()
	streams, out, _ := output.Test()
	f := &cmdutil.Factory{IOStreams: streams, JSON: true}
	cmd := newCmdHostPlan(f, env)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"--roster-file", file})
	err := cmd.Execute()
	var dto hostPlanDTO
	if out.Len() > 0 {
		if err := json.Unmarshal(out.Bytes(), &dto); err != nil {
			t.Fatalf("bad DTO %q: %v", out.String(), err)
		}
	}
	return dto, exitcode.FromError(err), out.String()
}
func fakePlan1635(inv hostInventory, calls *[]string) hostPlanEnv {
	return hostPlanEnv{
		Server: func() (string, error) { return "https://target.example", nil },
		App:    func(context.Context, string) (string, error) { *calls = append(*calls, "app"); return "app-id", nil },
		Worker: func(_ context.Context, app, ref string) (hostWorkerIdentity, error) {
			*calls = append(*calls, "worker:"+ref)
			return hostWorkerIdentity{ID: "id-" + strings.ToLower(ref), AppID: "app-id", Family: "codex"}, nil
		},
		Inventory: func(context.Context, []hostPlanWorker) (hostInventory, error) {
			*calls = append(*calls, "inventory")
			return inv, nil
		},
	}
}
func TestHostPlan1635DeterministicAbsentExistingConflict(t *testing.T) {
	repo := hostRepo1635(t)
	file := roster1635(t, repo, "Iris", "Dara")
	inv := hostInventory{Repos: map[string]string{repo: "common"}, Trees: []hostTree{{repo, "common", &hostBinding{"https://target.example", "id-iris", "app-id", "session-id"}}}, Terminals: []hostTerminal{{"term-1", repo, "codex"}}}
	calls := []string{}
	env := fakePlan1635(inv, &calls)
	before := snapshotHost1635(t, repo)
	dto, code, first := executePlan1635(t, file, env)
	if code != 0 || !dto.Complete || !dto.ReadOnly || dto.Version != 1 {
		t.Fatalf("code%d %s", code, first)
	}
	if !reflect.DeepEqual(calls, []string{"app", "worker:Iris", "worker:Dara", "inventory"}) {
		t.Fatal(calls)
	}
	if len(dto.Actions) != 3 || dto.Actions[0].Worker != "Dara" || len(dto.Workers[0].Sessions) != 1 || len(dto.Workers[1].Worktrees) != 0 {
		t.Fatal(first)
	}
	_, code, second := executePlan1635(t, file, env)
	if code != 0 || first != second {
		t.Fatal("plan is not stable")
	}
	if !reflect.DeepEqual(before, snapshotHost1635(t, repo)) {
		t.Fatal("planner wrote host state")
	}
	if !strings.Contains(first, `"conflicts": []`) || !strings.Contains(first, `"unavailable": []`) {
		t.Fatal(first)
	}
	inv.Trees = append(inv.Trees, hostTree{repo + "-wrong", "other-common", &hostBinding{"https://target.example", "id-dara", "app-id", "s-other"}})
	env = fakePlan1635(inv, &calls)
	dto, code, _ = executePlan1635(t, file, env)
	if code != 5 || len(dto.Conflicts) != 1 || dto.Conflicts[0].Reason != "binding_repository_mismatch" || len(dto.Actions) != 0 {
		t.Fatalf("conflict dropped: %+v code%d", dto, code)
	}
}
func TestHostPlan1635BadRosterNoDiscovery(t *testing.T) {
	repo := hostRepo1635(t)
	valid, _ := os.ReadFile(roster1635(t, repo, "Iris"))
	fixtures := []string{
		`null`, `{}`, `{"version":1,"app":"hrn:app:acme.com:team","workers":[]}`,
		strings.Replace(string(valid), `"version":1`, `"version":2`, 1),
		strings.Replace(string(valid), `"version":1`, `"Version":1`, 1),
		strings.Replace(string(valid), `"version":1`, `"version":1,"version":1`, 1),
		strings.Replace(string(valid), `"version":1`, `"version":1,"apply":true`, 1),
		strings.Replace(string(valid), `"codex"`, `"shell"`, 1),
		strings.Replace(string(valid), repo, "../escape", 1),
		strings.Replace(string(valid), repo, repo+"/../escape", 1),
		strings.Replace(string(valid), repo, t.TempDir(), 1),
		strings.Repeat("x", rosterLimit1635+1), string([]byte{255}),
	}
	for i, raw := range fixtures {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "bad.json")
			if err := os.WriteFile(file, []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			calls := []string{}
			_, code, out := executePlan1635(t, file, fakePlan1635(hostInventory{}, &calls))
			if code != 2 || len(calls) != 0 || out != "" {
				t.Fatalf("code%d calls%v out%s", code, calls, out)
			}
		})
	}
}
func TestHostPlan1635DuplicateAndForeignBeforeHostDiscovery(t *testing.T) {
	repo := hostRepo1635(t)
	file := roster1635(t, repo, "Iris", "IRIS")
	calls := []string{}
	env := fakePlan1635(hostInventory{}, &calls)
	_, code, _ := executePlan1635(t, file, env)
	if code != 2 || strings.Contains(strings.Join(calls, ","), "inventory") {
		t.Fatal(code, calls)
	}
	file = roster1635(t, repo, "Iris")
	env.Worker = func(context.Context, string, string) (hostWorkerIdentity, error) {
		return hostWorkerIdentity{ID: "id-iris", AppID: "foreign"}, nil
	}
	_, code, _ = executePlan1635(t, file, env)
	if code != 2 {
		t.Fatal(code)
	}
}
func TestHostPlan1635LaterWorkerAndHostFailuresAreIncomplete(t *testing.T) {
	repo := hostRepo1635(t)
	file := roster1635(t, repo, "Iris", "Dara")
	calls := []string{}
	env := fakePlan1635(hostInventory{}, &calls)
	env.Worker = func(_ context.Context, _, ref string) (hostWorkerIdentity, error) {
		if ref == "Dara" {
			return hostWorkerIdentity{}, exitcode.Newf(8, "SECRET-provider-diagnostic")
		}
		return hostWorkerIdentity{ID: "i", AppID: "app-id"}, nil
	}
	dto, code, out := executePlan1635(t, file, env)
	if code != 8 || dto.Complete || len(dto.Unavailable) != 1 || len(dto.Actions) != 0 || strings.Contains(out, "SECRET") {
		t.Fatal(code, out)
	}
	env = fakePlan1635(hostInventory{}, &calls)
	env.Inventory = func(context.Context, []hostPlanWorker) (hostInventory, error) {
		return hostInventory{}, exitcode.Newf(10, "SECRET-subprocess")
	}
	dto, code, out = executePlan1635(t, file, env)
	if code != 10 || dto.Complete || strings.Contains(out, "SECRET") {
		t.Fatal(code, out)
	}
}
func TestHostPlan1635ClosedReadArgv(t *testing.T) {
	path := filepath.Join(t.TempDir(), "$(touch NEVER); --flag")
	for _, args := range [][]string{{"-C", path, "rev-parse", "--show-toplevel"}, {"-C", path, "rev-parse", "--absolute-git-dir"}, {"-C", path, "rev-parse", "--path-format=absolute", "--git-common-dir"}, {"-C", path, "worktree", "list", "--porcelain", "-z"}} {
		if !hostReadAllowed("git", args) {
			t.Fatal(args)
		}
	}
	for _, tc := range []struct {
		bin  string
		args []string
	}{
		{"sh", []string{"-c", "git status"}}, {"git", []string{"-C", path, "worktree", "add", "x"}},
		{"git", []string{"-C", path, "fetch"}}, {"git", []string{"-C", path, "checkout", "main"}},
		{"git", []string{"-C", path, "reset", "--hard"}}, {"orca", []string{"terminal", "send", "text"}},
		{"orca", []string{"terminal", "create"}}, {"orca", []string{"terminal", "close"}}, {"claude", nil}, {"hadron", []string{"team", "session", "start"}},
	} {
		if hostReadAllowed(tc.bin, tc.args) {
			t.Fatalf("write operation allowed: %+v", tc)
		}
	}
}
func TestHostPlan1635DiscoveryFailureNeverEmpty(t *testing.T) {
	for _, raw := range []string{`{}`, `{"ok":true}`, `{"ok":true,"result":{}}`, `{"ok":true,"result":{"terminals":null}}`, `{"ok":true,"result":{"terminals":[null]}}`} {
		run := func(context.Context, string, []string) ([]byte, error) { return []byte(raw), nil }
		_, err := discoverHostInventory(context.Background(), nil, run)
		if exitcode.FromError(err) != 10 {
			t.Fatalf("bad inventory accepted: %s %v", raw, err)
		}
	}
	inv, err := discoverHostInventory(context.Background(), nil, func(context.Context, string, []string) ([]byte, error) {
		return []byte(`{"ok":true,"result":{"terminals":[]}}`), nil
	})
	if err != nil || len(inv.Terminals) != 0 {
		t.Fatal(err)
	}
	_, err = discoverHostInventory(context.Background(), []hostPlanWorker{{Repo: hostRepo1635(t)}}, func(_ context.Context, bin string, _ []string) ([]byte, error) {
		if bin == "orca" {
			return []byte(`{"ok":true,"result":{"terminals":[]}}`), nil
		}
		return nil, errors.New("SECRET")
	})
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatal(err)
	}
}

func TestHostPlan1635MutatingSurfaceRefused(t *testing.T) {
	for _, args := range [][]string{{"host", "apply"}, {"host", "plan", "--apply"}, {"host", "plan", "--start"}} {
		streams, out, _ := output.Test()
		root := NewCmdOrca(&cmdutil.Factory{IOStreams: streams})
		root.SetOut(streams.Out)
		root.SetErr(streams.ErrOut)
		root.SilenceUsage = true
		root.SilenceErrors = true
		root.SetArgs(args)
		err := root.Execute()
		if exitcode.FromError(err) != 2 {
			t.Fatalf("%v exit%d out%q err%v", args, exitcode.FromError(err), out.String(), err)
		}
	}
}

func snapshotHost1635(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if d.IsDir() {
			out[rel] = "dir"
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[rel] = fmt.Sprintf("%x", sha256.Sum256(raw))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestHostPlan1635AllArrayShapes(t *testing.T) {
	repo := hostRepo1635(t)
	file := roster1635(t, repo, "Iris")
	calls := []string{}
	inv := hostInventory{Repos: map[string]string{repo: "common"}, Trees: []hostTree{{repo, "common", &hostBinding{"https://target.example", "id-iris", "app-id", "s"}}}, Terminals: []hostTerminal{{"t", repo, "codex"}}}
	dto, code, out := executePlan1635(t, file, fakePlan1635(inv, &calls))
	if code != 0 {
		t.Fatal(code, out)
	}
	if dto.Workers == nil || dto.Actions == nil || dto.Conflicts == nil || dto.Unavailable == nil {
		t.Fatal("top-level arrays must be present and non-null", out)
	}
	for _, w := range dto.Workers {
		if w.Worktrees == nil || w.Terminals == nil || w.Sessions == nil {
			t.Fatal("worker arrays must be present and non-null", out)
		}
	}
	for _, key := range []string{"actions", "conflicts", "unavailable"} {
		if !strings.Contains(out, `"`+key+`": []`) {
			t.Fatal("explicit empty array missing", key, out)
		}
	}
}

func TestHostPlan1635BindingFromClonedServerIsConflict(t *testing.T) {
	repo := hostRepo1635(t)
	var b hostBinding
	if err := json.Unmarshal([]byte(`{"workerId":"id-iris","appId":"app-id","sessionId":"s","server":"https://other.example"}`), &b); err != nil {
		t.Fatal(err)
	}
	inv := hostInventory{Repos: map[string]string{repo: "common"}, Trees: []hostTree{{repo, "common", &b}}, Terminals: []hostTerminal{{"t", repo, "codex"}}}
	dto := hostPlanDTO{Workers: []hostPlanWorker{{Worker: "Iris", ID: "id-iris", Repo: repo, Family: "codex", Worktrees: []string{}, Terminals: []string{}, Sessions: []string{}}}, Actions: []hostPlanAction{}, Conflicts: []hostPlanProblem{}, Unavailable: []hostPlanProblem{}}
	assembleHostPlan(&dto, "app-id", "https://target.example/graphql", inv)
	if len(dto.Conflicts) != 1 || dto.Conflicts[0].Reason != "binding_server_mismatch" || len(dto.Workers[0].Worktrees) != 0 || len(dto.Actions) != 0 {
		t.Fatalf("foreign-server binding accepted: %+v", dto)
	}
}

func TestHostPlan1635ServerIdentitySpellings(t *testing.T) {
	for _, pair := range [][2]string{{"https://target.example", "https://TARGET.example:443/"}, {"http://target.example", "http://TARGET.example:80/"}, {"https://target.example/base/", "https://target.example/base"}} {
		a, e := hostServerIdentity(pair[0])
		if e != nil {
			t.Fatal(e)
		}
		b, e := hostServerIdentity(pair[1])
		if e != nil || a != b {
			t.Fatal(pair, a, b, e)
		}
	}
	for _, raw := range []string{"", "ftp://target.example", "https://user:SECRET@target.example", "https://target.example?token=SECRET", "https://target.example#SECRET"} {
		if _, err := hostServerIdentity(raw); err == nil {
			t.Fatalf("unsafe target accepted: %q", raw)
		}
	}
	repo := hostRepo1635(t)
	dto := hostPlanDTO{Workers: []hostPlanWorker{{Worker: "Iris", ID: "id-iris", Repo: repo, Family: "codex", Worktrees: []string{}, Terminals: []string{}, Sessions: []string{}}}, Actions: []hostPlanAction{}, Conflicts: []hostPlanProblem{}, Unavailable: []hostPlanProblem{}}
	inv := hostInventory{Repos: map[string]string{repo: "common"}, Trees: []hostTree{{repo, "common", &hostBinding{WorkerID: "id-iris", AppID: "app-id", SessionID: "s"}}}}
	assembleHostPlan(&dto, "app-id", "https://target.example/graphql", inv)
	if len(dto.Unavailable) != 1 || dto.Unavailable[0].Reason != "binding_server_unavailable" || len(dto.Actions) != 0 {
		t.Fatalf("unknown server treated as absent: %+v", dto)
	}
}
