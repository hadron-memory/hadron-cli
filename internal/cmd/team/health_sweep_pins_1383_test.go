//go:build !windows

package team

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
)

// #1383 (follow-up to #1382's seats): discriminating pins for the health
// sweep's registered-worker refusals and the joint-refusal precedence. Each
// refusal asserts the exit code, the fixed diagnostic and ZERO writes.

func health1383Registrations(workers string, live bool) string {
	l := "true"
	if !live {
		l = "false"
	}
	return `{"data":{"hostRegistrations":[{"hostId":"own","live":` + l + `,"workers":[` + workers + `]}]}}`
}

func health1383Worker(workerID, id, name string) string {
	return `{"workerId":"` + workerID + `","worker":{"id":"` + id + `","name":"` + name + `"},"worktrees":["iris"]}`
}

const (
	health1383Unresolved = "cannot resolve registered worker; nothing written"
	health1383Ambiguous  = "ambiguous registered worker name; nothing written"
	health1383NotLive    = "host registration is not live; nothing written"
	health1383NoState    = "cannot read registered host state; nothing written"
)

func health1383Refused(t *testing.T, registrations, state string, code int, diagnostic string) {
	t.Helper()
	_, n, _, err := health1181RunFixture(t, `{}`, 0, true, registrations, health1181Status, state)
	if n != 0 {
		t.Fatalf("%d writes on a refusal", n)
	}
	if got := exitcode.FromError(err); got != code {
		t.Fatalf("exit %d, want %d: %v", got, code, err)
	}
	if err == nil || err.Error() != diagnostic {
		t.Fatalf("diagnostic %v, want %q", err, diagnostic)
	}
}

// Each guard in the registered-worker loop is pinned by a registration that
// trips ONLY that guard, so removing one turns exactly its case red.
func TestHealthSweepRegisteredWorkerGuards1383(t *testing.T) {
	good := health1383Worker("w1", "w1", "Iris")
	for _, tc := range []struct {
		name, workers, diagnostic string
	}{
		{"empty workerId", health1383Worker("", "", "Iris"), health1383Unresolved},
		{"repeated workerId, distinct names", good + "," + health1383Worker("w1", "w1", "Other"), health1383Unresolved},
		{"blank name", health1383Worker("w1", "w1", "   "), health1383Unresolved},
		{"empty name", health1383Worker("w1", "w1", ""), health1383Unresolved},
		{"nested id differs from workerId", health1383Worker("w1", "w2", "Iris"), health1383Unresolved},
		{"duplicate name, distinct ids", good + "," + health1383Worker("w2", "w2", "Iris"), health1383Ambiguous},
	} {
		t.Run(tc.name, func(t *testing.T) {
			health1383Refused(t, health1383Registrations(tc.workers, true), `{"hostId":"own"}`, exitcode.Error, tc.diagnostic)
		})
	}
}

// A state file that is not there is refused like an empty one (#1181 pins the
// empty one): exit 1, the fixed diagnostic, nothing written.
func TestHealthSweepMissingStateFile1383(t *testing.T) {
	health1383Refused(t, "", health1383MissingState, exitcode.Error, health1383NoState)
}

// Joint case: a malformed registered worker on a host that is NOT live. The
// malformed-worker refusal (exit 1) runs before the liveness check (exit 8):
// this is today's order, kept on purpose because it is fail-closed and writes
// nothing either way (#1383, option a). The help states it.
func TestHealthSweepJointRefusalPrecedence1383(t *testing.T) {
	bad := health1383Registrations(health1383Worker("", "", "Iris"), false)
	health1383Refused(t, bad, `{"hostId":"own"}`, exitcode.Error, health1383Unresolved)
	// The same registration, live, gives the same answer: liveness did not decide it.
	health1383Refused(t, health1383Registrations(health1383Worker("", "", "Iris"), true), `{"hostId":"own"}`, exitcode.Error, health1383Unresolved)
	// A well-formed non-live registration is the 8.
	health1383Refused(t, health1383Registrations(health1383Worker("w1", "w1", "Iris"), false), `{"hostId":"own"}`, exitcode.Forbidden, health1383NotLive)
}

func TestHealthSweepHelpStatesJointRefusal1383(t *testing.T) {
	c := newCmdTeamHealth(nil)
	var sweep string
	for _, sub := range c.Commands() {
		if sub.Name() == "sweep" {
			sweep = sub.Long
		}
	}
	if sweep == "" {
		t.Fatal("no sweep command")
	}
	want := "A malformed registered worker is exit 1 even when the host registration is also not live: the sweep validates the registered workers before it checks whether the host registration is live."
	if !strings.Contains(strings.Join(strings.Fields(sweep), " "), want) {
		t.Fatalf("the sweep help does not state the joint case; want %q in:\n%s", want, sweep)
	}
}

// The human name sink: a registered name with terminal escapes reaches the
// text output only through SafeCell.
func TestHealthSweepHumanNameIsNeutralised1383(t *testing.T) {
	hostile := "Ir\\u001b[31mis"
	registrations := health1383Registrations(health1383Worker("w1", "w1", hostile), true)
	status := strings.Replace(health1181Status, `"worker":"Iris"`, `"worker":"`+hostile+`"`, 1)
	out, n, _, err := health1181RunFixture(t, `{"data":{"setHostWorkerTerminals":{"stored":[{"id":"w1"}],"refused":[]}}}`, 0, false, registrations, status, `{"hostId":"own"}`)
	if err != nil || n != 1 {
		t.Fatalf("writes %d err %v out %q", n, err, out)
	}
	if strings.ContainsRune(out, 0x1b) {
		t.Fatalf("raw escape in the text output: %q", out)
	}
	// SafeCell turns the escape byte into a space and keeps the rest of the
	// name: the worker's own row starts with exactly that cell.
	var row string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "Ir ") {
			row = line
		}
	}
	if !strings.HasPrefix(row, "Ir [31mis  locked") {
		t.Fatalf("the worker's row does not start with the sanitized name: %q in %q", row, out)
	}
}

// Oversized stdout kills the REAL child. The read is given a 6-second deadline
// and the child sleeps far longer, so the only thing that can end the run early
// is the overflow cancel: a run that ends in the deadline class (the bound was
// bypassed and the child ran into the timeout) fails, and so does one that takes
// the deadline's time. (#1181 pins the buffer callback.)
func TestHealthStatusOverflowKillsTheChild1383(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "fake-status")
	// The pid file sits next to the script, named from $0, so no path is
	// interpolated into the script text.
	script := "#!/bin/sh\necho $$ > \"$(dirname \"$0\")/pid\"\nhead -c 9437184 /dev/zero\nexec sleep 120\n"
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	const deadline = 6 * time.Second
	ctx := withHealthStatusTimeout(context.Background(), deadline)
	start := time.Now()
	raw, code, err := runHealthStatusCommandEnv(ctx, bin, filepath.Join(dir, "host.yaml"), hostpoller.ScrubbedEnv(os.Environ(), "SYNTHETIC_KEY_1383"))
	took := time.Since(start)
	var timedOut errHealthStatusTimeout
	if errors.As(err, &timedOut) {
		t.Fatalf("the read ended in its deadline after %s, not by the overflow cancel: %v", took, err)
	}
	if err == nil || raw != nil || code == 0 {
		t.Fatalf("an oversized read must fail with no output: code %d err %v len %d", code, err, len(raw))
	}
	if took >= deadline-time.Second {
		t.Fatalf("the child was not cancelled on overflow: the run took %s of a %s deadline", took, deadline)
	}
	b, rerr := os.ReadFile(filepath.Join(dir, "pid"))
	if rerr != nil {
		t.Fatal(rerr)
	}
	pid, perr := strconv.Atoi(strings.TrimSpace(string(b)))
	if perr != nil {
		t.Fatal(perr)
	}
	if syscall.Kill(pid, 0) == nil {
		t.Fatalf("child %d is still running after the overflow", pid)
	}
}
