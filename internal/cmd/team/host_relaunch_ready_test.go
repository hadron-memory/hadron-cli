package team

import (
	"context"
	"errors"
	"testing"
)

func TestRelaunchRequiresOneRunningDaemonAndTerminalList2060(t *testing.T) {
	for _, tc := range []struct {
		name, scopes      string
		scopeErr, listErr error
		want              bool
		calls             int
	}{
		{name: "one", scopes: "orca-daemon-one.scope loaded active running Orca\n", want: true, calls: 1},
		{name: "none"},
		{name: "multiple", scopes: "orca-daemon-one.scope loaded active running\norca-daemon-two.scope loaded active running\n"},
		{name: "no systemd", scopeErr: errors.New("unavailable")},
		{name: "list fails", scopes: "orca-daemon-one.scope loaded active running\n", listErr: errors.New("daemon missing"), calls: 1},
		{name: "unrelated", scopes: "other.scope loaded active running\n"},
		{name: "not running", scopes: "orca-daemon-one.scope loaded active exited\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			err := relaunchDaemonReady(context.Background(), func(context.Context) ([]byte, error) { return []byte(tc.scopes), tc.scopeErr }, func(context.Context) error { calls++; return tc.listErr })
			if (err == nil) != tc.want || calls != tc.calls {
				t.Fatalf("err=%v list calls=%d", err, calls)
			}
		})
	}
}
