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
	for _, mode := range []string{"lost", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			wt, tools := t.TempDir(), t.TempDir()
			out := filepath.Join(tools, "agent.out")
			agentBin := fakeAgentScript(t, tools, out, true)
			f := newFakeOrca(t)
			f.badReply, f.replyDelay = mode, 700*time.Millisecond
			a, err := orcaLauncherFor(t, f).Launch(context.Background(), OrcaLaunchSpec{Argv: []string{agentBin, PromptArg}, Dir: wt, Prompt: "go", Env: []string{"HADRON_LAUNCH_GRANT=hlg_secret"}})
			if err == nil || a != nil {
				t.Fatalf("an ambiguous create must fail with no agent: agent=%v err=%v", a, err)
			}
			// Give a started shell every chance to reach the agent.
			time.Sleep(1500 * time.Millisecond)
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
