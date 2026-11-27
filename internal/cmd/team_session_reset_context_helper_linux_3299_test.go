//go:build linux

package cmd

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// cli#3299: the pid path wins. An agent that exported its own pid is ended by the signal as before, and the
// configured helper is never run: the helper arm exists only for the exit-now branch.

// TestResetContextPidChild3299 is the child of the bash "agent": it runs reset-context with a helper configured and
// records the receipt and whether the helper ran.
func TestResetContextPidChild3299(t *testing.T) {
	result := os.Getenv("HADRON_TEST_RESET_RESULT_3299")
	if result == "" {
		t.Skip("helper process only")
	}
	bindWorktree(t)
	url, _ := shedServer(t, nil)
	rig, out, _, err := runRig3299(t, url, nil, nil)
	b, _ := json.Marshal(map[string]any{"out": out, "err": errString(err), "ran": rig.ran()})
	if werr := os.WriteFile(result, b, 0o600); werr != nil {
		t.Fatal(werr)
	}
}

func TestResetContextTheSignalPathNeverRunsTheHelper3299(t *testing.T) {
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	result := filepath.Join(t.TempDir(), "result.json")
	cmd := exec.Command("bash", "-c", `export HADRON_AGENT_PID=$$; "$0" -test.run='^TestResetContextPidChild3299$' >/dev/null 2>&1; sleep 30`, bin)
	cmd.Env = append(os.Environ(), "HADRON_TEST_RESET_RESULT_3299="+result)
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
	var r struct {
		Out string
		Err string
		Ran bool
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		b, rerr := os.ReadFile(result)
		if rerr == nil && json.Unmarshal(b, &r) == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the command recorded no result: %v", rerr)
		}
		time.Sleep(50 * time.Millisecond)
	}
	var d resetContextDTO
	if json.Unmarshal([]byte(r.Out), &d) != nil || d.Exit != "signal-pending" {
		t.Fatalf("want signal-pending, got %q (err %q)", r.Out, r.Err)
	}
	if r.Ran {
		t.Fatal("the helper ran on the signal path")
	}
}
