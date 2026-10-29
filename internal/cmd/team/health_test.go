package team

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// cli#1181: the DARK rule, pure. Monday 2026-10-26; a 5-minute ticker
// anchored at 00:00Z ticks on every :00 and :05.

func ts(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		panic(err)
	}
	return t
}

func tp(s string) *time.Time { t := ts(s); return &t }

func health5m(quiet *quietHours, subscribed string, workers ...string) healthTicker {
	subs := map[string]time.Time{}
	for _, w := range workers {
		subs[w] = ts(subscribed)
	}
	return healthTicker{Slug: "tick", Anchor: ts("2026-10-26T00:00:00Z"), Cadence: 5 * time.Minute, Quiet: quiet, Subscribers: subs}
}

func verdictOf(t *testing.T, dto healthDTO) string {
	t.Helper()
	if len(dto.Workers) != 1 {
		t.Fatalf("workers: %+v", dto.Workers)
	}
	return dto.Workers[0].Verdict
}

func TestHealthSlackBoundaryIsExclusive(t *testing.T) {
	// The 11:50 tick is exactly 5m old at 11:55: not due yet, so the latest
	// due tick is 11:45 and activity at 11:46 is OK. One millisecond later
	// the 11:50 tick is due and the same activity is DARK.
	w := []healthWorker{{Party: taskPartyDTO{ID: "a"}, Role: "coordinator", LastActiveAt: tp("2026-10-26T11:46:00Z")}}
	tk := []healthTicker{health5m(nil, "2026-10-01T00:00:00Z", "a")}
	if v := verdictOf(t, evaluateHealth(ts("2026-10-26T11:55:00Z"), 5*time.Minute, false, w, tk)); v != healthOK {
		t.Fatalf("at exactly the slack: %s, want OK", v)
	}
	if v := verdictOf(t, evaluateHealth(ts("2026-10-26T11:55:00.001Z"), 5*time.Minute, false, w, tk)); v != healthDark {
		t.Fatalf("1ms past the slack: %s, want DARK", v)
	}
}

func TestHealthActivityAtTheTickIsAReaction(t *testing.T) {
	w := []healthWorker{{Party: taskPartyDTO{ID: "a"}, Role: "coordinator", LastActiveAt: tp("2026-10-26T11:50:00Z")}}
	tk := []healthTicker{health5m(nil, "2026-10-01T00:00:00Z", "a")}
	if v := verdictOf(t, evaluateHealth(ts("2026-10-26T11:57:00Z"), 5*time.Minute, false, w, tk)); v != healthOK {
		t.Fatalf("activity at the tick itself: %s, want OK", v)
	}
}

func TestHealthTickBeforeSubscriptionDoesNotCount(t *testing.T) {
	w := []healthWorker{{Party: taskPartyDTO{ID: "a"}, Role: "coordinator", LastActiveAt: tp("2026-10-26T09:00:00Z")}}
	tk := []healthTicker{health5m(nil, "2026-10-26T11:52:00Z", "a")}
	dto := evaluateHealth(ts("2026-10-26T11:58:00Z"), 5*time.Minute, false, w, tk)
	if v := verdictOf(t, dto); v != healthOK || dto.Workers[0].Reason != "no tick due yet" {
		t.Fatalf("subscribed at 11:52, latest due tick 11:50: %+v", dto.Workers[0])
	}
}

func TestHealthTickDuringEndedPauseDoesNotCount(t *testing.T) {
	w := []healthWorker{{Party: taskPartyDTO{ID: "a"}, Role: "coordinator", LastActiveAt: tp("2026-10-26T09:00:00Z"), PausedUntil: tp("2026-10-26T11:52:00Z")}}
	tk := []healthTicker{health5m(nil, "2026-10-01T00:00:00Z", "a")}
	if v := verdictOf(t, evaluateHealth(ts("2026-10-26T11:58:00Z"), 5*time.Minute, false, w, tk)); v != healthOK {
		t.Fatalf("a tick before the pause ended was never delivered: %s, want OK", v)
	}
}

func TestHealthLatestDueTickAcrossTickers(t *testing.T) {
	// A slow ticker's older tick must not hide the fast one's newer tick.
	slow := healthTicker{Slug: "slow", Anchor: ts("2026-10-26T00:00:00Z"), Cadence: time.Hour, Subscribers: map[string]time.Time{"a": ts("2026-10-01T00:00:00Z")}}
	w := []healthWorker{{Party: taskPartyDTO{ID: "a"}, Role: "coordinator", LastActiveAt: tp("2026-10-26T11:30:00Z")}}
	dto := evaluateHealth(ts("2026-10-26T12:00:00Z"), 5*time.Minute, false, w, []healthTicker{slow, health5m(nil, "2026-10-01T00:00:00Z", "a")})
	if verdictOf(t, dto) != healthDark || *dto.Workers[0].DueTicker != "tick" {
		t.Fatalf("%+v", dto.Workers[0])
	}
}

func TestHealthSkipPrecedesNeverDriven(t *testing.T) {
	for _, w := range []healthWorker{
		{Party: taskPartyDTO{ID: "a"}, PendingWhy: "STOPPED"},
		{Party: taskPartyDTO{ID: "a"}, PausedUntil: tp("2026-10-26T13:00:00Z")},
	} {
		dto := evaluateHealth(ts("2026-10-26T12:00:00Z"), 5*time.Minute, false, []healthWorker{w}, []healthTicker{health5m(nil, "2026-10-01T00:00:00Z", "a")})
		if verdictOf(t, dto) != healthSkipped || !dto.Healthy {
			t.Fatalf("%+v", dto)
		}
	}
	dto := evaluateHealth(ts("2026-10-26T12:00:00Z"), 5*time.Minute, true, []healthWorker{{Party: taskPartyDTO{ID: "a"}}}, nil)
	if verdictOf(t, dto) != healthSkipped || dto.Workers[0].Reason != "team paused" {
		t.Fatalf("team pause: %+v", dto.Workers[0])
	}
}

func TestHealthNoWorkersIsNotHealthy(t *testing.T) {
	if dto := evaluateHealth(ts("2026-10-26T12:00:00Z"), 5*time.Minute, false, nil, nil); dto.Healthy || dto.Workers == nil {
		t.Fatalf("%+v", dto)
	}
}

func quiet(t *testing.T, js string) *quietHours {
	t.Helper()
	raw := json.RawMessage(js)
	q := parseQuietHours(&raw)
	if q == nil {
		t.Fatalf("unparsed: %s", js)
	}
	return q
}

func TestQuietHoursOvernightAndWeekWrap(t *testing.T) {
	// Sunday 22:00 to Monday 07:00 in Berlin (CET, UTC+1 on 2026-10-26).
	q := quiet(t, `{"timezone":"Europe/Berlin","windows":[{"days":[7],"from":"22:00","to":"07:00"}]}`)
	for at, want := range map[string]bool{
		"2026-10-25T20:59:00Z": false, // Sun 21:59 local
		"2026-10-25T21:00:00Z": true,  // Sun 22:00, start included
		"2026-10-26T05:59:00Z": true,  // Mon 06:59, wrapped past the end of the week
		"2026-10-26T06:00:00Z": false, // Mon 07:00, end excluded
		"2026-10-27T05:00:00Z": false, // Tue 06:00: the window is Sunday's only
	} {
		if got := q.contains(ts(at)); got != want {
			t.Errorf("%s: %t, want %t", at, got, want)
		}
	}
}

func TestQuietHoursSkipToLastNonQuietTick(t *testing.T) {
	q := quiet(t, `{"timezone":"UTC","windows":[{"days":[1],"from":"10:00","to":"13:00"}]}`)
	tk := health5m(q, "2026-10-01T00:00:00Z")
	got, ok := tk.latestTickBefore(ts("2026-10-26T11:55:00Z"))
	if !ok || !got.Equal(ts("2026-10-26T09:55:00Z")) {
		t.Fatalf("latest non-quiet tick: %v %t", got, ok)
	}
}

func TestQuietHoursUnloadableZoneIsIgnored(t *testing.T) {
	raw := json.RawMessage(`{"timezone":"Nowhere/Atlantis","windows":[{"days":[1],"from":"10:00","to":"13:00"}]}`)
	if parseQuietHours(&raw) != nil {
		t.Fatal("an unloadable zone counts every tick (errs toward DARK)")
	}
}

func TestLatestTickNeverReturnsTheAnchor(t *testing.T) {
	tk := health5m(nil, "2026-10-01T00:00:00Z")
	if _, ok := tk.latestTickBefore(ts("2026-10-26T00:04:00Z")); ok {
		t.Fatal("the anchor is not a tick")
	}
	if got, ok := tk.latestTickBefore(ts("2026-10-26T00:05:00.001Z")); !ok || !got.Equal(ts("2026-10-26T00:05:00Z")) {
		t.Fatalf("first tick: %v %t", got, ok)
	}
}

func TestHealthTickerGrace(t *testing.T) {
	c := 10 * time.Minute
	for _, tc := range []struct {
		reasons []gen.TickerReason
		grace   time.Duration
		counted bool
	}{
		{[]gen.TickerReason{gen.TickerReasonEveryTick}, 0, true},
		{[]gen.TickerReason{gen.TickerReasonLiveCheck, gen.TickerReasonEveryTick}, 0, true},
		{[]gen.TickerReason{gen.TickerReasonReadTeamChannel, gen.TickerReasonLiveCheck}, c, true},
		{[]gen.TickerReason{gen.TickerReasonReadTeamChannel}, 0, false},
		{nil, 0, false},
	} {
		g, ok := healthTickerGrace(tc.reasons, c)
		if ok != tc.counted || (ok && g != tc.grace) {
			t.Errorf("%v: grace %v counted %t", tc.reasons, g, ok)
		}
	}
}
