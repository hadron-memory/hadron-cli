package healthsched

import (
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestSweepRefusesCronAndLaunchdWithoutWrites2153(t *testing.T) {
	for _, platform := range []string{"linux", "darwin"} {
		t.Run(platform, func(t *testing.T) {
			home := t.TempDir()
			calls := 0
			e := Env{Home: home, GOOS: platform, UID: 1000, Job: Sweep, Which: func(n string) (string, error) {
				if n == "crontab" || n == "launchctl" || n == "logger" {
					return "/fake/" + n, nil
				}
				return "", exec.ErrNotFound
			}, Run: func([]string, string) ([]byte, error) { calls++; return nil, nil }}
			_, err := e.Install(Spec{Bin: "/fake/hadron", Config: "/fake/host.yaml", Path: "/fake", Interval: time.Minute})
			var usage *UsageError
			if !errors.As(err, &usage) || calls != 0 {
				t.Fatalf("refusal=%v scheduler calls=%d", err, calls)
			}
			entries, err := os.ReadDir(home)
			if err != nil || len(entries) != 0 {
				t.Fatalf("refusal wrote files: %v %v", entries, err)
			}
		})
	}
}
