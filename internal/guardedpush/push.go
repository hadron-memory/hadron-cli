package guardedpush

import (
	"context"
	"time"
)

type State int

const (
	Unknown State = iota
	Idle
	Busy
)

type Outcome string

const (
	Sent          Outcome = "sent"
	SentWithDraft Outcome = "sent-with-draft"
	HeldBusy      Outcome = "held-busy"
	HeldUnknown   Outcome = "held-unknown"
	Unconfirmed   Outcome = "unconfirmed"
)

type Frame struct {
	Identity, Family, Draft, Screen string
	State                           State
	Empty                           bool
}
type Request struct {
	Sender, Message, Attempt string
	Seq                      int
}
type Options struct{ Wait, DraftWait, Stable, Confirm, Poll time.Duration }

func Defaults() Options {
	return Options{120 * time.Second, 30 * time.Second, 30 * time.Second, 10 * time.Second, 250 * time.Millisecond}
}

type Result struct {
	Outcome       Outcome `json:"outcome"`
	Phase         string  `json:"phase"`
	Reason        string  `json:"reason"`
	DraftIncluded bool    `json:"draftIncluded"`
}
type Engine struct {
	Now     func() time.Time
	Sleep   func(context.Context, time.Duration) error
	Observe func(context.Context) (Frame, error)
	Clear   func(context.Context, string) error
	Submit  func(context.Context, string) error
	Locked  func(string, func(func() bool) Result) Result
}

func (e *Engine) Run(context.Context, Request, Options) Result { return Result{Outcome: HeldUnknown} }
