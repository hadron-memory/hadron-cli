package team

import (
	"testing"
	"time"
)

// A fixed clock pins every unit boundary of the quiet age (cli#2237): moving
// the hour threshold, or the 48h day threshold, by one step must fail a row.
func TestChannelCheckActivityLabelBoundaries(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	idle := &channelCheckDTO{Status: "NO_SESSION", NoSessionReason: ptr("IDLE")}
	for _, tc := range []struct {
		name string
		ago  time.Duration
		want string
	}{
		{"future", -2 * time.Hour, "NO_SESSION (open, quiet just now)"},
		{"now", 0, "NO_SESSION (open, quiet just now)"},
		{"59s", 59 * time.Second, "NO_SESSION (open, quiet just now)"},
		{"60s", 60 * time.Second, "NO_SESSION (open, quiet 1m)"},
		{"59m59s", 59*time.Minute + 59*time.Second, "NO_SESSION (open, quiet 59m)"},
		{"60m", 60 * time.Minute, "NO_SESSION (open, quiet 1h)"},
		{"90m", 90 * time.Minute, "NO_SESSION (open, quiet 1h)"},
		{"119m", 119 * time.Minute, "NO_SESSION (open, quiet 1h)"},
		{"47h59m", 47*time.Hour + 59*time.Minute, "NO_SESSION (open, quiet 47h)"},
		{"48h", 48 * time.Hour, "NO_SESSION (open, quiet 2d)"},
		{"71h", 71 * time.Hour, "NO_SESSION (open, quiet 2d)"},
		{"336h", 336 * time.Hour, "NO_SESSION (open, quiet 14d)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			at := now.Add(-tc.ago).Format(time.RFC3339)
			if got := channelCheckActivityLabel(idle, &at, now); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
	// Fractional-second RFC3339 parses, and still truncates.
	at := now.Add(-90 * time.Minute).Format(time.RFC3339Nano)
	if got := channelCheckActivityLabel(idle, &at, now); got != "NO_SESSION (open, quiet 1h)" {
		t.Errorf("fractional timestamp: %q", got)
	}
}

func TestChannelCheckLabelEmptyReasonIsBare(t *testing.T) {
	if got := channelCheckLabel(&channelCheckDTO{Status: "NO_SESSION", NoSessionReason: ptr("")}); got != "NO_SESSION" {
		t.Errorf("got %q", got)
	}
}

func ptr(s string) *string { return &s }
