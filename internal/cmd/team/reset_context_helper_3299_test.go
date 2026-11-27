//go:build !windows

package team

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A helper that does not return is stopped at the timeout and reported as failed; its output is never part of the
// error.
func TestRunExitHelperTimesOut3299(t *testing.T) {
	old := exitHelperTimeout
	exitHelperTimeout = 300 * time.Millisecond
	t.Cleanup(func() { exitHelperTimeout = old })
	script := filepath.Join(t.TempDir(), "slow")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho SECRET\nexec sleep 30\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	err := runExitHelper(script, "hrn:node:acme.com:iris:checkpoints:x")
	if err == nil || !strings.Contains(err.Error(), "timed out") || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("want a timeout without the helper's output, got %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("the helper was not stopped at the timeout: %v", time.Since(start))
	}
}

// The API token is not in the helper's environment.
func TestRunExitHelperDropsTheToken3299(t *testing.T) {
	t.Setenv("HADRON_TOKEN", "hdr_user_secret")
	work := t.TempDir()
	marker := filepath.Join(work, "env")
	script := filepath.Join(work, "h")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nenv > "+marker+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := runExitHelper(script, "x"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(marker)
	if strings.Contains(string(b), "HADRON_TOKEN") {
		t.Fatalf("the helper got the API token:\n%s", b)
	}
}
