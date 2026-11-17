//go:build linux

package hostpoller

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/delivery"
	"github.com/hadron-memory/hadron-cli/internal/hostrelaunch"
)

func TestCleanupFreshAuthorityOperands2208(t *testing.T) {
	for _, tc := range []struct {
		name, field, mode, reason, desired, wantReason string
		calls                                          int
	}{
		{name: "HOLDS_TASK positive", reason: "HOLDS_TASK", calls: 1},
		{name: "LONG_LIVED positive", reason: "LONG_LIVED", calls: 1},
		{name: "binding unusable", field: "usable", wantReason: "CLEANUP_BINDING_UNPROVEN"},
		{name: "binding app", field: "app", wantReason: "CLEANUP_BINDING_UNPROVEN"},
		{name: "binding worker", field: "worker", wantReason: "CLEANUP_BINDING_UNPROVEN"},
		{name: "binding session", field: "session", wantReason: "CLEANUP_BINDING_UNPROVEN"},
		{name: "binding server", field: "server", wantReason: "CLEANUP_BINDING_UNPROVEN"},
		{name: "binding read error with valid fields", field: "error", wantReason: "CLEANUP_BINDING_UNPROVEN"},
		{name: "binding lock unavailable", field: "lock", wantReason: "CLEANUP_BINDING_UNPROVEN"},
		{name: "OBSERVE is not APPLY", mode: "OBSERVE", wantReason: "CLEANUP_TARGET_UNPROVEN"},
		{name: "unsupported desired reason", reason: "NO_OPEN_TASK", wantReason: "CLEANUP_TARGET_UNPROVEN"},
		{name: "no longer RUNNING", desired: "STOPPED", wantReason: "CLEANUP_TARGET_UNPROVEN"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newLoopFixture2060(t)
			calls := 0
			fixture.p.relaunch.Cleanup = func(context.Context, Agent, hostrelaunch.Target) { calls++ }
			var receipts []Event
			fixture.p.emit = func(e Event) {
				if e.Kind == "relaunch-terminal-cleanup" {
					receipts = append(receipts, e)
				}
			}
			var held func()
			t.Cleanup(func() {
				if held != nil {
					held()
				}
			})
			fixture.p.relaunch.Notice = func(_ context.Context, target hostrelaunch.Target, _ hostrelaunch.Result) error {
				if tc.field == "lock" {
					var err error
					held, err = delivery.LockBinding(target.Worktree, time.Second)
					if err != nil {
						t.Errorf("private fixture failed to hold binding lock: %v", err)
					}
				} else if tc.field != "" {
					binding := hostrelaunch.Binding{Usable: true, AppID: target.AppID, WorkerID: target.WorkerID, SessionID: target.SessionID, Server: target.Server}
					var bindingErr error
					switch tc.field {
					case "usable":
						binding.Usable = false
					case "app":
						binding.AppID = "other-app"
					case "worker":
						binding.WorkerID = "other-worker"
					case "session":
						binding.SessionID = "other-session"
					case "server":
						binding.Server = "https://other.invalid"
					case "error":
						bindingErr = errors.New("synthetic binding read refusal")
					}
					fixture.p.relaunch.Binding = func(hostrelaunch.Target) (hostrelaunch.Binding, error) { return binding, bindingErr }
				}
				if tc.mode != "" {
					fixture.source.snapshot.SweepMode = tc.mode
				}
				if tc.reason != "" {
					fixture.source.snapshot.Workers[0].Reason = tc.reason
				}
				if tc.desired != "" {
					fixture.source.snapshot.Workers[0].Desired = tc.desired
				}
				return nil
			}
			for i := 0; i < 2; i++ {
				if err := fixture.p.Step(context.Background()); err != nil {
					t.Fatal(err)
				}
				fixture.p.Wait()
			}
			if len(fixture.launches) != 1 {
				t.Fatalf("test never reached successful relaunch: %d launches", len(fixture.launches))
			}
			if calls != tc.calls {
				t.Fatalf("cleanup calls=%d want %d; receipts=%+v", calls, tc.calls, receipts)
			}
			if tc.wantReason == "" {
				if len(receipts) != 0 {
					t.Fatalf("positive control refused: %+v", receipts)
				}
			} else if len(receipts) != 1 || receipts[0].Outcome != "skipped" || receipts[0].Reason != tc.wantReason {
				t.Fatalf("receipts=%+v want skipped/%s", receipts, tc.wantReason)
			}
		})
	}
}
