//go:build !windows

package orca

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func TestHostPlan1635ActualGitAndBindingReads(t *testing.T) {
	base := t.TempDir()
	marker := filepath.Join(base, "shell-injection-marker")
	repo := filepath.Join(base, "repo$(touch "+marker+")")
	if err := os.MkdirAll(repo, 0700); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	empty := t.TempDir()
	init := exec.Command("git", "init", "--quiet", repo)
	init.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_TEMPLATE_DIR=" + empty}
	if out, err := init.CombinedOutput(); err != nil {
		t.Fatalf("fixture init: %v %s", err, out)
	}
	repo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	binding := filepath.Join(repo, ".git", bindingFileName)
	raw := []byte(`{"server":"https://target.example","workerId":"id-iris","appId":"app-id","sessionId":"fixture-session","model":"codex","token":"SECRET-not-output"}`)
	if err := os.WriteFile(binding, raw, 0600); err != nil {
		t.Fatal(err)
	}
	calls := []string{}
	run := func(ctx context.Context, bin string, args []string) ([]byte, error) {
		calls = append(calls, bin+" "+strings.Join(args, "|"))
		if !hostReadAllowed(bin, args) {
			t.Fatalf("non-read call: %s %v", bin, args)
		}
		if bin == "orca" {
			if !reflect.DeepEqual(args, []string{"terminal", "list", "--json"}) {
				t.Fatal(args)
			}
			b, _ := json.Marshal(map[string]any{"ok": true, "result": map[string]any{"terminals": []map[string]any{{"handle": "t-iris", "worktreePath": repo, "agentIdentity": "codex"}}}})
			return b, nil
		}
		return runHostRead(ctx, bin, args)
	}
	rows := []hostPlanWorker{{Worker: "Iris", ID: "id-iris", Repo: repo, Family: "codex"}}
	before := snapshotHost1635(t, repo)
	inv, err := discoverHostInventory(context.Background(), rows, run)
	if _, e := os.Stat(marker); !os.IsNotExist(e) {
		t.Fatal("shell interpolation executed")
	}
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Trees) != 1 || inv.Trees[0].Binding == nil || inv.Trees[0].Binding.SessionID != "fixture-session" {
		t.Fatalf("binding not discovered: %+v", inv)
	}
	dto := hostPlanDTO{Workers: []hostPlanWorker{{Worker: "Iris", ID: "id-iris", Repo: repo, Family: "codex", Worktrees: []string{}, Sessions: []string{}, Terminals: []string{}}}, Actions: []hostPlanAction{}, Conflicts: []hostPlanProblem{}, Unavailable: []hostPlanProblem{}}
	assembleHostPlan(&dto, "app-id", "https://target.example/graphql", inv)
	if len(dto.Workers) != 1 || len(dto.Actions) != 0 || len(dto.Conflicts) != 0 || len(dto.Workers[0].Worktrees) != 1 || len(dto.Workers[0].Terminals) != 1 {
		t.Fatalf("fulfilled inventory: %+v", dto)
	}
	after, err := os.ReadFile(binding)
	if err != nil || string(after) != string(raw) {
		t.Fatal("binding changed")
	}
	b, _ := json.Marshal(dto)
	if strings.Contains(string(b), "SECRET") {
		t.Fatal("binding secret leaked")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("shell interpolation executed")
	}
	if !reflect.DeepEqual(before, snapshotHost1635(t, repo)) {
		t.Fatal("discovery changed repository or binding")
	}
	if len(calls) != 6 {
		t.Fatalf("expected closed 6-call read sequence, got %v", calls)
	}
}
func TestHostPlan1635FileTypeAndSymlinkRefusal(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if _, err := readHostFile(fifo, 1024); err == nil {
		t.Fatal("FIFO accepted")
	}
	if time.Since(started) > time.Second {
		t.Fatal("FIFO blocked")
	}
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readHostFile(link, 1024); err == nil {
		t.Fatal("symlink accepted")
	}
}

func TestHostPlan1635SubprocessOutputCap(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "orca"), []byte("#!/bin/sh\nexec /usr/bin/head -c 9000000 /dev/zero\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":/usr/bin:/bin")
	out, err := runHostRead(context.Background(), "orca", []string{"terminal", "list", "--json"})
	if err == nil || out != nil {
		t.Fatalf("oversized subprocess output accepted: bytes=%d err=%v", len(out), err)
	}
}

func TestHostPlan1635SubprocessEnvironment(t *testing.T) {
	dir := t.TempDir()
	script := `#!/bin/sh
if [ -n "$HADRON_TOKEN$PROVIDER_SECRET$GIT_CONFIG_COUNT$GIT_CONFIG_KEY_0$GIT_CONFIG_VALUE_0" ]; then echo leaked; exit 1; fi
if [ "$GIT_CONFIG_GLOBAL" != /dev/null ] || [ "$GIT_CONFIG_NOSYSTEM" != 1 ] || [ "$GIT_OPTIONAL_LOCKS" != 0 ] || [ "$GIT_TERMINAL_PROMPT" != 0 ]; then echo config-not-isolated; exit 1; fi
printf 'isolated'
`
	if err := os.WriteFile(filepath.Join(dir, "orca"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":/usr/bin:/bin")
	for _, key := range []string{"HADRON_TOKEN", "PROVIDER_SECRET", "GIT_CONFIG_COUNT", "GIT_CONFIG_KEY_0", "GIT_CONFIG_VALUE_0"} {
		t.Setenv(key, "SENTINEL")
	}
	out, err := runHostRead(context.Background(), "orca", []string{"terminal", "list", "--json"})
	if err != nil || string(out) != "isolated" {
		t.Fatalf("environment isolation failed: %q %v", out, err)
	}
}

func TestHostPlan1635RealLinkedAndPrunableWorktrees(t *testing.T) {
	root := t.TempDir()
	root, rootErr := filepath.EvalSymlinks(root)
	if rootErr != nil {
		t.Fatal(rootErr)
	}
	repo := filepath.Join(root, "repo")
	linked := filepath.Join(root, "linked")
	home := t.TempDir()
	git := func(args ...string) []byte {
		t.Helper()
		c := exec.Command("git", args...)
		c.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_TEMPLATE_DIR=" + t.TempDir()}
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("fixture git %v: %v %s", args, err, out)
		}
		return out
	}
	git("init", "--quiet", repo)
	git("-C", repo, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--quiet", "--allow-empty", "-m", "fixture")
	git("-C", repo, "worktree", "add", "--quiet", "--detach", linked)
	repo, _ = filepath.EvalSymlinks(repo)
	linked, _ = filepath.EvalSymlinks(linked)
	dir := strings.TrimSpace(string(git("-C", linked, "rev-parse", "--absolute-git-dir")))
	raw := []byte(`{"server":"https://target.example","workerId":"id-iris","appId":"app-id","sessionId":"linked-session"}`)
	if err := os.WriteFile(filepath.Join(dir, bindingFileName), raw, 0600); err != nil {
		t.Fatal(err)
	}
	run := func(ctx context.Context, bin string, args []string) ([]byte, error) {
		if bin == "orca" {
			return []byte(`{"ok":true,"result":{"terminals":[]}}`), nil
		}
		return runHostRead(ctx, bin, args)
	}
	rows := []hostPlanWorker{{Worker: "Iris", ID: "id-iris", Repo: repo, Family: "codex"}}
	inv, err := discoverHostInventory(context.Background(), rows, run)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tree := range inv.Trees {
		if tree.Path == linked && tree.Binding != nil && tree.Binding.SessionID == "linked-session" {
			found = true
		}
	}
	if !found {
		t.Fatalf("linked-worktree per-gitdir binding not read: %+v", inv)
	}
	if err := os.RemoveAll(linked); err != nil {
		t.Fatal(err)
	}
	before := git("-C", repo, "worktree", "list", "--porcelain", "-z")
	if !hostPorcelainAttribute1729(string(before), "prunable") {
		t.Fatal("fixture does not have a prunable record")
	}
	inv, err = discoverHostInventory(context.Background(), rows, run)
	if err != nil || len(inv.Trees) != 1 {
		t.Fatalf("prunable stale record poisoned scan: %+v %v", inv, err)
	}
	after := git("-C", repo, "worktree", "list", "--porcelain", "-z")
	if string(before) != string(after) {
		t.Fatal("read-only discovery modified stale git metadata")
	}
	// A missing locked worktree is not marked prunable: its absence is not
	// permission to omit it from the bounded inventory.
	locked := filepath.Join(root, "locked")
	git("-C", repo, "worktree", "add", "--quiet", "--detach", locked)
	git("-C", repo, "worktree", "lock", locked)
	if err := os.RemoveAll(locked); err != nil {
		t.Fatal(err)
	}
	before = git("-C", repo, "worktree", "list", "--porcelain", "-z")
	lockedRecord := ""
	for _, record := range strings.Split(string(before), "\x00\x00") {
		if strings.HasPrefix(record, "worktree "+locked+"\x00") {
			lockedRecord = record
		}
	}
	if lockedRecord == "" || !hostPorcelainAttribute1729(lockedRecord, "locked") || hostPorcelainAttribute1729(lockedRecord, "prunable") {
		t.Fatalf("fixture is not an unmarked missing worktree: %q", lockedRecord)
	}
	_, err = discoverHostInventory(context.Background(), rows, run)
	var inventoryErr *hostInventoryError
	if exitcode.FromError(err) != exitcode.Error || !errors.As(err, &inventoryErr) || inventoryErr.Reason != "git_inventory_unavailable" {
		t.Fatalf("unmarked absent worktree treated as empty: %v", err)
	}
	after = git("-C", repo, "worktree", "list", "--porcelain", "-z")
	if string(before) != string(after) {
		t.Fatal("failed discovery modified locked worktree metadata")
	}
}

func TestHostPlan1635SubprocessHonorsCallerDeadline(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "orca"), []byte("#!/bin/sh\nexec /bin/sleep 10\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":/usr/bin:/bin")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	out, err := runHostRead(ctx, "orca", []string{"terminal", "list", "--json"})
	if err == nil || out != nil || time.Since(start) > 2*time.Second {
		t.Fatalf("deadline not enforced: %v %v", err, time.Since(start))
	}
}

func hostPorcelainAttribute1729(record, name string) bool {
	for _, field := range strings.Split(record, "\x00") {
		if field == name || strings.HasPrefix(field, name+" ") {
			return true
		}
	}
	return false
}

func TestHostPorcelainFixtureAttributeIsExact1729(t *testing.T) {
	for _, name := range []string{"locked", "prunable"} {
		if hostPorcelainAttribute1729("worktree /tmp/"+name+"\x00HEAD abc\x00", name) {
			t.Fatalf("path forged %s attribute", name)
		}
		if !hostPorcelainAttribute1729("worktree /tmp/repo\x00"+name+" reason\x00", name) {
			t.Fatalf("actual %s field missed", name)
		}
	}
}
