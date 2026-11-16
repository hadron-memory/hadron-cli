//go:build linux

package healthsched

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Validation runs without a system manager; runtime acceptance below is opt-in.
func TestSweepSystemdVerify2183(t *testing.T) {
	if _, err := exec.LookPath("systemd-analyze"); err != nil {
		t.Skip("systemd-analyze unavailable")
	}
	dir := t.TempDir()
	s := Spec{Job: Sweep, Bin: "/bin/true", Config: filepath.Join(dir, "host.yaml"), Path: "/usr/bin:/bin", Logger: "/bin/true", Interval: time.Minute}
	files := writeSweepUnits2183(t, dir, s)
	args := append([]string{"verify"}, files...)
	out, err := exec.Command("systemd-analyze", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("generated units rejected: %v\n%s", err, out)
	}
}

func writeSweepUnits2183(t *testing.T, dir string, s Spec) []string {
	t.Helper()
	bodies := []struct{ name, body string }{
		{"hadron-team-sweep.service", SystemdService(s)},
		{"hadron-team-sweep.timer", SystemdTimer(s)},
		{"hadron-team-sweep-failed.service", SweepFailureService(s)},
	}
	var paths []string
	for _, b := range bodies {
		path := filepath.Join(dir, b.name)
		if err := os.WriteFile(path, []byte(b.body), 0600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	return paths
}

// Run ONLY in a disposable systemd container with no Hadron units installed.
// The emitted units are unmodified; the fake CLI is a local effect-free probe.
func TestSweepSystemdAcceptance2183(t *testing.T) {
	if os.Getenv("HADRON_TEST_SYSTEMD") != "1" {
		t.Skip("opt-in disposable systemd container; see docs/testing/sweep-systemd.md")
	}
	if os.Geteuid() != 0 {
		t.Fatal("requires container root")
	}
	if _, err := os.Stat("/run/systemd/container"); err != nil {
		t.Fatal("refusing outside a systemd container")
	}
	ctl := func(t *testing.T, args ...string) string {
		t.Helper()
		out, err := exec.Command("systemctl", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("systemctl %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	names := []string{"hadron-team-sweep.timer", "hadron-team-sweep.service", "hadron-team-sweep-failed.service"}
	for _, n := range names {
		for _, dir := range []string{"/run/systemd/system", "/etc/systemd/system", "/usr/lib/systemd/system", "/lib/systemd/system"} {
			if _, err := os.Lstat(filepath.Join(dir, n)); !os.IsNotExist(err) {
				t.Fatalf("refusing existing unit %s/%s", dir, n)
			}
		}
		out, _ := exec.Command("systemctl", "show", n, "-p", "LoadState", "--value").Output()
		if strings.TrimSpace(string(out)) != "not-found" {
			t.Fatalf("refusing loaded unit %s: %s", n, out)
		}
	}
	dir := t.TempDir()
	probe := filepath.Join(dir, "probe")
	logger := filepath.Join(dir, "logger")
	// Files are private to this test; neither probe reads host credentials.
	body := fmt.Sprintf("#!/bin/sh\necho run >> '%s/runs'\nmode=$(cat '%s/mode')\nif [ \"$mode\" = hold ]; then sleep 300; fi\nexit \"$mode\"\n", dir, dir)
	if err := os.WriteFile(probe, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logger, []byte(fmt.Sprintf("#!/bin/sh\necho failed >> '%s/failures'\n", dir)), 0700); err != nil {
		t.Fatal(err)
	}
	s := Spec{Job: Sweep, Bin: probe, Config: filepath.Join(dir, "host.yaml"), Path: "/usr/bin:/bin", Logger: logger, Interval: time.Minute}
	paths := writeSweepUnits2183(t, "/run/systemd/system", s)
	t.Cleanup(func() {
		_ = exec.Command("systemctl", append([]string{"stop"}, names...)...).Run()
		for _, p := range paths {
			_ = os.Remove(p)
		}
		_ = exec.Command("systemctl", "daemon-reload").Run()
		_ = exec.Command("systemctl", append([]string{"reset-failed"}, names...)...).Run()
	})
	ctl(t, "daemon-reload")
	count := func(name string) int {
		b, _ := os.ReadFile(filepath.Join(dir, name))
		return strings.Count(string(b), "\n")
	}
	mode := func(t *testing.T, v string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "mode"), []byte(v), 0600); err != nil {
			t.Fatal(err)
		}
	}
	wait := func(t *testing.T, limit time.Duration, label string, ok func() bool) {
		t.Helper()
		deadline := time.Now().Add(limit)
		for time.Now().Before(deadline) {
			if ok() {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for %s", label)
	}
	for _, code := range []string{"2", "5"} {
		t.Run("refusal-"+code, func(t *testing.T) {
			_ = exec.Command("systemctl", "reset-failed", names[1], names[2]).Run()
			mode(t, code)
			runs, failures := count("runs"), count("failures")
			_ = exec.Command("systemctl", "start", names[1]).Run() // expected failed exit
			wait(t, 5*time.Second, "OnFailure", func() bool { return count("failures") == failures+1 })
			time.Sleep(11 * time.Second) // beyond the shipped RestartSec=10s
			if count("runs") != runs+1 || ctl(t, "show", names[1], "-p", "NRestarts", "--value") != "0" {
				t.Fatal("prevented exit restarted")
			}
			if got := ctl(t, "show", names[1], "-p", "ExecMainStatus", "--value"); got != code {
				t.Fatalf("exit status=%s want %s", got, code)
			}
		})
	}
	t.Run("timer-retries-refusal", func(t *testing.T) {
		_ = exec.Command("systemctl", "reset-failed", names[1], names[2]).Run()
		mode(t, "2")
		// First refused invocation pins the activation clock. Start timer afterward;
		// its next trigger must be OnUnitActiveSec, not an immediate overdue boot tick.
		_ = exec.Command("systemctl", "start", names[1]).Run()
		baseline := count("runs")
		ctl(t, "start", names[0])
		wait(t, 85*time.Second, "first timer retry", func() bool { return count("runs") > baseline })
		// OnBootSec may be overdue or still pending in a newly booted
		// container. Observe another trigger to cover the recurring timer.
		baseline = count("runs")
		started := time.Now()
		wait(t, 145*time.Second, "recurring one-minute timer retry", func() bool { return count("runs") > baseline })
		if elapsed := time.Since(started); elapsed < 55*time.Second {
			t.Fatalf("recurring timer fired too early: %s", elapsed)
		}
		ctl(t, "stop", names[0])
		if ctl(t, "show", names[1], "-p", "NRestarts", "--value") != "0" {
			t.Fatal("timer retry used service restart")
		}
	})
	t.Run("sigkill-restarts", func(t *testing.T) {
		_ = exec.Command("systemctl", "reset-failed", names[1], names[2]).Run()
		mode(t, "hold")
		baseline := count("runs")
		ctl(t, "start", "--no-block", names[1])
		wait(t, 5*time.Second, "probe running", func() bool { return count("runs") > baseline })
		ctl(t, "kill", "--kill-whom=all", "--signal=KILL", names[1])
		wait(t, 20*time.Second, "on-failure restart", func() bool { return count("runs") >= baseline+2 })
		if ctl(t, "show", names[1], "-p", "NRestarts", "--value") != "1" {
			t.Fatal("SIGKILL did not cause one service restart")
		}
	})
}
