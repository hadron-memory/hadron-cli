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
