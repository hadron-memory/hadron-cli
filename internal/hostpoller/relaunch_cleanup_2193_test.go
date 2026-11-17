//go:build linux

package hostpoller

import (
	"context"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/hostrelaunch"
)

func TestOnlyVerifiedRelaunchCleansOldTabs2193(t *testing.T) {
	f := newLoopFixture2060(t)
	calls := 0
	f.p.relaunch.Cleanup = func(_ context.Context, a Agent, target hostrelaunch.Target) {
		calls++
		if a != f.agent || target.WorkerID != "id-iris" {
			t.Error("cleanup not linked to own launch")
		}
	}
	for i := 0; i < 2; i++ {
		f.p.relaunchStep(context.Background())
		f.p.Wait()
		if calls != i {
			t.Fatalf("cleanup calls=%d after pass %d, want %d", calls, i, i)
		}
	}
	f.p.relaunchStep(context.Background())
	f.p.Wait()
	if calls != 1 {
		t.Fatal("cleanup repeated for healthy worker")
	}
}
