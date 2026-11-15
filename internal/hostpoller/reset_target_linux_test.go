//go:build linux

package hostpoller

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestValidateResetTarget2057(t *testing.T) {
	root := t.TempDir()
	c := spawnAgent(t, root, "#!/bin/sh\nsleep 300 &\nwait\n")
	h, err := (LinuxProcessControl{}).Observe(c.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	target := ResetIdleTarget{Worktree: root, Family: "claude", Process: h.Identity()}
	if err := ValidateResetTarget(target); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "linked-tree")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	linked := target
	linked.Worktree = alias
	if err := ValidateResetTarget(linked); err != nil {
		t.Fatalf("symlinked worktree: %v", err)
	}
	wrong := target
	wrong.Process.StartTime = "wrong"
	if ValidateResetTarget(wrong) == nil {
		t.Fatal("reused pid accepted")
	}
	wrong = target
	wrong.Family = "codex"
	if ValidateResetTarget(wrong) == nil {
		t.Fatal("wrong family accepted")
	}
	wrong = target
	wrong.Worktree = t.TempDir()
	if ValidateResetTarget(wrong) == nil {
		t.Fatal("wrong directory accepted")
	}
	spawnAgent(t, root, "#!/bin/sh\nsleep 300 &\nwait\n")
	if ValidateResetTarget(target) == nil {
		t.Fatal("duplicate agents accepted")
	}
}

// A launch wrapper may own the group, but an agent in another group is not
// the pinned launch. These are private children, never a live worker.
func TestValidateResetTargetWrapper2057(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "claude"), []byte("#!/bin/sh\nsleep 300 &\nwait\n"), 0755); err != nil {
		t.Fatal(err)
	}
	wrapper := exec.Command("/bin/sh", "-c", "./claude & wait")
	wrapper.Dir = root
	wrapper.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := wrapper.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-wrapper.Process.Pid, syscall.SIGKILL); _, _ = wrapper.Process.Wait() })
	h, err := (LinuxProcessControl{}).Observe(wrapper.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	target := ResetIdleTarget{Worktree: root, Family: "claude", Process: h.Identity()}
	deadline := time.Now().Add(3 * time.Second)
	for {
		err := ValidateResetTarget(target)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("wrapper agent did not become ready: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	other := t.TempDir()
	otherAgent := spawnAgent(t, other, "#!/bin/sh\nsleep 300 &\nwait\n")
	oh, err := (LinuxProcessControl{}).Observe(otherAgent.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	defer oh.Close()
	target.Process = oh.Identity()
	if ValidateResetTarget(target) == nil {
		t.Fatal("agent from foreign process group accepted")
	}
}
