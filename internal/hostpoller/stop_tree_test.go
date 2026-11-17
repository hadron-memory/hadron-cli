package hostpoller

import (
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Windows uses this production decision before taskkill. No Windows process
// primitives are simulated here: terminate is a counting boundary, so a stale
// decision cannot kill anything on the test host.
func TestStopTreeAuthorizedRechecksExitAfterAuthorizationWait(t *testing.T) {
	for _, viaDone := range []bool{true, false} {
		name := "waiter recorded exit before closing Done"
		if viaDone {
			name = "Done closed"
		}
		t.Run(name, func(t *testing.T) {
			a := &execAgent{done: make(chan struct{}), pid: 4242}
			entered, resume := make(chan struct{}), make(chan struct{})
			t.Cleanup(func() {
				select {
				case <-resume:
				default:
					close(resume)
				}
			})
			type result struct {
				stopped  bool
				detail   string
				kills    int
				releases int
			}
			finished := make(chan result, 1)
			go func() {
				kills, releases := 0, 0
				stopped, detail := a.stopTreeAuthorized(func() (func(), bool) {
					close(entered)
					<-resume
					return func() { releases++ }, true
				}, func() (bool, string) { kills++; return true, "taskkill ran" })
				finished <- result{stopped, detail, kills, releases}
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("authorization was never requested")
			}
			if viaDone {
				close(a.done)
			} else {
				// waitReaping stores exited after cmd.Wait releases the process
				// handle, before it can acquire mu and close Done.
				a.exited.Store(true)
			}
			close(resume)
			select {
			case got := <-finished:
				if got.kills != 0 || !got.stopped || got.releases != 1 || !strings.Contains(got.detail, "already exited") || !strings.Contains(got.detail, "not touched") {
					t.Fatalf("after exit during authorization: %+v; want no kill, released authorization and truthful exit report", got)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("stop did not finish after authorization returned")
			}
		})
	}
}

func TestStopTreeAuthorizedLiveTargetKeepsAuthorizationThroughKill(t *testing.T) {
	a := &execAgent{done: make(chan struct{}), pid: 4242}
	held, releases, kills := false, 0, 0
	stopped, detail := a.stopTreeAuthorized(func() (func(), bool) {
		held = true
		return func() { held = false; releases++ }, true
	}, func() (bool, string) {
		kills++
		if !held {
			t.Error("taskkill ran without binding authorization")
		}
		return false, "taskkill failed"
	})
	if stopped || detail != "taskkill failed" || kills != 1 || held || releases != 1 {
		t.Fatalf("live stop result: stopped=%v detail=%q kills=%d held=%v releases=%d", stopped, detail, kills, held, releases)
	}
}

func TestStopTreeAuthorizedRefusedAuthorizationNeverKills(t *testing.T) {
	a := &execAgent{done: make(chan struct{}), pid: 4242}
	kills, releases := 0, 0
	stopped, detail := a.stopTreeAuthorized(func() (func(), bool) { return func() { releases++ }, false }, func() (bool, string) { kills++; return true, "" })
	if stopped || kills != 0 || releases != 0 || !strings.Contains(detail, "declined") {
		t.Fatalf("refused stop: stopped=%v kills=%d releases=%d detail=%q", stopped, kills, releases, detail)
	}
}

func TestStopTreeAuthorizedAlreadyDoneNeedsNoAuthorizationOrKill(t *testing.T) {
	a := &execAgent{done: make(chan struct{}), pid: 4242}
	close(a.done)
	stopped, detail := a.stopTreeAuthorized(func() (func(), bool) { t.Error("requested authorization after exit"); return func() {}, true }, func() (bool, string) { t.Error("killed after exit"); return false, "" })
	if !stopped || detail != "" {
		t.Fatalf("existing already-done contract changed: %v %q", stopped, detail)
	}
}

func TestStopTreeAuthorizedNilAuthorizationPreservesTerminationResult(t *testing.T) {
	for _, stopped := range []bool{false, true} {
		a := &execAgent{done: make(chan struct{}), pid: 4242}
		calls := 0
		got, detail := a.stopTreeAuthorized(nil, func() (bool, string) { calls++; return stopped, "runner detail" })
		if got != stopped || detail != "runner detail" || calls != 1 {
			t.Fatalf("want stopped=%v; got stopped=%v detail=%q calls=%d", stopped, got, detail, calls)
		}
	}
}

// This executes the real no-hold waiter used on Windows, with a private child
// test process. Holding mu drives the actual post-Wait/pre-Done state instead
// of setting the exit flag by hand. No taskkill or Windows runtime is needed.
func TestStopTreeAuthorizedObservesRealWaiterExitBeforeDone(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestStopTreeWaiterHelper$")
	cmd.Env = append(os.Environ(), "HADRON_STOP_WAITER_HELPER=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		t.Fatal(err)
	}
	a := &execAgent{cmd: cmd, pid: cmd.Process.Pid, done: make(chan struct{}), watcherDone: make(chan struct{})}
	a.mu.Lock()
	locked := true
	t.Cleanup(func() {
		_ = stdin.Close()
		if locked {
			a.mu.Unlock()
		}
		_ = cmd.Process.Kill() // only this test's child handle, never a group
		select {
		case <-a.done:
		case <-time.After(5 * time.Second):
			t.Error("private waiter did not finish")
		}
	})
	go a.waitReaping()
	kills, releases := 0, 0
	stopped, detail := a.stopTreeAuthorized(func() (func(), bool) {
		_ = stdin.Close() // EOF lets the helper exit; the real cmd.Wait reaps it
		deadline := time.Now().Add(5 * time.Second)
		for !a.exited.Load() {
			if time.Now().After(deadline) {
				t.Fatal("the real waiter did not publish the exit")
			}
			time.Sleep(time.Millisecond)
		}
		select {
		case <-a.done:
			t.Fatal("Done closed while the real waiter was blocked on mu")
		default:
		}
		return func() { releases++ }, true
	}, func() (bool, string) { kills++; return false, "unexpected kill" })
	if !stopped || kills != 0 || releases != 1 || !strings.Contains(detail, "already exited") || !strings.Contains(detail, strconv.Itoa(a.pid)) {
		t.Fatalf("stopped=%v kills=%d releases=%d detail=%q", stopped, kills, releases, detail)
	}
	a.mu.Unlock()
	locked = false
	select {
	case <-a.done:
	case <-time.After(5 * time.Second):
		t.Fatal("waiter did not close Done after mu was released")
	}
}

func TestStopTreeWaiterHelper(t *testing.T) {
	if os.Getenv("HADRON_STOP_WAITER_HELPER") != "1" {
		return
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}
