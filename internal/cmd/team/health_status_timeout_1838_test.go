package team

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// #1838: the sweep's status read had a fixed 2-minute deadline that a full
// roster outran, failing with only the generic "status command failed".

// slowStatusChild sleeps, then prints an empty report. A child that must be
// killed execs sleep so the kill reaches the process holding stdout.
func slowStatusChild(t *testing.T, sleep string) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "fake-status")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+map[bool]string{true: "exec ", false: ""}[sleep == "5"]+"sleep "+sleep+"\nprintf '[]'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return bin
}

// Red on main: there the deadline is fixed and cannot be injected, and the
// kill reads as the generic failure class.
func TestHealth1838DeadlineKillNamesTheTimeout(t *testing.T) {
	bin := slowStatusChild(t, "5")
	ctx := withHealthStatusTimeout(context.Background(), 300*time.Millisecond)
	_, code, err := runHealthStatusCommandEnv(ctx, bin, "host.yaml", hostpoller.ScrubbedEnv(os.Environ(), "K"))
	var to errHealthStatusTimeout
	if err == nil || code == 0 || !errors.As(err, &to) {
		t.Fatalf("want the timeout class, got code %d err %v", code, err)
	}
	if !strings.Contains(err.Error(), "terminal status timed out after 300ms") {
		t.Errorf("the error must name the limit: %v", err)
	}
}

// A caller's own cancellation is not a timeout.
func TestHealth1838CallerCancelIsNotATimeout(t *testing.T) {
	bin := slowStatusChild(t, "5")
	ctx, cancel := context.WithCancel(withHealthStatusTimeout(context.Background(), time.Minute))
	time.AfterFunc(100*time.Millisecond, cancel)
	_, _, err := runHealthStatusCommandEnv(ctx, bin, "host.yaml", hostpoller.ScrubbedEnv(os.Environ(), "K"))
	var to errHealthStatusTimeout
	if err == nil || errors.As(err, &to) {
		t.Fatalf("a cancelled caller must not read as a timeout: %v", err)
	}
}

// A child that finishes just under the injected deadline succeeds.
func TestHealth1838ChildUnderTheDeadlineSucceeds(t *testing.T) {
	bin := slowStatusChild(t, "0.3")
	ctx := withHealthStatusTimeout(context.Background(), 5*time.Second)
	raw, code, err := runHealthStatusCommandEnv(ctx, bin, "host.yaml", hostpoller.ScrubbedEnv(os.Environ(), "K"))
	if err != nil || code != 0 || string(raw) != "[]" {
		t.Fatalf("code %d err %v out %q", code, err, raw)
	}
}

func TestHealth1838DeadlineIsSizedFromTheRosterWithTheOldFloor(t *testing.T) {
	for n, want := range map[int]time.Duration{
		0: 2 * time.Minute, 1: 2 * time.Minute, 24: 2 * time.Minute, 25: 125 * time.Second,
		48: 240 * time.Second, 200: 1000 * time.Second,
	} {
		if got := healthStatusDeadline(n); got != want {
			t.Errorf("%d workers: %v, want %v", n, got, want)
		}
	}
	if healthStatusTimeout(context.Background()) != 2*time.Minute {
		t.Error("an unset deadline must keep the old 2-minute default")
	}
}

func health1838Registrations(n int) string {
	var ws []string
	for i := 1; i <= n; i++ {
		ws = append(ws, fmt.Sprintf(`{"workerId":"w%d","worker":{"id":"w%d","name":"W%d","urn":"hrn:worker:test:team:w%d"},"worktrees":["x"]}`, i, i, i, i))
	}
	return `{"data":{"hostRegistrations":[{"hostId":"own","live":true,"workers":[` + strings.Join(ws, ",") + `]}]}}`
}

// sweep1838 runs the sweep against n registered workers with a reader that
// records the deadline it was handed and returns (readErr).
func sweep1838(t *testing.T, n int, extra []string, readErr error) (time.Duration, error) {
	t.Helper()
	dir := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, health1838Registrations(n))
	}))
	defer srv.Close()
	for name, body := range map[string]string{"key": "SYNTHETIC_HOST_KEY_1838", "state.json": `{"hostId":"own"}`} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := fmt.Sprintf("server: %q\napp: hrn:app:test:team\nkey_file: key\nstate_file: state.json\nworktrees_root: %q\nworkers:\n  w1:\n    family: codex\n    launch: [fake]\n    worktrees: [x]\n", srv.URL, dir)
	path := filepath.Join(dir, "host.yaml")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	f := cmdutil.NewFactory()
	streams, _, _ := output.Test()
	f.IOStreams = streams
	var got time.Duration
	c := newCmdTeamHealthWithReader(f, func(ctx context.Context, _ string, _ *hostpoller.Config) ([]byte, int, error) {
		got = healthStatusTimeout(ctx)
		return nil, 1, readErr
	})
	c.SetArgs(append([]string{"sweep", "--config", path}, extra...))
	// Execute first: the reader sets got during the call, and Go does not order
	// a variable operand against a call in the same return statement.
	err := c.Execute()
	return got, err
}

func TestHealth1838SweepSizesTheReadFromRegisteredWorkersAndFlagOverrides(t *testing.T) {
	got, _ := sweep1838(t, 3, nil, errors.New("x"))
	if got != 2*time.Minute {
		t.Errorf("3 workers: %v, want the 2m floor", got)
	}
	got, _ = sweep1838(t, 40, nil, errors.New("x"))
	if got != 200*time.Second {
		t.Errorf("40 workers: %v, want 200s", got)
	}
	got, _ = sweep1838(t, 40, []string{"--status-timeout", "9m"}, errors.New("x"))
	if got != 9*time.Minute {
		t.Errorf("--status-timeout: %v, want 9m", got)
	}
	_, err := sweep1838(t, 3, []string{"--status-timeout", "-1s"}, errors.New("x"))
	if exitcode.FromError(err) != exitcode.Usage {
		t.Errorf("a negative --status-timeout is a usage error: %v", err)
	}
}

func TestHealth1838SweepReportsTimeoutAndKeepsTheGenericClassForOtherFailures(t *testing.T) {
	_, err := sweep1838(t, 3, nil, errHealthStatusTimeout{200 * time.Second})
	if err == nil || exitcode.FromError(err) != exitcode.Error || !strings.Contains(err.Error(), "terminal status timed out after 3m20s; nothing written") {
		t.Fatalf("timeout must be named, exit 1: %v", err)
	}
	_, err = sweep1838(t, 3, nil, errors.New("status command failed"))
	if err == nil || err.Error() != "terminal status command failed; nothing written" {
		t.Fatalf("a non-timeout failure keeps its fixed class: %v", err)
	}
}

// A caller's own, shorter deadline expiring must not be reported as the sized
// limit: that message would name a duration that never elapsed.
func TestHealth1838CallerDeadlineIsNotNamedAsTheSizedLimit(t *testing.T) {
	bin := slowStatusChild(t, "5")
	parent, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	ctx := withHealthStatusTimeout(parent, time.Minute)
	_, _, err := runHealthStatusCommandEnv(ctx, bin, "host.yaml", hostpoller.ScrubbedEnv(os.Environ(), "K"))
	var to errHealthStatusTimeout
	if err == nil || errors.As(err, &to) {
		t.Fatalf("the caller's deadline must not read as the sized limit: %v", err)
	}
}

// An output overflow that races the deadline is an ordinary failure, never the
// timeout class (BLIND P3): the sized limit did not cause it.
func TestHealth1838OverflowRacingTheDeadlineIsNotATimeout(t *testing.T) {
	d := context.DeadlineExceeded
	cases := []struct {
		name       string
		child, par error
		overflowed bool
		want       bool
	}{
		{"deadline only", d, nil, false, true},
		{"deadline plus overflow", d, nil, true, false},
		{"caller deadline", d, d, false, false},
		{"caller cancel", context.Canceled, context.Canceled, false, false},
		{"overflow cancel", context.Canceled, nil, true, false},
	}
	for _, c := range cases {
		if got := isHealthStatusTimeout(c.child, c.par, c.overflowed); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
	b := healthStatusBuffer{}
	_, _ = b.Write(make([]byte, 8<<20))
	if b.overflowed {
		t.Error("a full buffer is not an overflow")
	}
	_, _ = b.Write([]byte("x"))
	if !b.overflowed {
		t.Error("overflow must be recorded")
	}
}

// Gate pin (Gene P2): N registered workers get a BUDGET past the OLD fixed
// 2 minutes and a sweep over them stores N reports (budget and storage only;
// the elapsed-time pin through the real child is the next test).
func TestHealth1838SweepWithNWorkersIsGivenABudgetPastTheOldDeadlineAndStoresNReports(t *testing.T) {
	const n = 40 // sized: 200s, beyond the old fixed 2m
	deadline, stored, writes := sweepStoresN(t, n, func(rows, _ string) healthStatusReader {
		return func(ctx context.Context, _ string, _ *hostpoller.Config) ([]byte, int, error) {
			return []byte(rows), 0, nil
		}
	})
	if deadline <= 2*time.Minute {
		t.Errorf("the read must be allowed longer than the old 2m for %d workers: %v", n, deadline)
	}
	if writes != 1 || stored != n {
		t.Errorf("writes=%d stored=%d, want 1 write storing %d", writes, stored, n)
	}
}

// Gene F1: the same through the REAL subprocess boundary. The floor and
// per-worker allowance are shrunk so the child outlives the floor but not the
// sized deadline; a ceiling that reinstated the floor would kill it.
func TestHealth1838ChildPastTheFloorButInsideTheSizedDeadlineIsStored(t *testing.T) {
	oldFloor, oldPer := healthStatusFloor, healthStatusPerWorker
	healthStatusFloor, healthStatusPerWorker = 300*time.Millisecond, 700*time.Millisecond
	t.Cleanup(func() { healthStatusFloor, healthStatusPerWorker = oldFloor, oldPer })
	const n = 3 // sized 2.1s; the child below takes ~1s, well past the 0.3s floor
	deadline, stored, writes := sweepStoresN(t, n, func(rows, dir string) healthStatusReader {
		bin := filepath.Join(dir, "fake-status")
		script := "#!/bin/sh\nsleep 1\ncat <<'EOF'\n[" + rows[1:len(rows)-1] + "]\nEOF\n"
		if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
		return func(ctx context.Context, path string, _ *hostpoller.Config) ([]byte, int, error) {
			return runHealthStatusCommandEnv(ctx, bin, path, hostpoller.ScrubbedEnv(os.Environ(), "K"))
		}
	})
	if deadline != 2100*time.Millisecond {
		t.Errorf("sized deadline %v, want 2.1s", deadline)
	}
	if writes != 1 || stored != n {
		t.Errorf("writes=%d stored=%d, want 1 write storing %d", writes, stored, n)
	}
}

// sweepStoresN runs the sweep against n registered workers with a reader built
// by mk, and returns the deadline the reader saw, reports stored, and writes.
func sweepStoresN(t *testing.T, n int, mk func(rows, dir string) healthStatusReader) (time.Duration, int, int) {
	t.Helper()
	dir := t.TempDir()
	stored, writes := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			OperationName string `json:"operationName"`
			Variables     struct {
				Input struct {
					Reports []struct {
						Worker string `json:"worker"`
					} `json:"reports"`
				} `json:"input"`
			} `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&q)
		w.Header().Set("Content-Type", "application/json")
		if q.OperationName == "HostRegistrations" {
			_, _ = io.WriteString(w, health1838Registrations(n))
			return
		}
		writes++
		var ids []string
		for _, rep := range q.Variables.Input.Reports {
			ids = append(ids, fmt.Sprintf(`{"id":%q,"urn":"hrn:worker:test:team:x"}`, rep.Worker))
		}
		stored = len(ids)
		_, _ = io.WriteString(w, `{"data":{"setHostWorkerTerminals":{"stored":[`+strings.Join(ids, ",")+`],"refused":[]}}}`)
	}))
	defer srv.Close()
	for name, body := range map[string]string{"key": "SYNTHETIC_HOST_KEY_1838", "state.json": `{"hostId":"own"}`} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := fmt.Sprintf("server: %q\napp: hrn:app:test:team\nkey_file: key\nstate_file: state.json\nworktrees_root: %q\nworkers:\n  w1:\n    family: codex\n    launch: [fake]\n    worktrees: [x]\n", srv.URL, dir)
	path := filepath.Join(dir, "host.yaml")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	var rows []string
	for i := 1; i <= n; i++ {
		rows = append(rows, fmt.Sprintf(`{"contract":"terminal-status@1","worker":"W%d","status":"idle","prompt":"empty","reason":null,"lastTurnAt":null,"checkedAt":"2026-11-10T09:00:00Z"}`, i))
	}
	f := cmdutil.NewFactory()
	streams, _, _ := output.Test()
	f.IOStreams = streams
	var deadline time.Duration
	inner := mk("["+strings.Join(rows, ",")+"]", dir)
	c := newCmdTeamHealthWithReader(f, func(ctx context.Context, p string, cfg *hostpoller.Config) ([]byte, int, error) {
		deadline = healthStatusTimeout(ctx)
		return inner(ctx, p, cfg)
	})
	c.SetArgs([]string{"sweep", "--config", path})
	if err := c.Execute(); err != nil {
		t.Fatal(err)
	}
	return deadline, stored, writes
}
