//go:build !windows

package hostpoller

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The census finds only processes working IN the worktree: cwd at the root
// or under it, never a sibling. A process elsewhere is another worktree's
// business — stopping it from this worktree's sweep would be the wrong tree.
func TestScanWorktreeAgentsScopesOnCwd(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	bin := filepath.Join(root, "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nwhile :; do sleep 1; done\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	spawn := func(dir string) *exec.Cmd {
		c := exec.Command(bin)
		c.Dir = dir
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Process.Kill(); _, _ = c.Process.Wait() })
		time.Sleep(50 * time.Millisecond)
		return c
	}
	inside := spawn(sub)
	spawn(outside)
	found := ScanWorktreeAgents(root)
	if len(found) != 1 || found[0].PID != inside.Process.Pid || found[0].Base != "claude" || found[0].Supervised {
		t.Fatalf("census = %+v, want exactly the process in %s", found, sub)
	}
	if found[0].StartTicks == 0 {
		t.Fatal("census lost the start-time ticks the stop must re-verify")
	}
}

// hadron-cli#2027 — the duplicate census's identification rules. agentBase is
// the closed set: each row names an identification the sweep must NOT make
// (the wrapper counted as its child's agent, a "-c" command line counted as
// an invocation, a passing mention counted as a process).

func TestAgentBaseIdentifiesOnlyAgents(t *testing.T) {
	for _, tc := range []struct {
		name string
		argv []string
		want string
	}{
		{"direct claude", []string{"/usr/local/bin/claude", "--model", "glm"}, "claude"},
		{"direct codex", []string{"codex"}, "codex"},
		{"shebang shim names the agent", []string{"/bin/sh", "/home/u/bin/claude"}, "claude"},
		{"bash shim", []string{"/bin/bash", "/opt/codex"}, "codex"},
		{"the supervisor wrapper is never an agent", []string{"/bin/sh", "/home/u/bin/worker-supervise", "/home/u/bin/claude"}, ""},
		{"direct supervisor is never an agent", []string{"/usr/local/bin/worker-supervise", "--family", "codex"}, ""},
		{"-c is not an invocation", []string{"/bin/sh", "-c", "claude --help"}, ""},
		{"a passing mention is not a process", []string{"/bin/sh", "/home/u/bin/worker-supervise", "--print", "claude"}, ""},
		{"an unrelated script", []string{"/bin/sh", "/home/u/bin/tui"}, ""},
		{"an unrelated binary", []string{"/bin/cat", "/home/u/bin/claude"}, ""},
		{"empty", nil, ""},
	} {
		if got := agentBase(tc.argv); got != tc.want {
			t.Errorf("%s: agentBase(%q) = %q, want %q", tc.name, tc.argv, got, tc.want)
		}
	}
}

// procStat reads ppid and start ticks after the LAST ')' — comm can hold
// spaces and parens, and miscounting it names the wrong parent (a supervised
// agent read as unsupervised gets stopped).
func TestProcStatCountsAfterTheLastParen(t *testing.T) {
	fields := make([]string, 44)
	for i := range fields {
		fields[i] = "0"
	}
	fields[1] = "4242" // ppid, stat field 4
	fields[19] = "987" // start ticks, stat field 22
	stat := "1234 (a name (with parens)) " + strings.Join(fields, " ")
	ppid, start, ok := parseStatLine(stat)
	if !ok || ppid != 4242 || start != 987 {
		t.Fatalf("ppid=%d start=%d ok=%v", ppid, start, ok)
	}
	if _, _, ok := parseStatLine("1234 (short) S 1"); ok {
		t.Fatal("a truncated stat line must not parse")
	}
}

// hadron-cli#2027 review (P1): one logical agent can run as a CHAIN — the
// supervisor wrapper launches a shim, the shim runs the real binary as a
// child. Counting the chain's members as separate agents made the REAL agent
// (the child, whose parent is the shim, not the wrapper) a stop subject of a
// perfectly healthy worker. The census must answer ONE supervised agent.
func TestScanWorktreeAgentsCollapsesAChainToOneSupervisedAgent(t *testing.T) {
	root := t.TempDir()
	shim := filepath.Join(root, "claude")       // interpreter-wrapped match
	child := filepath.Join(root, "codex-child") // interpreter-wrapped match
	if err := os.WriteFile(child, []byte("#!/bin/sh\nwhile :; do sleep 1; done\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(shim, []byte("#!/bin/sh\n\""+child+"\" &\nwait\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	wrap := filepath.Join(root, "worker-supervise")
	if err := os.WriteFile(wrap, []byte("#!/bin/sh\n\""+shim+"\" &\nwait\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := exec.Command(wrap)
	c.Dir = root
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Process.Kill(); _, _ = c.Process.Wait() })
	found := []WorktreeAgent{}
	for i := 0; i < 100 && len(found) == 0; i++ {
		time.Sleep(20 * time.Millisecond)
		found = ScanWorktreeAgents(root)
	}
	if len(found) != 1 {
		t.Fatalf("census counted %d agents for one chain: %+v", len(found), found)
	}
	if !found[0].Supervised {
		t.Fatalf("the chain's agent must be supervised through its shim: %+v", found[0])
	}
	if found[0].Base != "claude" || !strings.Contains(found[0].Display, string(os.PathSeparator)+"claude") {
		t.Fatalf("the logical agent is the chain's oldest census member (the shim): %+v", found[0])
	}
}

// An agent's own nested one-shot (behind a tool shell that stays in the
// worktree) is the SAME logical agent, not a duplicate: without the merge the
// worker's own tooling would be stopped as an unsupervised second agent.
func TestScanWorktreeAgentsMergesANestedOneShotIntoItsAgent(t *testing.T) {
	root := t.TempDir()
	// The main agent runs its own tool shell; the tool shell runs a nested
	// one-shot as ITS child — one logical agent, three processes.
	nested := filepath.Join(root, "codex")
	if err := os.WriteFile(nested, []byte("#!/bin/sh\nwhile :; do sleep 1; done\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	tool := filepath.Join(root, "tool-shell")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\n\""+nested+"\" &\nwait\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	agent := filepath.Join(root, "claude")
	if err := os.WriteFile(agent, []byte("#!/bin/sh\n\""+tool+"\" &\nwait\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := exec.Command(agent)
	c.Dir = root
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Process.Kill(); _, _ = c.Process.Wait() })
	found := []WorktreeAgent{}
	for i := 0; i < 100 && len(found) == 0; i++ {
		time.Sleep(20 * time.Millisecond)
		found = ScanWorktreeAgents(root)
	}
	if len(found) != 1 {
		t.Fatalf("census counted %d agents for one agent with its own one-shot: %+v", len(found), found)
	}
	if found[0].Base != "claude" {
		t.Fatalf("the logical agent is the chain's oldest member: %+v", found[0])
	}
	if found[0].Supervised {
		t.Fatalf("no worker-supervise anywhere in this chain: %+v", found[0])
	}
}
