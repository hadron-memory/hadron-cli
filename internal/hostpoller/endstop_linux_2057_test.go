//go:build linux

package hostpoller

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// spawnAgent starts a fake agent named claude in its own session and process
// group, with a child in the same group (the tool shell an agent runs).
func spawnAgent(t *testing.T, dir, script string) *exec.Cmd {
	t.Helper()
	bin := filepath.Join(dir, "claude")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	c := exec.Command(bin)
	c.Dir = dir
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL); _, _ = c.Process.Wait() })
	time.Sleep(150 * time.Millisecond)
	return c
}

func reaped(c *exec.Cmd) <-chan struct{} {
	ch := make(chan struct{})
	go func() { _, _ = c.Process.Wait(); close(ch) }()
	return ch
}

func TestStopWorktreeAgentsStopsTheAgentAndItsGroup(t *testing.T) {
	root := t.TempDir()
	c := spawnAgent(t, root, "#!/bin/sh\nsleep 300 &\nwait\n")
	done := reaped(c)
	stops, err := stopWorktreeAgents(root, 2*time.Second)
	if err != nil || len(stops) != 1 || !stops[0].Stopped || stops[0].PID != c.Process.Pid {
		t.Fatalf("stops = %+v err = %v", stops, err)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the agent is still running")
	}
	if groupHasOtherMembers(c.Process.Pid, c.Process.Pid) {
		t.Fatal("the agent's child outlived the stop: the whole group must go")
	}
}

func TestStopWorktreeAgentsKillsAnAgentThatIgnoresSIGTERM(t *testing.T) {
	root := t.TempDir()
	c := spawnAgent(t, root, "#!/bin/sh\ntrap '' TERM\nwhile :; do sleep 1; done\n")
	done := reaped(c)
	start := time.Now()
	stops, err := stopWorktreeAgents(root, 300*time.Millisecond)
	if err != nil || len(stops) != 1 || !stops[0].Stopped || !strings.Contains(stops[0].Detail, "killed") {
		t.Fatalf("stops = %+v err = %v", stops, err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("took %s", time.Since(start))
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("SIGKILL did not end the agent")
	}
}

func TestStopWorktreeAgentsLeavesAnotherWorktreesAgentAlone(t *testing.T) {
	root, other := t.TempDir(), t.TempDir()
	mine := spawnAgent(t, root, "#!/bin/sh\nwhile :; do sleep 1; done\n")
	theirs := spawnAgent(t, other, "#!/bin/sh\nwhile :; do sleep 1; done\n")
	_ = reaped(mine)
	if _, err := stopWorktreeAgents(root, time.Second); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(theirs.Process.Pid, 0); err != nil {
		t.Fatalf("another worktree's agent was touched: %v", err)
	}
}

// A group that holds a member outside the worktree is not the worker's alone
// (a multiplexer started the agent without a group of its own): only the
// agent itself is signalled, never the group.
func TestStopWorktreeAgentsSignalsOnlyTheAgentWhenItsGroupIsShared(t *testing.T) {
	root, elsewhere := t.TempDir(), t.TempDir()
	bin := filepath.Join(root, "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nwhile :; do sleep 1; done\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A shell leads the group; a bystander with another cwd shares it.
	lead := exec.Command("/bin/sh", "-c", "cd "+elsewhere+" && sleep 300 & cd "+root+" && exec "+bin)
	lead.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := lead.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-lead.Process.Pid, syscall.SIGKILL); _, _ = lead.Process.Wait() })
	time.Sleep(200 * time.Millisecond)
	_ = reaped(lead)
	stops, err := stopWorktreeAgents(root, time.Second)
	if err != nil || len(stops) != 1 || !stops[0].Stopped || !strings.Contains(stops[0].Detail, "agent process only") {
		t.Fatalf("stops = %+v err = %v", stops, err)
	}
	if err := syscall.Kill(-lead.Process.Pid, 0); err != nil {
		t.Fatalf("the bystander in the shared group was signalled: %v", err)
	}
}

// A pid that now belongs to another process than the census saw (the number
// was reused) is never signalled.
func TestStopAgentGroupRefusesAReusedPid(t *testing.T) {
	root := t.TempDir()
	c := spawnAgent(t, root, "#!/bin/sh\nwhile :; do sleep 1; done\n")
	stop := stopAgentGroup(WorktreeAgent{PID: c.Process.Pid, Display: "claude", StartTicks: 1}, root, time.Second)
	if stop.Stopped || !strings.Contains(stop.Detail, "not touched") {
		t.Fatalf("stop = %+v", stop)
	}
	if err := syscall.Kill(c.Process.Pid, 0); err != nil {
		t.Fatalf("the process was signalled: %v", err)
	}
}
