package orcaterm

import (
	"testing"
	"time"

	ts "github.com/hadron-memory/hadron-cli/internal/terminalstatus"
)

// cli#2676: a usage-limit screen is its own locked state with the reset time
// read into UTC, not "locked (unrecognized)".
const glmLimit = "  429 Usage limit reached for 5 hour ... reset 2026-11-20 16:45:32 (UTC+8)"

func TestClassifyUsageLimit2676(t *testing.T) {
	at := func(s string) *time.Time {
		v, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return &v
	}
	local := time.FixedZone("host", -5*3600)
	nowLocal := time.Date(2026, 11, 20, 1, 0, 0, 0, local)
	for name, c := range map[string]struct {
		tail      []string
		now       time.Time
		resetAt   *time.Time
		assumedLC bool
	}{
		"explicit UTC+8 offset":      {[]string{"⏺ Working", glmLimit, rule, "❯ ", rule, claudeHint}, now, at("2026-11-20T08:45:32Z"), false},
		"limit line alone":           {[]string{glmLimit}, now, at("2026-11-20T08:45:32Z"), false},
		"negative offset":            {[]string{"429 Usage limit reached for 5 hour ... reset 2026-11-20 03:00:00 (UTC-5)"}, now, at("2026-11-20T08:00:00Z"), false},
		"half-hour offset":           {[]string{"429 Usage limit reached for 5 hour ... reset 2026-11-20 14:15:00 (UTC+5:30)"}, now, at("2026-11-20T08:45:00Z"), false},
		"no offset: host local time": {[]string{"429 Usage limit reached for 5 hour ... reset 2026-11-20 03:45:32"}, nowLocal, at("2026-11-20T08:45:32Z"), true},
		"no parsable time":           {[]string{"429 Usage limit reached for 5 hour ... try again later"}, now, nil, false},
		"impossible time":            {[]string{"429 Usage limit reached ... reset 2026-13-45 99:99:99 (UTC+8)"}, now, nil, false},
	} {
		t.Run(name, func(t *testing.T) {
			got := Classify(agent("claude"), fix(c.tail), c.now)
			if got.Status != ts.Locked || got.Reason != ts.RateLimited || got.Prompt != ts.PromptUnknown {
				t.Fatalf("got %s/%s/%q, want locked/unknown/rate_limited", got.Status, got.Prompt, got.Reason)
			}
			if (got.ResetAt == nil) != (c.resetAt == nil) || (got.ResetAt != nil && !got.ResetAt.Equal(*c.resetAt)) {
				t.Fatalf("resetAt %v, want %v", got.ResetAt, c.resetAt)
			}
			if got.ResetAt != nil && got.ResetAt.Location() != time.UTC {
				t.Fatalf("resetAt must be UTC, got %v", got.ResetAt.Location())
			}
			if got.ResetAssumedLocal != c.assumedLC {
				t.Fatalf("ResetAssumedLocal %v, want %v", got.ResetAssumedLocal, c.assumedLC)
			}
			if got.Detail != "" {
				t.Fatalf("a recognised state names no unmatched line: %q", got.Detail)
			}
		})
	}
}

// A stale limit line that has scrolled far above a live, idle composer does
// not hold the terminal: only the last few lines count.
func TestClassifyUsageLimitScrolledAwayIsNotLimited2676(t *testing.T) {
	tail := []string{glmLimit, "⏺ a", "⏺ b", "⏺ c", "⏺ d", "⏺ e", "⏺ f", "⏺ g", "⏺ h", "⏺ Done.", "✻ Cogitated for 1m 3s", rule, "❯ ", rule, claudeHint}
	got := Classify(agent("claude"), fix(tail), now)
	if got.Reason == ts.RateLimited || got.Status != ts.Idle {
		t.Fatalf("a scrolled-away limit line must not hold an idle agent: %s/%q", got.Status, got.Reason)
	}
}

// The recognition is a classification of what is shown, so an unwritable
// terminal and a dead agent keep their earlier answers.
func TestClassifyUsageLimitDoesNotOutrankDark2676(t *testing.T) {
	gone := agent("claude")
	gone.Connected = false
	if got := Classify(gone, fix([]string{glmLimit}), now); got.Status != ts.Dark || got.Reason != ts.NoAgent {
		t.Fatalf("got %s/%q", got.Status, got.Reason)
	}
}
