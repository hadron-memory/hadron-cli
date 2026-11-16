//go:build linux

package cmd

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
	"github.com/hadron-memory/hadron-cli/internal/delivery"
)

// Wolfgang #109891 / Jibril's delta P2 on #2167, at the command level: an
// agent (bash) that exported its own pid and dies at once on SIGTERM runs
// `session reset-context`. The command writes the checkpoint, the claim and
// the stamp, completes its receipt with exit "signalled", and the agent ends
// by SIGTERM. (The receipt-before-signal ORDER is pinned in
// internal/cmd/team TestResetContextWritesTheReceiptBeforeTheSignal2161.)

// runResetContextToStdout runs the command with its receipt on this
// process's real stdout, so the ancestor reads what the command wrote.
func runResetContextToStdout(t *testing.T, url string) error {
	t.Helper()
	f, _ := testFactory(t)
	f.IOStreams.Out = os.Stdout
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "reset-context", "--server", url, "--json"})
	return root.Execute()
}

// TestResetContextStdoutHelper2161 is the child for the delivery test: the
// receipt goes to the real stdout the bash "agent" reads.
func TestResetContextStdoutHelper2161(t *testing.T) {
	if os.Getenv("HADRON_TEST_RESET_STDOUT_2161") != "1" {
		t.Skip("helper process only")
	}
	bindWorktree(t)
	url, _ := shedServer(t, nil)
	if err := runResetContextToStdout(t, url); err != nil {
		t.Fatal(err)
	}
}

// Jibril's delta P3: the agent's own output reader receives the complete
// receipt before the signal ends it. The bash agent reads the command's
// stdout line by line; its TERM trap first drains what is already in the
// pipe, then records it and exits by SIGTERM.
func TestResetContextReceiptReachesTheAgentBeforeItEnds2161(t *testing.T) {
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "receipt.json")
	// The agent's reader is the pipe itself: what the command wrote before
	// the signal is there when the TERM trap drains it.
	script := `export HADRON_AGENT_PID=$$
exec 3< <("$0" -test.run='^TestResetContextStdoutHelper2161$' 2>/dev/null)
trap 'buf=""; while IFS= read -r -t 0.2 l <&3; do buf+="$l"$'"'"'\n'"'"'; done; printf "%s" "$buf" > "$1"; trap - TERM; kill -TERM $$' TERM
sleep 30 & wait`
	cmd := exec.Command("bash", "-c", script, bin, out)
	cmd.Env = append(os.Environ(), "HADRON_TEST_RESET_STDOUT_2161=1")
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
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("the agent was not ended")
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("the agent recorded no receipt: %v", err)
	}
	// The receipt is the JSON object up to its closing brace (go test's own
	// PASS line may follow it).
	receipt, _, complete := strings.Cut(string(b), "\n}\n")
	var d resetContextDTO
	if !complete || json.Unmarshal([]byte(receipt+"\n}\n"), &d) != nil || d.Status != "written" || d.Relaunch != "pending" || d.Exit != "signal-pending" {
		t.Fatalf("the agent received %q", b)
	}
}

// TestResetContextHelper2161 is not a test on its own: the parent runs it as
// the child of the bash "agent" and reads what it writes to RESULT.
func TestResetContextHelper2161(t *testing.T) {
	result := os.Getenv("HADRON_TEST_RESET_RESULT_2161")
	if result == "" {
		t.Skip("helper process only")
	}
	bindWorktree(t)
	url, _ := shedServer(t, nil)
	out, err := runResetContext(t, url, "--json")
	stamp, serr := os.ReadFile(filepath.Join(os.Getenv(team.GitDirEnv), delivery.RelaunchStampFile))
	b, _ := json.Marshal(map[string]any{"out": out, "err": errString(err), "stamp": string(stamp), "stampErr": errString(serr)})
	if werr := os.WriteFile(result, b, 0o600); werr != nil {
		t.Fatal(werr)
	}
}

func TestResetContextEndsAnAgentThatDiesAtOnceAfterItsReceipt2161(t *testing.T) {
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	result := filepath.Join(t.TempDir(), "result.json")
	cmd := exec.Command("bash", "-c", `export HADRON_AGENT_PID=$$; "$0" -test.run='^TestResetContextHelper2161$' >/dev/null 2>&1; sleep 30`, bin)
	cmd.Env = append(os.Environ(), "HADRON_TEST_RESET_RESULT_2161="+result)
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
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("the agent was not ended")
	}
	// The helper outlives its agent and records what the command did.
	var r struct{ Out, Err, Stamp, StampErr string }
	deadline := time.Now().Add(20 * time.Second)
	for {
		if b, err := os.ReadFile(result); err == nil && json.Unmarshal(b, &r) == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the command never finished")
		}
		time.Sleep(50 * time.Millisecond)
	}
	var d resetContextDTO
	if r.Err != "" || json.Unmarshal([]byte(r.Out), &d) != nil || d.Status != "written" || d.Relaunch != "pending" || d.Exit != "signal-pending" {
		t.Fatalf("receipt %q (err %q)", r.Out, r.Err)
	}
	if r.StampErr != "" || !strings.Contains(r.Stamp, `"sessionId":"s-new"`) {
		t.Fatalf("stamp %q (%s)", r.Stamp, r.StampErr)
	}
}
