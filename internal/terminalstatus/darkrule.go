package terminalstatus

import (
	"fmt"
	"time"
)

// Ticker is one ticker the worker is actively subscribed to.
type Ticker struct {
	ID       string
	Name     string
	Interval time.Duration
}

// DueTick is the oldest unacknowledged tick of one ticker the server still
// offers the worker: its wake reason's tickDueAt (hadron-server#2790,
// hadron-cli#1216), per ticker even in a wake merged with a mention, a task or
// another ticker's tick. A turn answers and acknowledges its wake, so an old
// unanswered tick is the evidence that no turn happened. Neither
// DueWake.anchorAt (the oldest reason of any kind) nor the ticker's lastTickAt
// (its newest tick: with a 5-minute ticker and 5 minutes of slack never old
// enough, #1181) is an input.
type DueTick struct {
	TickerID   string
	TickerName string
	DueAt      time.Time
}

// IsDark is true when a ticker the worker subscribes to owed it a turn at
// least one FASTEST-subscribed-ticker interval ago and the tick is still
// unanswered. Only due-wake evidence counts, so the answer is the same for
// a harness whose screen yields no lastTurnAt. A worker with nothing due, or
// no subscription, is never dark. The detail names the oldest overdue tick.
func IsDark(now time.Time, subscribed []Ticker, due []DueTick) (bool, string) {
	if len(subscribed) == 0 {
		return false, ""
	}
	fastest := subscribed[0].Interval
	known := map[string]bool{}
	for _, t := range subscribed {
		known[t.ID] = true
		if t.Interval < fastest {
			fastest = t.Interval
		}
	}
	var oldest *DueTick
	for i := range due {
		d := &due[i]
		if !known[d.TickerID] || now.Sub(d.DueAt) < fastest {
			continue
		}
		if oldest == nil || d.DueAt.Before(oldest.DueAt) {
			oldest = d
		}
	}
	if oldest == nil {
		return false, ""
	}
	return true, fmt.Sprintf("ticker %s (%s) due %s", oldest.TickerName, oldest.TickerID, oldest.DueAt.UTC().Format(time.RFC3339))
}
