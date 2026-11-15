package hostpoller

import (
	"strings"
	"testing"
	"time"
)

// Windows uses this production decision before taskkill. No Windows process
// primitives are simulated here: terminate is a counting boundary, so a stale
// decision cannot kill anything on the test host.
func TestStopProcessTreeRechecksExitAfterAuthorizationWait(t *testing.T) {
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
				stopped, detail := a.stopProcessTree(func() (func(), bool) {
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

func TestStopProcessTreeLiveTargetKeepsAuthorizationThroughKill(t *testing.T) {
	a := &execAgent{done: make(chan struct{}), pid: 4242}
	held, releases, kills := false, 0, 0
	stopped, detail := a.stopProcessTree(func() (func(), bool) {
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

func TestStopProcessTreeRefusedAuthorizationNeverKills(t *testing.T) {
	a := &execAgent{done: make(chan struct{}), pid: 4242}
	kills := 0
	stopped, detail := a.stopProcessTree(func() (func(), bool) { return nil, false }, func() (bool, string) { kills++; return true, "" })
	if stopped || kills != 0 || !strings.Contains(detail, "declined") {
		t.Fatalf("refused stop: stopped=%v kills=%d detail=%q", stopped, kills, detail)
	}
}

func TestStopProcessTreeAlreadyDoneNeedsNoAuthorizationOrKill(t *testing.T) {
	a := &execAgent{done: make(chan struct{}), pid: 4242}
	close(a.done)
	stopped, detail := a.stopProcessTree(func() (func(), bool) { t.Error("requested authorization after exit"); return func() {}, true }, func() (bool, string) { t.Error("killed after exit"); return false, "" })
	if !stopped || detail != "" {
		t.Fatalf("existing already-done contract changed: %v %q", stopped, detail)
	}
}
