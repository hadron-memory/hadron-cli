//go:build linux

package hostpoller

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// cli#2130 (Jibril's P2, reproduced by Chip against real Orca): Orca really
// creates the terminal and its shell starts, but the create reply is lost
// (non-zero) or malformed (exit 0). Launch has no handle, so it can close
// nothing; the agent must therefore never have started. Before the fix the
// shell sourced the launch file and exec'd the agent, which then ran
// untracked (no agents entry, no stop).
func TestOrcaLaunchStartsNoAgentWhenTheCreateReplyIsLostOrMalformed2130(t *testing.T) {
	for _, c := range []struct {
		mode      string
		noCleanup bool // the poller died before deleting the launch file
	}{{"lost", false}, {"malformed", false}, {"lost", true}, {"malformed", true}} {
		mode := c.mode
		name := mode
		if c.noCleanup {
			name += ", launch file left behind"
		}
		t.Run(name, func(t *testing.T) {
			if c.noCleanup {
				old := removeLaunchFile
				removeLaunchFile = func(string) error { return nil }
				t.Cleanup(func() { removeLaunchFile = old })
			}
			wt, tools := t.TempDir(), t.TempDir()
			out := filepath.Join(tools, "agent.out")
			agentBin := fakeAgentScript(t, tools, out, true)
			f := newFakeOrca(t)
			f.badReply, f.replyDelay = mode, 700*time.Millisecond
			// A short gate so the check below outlasts the command's wait: a
			// shell that starts the agent once the wait runs out is caught.
			old := gateTries
			gateTries = 10
			t.Cleanup(func() { gateTries = old })
			l := orcaLauncherFor(t, f)
			a, err := l.Launch(context.Background(), OrcaLaunchSpec{Argv: []string{agentBin, PromptArg}, Dir: wt, Prompt: "go", Env: []string{"HADRON_LAUNCH_GRANT=hlg_secret"}})
			if err == nil || a != nil {
				t.Fatalf("an ambiguous create must fail with no agent: agent=%v err=%v", a, err)
			}
			if !c.noCleanup {
				// Rook's P3 (cli#2136): the launch file holds the grant and the
				// prompt; it and any gate are gone when Launch returns.
				left, _ := filepath.Glob(filepath.Join(l.StateDir, "launch", "*"))
				if len(left) != 0 {
					t.Fatalf("launch files left behind: %v", left)
				}
			}
			// Past the whole gate wait (10 x 0.1s after the shell starts).
			noAgentAfter(t, out, 2500*time.Millisecond)
			if len(f.typed) != 1 {
				t.Fatalf("creates %d, want 1", len(f.typed))
			}
		})
	}
}

func shortGate(t *testing.T) {
	t.Helper()
	old := gateTries
	gateTries = 10 // 1s
	t.Cleanup(func() { gateTries = old })
}

func noAgentAfter(t *testing.T, out string, d time.Duration) {
	t.Helper()
	time.Sleep(d)
	_, err := os.Stat(out)
	if err == nil {
		b, _ := os.ReadFile(out)
		t.Fatalf("an agent started:\n%s", b)
	}
	if !os.IsNotExist(err) {
		t.Fatalf("could not tell whether an agent started: %v", err) // never a pass (cli#2136)
	}
}

// Wolfgang's (2): a good create reply that arrives once the command may have
// stopped waiting writes no gate; the launch fails honestly and closes the
// terminal, which held only an empty shell.
func TestOrcaLaunchWritesNoGateForALateCreateReply2130(t *testing.T) {
	shortGate(t)
	wt, tools := t.TempDir(), t.TempDir()
	out := filepath.Join(tools, "agent.out")
	agentBin := fakeAgentScript(t, tools, out, true)
	f := newFakeOrca(t)
	f.beforeReply = func() { time.Sleep(1500 * time.Millisecond) }
	a, err := orcaLauncherFor(t, f).Launch(context.Background(), OrcaLaunchSpec{Argv: []string{agentBin, PromptArg}, Dir: wt, Prompt: "go"})
	if err == nil || a != nil || !strings.Contains(err.Error(), "too late") || !strings.Contains(err.Error(), "nothing was started") || len(f.closed) != 1 {
		t.Fatalf("a late reply: agent=%v err=%v closed=%v", a, err, f.closed)
	}
	noAgentAfter(t, out, 1500*time.Millisecond)
}

// Rook's (2): a launch cancelled after a good reply writes no gate.
func TestOrcaLaunchWritesNoGateOnceCancelled2130(t *testing.T) {
	wt, tools := t.TempDir(), t.TempDir()
	out := filepath.Join(tools, "agent.out")
	agentBin := fakeAgentScript(t, tools, out, true)
	f := newFakeOrca(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.beforeReply = cancel
	a, err := orcaLauncherFor(t, f).Launch(ctx, OrcaLaunchSpec{Argv: []string{agentBin, PromptArg}, Dir: wt, Prompt: "go"})
	if err == nil || a != nil || !strings.Contains(err.Error(), "cancelled") || len(f.closed) != 1 {
		t.Fatalf("a cancelled launch: agent=%v err=%v closed=%v", a, err, f.closed)
	}
	noAgentAfter(t, out, 1200*time.Millisecond)
}

// Rook's (1) and (4): the gate is a hard condition after the wait. A gate
// that appears only once the command has stopped waiting starts nothing,
// even with the launch file still there (a poller that died before cleanup).
func TestOrcaLaunchCommandIgnoresAGateAfterItsWait2130(t *testing.T) {
	shortGate(t)
	old := removeLaunchFile
	removeLaunchFile = func(string) error { return nil }
	t.Cleanup(func() { removeLaunchFile = old })
	wt, tools := t.TempDir(), t.TempDir()
	out := filepath.Join(tools, "agent.out")
	agentBin := fakeAgentScript(t, tools, out, true)
	f := newFakeOrca(t)
	f.badReply = "lost"
	l := orcaLauncherFor(t, f)
	if _, err := l.Launch(context.Background(), OrcaLaunchSpec{Argv: []string{agentBin, PromptArg}, Dir: wt, Prompt: "go"}); err == nil {
		t.Fatal("a lost reply was taken as a launch")
	}
	envs, _ := filepath.Glob(filepath.Join(l.StateDir, "launch", "*.env"))
	if len(envs) != 1 {
		t.Fatalf("launch files %v", envs)
	}
	time.Sleep(1500 * time.Millisecond) // past the command's 1s wait
	if err := writeGate(strings.TrimSuffix(envs[0], ".env") + ".go"); err != nil {
		t.Fatal(err)
	}
	noAgentAfter(t, out, 1500*time.Millisecond)
}
