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

// A transcript that merely says the phrase (an agent working on rate-limit
// code) is not the banner and must not hold an idle agent.
func TestClassifyUsageLimitProseMentionIsNotLimited2676(t *testing.T) {
	tail := []string{"⏺ The API said usage limit reached, so I added a retry.", "✻ Cogitated for 1m 3s", rule, "❯ ", rule, claudeHint}
	got := Classify(agent("claude"), fix(tail), now)
	if got.Reason == ts.RateLimited || got.Status != ts.Idle {
		t.Fatalf("a prose mention must not lock: %s/%q", got.Status, got.Reason)
	}
}

// The window is the last eight non-blank lines: the banner at line eight
// counts, at line nine it does not. A running composer under the banner still
// reads as limited.
func TestClassifyUsageLimitWindowBoundary2676(t *testing.T) {
	pad := func(n int) []string {
		out := []string{glmLimit}
		for i := 0; i < n; i++ {
			out = append(out, "⏺ line")
		}
		return out
	}
	if got := Classify(agent("claude"), fix(pad(7)), now); got.Reason != ts.RateLimited {
		t.Fatalf("banner 8th from the end must count: %s/%q", got.Status, got.Reason)
	}
	if got := Classify(agent("claude"), fix(pad(8)), now); got.Reason == ts.RateLimited {
		t.Fatal("banner 9th from the end must not count")
	}
}

// Offset edges: +14:00 is accepted, +15:00 is refused as an unparsable time;
// a lower-case (utc) reads as UTC, not as host-local time.
func TestClassifyUsageLimitOffsetEdges2676(t *testing.T) {
	line := func(z string) []string {
		return []string{"429 Usage limit reached for 5 hour ... reset 2026-11-20 16:45:32 " + z}
	}
	if got := Classify(agent("claude"), fix(line("(UTC+14)")), now); got.ResetAt == nil {
		t.Fatal("+14 must parse")
	}
	if got := Classify(agent("claude"), fix(line("(UTC+15)")), now); got.Reason != ts.RateLimited || got.ResetAt != nil {
		t.Fatalf("+15 must be limited with no resetAt, got %q %v", got.Reason, got.ResetAt)
	}
	got := Classify(agent("claude"), fix(line("(utc)")), now)
	if got.ResetAt == nil || got.ResetAssumedLocal {
		t.Fatalf("(utc) must read as UTC, not assumed local: %v %v", got.ResetAt, got.ResetAssumedLocal)
	}
}

// A variant without the 429 prefix still reads when it carries the reset
// clause; a bare phrase without either does not.
func TestClassifyUsageLimitResetClauseAnchorsWithout429Prefix2676(t *testing.T) {
	v := []string{"Usage limit reached for 5 hour ... reset 2026-11-20 16:45:32 (UTC+8)"}
	if got := Classify(agent("claude"), fix(v), now); got.Reason != ts.RateLimited || got.ResetAt == nil {
		t.Fatalf("a banner with a reset clause but no 429 must read: %s/%q", got.Status, got.Reason)
	}
}

// An unexpired banner outranks a drawn, empty composer; once the reset time
// has passed the stale banner no longer holds the agent (its turn runs).
func TestClassifyUsageLimitExpiryAndComposer2676(t *testing.T) {
	tail := []string{"⏺ Working", glmLimit, rule, "❯ ", rule, claudeHint}
	before := time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)
	after := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	if got := Classify(agent("claude"), fix(tail), before); got.Reason != ts.RateLimited {
		t.Fatalf("before the reset the banner holds: %s/%q", got.Status, got.Reason)
	}
	if got := Classify(agent("claude"), fix(tail), after); got.Reason == ts.RateLimited {
		t.Fatalf("after the reset a stale banner must not hold: %s/%q", got.Status, got.Reason)
	}
}
