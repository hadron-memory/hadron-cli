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
	if r.Err != "" || json.Unmarshal([]byte(r.Out), &d) != nil || d.Status != "written" || d.Relaunch != "pending" || d.Exit != "signalled" {
		t.Fatalf("receipt %q (err %q)", r.Out, r.Err)
	}
	if r.StampErr != "" || !strings.Contains(r.Stamp, `"sessionId":"s-new"`) {
		t.Fatalf("stamp %q (%s)", r.Stamp, r.StampErr)
	}
}
