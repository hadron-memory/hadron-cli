//go:build unix

package hostpoller

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These two tests stop real process groups with real signals and read /proc:
// they exercise Unix process-group behavior and have no Windows meaning, so
// they live in a unix-tagged file with their helper patientKillWait (defined in
// wait_for_unix_test.go). exechost_test.go keeps the portable tests, which must
// compile everywhere (cli#2315).

// Terminate stops the whole process group a launch created: the launched
// process AND the child it runs (#1474 — a wrapper relaunching its agent
// must not survive its start's failure). A bare kill of the leader would
// leave the child working in the tree; the group signal must not.
func TestExecHostTerminateKillsTheLaunchedGroup(t *testing.T) {
	patientKillWait(t)
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	if _, err := os.Stat("/proc/self/cwd"); err != nil {
		t.Skip("no /proc")
	}
	dir := t.TempDir()
	ready := filepath.Join(dir, "child-running")
	// The grace is how long the stop waits to SEE the TERMed group drain before it
	// escalates to KILL; the wording asserted below is the TERM path's. On a saturated
	// host seeing the drain takes seconds (#2173), so the grace is generous: a TERM
	// that really works is observed and the stop returns at once, and a TERM that
	// does not still escalates after the grace.
	// The child lives far longer than the grace (900 s vs 300 s), so a stop that did
	// not really take the group down cannot pass by the child ending on its own.
	h := ExecHost{TerminateGrace: 300 * time.Second}
	// sleep as a child the leader waits on: killing only the leader would
	// orphan it in the tree.
	a, err := h.Launch(context.Background(), LaunchSpec{Argv: []string{"sh", "-c", `sleep 900 & touch "$0"; wait`, ready}, Dir: dir, Env: os.Environ()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = a.Terminate() }()
	// Gate on the child existing: a TERM that outruns the fork kills the
	// leader before there is a group to leave behind, which would prove
	// nothing.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the child never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if stopped, detail := a.Terminate(); !stopped || !strings.Contains(detail, "terminated the launched process group") {
		t.Errorf("a live group is stopped: stopped=%v %q", stopped, detail)
	}
	select {
	case <-a.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the launched process must exit after Terminate")
	}
	if h.Alive(a.PID()) {
		t.Error("the group leader died with its group")
	}
	// The child went with it: nothing is left working in the tree.
	sweepDeadline := time.Now().Add(5 * time.Second)
	for {
		who, _ := h.Occupied(context.Background(), dir)
		if who == "" {
			break
		}
		if time.Now().After(sweepDeadline) {
			t.Fatalf("the group's child survived the leader: %q", who)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// The incident's exact shape: a wrapper that ignores SIGTERM and keeps
// relaunching a child is killed by the escalation after the grace.
func TestExecHostTerminateEscalatesOnAWrapperThatKeepsRelaunching(t *testing.T) {
	patientKillWait(t)
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	dir := t.TempDir()
	ready := filepath.Join(dir, "armed")
	h := ExecHost{TerminateGrace: 300 * time.Millisecond}
	a, err := h.Launch(context.Background(), LaunchSpec{
		Argv: []string{"sh", "-c", `trap '' TERM; touch "$0"; while :; do sleep 5 & wait $!; done`, ready},
		Dir:  dir, Env: os.Environ(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = a.Terminate() }()
	// Wait for the trap to be armed: a TERM that lands first kills the shell
	// before it ever ignores one, which is not the shape under test.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the wrapper never armed its trap")
		}
		time.Sleep(10 * time.Millisecond)
	}
	start := time.Now()
	if stopped, detail := a.Terminate(); !stopped || !strings.Contains(detail, "killed the launched process group") {
		t.Errorf("a TERM-ignoring wrapper is killed after the grace: stopped=%v %q", stopped, detail)
	}
	if elapsed := time.Since(start); elapsed < 250*time.Millisecond {
		t.Errorf("the escalation must wait the grace first: %s", elapsed)
	}
	select {
	case <-a.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the wrapper must be dead after the escalation")
	}
}
