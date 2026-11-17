//go:build linux

package hostpoller

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/hostrelaunch"
)

func TestOnlyVerifiedRelaunchCleansOldTabs2193(t *testing.T) {
	f := newLoopFixture2060(t)
	calls := 0
	f.p.relaunch.Cleanup = func(_ context.Context, a Agent, target hostrelaunch.Target) {
		calls++
		if release, err := f.p.reserveWorktreeLaunch("repo/iris"); err == nil {
			release()
			t.Error("cleanup ran after launch admission was released")
		}
		if a != f.agent || target.WorkerID != "id-iris" {
			t.Error("cleanup not linked to own launch")
		}
	}
	for i := 0; i < 2; i++ {
		if err := f.p.Step(context.Background()); err != nil {
			t.Fatal(err)
		}
		f.p.Wait()
		if calls != i {
			t.Fatalf("cleanup calls=%d after pass %d, want %d", calls, i, i)
		}
	}
	if err := f.p.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.p.Wait()
	if calls != 1 {
		t.Fatal("cleanup repeated for healthy worker")
	}
}

func TestFailedRelaunchDoesNotCleanTabs2193(t *testing.T) {
	f := newLoopFixture2060(t)
	calls := 0
	f.p.relaunch.Cleanup = func(context.Context, Agent, hostrelaunch.Target) { calls++ }
	f.p.relaunch.Verify = func(context.Context, Agent, hostrelaunch.Target, hostrelaunch.Observation) error {
		return errors.New("unproven launched identity")
	}
	for i := 0; i < 2; i++ {
		if err := f.p.Step(context.Background()); err != nil {
			t.Fatal(err)
		}
		f.p.Wait()
	}
	if len(f.launches) != 1 || f.agent.stops != 1 || calls != 0 {
		t.Fatalf("launches=%d rollback=%d cleanup=%d", len(f.launches), f.agent.stops, calls)
	}
}

func TestStalledTabCleanupDoesNotBlockNextCollection2193(t *testing.T) {
	f := newLoopFixture2060(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := f.p.Step(ctx); err != nil {
		t.Fatal(err)
	}
	f.p.Wait()
	entered := make(chan struct{})
	f.p.relaunch.Cleanup = func(ctx context.Context, _ Agent, _ hostrelaunch.Target) { close(entered); <-ctx.Done() }
	if err := f.p.Step(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-ctx.Done():
		f.p.Wait()
		t.Fatal("cleanup was never reached")
	}
	next := make(chan error, 1)
	go func() { next <- f.p.Step(ctx) }()
	select {
	case err := <-next:
		if err != nil {
			t.Error(err)
		}
	case <-ctx.Done():
		t.Error("next collection blocked on tab cleanup")
	}
	cancel()
	f.p.Wait()
}
