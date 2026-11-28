package orcaterm

import (
	"testing"
	"time"

	ts "github.com/hadron-memory/hadron-cli/internal/terminalstatus"
)

// hadron-cli#3090. NOT CAPTURED SCREENS: the reporter kept no capture of the
// z.ai screen the two change managers showed (#146276); the line below is the
// reporter's quote of it, so these pin the PARSER on that quoted line and the
// guard, and nothing here claims to be the real screen. The red-first pins on a
// captured screen (items 1-4) wait for one: capture the next live usage-limit
// screen with `orca terminal read` and add it under testdata.
const quotedZai1308 = "API Error 429 [1308] usage limit, resets 2026-11-23 18:58"

func TestUsageLimitQuotedLineParses3090(t *testing.T) {
	loc := time.FixedZone("host", 0)
	before := time.Date(2026, 11, 23, 10, 0, 0, 0, loc)
	after := time.Date(2026, 11, 24, 5, 36, 0, 0, loc)
	for name, c := range map[string]struct {
		line    string
		now     time.Time
		limited bool
		reset   string
	}{
		"quoted z.ai line, reset ahead":    {quotedZai1308, before, true, "2026-11-23T18:58:00Z"},
		"quoted z.ai line, reset past":     {quotedZai1308, after, false, ""},
		"parenthesised 429, will reset at": {"API Error: Request rejected (429) Usage limit reached, will reset at 2026-11-23 18:58", before, true, "2026-11-23T18:58:00Z"},
		"429 and phrase, no time":          {"API Error 429 usage limit, try later", before, true, ""},
	} {
		t.Run(name, func(t *testing.T) {
			limited, at, _ := usageLimit([]string{c.line}, c.now)
			if limited != c.limited {
				t.Fatalf("limited = %v, want %v", limited, c.limited)
			}
			if (at == nil) != (c.reset == "") || (at != nil && at.Format(time.RFC3339) != c.reset) {
				t.Fatalf("resetAt = %v, want %q", at, c.reset)
			}
		})
	}
}

// Item 5: a transcript that merely mentions the phrase holds no agent, with or
// without a reset-looking date, and a banner that has scrolled out of the last
// lines above a live composer does not hold one either.
func TestUsageLimitMentionDoesNotHold3090(t *testing.T) {
	now := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	for name, tail := range map[string][]string{
		"mention only":                {"we discussed the usage limit earlier"},
		"mention with a reset date":   {"the usage limit resets 2026-12-01 00:00 each month"},
		"429 without the phrase":      {"HTTP 429 from the proxy, retrying"},
		"banner scrolled out of view": append([]string{quotedZai1308}, append(make([]string, 0), "a", "b", "c", "d", "e", "f", "g", "h")...),
	} {
		t.Run(name, func(t *testing.T) {
			if limited, _, _ := usageLimit(tail, now); limited {
				t.Fatalf("held an agent on %q", tail)
			}
		})
	}
	// And through Classify: a live composer under a mention is not rate_limited.
	got := Classify(agent("claude"), fix([]string{"⏺ the usage limit was mentioned", rule, "❯ ", rule, claudeHint}), now)
	if got.Reason == ts.RateLimited {
		t.Fatalf("a mention classified as rate_limited: %+v", got)
	}
}
