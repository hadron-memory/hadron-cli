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
		// cli#1856: the threshold is 30 minutes; at it the report is still fresh.
		{"stale 8h", &workerTerminalDTO{Status: "idle", CheckedAt: ago(8 * time.Hour)}, "stale (8h)"},
		{"fresh 3m", &workerTerminalDTO{Status: "idle", CheckedAt: ago(3 * time.Minute)}, "idle (3m)"},
		{"at the threshold is fresh", &workerTerminalDTO{Status: "idle", CheckedAt: ago(30 * time.Minute)}, "idle (30m)"},
		{"just past the threshold is stale", &workerTerminalDTO{Status: "idle", CheckedAt: ago(31 * time.Minute)}, "stale (31m)"},
		{"stale dark keeps no status claim", &workerTerminalDTO{Status: "dark", Reason: reason("no_agent"), CheckedAt: ago(9 * time.Hour)}, "stale (9h)"},
		{"idle", &workerTerminalDTO{Status: "idle", CheckedAt: ago(3 * time.Minute)}, "idle (3m)"},
		{"old report reads stale, with its age", &workerTerminalDTO{Status: "idle", CheckedAt: ago(50 * time.Hour)}, "stale (2d)"},
		{"locked", &workerTerminalDTO{Status: "locked", Reason: reason("model_at_capacity"), CheckedAt: ago(5 * time.Hour)}, "stale (5h)"},
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

// cli#1856: --json carries a boolean `stale`, computed against the same
// threshold as the cell, so a script need not parse the age.
func TestTerminalStaleFlag1856(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	for name, c := range map[string]struct {
		age  time.Duration
		want bool
	}{"fresh": {3 * time.Minute, false}, "at threshold": {30 * time.Minute, false}, "past": {31 * time.Minute, true}, "8h": {8 * time.Hour, true}} {
		if got := terminalStale(now.Add(-c.age).Format(time.RFC3339), now); got != c.want {
			t.Errorf("%s: stale = %v, want %v", name, got, c.want)
		}
	}
	if terminalStale("garbage", now) {
		t.Error("an unparseable time is shown as age ? and is not claimed stale")
	}
}
