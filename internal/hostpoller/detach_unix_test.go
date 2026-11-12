//go:build !windows

package hostpoller

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// An agent that already exited is signalled nothing — its pid may have been
// reused — and Terminate says so: the group probe finds the group empty, so
// the stop is confirmed without a signal going out (#1474 review P2-1).
func TestExecHostTerminateAfterExitSignalsNothing(t *testing.T) {
	if _, err := exec.LookPath("true"); err != nil {
		t.Skip("no true")
	}
	signals := 0
	realSignal := signalGroup
	signalGroup = func(pgid int, sig syscall.Signal) error {
		signals++
		return realSignal(pgid, sig)
	}
	defer func() { signalGroup = realSignal }()
	a, err := ExecHost{}.Launch(context.Background(), LaunchSpec{Argv: []string{"true"}, Dir: t.TempDir(), Env: os.Environ()})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-a.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("true exits at once")
	}
	stopped, detail := a.Terminate()
	if !stopped || detail != "" {
		t.Errorf("an agent whose group is gone is a silent confirmed stop: stopped=%v %q", stopped, detail)
	}
	if signals != 0 {
		t.Errorf("an exited agent is signalled nothing: %d signals", signals)
	}
}

// #1474 review P2-1: the leader exiting first must not end the group stop.
// A launcher that backgrounds a TERM-ignoring agent and returns is the
// documented failure shape; Terminate — called AFTER the leader is gone —
// must still stop the whole group, because it probes the group, not the
// leader.
func TestExecHostTerminateStopsTheGroupAfterTheLeaderExits(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	if _, err := os.Stat("/proc/self/cwd"); err != nil {
		t.Skip("no /proc")
	}
	dir := t.TempDir()
	ready := filepath.Join(dir, "child-armed")
	// The child traps TERM and stays; the leader forks it and exits at once.
	// (argv[1] of a `sh -c` script is $0 — the sibling tests' convention.)
	a, err := ExecHost{TerminateGrace: 300 * time.Millisecond}.Launch(context.Background(), LaunchSpec{
		Argv: []string{"sh", "-c", `sh -c 'trap "" TERM; touch "$0"; sleep 30' "$0" & exit 0`, ready},
		Dir:  dir, Env: os.Environ(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = a.Terminate() }()
	select {
	case <-a.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the leader must exit at once")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the child never armed its trap")
		}
		time.Sleep(10 * time.Millisecond)
	}
	stopped, detail := a.Terminate()
	if !stopped || !strings.Contains(detail, "killed the launched process group") {
		t.Errorf("a group whose leader exited first is still killed: stopped=%v %q", stopped, detail)
	}
	// Nothing is left working in the tree (the review's requested
	// assertion).
	sweep := time.Now().Add(5 * time.Second)
	for {
		who, _ := ExecHost{}.Occupied(context.Background(), dir)
		if who == "" {
			return
		}
		if time.Now().After(sweep) {
			t.Fatalf("the TERM-ignoring child survived its leader's group: %q", who)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// The paths a healthy host never takes: a group signal that fails outright,
// and a group that outlives SIGKILL — both must report stopped=false, so the
// caller keeps the agent's records (#1474 review P2-2).
func TestTerminateGroupReportsWhatItCouldNotConfirm(t *testing.T) {
	t.Run("signal fails", func(t *testing.T) {
		realSignal, realProbe := signalGroup, probeGroup
		signalGroup = func(int, syscall.Signal) error { return syscall.EPERM }
		probeGroup = func(int) error { return nil } // the group is there
		defer func() { signalGroup, probeGroup = realSignal, realProbe }()
		stopped, detail := terminateGroup(999998, 50*time.Millisecond)
		if stopped || !strings.Contains(detail, "could not stop") {
			t.Errorf("a failed signal is not a stop: stopped=%v %q", stopped, detail)
		}
	})
	t.Run("group outlives the kill", func(t *testing.T) {
		realSignal, realProbe, realWait := signalGroup, probeGroup, postKillWait
		signalGroup = func(int, syscall.Signal) error { return nil }
		probeGroup = func(int) error { return nil } // never gone
		postKillWait = 80 * time.Millisecond
		defer func() {
			signalGroup, probeGroup, postKillWait = realSignal, realProbe, realWait
		}()
		stopped, detail := terminateGroup(999998, 50*time.Millisecond)
		if stopped || !strings.Contains(detail, "did not exit within") {
			t.Errorf("a group that outlives SIGKILL is not a stop: stopped=%v %q", stopped, detail)
		}
	})
}

// The occupied-tree refusal names the occupant without carrying its
// credentials or an unbounded tail (#1474 review P2-4).
func TestRenderArgvBoundsAndRedacts(t *testing.T) {
	for name, tc := range map[string]struct {
		argv []string
		want string
	}{
		"plain":                   {[]string{"sleep", "30"}, "sleep 30"},
		"assignment":              {[]string{"agent", "--token=hdr_secret_1"}, "agent --token=<redacted>"},
		"env style":               {[]string{"agent", "HADRON_TOKEN=hdr_x"}, "agent HADRON_TOKEN=<redacted>"},
		"bare flag":               {[]string{"agent", "--token", "hdr_secret_1", "--verbose"}, "agent --token <redacted> --verbose"},
		"header":                  {[]string{"curl", "-H", "Authorization: Bearer hdr_x"}, "curl -H Authorization: <redacted>"},
		"cookie":                  {[]string{"curl", "-H", "Cookie: session=hdr_x"}, "curl -H Cookie: <redacted>"},
		"spaces kept unambiguous": {[]string{"sh", "-c", "sleep 30 & wait"}, `sh -c "sleep 30 & wait"`},
		"empty argument survives": {[]string{"a", "", "b"}, `a "" b`},
	} {
		t.Run(name, func(t *testing.T) {
			if got := renderArgv(tc.argv); got != tc.want {
				t.Errorf("renderArgv(%q) = %q, want %q", tc.argv, got, tc.want)
			}
		})
	}
	long := make([]string, 50)
	for i := range long {
		long[i] = "0123456789"
	}
	if got := renderArgv(long); len(got) > argvDisplayLimit+len("…") {
		t.Errorf("the rendered line is bounded: %d bytes", len(got))
	}
	long[len(long)-1] = "HADRON_TOKEN=hdr_secret_tail"
	if rendered := renderArgv(long); strings.Contains(rendered, "hdr_secret_tail") {
		t.Error("redaction applies inside the bounded tail too")
	}
}
