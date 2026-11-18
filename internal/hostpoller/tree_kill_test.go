package hostpoller

import (
	"context"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// #2087: cancellation requests do not release binding authority while the
// taskkill runner is still active. Only its return ends that authority window.
func TestTreeKillDeadlineKeepsAuthorizationUntilRunnerReturns(t *testing.T) {
	a := &execAgent{done: make(chan struct{}), pid: 4242}
	var held, runnerReturned, releasedEarly atomic.Bool
	cancelled, finishRunner := make(chan struct{}), make(chan struct{})
	var once sync.Once
	finish := func() { once.Do(func() { close(finishRunner) }) }
	t.Cleanup(finish)
	type result struct {
		stopped bool
		detail  string
	}
	finished := make(chan result, 1)
	go func() {
		stopped, detail := a.stopTreeAuthorized(func() (func(), bool) {
			held.Store(true)
			return func() {
				if !runnerReturned.Load() {
					releasedEarly.Store(true)
				}
				held.Store(false)
			}, true
		}, func() (bool, string) {
			return runTreeKill(a.pid, 20*time.Millisecond, func(ctx context.Context, pid int) ([]byte, error) {
				defer runnerReturned.Store(true)
				if pid != 4242 || !held.Load() {
					t.Error("runner entered without this launch's authorization")
				}
				select {
				case <-ctx.Done():
					close(cancelled)
				case <-finishRunner:
					return nil, nil // lets the red version clean up too
				}
				<-finishRunner
				return nil, ctx.Err()
			})
		})
		finished <- result{stopped, detail}
	}()
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		finish()
		<-finished
		t.Fatal("taskkill received no cancellation deadline")
	}
	if !held.Load() {
		t.Error("authorization was released before the cancelled runner returned")
	}
	select {
	case got := <-finished:
		t.Fatalf("stop returned before the runner: %+v", got)
	default:
	}
	finish()
	select {
	case got := <-finished:
		if releasedEarly.Load() {
			t.Error("authorization release ran before the runner returned")
		}
		if got.stopped || held.Load() || !strings.Contains(got.detail, "not confirmed") || !strings.Contains(got.detail, "4242") {
			t.Fatalf("deadline must report unconfirmed only after releasing authority: %+v held=%v", got, held.Load())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runner returned but stop did not finish")
	}
}

func TestTreeKillPreservesSuccessAndFailure(t *testing.T) {
	for _, success := range []bool{true, false} {
		stopped, detail := runTreeKill(4242, time.Second, func(ctx context.Context, pid int) ([]byte, error) {
			if pid != 4242 || ctx.Err() != nil {
				t.Errorf("unexpected runner input: pid=%d err=%v", pid, ctx.Err())
			}
			if !success {
				return []byte("failure detail"), context.Canceled
			}
			return nil, nil
		})
		if stopped != success || !strings.Contains(detail, "4242") || (!success && !strings.Contains(detail, "failure detail")) {
			t.Fatalf("success=%v stopped=%v detail=%q", success, stopped, detail)
		}
	}
}

func TestTreeKillDoesNotConfirmSuccessAfterDeadline(t *testing.T) {
	stopped, detail := runTreeKill(4242, 20*time.Millisecond, func(ctx context.Context, _ int) ([]byte, error) {
		if _, ok := ctx.Deadline(); !ok {
			return nil, nil // red control: an unbounded runner would confirm
		}
		<-ctx.Done()
		return nil, nil // exit status alone cannot prove what happened in time
	})
	if stopped || !strings.Contains(detail, "not confirmed") {
		t.Fatalf("late success must stay unconfirmed: stopped=%v detail=%q", stopped, detail)
	}
}

// Goexit ends the runner's goroutine after executing its defers. A synchronous
// runner therefore unwinds the authorized caller too. An abandoned goroutine
// instead lets runTreeKill return normally at the deadline. No negative sleep
// or scheduler race decides this assertion.
func TestTreeKillRunnerRemainsOnAuthorizedCaller(t *testing.T) {
	a := &execAgent{done: make(chan struct{}), pid: 4242}
	var returned, released, runnerExited atomic.Bool
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.stopTreeAuthorized(func() (func(), bool) {
			return func() {
				if !runnerExited.Load() {
					t.Error("authority released before runner exited")
				}
				released.Store(true)
			}, true
		}, func() (bool, string) {
			return runTreeKill(a.pid, 20*time.Millisecond, func(ctx context.Context, _ int) ([]byte, error) {
				defer runnerExited.Store(true)
				<-ctx.Done()
				runtime.Goexit()
				return nil, nil
			})
		})
		returned.Store(true)
	}()
	select {
	case <-done:
		if returned.Load() || !released.Load() || !runnerExited.Load() {
			t.Fatalf("runner must unwind its authorized caller: returned=%v released=%v runnerExited=%v", returned.Load(), released.Load(), runnerExited.Load())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runner failed to unwind its caller")
	}
}
