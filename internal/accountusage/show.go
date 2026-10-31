package accountusage

import (
	"encoding/json"
	"math"
	"sort"
	"time"
)

// WindowView is one window's current state for `team usage show --json`.
// Null fields mean unknown, never 0.
type WindowView struct {
	UsedPct *float64 `json:"usedPct"`
	// ResetsAt is when the window resets (RFC 3339).
	ResetsAt *string `json:"resetsAt"`
	// Expired is true when ResetsAt has passed: the percentage describes a
	// window that is over, so it is reported but must not be read as current.
	Expired bool `json:"expired"`
	// BurnPctPerHour is the rise in used percentage per hour across this
	// window's recorded points (needs two at different times, same window).
	BurnPctPerHour *float64 `json:"burnPctPerHour"`
	// ExhaustsAt projects when the window reaches 100% at that burn rate.
	ExhaustsAt *string `json:"exhaustsAt"`
	// ExhaustsBeforeReset is true when ExhaustsAt falls before ResetsAt.
	ExhaustsBeforeReset *bool `json:"exhaustsBeforeReset"`
}

// AccountView is one account's current usage.
type AccountView struct {
	Label      string     `json:"label"`
	Family     string     `json:"family"`
	Host       string     `json:"host"`
	ObservedAt string     `json:"observedAt"`
	FiveHour   WindowView `json:"fiveHour"`
	SevenDay   WindowView `json:"sevenDay"`
	Workers    []string   `json:"workers"`
	// WorkersConfigured is false when the account's workers list was never
	// configured: Workers is then empty because it is unknown, not because
	// nobody ran on the account.
	WorkersConfigured bool `json:"workersConfigured"`
	Points            int  `json:"points"`
}

type point struct {
	rec Record
	at  time.Time
}

// View builds the per-account view from recorded objects (any order, any
// number of accounts). A record that does not parse as the documented shape is
// skipped, not guessed at. now decides which windows are expired.
func View(objects []json.RawMessage, now time.Time) []AccountView {
	byLabel := map[string][]point{}
	for _, raw := range objects {
		var r Record
		if json.Unmarshal(raw, &r) != nil || r.Label == "" {
			continue
		}
		at, err := time.Parse(time.RFC3339, r.ObservedAt)
		if err != nil {
			continue
		}
		byLabel[r.Label] = append(byLabel[r.Label], point{r, at})
	}
	labels := make([]string, 0, len(byLabel))
	for l := range byLabel {
		labels = append(labels, l)
	}
	sort.Strings(labels)
	out := make([]AccountView, 0, len(labels))
	for _, l := range labels {
		pts := byLabel[l]
		sort.SliceStable(pts, func(i, j int) bool { return pts[i].at.Before(pts[j].at) })
		last := pts[len(pts)-1].rec
		v := AccountView{
			Label: l, Family: last.Family, Host: last.Host, ObservedAt: last.ObservedAt,
			Workers: append([]string{}, last.Workers...), WorkersConfigured: last.WorkersConfigured, Points: len(pts),
		}
		v.FiveHour = windowView(pts, now, func(r Record) (*float64, *string) { return r.FiveHourPct, r.FiveHourResetsAt })
		v.SevenDay = windowView(pts, now, func(r Record) (*float64, *string) { return r.SevenDayPct, r.SevenDayResetsAt })
		out = append(out, v)
	}
	return out
}

func windowView(pts []point, now time.Time, pick func(Record) (*float64, *string)) WindowView {
	var v WindowView
	// Current = the newest point that reports this window at all.
	cur := -1
	for i := len(pts) - 1; i >= 0; i-- {
		if p, r := pick(pts[i].rec); p != nil && r != nil {
			cur = i
			break
		}
	}
	if cur < 0 {
		return v
	}
	pct, resets := pick(pts[cur].rec)
	v.UsedPct, v.ResetsAt = copyF(*pct), copyS(*resets)
	resetsAt, err := time.Parse(time.RFC3339, *resets)
	if err != nil {
		return v
	}
	v.Expired = !resetsAt.After(now)
	// Burn rate: the earliest point of the SAME window (same resetsAt)
	// against the current one. A reset starts a new window, so points from
	// before it never mix into a rate.
	first := cur
	for i := cur - 1; i >= 0; i-- {
		p, r := pick(pts[i].rec)
		if p == nil || r == nil || *r != *resets {
			break
		}
		first = i
	}
	dt := pts[cur].at.Sub(pts[first].at).Hours()
	if first == cur || dt <= 0 {
		return v
	}
	p0, _ := pick(pts[first].rec)
	rate := (*pct - *p0) / dt
	rate = math.Round(rate*100) / 100
	v.BurnPctPerHour = &rate
	if rate > 0 && !v.Expired {
		hours := (100 - *pct) / rate
		ex := pts[cur].at.Add(time.Duration(hours * float64(time.Hour))).UTC()
		s := rfc(ex)
		before := ex.Before(resetsAt)
		v.ExhaustsAt, v.ExhaustsBeforeReset = &s, &before
	}
	return v
}

func copyF(f float64) *float64 { return &f }
func copyS(s string) *string   { return &s }
