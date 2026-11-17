//go:build linux

package hostpoller

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// cli#2161 (C12): `team session reset-context` ends its own agent by exact
// pid. The launch tells the agent that pid as HADRON_AGENT_PID: the shell's
// `$$`, which the exec keeps, so it is the pid the launcher pinned. It goes
// in the launch file, never the typed text.
func TestOrcaLaunchTellsTheAgentItsOwnPinnedPID2161(t *testing.T) {
	wt, tools := t.TempDir(), t.TempDir()
	out := filepath.Join(tools, "agent.out")
	bin := filepath.Join(tools, "agent")
	if err := os.WriteFile(bin, []byte("#!/bin/bash\necho \"$HADRON_AGENT_PID\" > "+shq(out)+"\nexec sleep 60\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	f := newFakeOrca(t)
	l := OrcaLauncher{Run: f.run, StateDir: t.TempDir(), Proc: LinuxProcessControl{}, MarkerWait: 3 * time.Second, StopWait: 3 * time.Second}
	// A stale value inherited from the poller's own environment never wins.
	a, err := l.Launch(context.Background(), OrcaLaunchSpec{Argv: []string{bin, PromptArg}, Dir: wt, Prompt: "go", Env: []string{"HADRON_AGENT_PID=1"}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.TerminateWith(nil)
	if got := strings.TrimSpace(waitFile(t, out)); got != strconv.Itoa(a.PID()) {
		t.Fatalf("HADRON_AGENT_PID=%q, the launcher pinned %d", got, a.PID())
	}
	if strings.Contains(f.typed[0], "HADRON_AGENT_PID") {
		t.Fatalf("the typed text names the variable: %q", f.typed[0])
	}
}
