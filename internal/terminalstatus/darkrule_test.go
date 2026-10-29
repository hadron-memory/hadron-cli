package terminalstatus

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 29, 16, 30, 0, 0, time.UTC)

func min5() Ticker  { return Ticker{ID: "tk_5", Name: "base", Interval: 5 * time.Minute} }
func min30() Ticker { return Ticker{ID: "tk_30", Name: "slow", Interval: 30 * time.Minute} }

func TestDarkRule(t *testing.T) {
	for name, c := range map[string]struct {
		subs []Ticker
		due  []DueTick
		want bool
	}{
		"due a full interval ago with no turn is dark":  {[]Ticker{min5()}, []DueTick{{TickerID: "tk_5", DueAt: t0.Add(-5 * time.Minute)}}, true},
		"due less than an interval ago is not yet":      {[]Ticker{min5()}, []DueTick{{TickerID: "tk_5", DueAt: t0.Add(-4 * time.Minute)}}, false},
		"nothing due is never dark":                     {[]Ticker{min5()}, nil, false},
		"no subscription is never dark":                 {nil, []DueTick{{TickerID: "tk_5", DueAt: t0.Add(-time.Hour)}}, false},
		"the interval is the FASTEST subscribed":        {[]Ticker{min30(), min5()}, []DueTick{{TickerID: "tk_30", DueAt: t0.Add(-6 * time.Minute)}}, true},
		"a slow-only worker waits its own interval":     {[]Ticker{min30()}, []DueTick{{TickerID: "tk_30", DueAt: t0.Add(-6 * time.Minute)}}, false},
		"a due tick of an unsubscribed ticker is noise": {[]Ticker{min5()}, []DueTick{{TickerID: "tk_x", DueAt: t0.Add(-time.Hour)}}, false},
	} {
		t.Run(name, func(t *testing.T) {
			got, _ := Dark(t0, c.subs, c.due)
			if got != c.want {
				t.Fatalf("dark = %v, want %v", got, c.want)
			}
		})
	}
}

// A faster ticker subscribed later shortens the interval: the same due tick
// that was still within a 30-minute worker's slack is dark once a 5-minute
// ticker joins.
func TestDarkRuleFasterTickerSubscribedLaterShortensInterval(t *testing.T) {
	due := []DueTick{{TickerID: "tk_30", DueAt: t0.Add(-10 * time.Minute)}}
	if d, _ := Dark(t0, []Ticker{min30()}, due); d {
		t.Fatal("dark before the fast ticker joined")
	}
	if d, _ := Dark(t0, []Ticker{min30(), min5()}, due); !d {
		t.Fatal("not dark after the fast ticker joined")
	}
}

// Jonas's #1181 lesson: the input is the OLDEST UNACKNOWLEDGED due tick, not
// the ticker's lastTickAt. A ticker that ticked a minute ago (lastTickAt
// always within slack) still has an older tick nobody answered.
func TestDarkRuleUsesTheDueTickNotTheLatestTick(t *testing.T) {
	due := []DueTick{{TickerID: "tk_5", DueAt: t0.Add(-11 * time.Minute)}}
	d, detail := Dark(t0, []Ticker{min5()}, due)
	if !d {
		t.Fatal("a due tick 11m old on a 5m ticker must be dark")
	}
	if detail == "" {
		t.Fatal("dark carries a detail naming the ticker and due time")
	}
}

func TestDarkRuleNamesTheOldestOverdueTick(t *testing.T) {
	_, detail := Dark(t0, []Ticker{min5(), min30()}, []DueTick{
		{TickerID: "tk_5", TickerName: "base", DueAt: t0.Add(-7 * time.Minute)},
		{TickerID: "tk_30", TickerName: "slow", DueAt: t0.Add(-20 * time.Minute)},
	})
	want := "ticker slow (tk_30) due 2026-10-29T16:10:00Z"
	if detail != want {
		t.Fatalf("detail %q, want %q", detail, want)
	}
}
