//go:build !windows

package orca

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
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
	raw := []byte(`{"workerId":"id-iris","appId":"app-id","sessionId":"fixture-session","model":"codex","token":"SECRET-not-output"}`)
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
	rows := []hostRosterWorker{{Worker: "Iris", Repo: repo, Family: "codex"}}
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
	assembleHostPlan(&dto, "app-id", inv)
	if len(dto.Actions) != 0 || len(dto.Conflicts) != 0 || len(dto.Workers[0].Worktrees) != 1 || len(dto.Workers[0].Terminals) != 1 {
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
