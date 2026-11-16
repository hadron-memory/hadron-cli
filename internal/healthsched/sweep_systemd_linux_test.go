//go:build linux

package healthsched

import (
	"context"
	"errors"
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
	if err != nil || len(sweepUnitWarnings2183(string(out), files)) != 0 {
		t.Fatalf("generated units rejected or warned: %v\n%s", err, out)
	}
}

// verify loads host dependencies too: only diagnostics naming our generated
// units are relevant when the tool itself reports success.
func sweepUnitWarnings2183(out string, paths []string) []string {
	var warnings []string
	for _, line := range strings.Split(out, "\n") {
		for _, path := range paths {
			if strings.Contains(line, filepath.Base(path)) {
				warnings = append(warnings, line)
				break
			}
		}
	}
	return warnings
}

func TestSweepSystemdVerifyWarningScope2183(t *testing.T) {
	// Actual unrelated diagnostics from CI43801785653, systemd on Ubuntu.
	ambient := "/usr/lib/systemd/system/xfs_scrub_all.service:26: Support for option CPUAccounting= has been removed and it is ignored\n" +
		"/usr/lib/systemd/system/system-xfs_scrub.slice:15: Support for option CPUAccounting= has been removed and it is ignored\n" +
		"/usr/lib/systemd/system/walinuxagent.service:21: Support for option CPUAccounting= has been removed and it is ignored\n"
	paths := []string{"/test/hadron-team-sweep.service", "/test/hadron-team-sweep.timer", "/test/hadron-team-sweep-failed.service"}
	if got := sweepUnitWarnings2183(ambient, paths); len(got) != 0 {
		t.Fatalf("unrelated host warnings: %v", got)
	}
	// Match both absolute-path and unit-name diagnostics; keep every warning.
	relevant := []string{
		"/test/hadron-team-sweep.service:9: Failed to parse Restart=bogus, ignoring: Invalid argument",
		"hadron-team-sweep.service:10: Failed to parse exit status, ignoring: two five",
		"/test/hadron-team-sweep.service:4: Unknown key 'OnFailur' in section [Unit], ignoring.",
		"/test/hadron-team-sweep.timer:8: Failed to parse timer value, ignoring: x1min",
		"/test/hadron-team-sweep-failed.service:7: Unknown key 'ExecStar' in section [Service], ignoring.",
	}
	for _, warning := range relevant {
		got := sweepUnitWarnings2183(ambient+warning, paths)
		if len(got) != 1 || got[0] != warning {
			t.Fatalf("lost generated-unit warning %q: %v", warning, got)
		}
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
	comm, err := os.ReadFile("/proc/1/comm")
	if err != nil || strings.TrimSpace(string(comm)) != "systemd" {
		t.Fatal("requires systemd PID 1")
	}
	if out, err := exec.Command("systemd-detect-virt", "--container").CombinedOutput(); err != nil || len(strings.TrimSpace(string(out))) == 0 {
		t.Fatal("container identity unproven")
	}
	command := func(args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return exec.CommandContext(ctx, "systemctl", args...).CombinedOutput()
	}
	ctl := func(t *testing.T, args ...string) string {
		t.Helper()
		out, err := command(args...)
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
	paths := make([]string, 0, len(names))
	for _, n := range names {
		paths = append(paths, filepath.Join("/run/systemd/system", n))
	}
	t.Cleanup(func() {
		_ = exec.Command("systemctl", append([]string{"stop"}, names...)...).Run()
		for _, p := range paths {
			_ = os.Remove(p)
		}
		_ = exec.Command("systemctl", "daemon-reload").Run()
		_ = exec.Command("systemctl", append([]string{"reset-failed"}, names...)...).Run()
	})
	writeSweepUnits2183(t, "/run/systemd/system", s)
	ctl(t, "daemon-reload")
	duration := func(t *testing.T, unit, property string) time.Duration {
		t.Helper()
		value := ctl(t, "show", unit, "-p", property, "--value")
		d, err := time.ParseDuration(strings.ReplaceAll(strings.ReplaceAll(value, "min", "m"), " ", ""))
		if err != nil || d < 0 || d > 2*time.Minute {
			t.Fatalf("unsupported %s=%q", property, value)
		}
		return d
	}
	restartDelay := duration(t, names[1], "RestartUSec")
	accuracy := duration(t, names[0], "AccuracyUSec")
	randomDelay := duration(t, names[0], "RandomizedDelayUSec")
	timerWait := s.Interval + accuracy + randomDelay + 30*time.Second
	reset := func(t *testing.T) {
		t.Helper()
		t.Cleanup(func() { _, _ = command(append([]string{"stop"}, names...)...) })
		// reset-failed alone does not cancel queued auto-restarts. Stop every
		// owned unit first so a failed subtest cannot contaminate the next.
		ctl(t, append([]string{"stop"}, names...)...)
		for _, n := range names {
			if ctl(t, "show", n, "-p", "ActiveState", "--value") == "failed" {
				ctl(t, "reset-failed", n)
			}
			if got := ctl(t, "show", n, "-p", "ActiveState", "--value"); got != "inactive" {
				t.Fatalf("%s did not stop: %s", n, got)
			}
		}
	}
	startRefusal := func(t *testing.T, code string) {
		t.Helper()
		out, err := command("start", names[1])
		var exited *exec.ExitError
		if !errors.As(err, &exited) || exited.ExitCode() != 1 {
			t.Fatalf("expected service failure, got %v: %s", err, out)
		}
		if got := ctl(t, "show", names[1], "-p", "ExecMainStatus", "--value"); got != code {
			t.Fatalf("exit status=%s want %s; start output=%s", got, code, out)
		}
	}

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
			reset(t)
			mode(t, code)
			runs, failures := count("runs"), count("failures")
			startRefusal(t, code)
			wait(t, 5*time.Second, "OnFailure", func() bool { return count("failures") == failures+1 })
			time.Sleep(restartDelay + time.Second) // beyond the manager’s actual restart delay
			if count("runs") != runs+1 || ctl(t, "show", names[1], "-p", "NRestarts", "--value") != "0" {
				t.Fatal("prevented exit restarted")
			}
			if ctl(t, "show", names[1], "-p", "ActiveState", "--value") != "failed" {
				t.Fatal("refused service did not stay failed")
			}
			if count("failures") != failures+1 {
				t.Fatal("OnFailure ran more than once")
			}
		})
	}
	t.Run("timer-retries-refusal", func(t *testing.T) {
		reset(t)
		mode(t, "2")
		// The first timer trigger may be the boot timer. A second trigger
		// establishes recurring retry; include the manager's accuracy window.
		startRefusal(t, "2")
		t.Cleanup(func() { _ = exec.Command("systemctl", "stop", names[0]).Run() })
		baseline := count("runs")
		ctl(t, "start", names[0])
		wait(t, timerWait, "first timer retry", func() bool { return count("runs") > baseline })
		// OnBootSec may be overdue or still pending in a newly booted
		// container. Observe another trigger to cover the recurring timer.
		baseline = count("runs")
		started := time.Now()
		wait(t, timerWait, "recurring one-minute timer retry", func() bool { return count("runs") > baseline })
		if elapsed := time.Since(started); elapsed < s.Interval-5*time.Second {
			t.Fatalf("recurring timer fired too early: %s", elapsed)
		}
		ctl(t, "stop", names[0])
		if ctl(t, "show", names[1], "-p", "NRestarts", "--value") != "0" {
			t.Fatal("timer retry used service restart")
		}
	})
	t.Run("sigkill-restarts", func(t *testing.T) {
		reset(t)
		mode(t, "hold")
		baseline := count("runs")
		ctl(t, "start", "--no-block", names[1])
		wait(t, 5*time.Second, "probe running", func() bool { return count("runs") > baseline })
		ctl(t, "kill", "--kill-whom=all", "--signal=KILL", names[1])
		wait(t, restartDelay+10*time.Second, "on-failure restart", func() bool { return count("runs") >= baseline+2 })
		if ctl(t, "show", names[1], "-p", "NRestarts", "--value") != "1" {
			t.Fatal("SIGKILL did not cause one service restart")
		}
	})
}
