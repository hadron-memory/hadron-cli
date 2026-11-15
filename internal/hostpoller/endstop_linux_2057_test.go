//go:build linux

package hostpoller

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
	// Under load the shell may not have forked its child yet: the stop can
	// only be asked to take down a group that has one.
	waitFor(t, "the agent's child to start", func() bool { return groupHasOtherMembers(c.Process.Pid, c.Process.Pid) })
	done := reaped(c)
	stops, err := stopWorktreeAgents(root, 2*time.Second, nil)
	if err != nil || len(stops) != 1 || !stops[0].Stopped || stops[0].PID != c.Process.Pid {
		t.Fatalf("stops = %+v err = %v", stops, err)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the agent is still running")
	}
	waitFor(t, "the agent's child to be gone (the whole group must go)", func() bool { return !groupHasOtherMembers(c.Process.Pid, c.Process.Pid) })
}

func TestStopWorktreeAgentsKillsAnAgentThatIgnoresSIGTERM(t *testing.T) {
	root := t.TempDir()
	c := spawnAgent(t, root, "#!/bin/sh\ntrap '' TERM\nwhile :; do sleep 1; done\n")
	done := reaped(c)
	start := time.Now()
	stops, err := stopWorktreeAgents(root, 300*time.Millisecond, nil)
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
	if _, err := stopWorktreeAgents(root, time.Second, nil); err != nil {
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
	stops, err := stopWorktreeAgents(root, time.Second, nil)
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
	stop := stopAgentGroup(WorktreeAgent{PID: c.Process.Pid, Display: "claude", StartTicks: 1}, root, time.Second, nil)
	if stop.Stopped || !strings.Contains(stop.Detail, "not touched") {
		t.Fatalf("stop = %+v", stop)
	}
	if err := syscall.Kill(c.Process.Pid, 0); err != nil {
		t.Fatalf("the process was signalled: %v", err)
	}
}

type stopHandle struct {
	id  ProcessIdentity
	err error
}

func (f stopHandle) Identity() ProcessIdentity { return f.id }
func (f stopHandle) SignalWithin(syscall.Signal, time.Duration, time.Duration, func() (time.Duration, error)) (time.Duration, error) {
	return 0, f.err
}
func (f stopHandle) Close() {}

// Local P2: a process that could not be pinned for any reason but its being
// gone was not signalled and is not reported stopped.
func TestStopAgentGroupDoesNotReportStoppedWhenItCouldNotPinTheProcess(t *testing.T) {
	root := t.TempDir()
	c := spawnAgent(t, root, "#!/bin/sh\nwhile :; do sleep 1; done\n")
	a := WorktreeAgent{PID: c.Process.Pid, Display: "claude", StartTicks: 1}
	deny := func(int) (ProcessHandle, error) { return nil, syscall.EPERM }
	if s := stopAgentGroupWith(a, root, time.Second, nil, deny); s.Stopped || !strings.Contains(s.Detail, "not touched") {
		t.Fatalf("stop = %+v", s)
	}
	gone := func(int) (ProcessHandle, error) { return nil, syscall.ESRCH }
	if s := stopAgentGroupWith(a, root, time.Second, nil, gone); !s.Stopped {
		t.Fatalf("a process that is gone is a stopped one: %+v", s)
	}
}

// Blind P2: a failed liveness probe is not proof of an exit.
func TestPinnedGoneOnlyOnADefinitiveAbsence(t *testing.T) {
	ctrl := LinuxProcessControl{}
	for name, tc := range map[string]struct {
		err  error
		want bool
	}{
		"no such process": {syscall.ESRCH, true},
		"permission":      {syscall.EPERM, false},
		"clock":           {ErrClock, false},
		"lease":           {errors.New("odd"), false},
	} {
		m := pinned{pid: os.Getpid(), h: stopHandle{id: ProcessIdentity{PID: os.Getpid()}, err: tc.err}}
		if got := m.gone(ctrl, 0); got != tc.want {
			t.Errorf("%s: gone = %v, want %v", name, got, tc.want)
		}
	}
}

// Blind P2 (second): the lead process is re-checked against the worktree
// before it becomes a signal target.
func TestStopAgentGroupDeclinesAnAgentThatLeftTheWorktree(t *testing.T) {
	root, elsewhere := t.TempDir(), t.TempDir()
	c := spawnAgent(t, elsewhere, "#!/bin/sh\nwhile :; do sleep 1; done\n")
	// The census saw it under root; it now works in elsewhere.
	ticks := startTicks(t, c.Process.Pid)
	s := stopAgentGroup(WorktreeAgent{PID: c.Process.Pid, Display: "claude", StartTicks: ticks}, root, time.Second, nil)
	if s.Stopped || !strings.Contains(s.Detail, "no longer working inside") {
		t.Fatalf("stop = %+v", s)
	}
	if err := syscall.Kill(c.Process.Pid, 0); err != nil {
		t.Fatalf("an agent outside the worktree was signalled: %v", err)
	}
}

// The binding guard is the last thing asked before the first signal.
func TestStopAgentGroupSignalsNothingWhenTheGuardSaysNo(t *testing.T) {
	root := t.TempDir()
	c := spawnAgent(t, root, "#!/bin/sh\nwhile :; do sleep 1; done\n")
	ticks := startTicks(t, c.Process.Pid)
	s := stopAgentGroup(WorktreeAgent{PID: c.Process.Pid, Display: "claude", StartTicks: ticks}, root, time.Second, func() (func(), bool) { return nil, false })
	if s.Stopped || !strings.Contains(s.Detail, "binding changed") {
		t.Fatalf("stop = %+v", s)
	}
	if err := syscall.Kill(c.Process.Pid, 0); err != nil {
		t.Fatalf("signalled despite the guard: %v", err)
	}
}

func startTicks(t *testing.T, pid int) int64 {
	t.Helper()
	st, ok := readStat(pid)
	if !ok {
		t.Fatal("no stat")
	}
	n, err := strconv.ParseInt(st.start, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// Blind delta P2: a successful probe followed by a stat read that FAILED is
// not an exit; only a missing /proc entry is.
func TestPinnedGoneAfterAProbeTreatsAFailedStatAsUnknown(t *testing.T) {
	ctrl := LinuxProcessControl{}
	was := statReader
	defer func() { statReader = was }()
	m := pinned{pid: 1, h: stopHandle{id: ProcessIdentity{PID: 1, StartTime: "5"}}}
	for name, tc := range map[string]struct {
		err  error
		want bool
	}{
		"no /proc entry": {os.ErrNotExist, true},
		"permission":     {os.ErrPermission, false},
		"io":             {errors.New("input/output error"), false},
	} {
		statReader = func(int) (procStatLine, error) { return procStatLine{}, tc.err }
		if got := m.gone(ctrl, 0); got != tc.want {
			t.Errorf("%s: gone = %v, want %v", name, got, tc.want)
		}
	}
	statReader = func(int) (procStatLine, error) { return procStatLine{state: "Z", start: "5"}, nil }
	if !m.gone(ctrl, 0) {
		t.Error("a zombie cannot run")
	}
	statReader = func(int) (procStatLine, error) { return procStatLine{state: "S", start: "6"}, nil }
	if !m.gone(ctrl, 0) {
		t.Error("a replaced process is not the pinned one")
	}
	statReader = func(int) (procStatLine, error) { return procStatLine{state: "S", start: "5"}, nil }
	if m.gone(ctrl, 0) {
		t.Error("the same live process is not gone")
	}
}

// The binding is asked again before SIGKILL: a replacement that bound during
// the grace period is not killed by the earlier end.
func TestStopAgentGroupDoesNotEscalateWhenTheBindingChangedDuringTheGrace(t *testing.T) {
	root := t.TempDir()
	c := spawnAgent(t, root, "#!/bin/sh\ntrap '' TERM\nwhile :; do sleep 1; done\n")
	ticks := startTicks(t, c.Process.Pid)
	calls := 0
	s := stopAgentGroup(WorktreeAgent{PID: c.Process.Pid, Display: "claude", StartTicks: ticks}, root, 300*time.Millisecond, func() (func(), bool) { calls++; return func() {}, calls == 1 })
	if s.Stopped || !strings.Contains(s.Detail, "was not killed") {
		t.Fatalf("stop = %+v", s)
	}
	if err := syscall.Kill(c.Process.Pid, 0); err != nil {
		t.Fatalf("killed despite the binding change: %v", err)
	}
}

// A stat line that cannot be parsed is an unknown, never a gone process.
func TestParseProcStatRefusesAMalformedLine(t *testing.T) {
	for _, line := range []string{"", "12 (claude) S", "12 claude S 1 2 3", "12 (c) S 1 x 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22"} {
		if _, ok := parseProcStat(line); ok {
			t.Errorf("parsed %q", line)
		}
	}
	if st, ok := parseProcStat("12 (a (b) c) S 1 99 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 777 22"); !ok || st.pgrp != 99 || st.start != "777" || st.state != "S" {
		t.Errorf("comm with parens misread: %+v ok=%v", st, ok)
	}
}

// #2068: what the authorization grants is released right after each signal,
// before the grace wait: nothing holds the binding lock while the stop waits.
func TestStopAgentGroupReleasesTheAuthorizationBeforeTheGraceWait(t *testing.T) {
	root := t.TempDir()
	c := spawnAgent(t, root, "#!/bin/sh\ntrap '' TERM\nwhile :; do sleep 1; done\n")
	ticks := startTicks(t, c.Process.Pid)
	asked, released := 0, 0
	var heldAtSecondAsk int
	auth := func() (func(), bool) {
		asked++
		if asked == 2 {
			heldAtSecondAsk = asked - 1 - released // grants still outstanding when KILL is asked
		}
		return func() { released++ }, true
	}
	s := stopAgentGroup(WorktreeAgent{PID: c.Process.Pid, Display: "claude", StartTicks: ticks}, root, 300*time.Millisecond, auth)
	if !s.Stopped {
		t.Fatalf("%+v", s)
	}
	if asked != 2 || released != 2 {
		t.Fatalf("asked=%d released=%d", asked, released)
	}
	if heldAtSecondAsk != 0 {
		t.Fatalf("the first grant was still held across the grace wait (%d outstanding)", heldAtSecondAsk)
	}
}
