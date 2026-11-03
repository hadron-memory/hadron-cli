package team

import (
	"testing"
	"time"
)

func TestTerminalLabel(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) string { return now.Add(-d).Format(time.RFC3339) }
	reason := func(s string) *string { return &s }
	cases := []struct {
		name string
		in   *workerTerminalDTO
		want string
	}{
		{"null", nil, "—"},
		{"busy", &workerTerminalDTO{Status: "busy", CheckedAt: ago(10 * time.Second)}, "busy (<1m)"},
		{"idle", &workerTerminalDTO{Status: "idle", CheckedAt: ago(3 * time.Minute)}, "idle (3m)"},
		{"stale idle shows its age", &workerTerminalDTO{Status: "idle", CheckedAt: ago(50 * time.Hour)}, "idle (2d)"},
		{"locked", &workerTerminalDTO{Status: "locked", Reason: reason("model_at_capacity"), CheckedAt: ago(5 * time.Hour)}, "locked (model_at_capacity, 5h)"},
		{"dark", &workerTerminalDTO{Status: "dark", Reason: reason("no_turn_for_due_ticker"), CheckedAt: ago(12 * time.Minute)}, "dark (no_turn_for_due_ticker, 12m)"},
		// Age is from checkedAt, never reportedAt.
		{"age from checkedAt", &workerTerminalDTO{Status: "idle", CheckedAt: ago(3 * time.Minute), ReportedAt: ago(9 * time.Hour)}, "idle (3m)"},
		{"unknown status", &workerTerminalDTO{Status: "\x1b[31mevil", CheckedAt: ago(time.Minute)}, "? (1m)"},
		{"unknown reason", &workerTerminalDTO{Status: "dark", Reason: reason("newer_reason"), CheckedAt: ago(time.Minute)}, "dark (?, 1m)"},
		{"bad time", &workerTerminalDTO{Status: "idle", CheckedAt: "garbage"}, "idle (?)"},
		{"future time", &workerTerminalDTO{Status: "idle", CheckedAt: now.Add(time.Hour).Format(time.RFC3339)}, "idle (<1m)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := terminalLabel(tc.in, now); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
