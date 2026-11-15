//go:build linux

package hostpoller

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/hostrelaunch"
)

func TestSetupFailureBeforeOrcaRuns2149(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	l := OrcaLauncher{StateDir: blocker, Run: func(context.Context, []string) (int, string, string) { calls++; return 0, "", "" }}
	_, err := l.Launch(context.Background(), OrcaLaunchSpec{Argv: []string{"claude"}, Dir: t.TempDir()})
	var classified interface{ RelaunchFailureReason() string }
	if !errors.As(err, &classified) || classified.RelaunchFailureReason() != "SETUP_FAILED" || calls != 0 {
		t.Fatalf("prelaunch failure=%v calls=%d", err, calls)
	}
}
func TestDiskTmpFailureIsSetupBeforeLaunch2149(t *testing.T) {
	f := newLoopFixture2060(t)
	if err := os.WriteFile(f.disk, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	var result hostrelaunch.Result
	f.p.relaunch.Notice = func(_ context.Context, _ hostrelaunch.Target, r hostrelaunch.Result) error { result = r; return nil }
	for range 2 {
		if err := f.p.Step(context.Background()); err != nil {
			t.Fatal(err)
		}
		f.p.Wait()
	}
	if result.Status != hostrelaunch.Failed || result.FailureReason != "SETUP_FAILED" || len(f.launches) != 0 || len(f.journal["id-iris"].Attempts) != 1 {
		t.Fatalf("result=%+v launches=%d journal=%+v", result, len(f.launches), f.journal)
	}
}
