//go:build linux

package team

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// cli#2161 (C12): `session reset-context` ends its own agent by exact pid
// (Wolfgang #109662): the pid the Orca launch exported as HADRON_AGENT_PID,
// and only when that pid is an ANCESTOR of the command. Anything else is
// never signalled: the agent is told to exit instead.

// TestEndAgentHelper2161 is not a test on its own: the parent test runs the
// test binary as a child of a bash that exported its own pid, and this child
// ends that bash.
func TestEndAgentHelper2161(t *testing.T) {
	if os.Getenv("HADRON_TEST_END_AGENT_2161") != "1" {
		t.Skip("helper process only")
	}
	want := agentEnded
	if os.Getenv("HADRON_TEST_SIGNAL_FAILS_2161") == "1" {
		sendAgentSignal = func(int) error { return syscall.EPERM }
		want = agentExitNow
	}
	if got := endOwnAgent(os.Getenv(AgentPIDEnv)); got != want {
		t.Fatalf("endOwnAgent = %q, want %q", got, want)
	}
}

func TestResetContextEndsItsOwnAgentByExactPID2161(t *testing.T) {
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// bash is the "agent": it exports its own $$, runs the command as a child
	// (as Claude Code runs a tool), and would otherwise live on.
	cmd := exec.Command("bash", "-c", `export HADRON_AGENT_PID=$$; "$0" -test.run='^TestEndAgentHelper2161$' >/dev/null 2>&1; sleep 30`, bin)
	cmd.Env = append(os.Environ(), "HADRON_TEST_END_AGENT_2161=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		var ee *exec.ExitError
		if !errors.As(err, &ee) || ee.Sys().(syscall.WaitStatus).Signal() != syscall.SIGTERM {
			t.Fatalf("the agent did not end by SIGTERM: %v", err)
		}
	case <-time.After(20 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("the agent was not ended")
	}
}

func TestResetContextNeverSignalsAPIDThatIsNotItsAncestor2161(t *testing.T) {
	bystander := exec.Command("sleep", "30")
	if err := bystander.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = bystander.Process.Kill(); _ = bystander.Wait() }()
	for name, value := range map[string]string{
		"unset":               "",
		"not a number":        "12abc",
		"negative":            "-5",
		"init":                "1",
		"itself":              strconv.Itoa(os.Getpid()),
		"a bystander, no kin": strconv.Itoa(bystander.Process.Pid),
		"a pid that is gone":  "2147483646",
		// Never a real ancestor in a negative case: a bug here must not end
		// the test runner.
		"leading plus":           "+" + strconv.Itoa(bystander.Process.Pid),
		"surrounding whitespace": " " + strconv.Itoa(bystander.Process.Pid),
	} {
		t.Run(name, func(t *testing.T) {
			if got := endOwnAgent(value); got != agentExitNow {
				t.Fatalf("endOwnAgent(%q) = %q", value, got)
			}
		})
	}
	if err := bystander.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("the bystander was signalled: %v", err)
	}
}

// Jibril's P2: a proven ancestor whose signal fails is reported exit-now, so
// the agent is told to exit, never "signalled" while it lives on.
func TestResetContextReportsExitNowWhenTheSignalFails2161(t *testing.T) {
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// The helper's verdict decides bash's exit status; bash then ends itself.
	cmd := exec.Command("bash", "-c", `export HADRON_AGENT_PID=$$; "$0" -test.run='^TestEndAgentHelper2161$' >/dev/null 2>&1; exit $?`, bin)
	cmd.Env = append(os.Environ(), "HADRON_TEST_END_AGENT_2161=1", "HADRON_TEST_SIGNAL_FAILS_2161=1")
	if err := cmd.Run(); err != nil {
		t.Fatalf("a failed signal was not reported exit-now (or the agent was signalled anyway): %v", err)
	}
}
