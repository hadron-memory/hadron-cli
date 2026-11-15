//go:build linux

package hostpoller

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/hostrelaunch"
)

func TestOrcaCreateFailureCarriesOnlyObservedRepairReason2060(t *testing.T) {
	for _, test := range []struct {
		name, reply, want string
		code              int
	}{
		{"explicit refusal", `{"ok":false,"error":{"code":"permission_denied"}}`, hostrelaunch.FailureTerminalCreateRefused, 1},
		{"unusable reply", `{broken`, "", 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			l := OrcaLauncher{StateDir: t.TempDir(), Run: func(context.Context, []string) (int, string, string) { return test.code, test.reply, "" }}
			_, err := l.Launch(context.Background(), OrcaLaunchSpec{Argv: []string{"claude"}, Dir: t.TempDir()})
			if err == nil {
				t.Fatal("failed create accepted")
			}
			var classified interface{ RelaunchFailureReason() string }
			got := ""
			if errors.As(err, &classified) {
				got = classified.RelaunchFailureReason()
			}
			if got != test.want {
				t.Fatalf("classification=%q want%q error=%v", got, test.want, err)
			}
		})
	}
}
func TestOrcaMarkerTimeoutCarriesTimeoutRepairReason2060(t *testing.T) {
	fake := newFakeOrca(t)
	fake.runNoCmd = true
	l := orcaLauncherFor(t, fake)
	l.MarkerWait = 20 * time.Millisecond
	_, err := l.Launch(context.Background(), OrcaLaunchSpec{Argv: []string{"claude"}, Dir: t.TempDir()})
	var classified interface{ RelaunchFailureReason() string }
	if !errors.As(err, &classified) || classified.RelaunchFailureReason() != hostrelaunch.FailureTimeout {
		t.Fatalf("timeout classification missing: %v", err)
	}
}
