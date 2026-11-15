//go:build linux

package hostpoller

import (
	"context"
	"os"
	"path/filepath"
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
			a, err := orcaLauncherFor(t, f).Launch(context.Background(), OrcaLaunchSpec{Argv: []string{agentBin, PromptArg}, Dir: wt, Prompt: "go", Env: []string{"HADRON_LAUNCH_GRANT=hlg_secret"}})
			if err == nil || a != nil {
				t.Fatalf("an ambiguous create must fail with no agent: agent=%v err=%v", a, err)
			}
			// Past the whole gate wait (10 x 0.1s after the shell starts).
			time.Sleep(2500 * time.Millisecond)
			if _, serr := os.Stat(out); serr == nil {
				b, _ := os.ReadFile(out)
				t.Fatalf("the agent started although the create reply was %s: it runs untracked\n%s", mode, b)
			}
			if len(f.typed) != 1 {
				t.Fatalf("creates %d, want 1", len(f.typed))
			}
		})
	}
}
