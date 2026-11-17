//go:build linux

package hostpoller

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/hostrelaunch"
)

func TestWorkerEndReportsBindingOwnerRefusal2076(t *testing.T) {
	p, srv, host := testPoller(t)
	withStop(p, StopOnEndAlways)
	reason := "the binding cache is missing and this host runs as uid 1001 but the worktree belongs to uid 1002: nothing was created; start the worker once so its own session start creates it, or run the host as that user"
	p.lockBinding = func(string, time.Duration) (func(), error) { return nil, errors.New(reason) }
	var events []Event
	p.emit = func(e Event) { events = append(events, e) }
	srv.batches = [][]Operation{{stopOp(IssuedByUser)}}
	step(t, p)
	if len(host.stopped) != 0 {
		t.Fatal("signalled despite owner refusal")
	}
	for _, e := range events {
		if e.Kind == "stop-skipped" && strings.Contains(e.Detail, reason) {
			return
		}
	}
	t.Fatalf("owner refusal missing from stop-skipped: %+v", events)
}

func TestRelaunchLockReturnsBindingErrorAndReleasesAdmission2076(t *testing.T) {
	f := newLoopFixture2060(t)
	if err := f.p.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.p.Wait()
	snap := f.source.snapshot
	runner, target, reason, ok := f.p.relaunchRunner(f.p.relaunch, snap, snap.Workers[0], f.journal, 1, nil)
	if !ok {
		t.Fatal(reason)
	}
	// A private fixture supplies a real lock-open failure through delivery;
	// uid policy is pinned with delivery's private credential seams.
	cache := filepath.Join(target.Worktree, ".hadron-team")
	if err := os.MkdirAll(cache, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(cache, "hadron-team-session.lock")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(cache, "hadron-team-session.lock"), 0700); err != nil {
		t.Fatal(err)
	}
	release, err := runner.Lock(context.Background(), target)
	if release != nil {
		release()
		t.Fatal("lock failure returned release")
	}
	if err == nil || !strings.Contains(err.Error(), "session binding lock") {
		t.Fatalf("binding failure lost: %v", err)
	}
	// Failed locking must not keep C3/C5 admission reserved.
	if err := os.Remove(filepath.Join(cache, "hadron-team-session.lock")); err != nil {
		t.Fatal(err)
	}
	release, err = runner.Lock(context.Background(), hostrelaunch.Target{Worktree: target.Worktree})
	if err != nil {
		t.Fatalf("admission retained after refusal: %v", err)
	}
	release()
}
