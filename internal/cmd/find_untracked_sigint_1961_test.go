package cmd

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1971 QA F1: a REAL SIGINT, sent to the compiled binary while the adapter
// is mid-describe, must exit Cancelled (6), not die by signal (130). The
// in-process pins inject a context and cannot see this.
func TestCompiledBinaryInterruptMidDescribeExitsCancelled1961(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX signals and a shell-script adapter")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "hadron")
	if out, err := exec.Command("go", "build", "-o", bin, "github.com/hadron-memory/hadron-cli/cmd/hadron").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	started := filepath.Join(dir, "started")
	adapter := filepath.Join(dir, "adapter")
	script := "#!/bin/sh\ncat >/dev/null\n: > '" + started + "'\nsleep 20\n"
	if err := os.WriteFile(adapter, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, "team", "task", "find-untracked", "--adapter", adapter)
	cmd.Env = append(os.Environ(), "HADRON_TOKEN=unused", "HADRON_INTAKE_ADAPTER=", "HOME="+dir)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			t.Fatalf("the adapter never began describe: %s", stderr.String())
		}
		time.Sleep(25 * time.Millisecond)
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		var ee *exec.ExitError
		if !errors.As(err, &ee) || ee.ExitCode() != exitcode.Cancelled {
			t.Fatalf("want exit %d, got %v\nstderr: %s", exitcode.Cancelled, err, stderr.String())
		}
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("the binary did not stop on interrupt\nstderr: %s", stderr.String())
	}
}
