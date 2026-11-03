//go:build linux

package hostpoller

import (
	"errors"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func startSleeper(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
	return cmd
}

func TestObserveNamesTheProcessByKernelStartTime1043(t *testing.T) {
	cmd := startSleeper(t)
	h, err := LinuxProcessControl{}.Observe(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	id := h.Identity()
	if id.PID != cmd.Process.Pid || id.StartTime == "" {
		t.Fatalf("identity %+v", id)
	}
	again, err := readStartTime(cmd.Process.Pid)
	if err != nil || again != id.StartTime {
		t.Fatalf("start time %q then %q (%v)", id.StartTime, again, err)
	}
	// The token is in clock ticks, so two processes started in one tick
	// share it; with the pid it names one process. pid 1 started at boot.
	if initStart, err := readStartTime(1); err == nil && initStart == id.StartTime {
		t.Fatal("the sleeper's token equals init's; it proves nothing")
	}
}

func TestObserveAGoneProcessFails1043(t *testing.T) {
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if _, err := (LinuxProcessControl{}).Observe(cmd.Process.Pid); err == nil {
		t.Fatal("observed a reaped process")
	}
}

func TestSignalInsideTheLeaseStopsTheObservedProcess1043(t *testing.T) {
	cmd := startSleeper(t)
	pc := LinuxProcessControl{}
	h, err := pc.Observe(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	t0 := pc.Now()
	late, err := h.SignalWithin(syscall.SIGTERM, t0, time.Minute, pc.Now)
	if err != nil {
		t.Fatal(err)
	}
	if late != 0 {
		t.Fatalf("late by %s inside a minute's lease", late)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		// It died of the SIGTERM, not of anything else.
		var ee *exec.ExitError
		if !errors.As(err, &ee) || ee.Sys().(syscall.WaitStatus).Signal() != syscall.SIGTERM {
			t.Fatalf("the signalled process ended with %v, want death by SIGTERM", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the signalled process kept running")
	}
}

func TestSignalAfterTheLeaseSendsNothing1043(t *testing.T) {
	cmd := startSleeper(t)
	pc := LinuxProcessControl{}
	h, err := pc.Observe(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	t0 := pc.Now() - time.Hour // the window opened an hour ago (a delayed authorizeStop answer)
	if _, err := h.SignalWithin(syscall.SIGTERM, t0, 15*time.Second, pc.Now); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("err %v, want ErrLeaseExpired", err)
	}
	if !processAlive(cmd.Process.Pid) {
		t.Fatal("an expired lease still signalled")
	}
}

func TestSignalReportsALateStop1043(t *testing.T) {
	cmd := startSleeper(t)
	pc := LinuxProcessControl{}
	h, err := pc.Observe(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	// A clock whose first read is inside the lease and whose second (after
	// the send) is past it: a stall between check and syscall (ruling 0087 A).
	reads := []time.Duration{10 * time.Second, 20 * time.Second}
	clock := func() time.Duration { d := reads[0]; reads = reads[1:]; return d }
	late, err := h.SignalWithin(syscall.SIGTERM, 0, 15*time.Second, clock)
	if err != nil {
		t.Fatal(err)
	}
	if late != 5*time.Second {
		t.Fatalf("late by %s, want 5s", late)
	}
}

func TestSignalAfterCloseSendsNothing1043(t *testing.T) {
	cmd := startSleeper(t)
	pc := LinuxProcessControl{}
	h, err := pc.Observe(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	h.Close()
	if _, err := h.SignalWithin(syscall.SIGTERM, pc.Now(), time.Minute, pc.Now); err == nil {
		t.Fatal("a closed handle signalled")
	}
	if !processAlive(cmd.Process.Pid) {
		t.Fatal("a closed handle signalled")
	}
}

func TestSignalOnAClosedHandleNeverReachesAReusedDescriptor1043(t *testing.T) {
	first, second := startSleeper(t), startSleeper(t)
	pc := LinuxProcessControl{}
	h, err := pc.Observe(first.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	h.Close()
	// The kernel hands out the lowest free descriptor: the second pidfd
	// takes the number the closed handle held.
	h2, err := pc.Observe(second.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	defer h2.Close()
	if h.(*linuxHandle).fd != h2.(*linuxHandle).fd {
		t.Skip("descriptor not reused; the hazard is not set up")
	}
	if _, err := h.SignalWithin(syscall.SIGTERM, pc.Now(), time.Minute, pc.Now); !errors.Is(err, ErrHandleClosed) {
		t.Fatalf("err %v, want ErrHandleClosed", err)
	}
	time.Sleep(50 * time.Millisecond)
	if !processAlive(second.Process.Pid) {
		t.Fatal("a closed handle signalled the process that reused its descriptor")
	}
}

func TestObserveRefusesWhenTheProcessDiesBeforeItsTokenIsProven1043(t *testing.T) {
	cmd := startSleeper(t)
	orig := observeStartTime
	t.Cleanup(func() { observeStartTime = orig })
	observeStartTime = func(pid int) (string, error) {
		start, err := orig(pid)
		// Exit and reap between the open and the proof: the pid is free
		// for reuse, so this token proves nothing about the pidfd.
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		return start, err
	}
	if _, err := (LinuxProcessControl{}).Observe(cmd.Process.Pid); err == nil {
		t.Fatal("observed a process that died before its token was proven")
	}
}

func TestParseStartTimeSurvivesParensInComm1043(t *testing.T) {
	stat := "4242 (we(i)rd) name) S 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 987654 20 21"
	got, err := parseStartTime(stat)
	if err != nil || got != "987654" {
		t.Fatalf("got %q %v", got, err)
	}
	if _, err := parseStartTime("4242 (x) S 1 2"); err == nil {
		t.Fatal("a short stat line parsed")
	}
}
