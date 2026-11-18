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
	files := writeSweepUnits2183(t, dir, s, nil)
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

func writeSweepUnits2183(t *testing.T, dir string, s Spec, created func(string)) []string {
	t.Helper()
	bodies := []struct{ name, body string }{
		{"hadron-team-sweep.service", SystemdService(s)},
		{"hadron-team-sweep.timer", SystemdTimer(s)},
		{"hadron-team-sweep-failed.service", SweepFailureService(s)},
	}
	var paths []string
	for _, b := range bodies {
		path := filepath.Join(dir, b.name)
		if err := writeSweepUnit2187(path, b.body, created); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	return paths
}

func writeSweepUnit2187(path, body string, created func(string)) error {
	return writeSweepUnit2294(path, body, created, (*os.File).WriteString)
}

// The injected write keeps partial-write failure tests on the actual file path.
func writeSweepUnit2294(path, body string, created func(string), write func(*os.File, string) (int, error)) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	// Creation proves ownership. Register before writing content, including
	// any renamed path, so a partial write cannot escape acceptance cleanup.
	if created != nil {
		created(path)
	}
	_, writeErr := write(f, body)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return nil
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
	created, cleanupFiles := sweepUnitOwnership2294()
	t.Cleanup(func() {
		_ = exec.Command("systemctl", append([]string{"stop"}, names...)...).Run()
		cleanupFiles()
		_ = exec.Command("systemctl", "daemon-reload").Run()
		_ = exec.Command("systemctl", append([]string{"reset-failed"}, names...)...).Run()
	})
	if err := writeAndCheckSweepUnits2187(paths, func() []string {
		return writeSweepUnits2183(t, "/run/systemd/system", s, created)
	}); err != nil {
		t.Fatal(err)
	}
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
		if err := resetSweepUnits2187(names, command); err != nil {
			t.Fatal(err)
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

// These test-only helpers expose the acceptance harness's command/path
// boundary without requiring a manager in unit tests.
func resetSweepUnits2187(names []string, command func(...string) ([]byte, error)) error {
	if out, err := command(append([]string{"stop"}, names...)...); err != nil {
		return fmt.Errorf("stop owned units: %w: %s", err, out)
	}
	for _, n := range names {
		// Inactive units can still retain start-rate counters from a prior
		// failed case. Reset every owned unit after cancelling queued restarts.
		// A pattern matching only this name makes systemctl consider loaded
		// units; a literal name fails after inactive-unit garbage collection.
		// Unloading already flushes its counters. Never reset unrelated units.
		pattern := "[" + n[:1] + "]" + n[1:]
		if out, err := command("reset-failed", pattern); err != nil {
			return fmt.Errorf("reset %s: %w: %s", n, err, out)
		}
		state, err := command("show", n, "-p", "ActiveState", "--value")
		if err != nil {
			return fmt.Errorf("show %s: %w: %s", n, err, state)
		}
		if got := strings.TrimSpace(string(state)); got != "inactive" {
			return fmt.Errorf("%s did not stop: %s", n, got)
		}
	}
	return nil
}

func writeAndCheckSweepUnits2187(owned []string, write func() []string) error {
	written := write()
	mismatch := func() error {
		return fmt.Errorf("written unit paths do not match registered cleanup paths: got %v, want %v", written, owned)
	}
	if len(written) != len(owned) {
		return mismatch()
	}
	remaining := make(map[string]bool, len(owned))
	for _, path := range owned {
		remaining[path] = true
	}
	for _, path := range written {
		if !remaining[path] {
			return mismatch()
		}
		delete(remaining, path)
	}
	if len(remaining) != 0 {
		return mismatch()
	}
	return nil
}

func TestSweepAcceptanceResetClearsInactiveStartLimit2187(t *testing.T) {
	names := []string{"hadron-team-sweep.timer", "hadron-team-sweep.service", "hadron-team-sweep-failed.service"}
	limited := map[string]bool{}
	for _, n := range names {
		limited[n] = true
	}
	var calls []string
	command := func(args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		switch args[0] {
		case "stop":
			return nil, nil // stopping does not clear start-rate counters
		case "show":
			return []byte("inactive\n"), nil
		case "reset-failed":
			if len(args) != 2 || !strings.Contains(args[1], "[") {
				return nil, fmt.Errorf("expected exact-name loaded-unit pattern: %v", args)
			}
			for _, n := range names {
				matched, err := filepath.Match(args[1], n)
				if err != nil {
					return nil, err
				}
				if matched {
					limited[n] = false
				}
			}
			for _, foreign := range []string{"unrelated.service", "hadron-team-sweep-other.service", "hadron-team-sweep.timer-other"} {
				if matched, _ := filepath.Match(args[1], foreign); matched {
					return nil, fmt.Errorf("pattern reaches unowned unit: %s", foreign)
				}
			}
			return nil, nil
		case "start":
			if limited[args[1]] {
				return nil, fmt.Errorf("prior case's start limit survived")
			}
			return nil, nil
		default:
			return nil, fmt.Errorf("unexpected command %v", args)
		}
	}
	if err := resetSweepUnits2187(names, command); err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if limited[n] {
			t.Errorf("inactive owned unit %s kept its prior start-limit counter; calls=%v", n, calls)
		}
	}
	if _, err := command("start", names[1]); err != nil {
		t.Errorf("successor case contaminated: %v", err)
	}
	if len(calls) == 0 || calls[0] != "stop "+strings.Join(names, " ") {
		t.Errorf("must stop all owned units first: %v", calls)
	}
}

func TestSweepAcceptanceWrittenPathsMatchCleanup2187(t *testing.T) {
	dir := t.TempDir()
	owned := []string{filepath.Join(dir, "hadron-team-sweep.service"), filepath.Join(dir, "hadron-team-sweep.timer"), filepath.Join(dir, "hadron-team-sweep-failed.service")}
	for _, tc := range []struct {
		name  string
		paths []string
		bad   bool
	}{
		{"same", owned, false},
		{"reordered", []string{owned[2], owned[0], owned[1]}, false},
		{"renamed", []string{owned[0] + "-renamed", owned[1], owned[2]}, true},
		{"duplicate", []string{owned[0], owned[0], owned[2]}, true},
		{"missing", owned[:2], true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			err := writeAndCheckSweepUnits2187(owned, func() []string { called = true; return tc.paths })
			if !called {
				t.Fatal("writer not exercised")
			}
			if tc.bad && (err == nil || !strings.Contains(err.Error(), "written unit paths do not match")) {
				t.Fatalf("expected owned/written path mismatch refusal, got %v", err)
			}
			if !tc.bad && err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSweepAcceptanceRealWriterRegistersOwnedPathsBeforeBody2187(t *testing.T) {
	dir := t.TempDir()
	owned := []string{filepath.Join(dir, "hadron-team-sweep.service"), filepath.Join(dir, "hadron-team-sweep.timer"), filepath.Join(dir, "hadron-team-sweep-failed.service")}
	var created []string
	spec := Spec{Job: Sweep, Bin: "/bin/true", Config: filepath.Join(dir, "host.yaml"), Path: "/usr/bin:/bin", Logger: "/bin/true", Interval: time.Minute}
	err := writeAndCheckSweepUnits2187(owned, func() []string {
		return writeSweepUnits2183(t, dir, spec, func(path string) {
			info, err := os.Stat(path)
			if err != nil {
				t.Error(err)
			} else if info.Size() != 0 {
				t.Errorf("cleanup registration happened after content write: %s", path)
			}
			created = append(created, path)
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 3 {
		t.Fatalf("created-path ownership missing: %v", created)
	}
	for _, path := range created {
		if body, err := os.ReadFile(path); err != nil || len(body) == 0 {
			t.Errorf("writer did not finish %s: %v", path, err)
		}
	}
}

func TestSweepAcceptanceResetToleratesUnloadedOwnedUnits2187(t *testing.T) {
	names := []string{"hadron-team-sweep.timer", "hadron-team-sweep.service", "hadron-team-sweep-failed.service"}
	// Only the service remains loaded with a counter; the others were GC'd.
	retained := true
	command := func(args ...string) ([]byte, error) {
		switch args[0] {
		case "stop":
			return nil, nil
		case "show":
			return []byte("inactive"), nil
		case "reset-failed":
			if !strings.Contains(args[1], "[") {
				return nil, fmt.Errorf("literal reset of an unloaded unit is refused")
			}
			if ok, _ := filepath.Match(args[1], names[1]); ok {
				retained = false
			}
			return nil, nil
		default:
			return nil, fmt.Errorf("unexpected %v", args)
		}
	}
	if err := resetSweepUnits2187(names, command); err != nil {
		t.Fatal(err)
	}
	if retained {
		t.Fatal("loaded inactive unit retained its counter")
	}
}

func TestSweepAcceptanceWriterPreservesPreexistingPath2187(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hadron-team-sweep-renamed.service")
	const sentinel = "pre-existing unowned unit contents"
	if err := os.WriteFile(path, []byte(sentinel), 0600); err != nil {
		t.Fatal(err)
	}
	claimed := false
	err := writeSweepUnit2187(path, "new unit", func(string) { claimed = true })
	if !errors.Is(err, os.ErrExist) {
		t.Errorf("pre-existing unit must be refused: %v", err)
	}
	if claimed {
		t.Error("pre-existing unit was claimed for cleanup")
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil || string(got) != sentinel {
		t.Errorf("pre-existing unit contents changed: %q, %v", got, readErr)
	}
}

func TestSweepAcceptanceResetShowErrorContext2294(t *testing.T) {
	const unit = "case-2294.service"
	sentinel := errors.New("show sentinel")
	var calls []string
	err := resetSweepUnits2187([]string{unit}, func(args ...string) ([]byte, error) {
		calls = append(calls, args[0])
		switch args[0] {
		case "stop", "reset-failed":
			return nil, nil
		case "show":
			if len(args) < 2 || args[1] != unit {
				t.Fatalf("wrong show target: %v", args)
			}
			return []byte("manager diagnostic"), sentinel
		default:
			t.Fatalf("unexpected command %v", args)
			return nil, nil
		}
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("show error not preserved: %v", err)
	}
	for _, want := range []string{unit, "manager diagnostic"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("show error missing %q: %v; calls=%v", want, err, calls)
		}
	}
}

func TestSweepAcceptanceResetCommandErrors2294(t *testing.T) {
	for _, fail := range []string{"stop", "reset-failed"} {
		t.Run(fail, func(t *testing.T) {
			sentinel := errors.New(fail + " sentinel")
			var calls []string
			command := func(args ...string) ([]byte, error) {
				calls = append(calls, args[0])
				if args[0] == fail {
					return []byte(fail + " diagnostic"), sentinel
				}
				return []byte("inactive"), nil
			}
			err := resetSweepUnits2187([]string{"case-2294.service"}, command)
			if err == nil {
				_, _ = command("start", "case-2294.service")
			}
			if !errors.Is(err, sentinel) {
				t.Errorf("%s error not propagated: %v; calls=%v", fail, err, calls)
			} else if !strings.Contains(err.Error(), fail+" diagnostic") {
				t.Errorf("command diagnostic lost: %v", err)
			}
			want := []string{"stop"}
			if fail == "reset-failed" {
				want = append(want, "reset-failed")
			}
			if strings.Join(calls, ",") != strings.Join(want, ",") {
				t.Errorf("commands continued after %s failure: %v", fail, calls)
			}
		})
	}
}

// Both the manager acceptance and the partial-write pin use this ownership
// registry. Callers register cleanup before the writer can create any files.
func sweepUnitOwnership2294() (func(string), func()) {
	var paths []string
	created := func(path string) {
		for _, existing := range paths {
			if existing == path {
				return
			}
		}
		paths = append(paths, path)
	}
	cleanup := func() {
		for _, path := range paths {
			_ = os.Remove(path)
		}
	}
	return created, cleanup
}

func TestSweepAcceptancePartialWriteCleanup2294(t *testing.T) {
	dir := t.TempDir()
	owned := filepath.Join(dir, "owned-partial.service")
	unowned := filepath.Join(dir, "pre-existing.service")
	const sentinelBody = "pre-existing unowned contents"
	if err := os.WriteFile(unowned, []byte(sentinelBody), 0600); err != nil {
		t.Fatal(err)
	}
	writeErr := errors.New("injected partial write failure")
	const body = "partial unit body not fully written"
	const partial = "partial"
	t.Run("write fails after creating content", func(t *testing.T) {
		created, cleanup := sweepUnitOwnership2294()
		t.Cleanup(cleanup)
		write := func(f *os.File, body string) (int, error) {
			n, err := f.WriteString(body[:len(partial)])
			if err != nil {
				return n, err
			}
			got, err := os.ReadFile(f.Name())
			if err != nil || string(got) != partial {
				return n, fmt.Errorf("partial write not observed: %q: %v", got, err)
			}
			return n, writeErr
		}
		if err := writeSweepUnit2294(owned, body, created, write); !errors.Is(err, writeErr) {
			t.Errorf("partial write error not preserved: %v", err)
		}
		if err := writeSweepUnit2294(unowned, body, created, write); !errors.Is(err, os.ErrExist) {
			t.Errorf("existing file was not refused: %v", err)
		}
	})
	if _, err := os.Lstat(owned); !os.IsNotExist(err) {
		t.Errorf("owned partial file survived cleanup: %v", err)
	}
	if got, err := os.ReadFile(unowned); err != nil || string(got) != sentinelBody {
		t.Errorf("unowned sentinel changed or removed: %q: %v", got, err)
	}
}
