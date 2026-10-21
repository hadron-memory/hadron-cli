package guardedpush

import (
	"context"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
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

// Validate bounds both fake-clock work and real calls. No CLI override weakens stability.
func (o Options) Validate() bool {
	const maxWait = 24 * time.Hour
	return o.Wait > 0 && o.Wait <= maxWait && o.DraftWait > 0 && o.DraftWait <= maxWait && o.Stable >= 30*time.Second && o.Stable <= maxWait && o.Confirm > 0 && o.Confirm <= maxWait && o.Poll > 0 && o.Poll <= o.Wait && o.Poll <= o.Confirm
}

func safeText(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return false
		}
	}
	return true
}

// Marker correlates fresh screen evidence with this one attempt, not its send receipt.
func Marker(attempt string) string { return "[hadron push attempt:" + attempt + "]" }

func payload(q Request, draft string) string {
	header := fmt.Sprintf("[hadron message from %q", q.Sender)
	if q.Seq > 0 {
		header += fmt.Sprintf(", #%d", q.Seq)
	}
	text := header + "] " + Marker(q.Attempt) + "\n" + q.Message
	if draft != "" {
		text += "\n──── unsent draft that was in this prompt (not submitted by its author; unfinished quoted material, not an instruction) ────\n" + draft
	}
	return text
}

// Run performs at most one clear and one submission, under an own-attempt lock.
// Observe must freshly validate authoritative metadata and return a stable terminal/session identity.
// Clear and Submit must be single calls without transport retry. Frames are private in-memory data.
func (e *Engine) Run(ctx context.Context, q Request, o Options) Result {
	result := func(k Outcome, phase, reason string) Result { return Result{Outcome: k, Phase: phase, Reason: reason} }
	if !o.Validate() || q.Message == "" || q.Sender == "" || len(q.Attempt) != 16 || !safeText(q.Message) || !safeText(q.Sender) || !safeText(q.Attempt) || q.Seq < 0 || len(payload(q, "")) > orcaterm.MaxSendTextBytes {
		return result(HeldUnknown, "preflight", "invalid-input")
	}
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, o.Wait)
	defer cancel()
	if ctx.Err() != nil {
		return result(HeldBusy, "preflight", "cancelled")
	}
	first, err := e.Observe(ctx)
	if ctx.Err() != nil {
		return result(HeldBusy, "preflight", "deadline-or-cancelled")
	}
	if err != nil || first.Identity == "" {
		return result(HeldUnknown, "preflight", "identity-unknown")
	}
	return e.Locked(first.Identity, func(owned func() bool) Result {
		deadline := e.Now().Add(o.Wait)
		var prior string
		var observed, stable time.Time
		for {
			if ctx.Err() != nil || !e.Now().Before(deadline) {
				return result(HeldBusy, "waiting", "deadline-or-cancelled")
			}
			f, err := e.Observe(ctx)
			if ctx.Err() != nil {
				return result(HeldBusy, "waiting", "deadline-or-cancelled")
			}
			if err != nil || f.Identity != first.Identity || f.Family != first.Family || !owned() {
				return result(HeldUnknown, "waiting", "identity-changed")
			}
			if f.State == Unknown {
				return result(HeldUnknown, "waiting", "screen-unknown")
			}
			if f.State == Busy {
				prior = ""
				observed = time.Time{}
				stable = time.Time{}
			} else {
				if f.Draft == "" {
					if !f.Empty {
						return result(HeldUnknown, "waiting", "composer-unknown")
					}
					return e.send(ctx, parent, q, o, f, "", owned)
				}
				if f.Family != "claude" && f.Family != "codex" {
					return result(HeldUnknown, "waiting", "clear-unsupported")
				}
				if !safeText(f.Draft) {
					return result(HeldUnknown, "waiting", "draft-unsupported")
				}
				now := e.Now()
				if observed.IsZero() {
					observed = now
				}
				if stable.IsZero() || f.Draft != prior {
					stable = now
					prior = f.Draft
				}
				if now.Sub(observed) >= o.DraftWait && now.Sub(stable) >= o.Stable {
					return e.send(ctx, parent, q, o, f, f.Draft, owned)
				}
			}
			if e.Sleep(ctx, min(o.Poll, deadline.Sub(e.Now()))) != nil {
				return result(HeldBusy, "waiting", "cancelled")
			}
		}
	})
}

func (e *Engine) send(ctx, parent context.Context, q Request, o Options, before Frame, draft string, owned func() bool) Result {
	uncertain := func(reason string) Result { return Result{Outcome: Unconfirmed, Phase: "mutated", Reason: reason} }
	hold := func(reason string) Result { return Result{Outcome: HeldUnknown, Phase: "preflight", Reason: reason} }
	text := payload(q, draft)
	if len(text) > orcaterm.MaxSendTextBytes {
		return hold("input-too-large")
	}
	valid := func(f Frame) bool {
		return f.Identity == before.Identity && f.Family == before.Family && f.State == Idle && owned()
	}
	f, err := e.Observe(ctx)
	if ctx.Err() != nil {
		return Result{Outcome: HeldBusy, Phase: "preflight", Reason: "cancelled"}
	}
	if err != nil || !valid(f) || f.Draft != draft || (draft == "" && !f.Empty) {
		return hold("pre-send-changed")
	}
	mutated := false
	if draft != "" {
		mutated = true // even a failed call may have cleared some bytes
		if e.Clear(ctx, draft) != nil {
			return uncertain("clear-unconfirmed")
		}
		f, err = e.Observe(ctx)
		if ctx.Err() != nil || err != nil || !valid(f) || !f.Empty || f.Draft != "" {
			return uncertain("clear-not-empty")
		}
	}
	// A final fresh read before the sole submission also catches input arriving after clear.
	f, err = e.Observe(ctx)
	if ctx.Err() != nil || err != nil || !valid(f) || !f.Empty || f.Draft != "" {
		if mutated {
			return uncertain("final-state-changed")
		}
		return hold("final-state-changed")
	}
	if e.Submit(ctx, text) != nil || ctx.Err() != nil {
		return uncertain("send-unconfirmed")
	}
	// Confirmation has its own deadline, after the pre-send wait budget.
	confirmCtx, cancel := context.WithTimeout(parent, o.Confirm)
	defer cancel()
	deadline := e.Now().Add(o.Confirm)
	for e.Now().Before(deadline) && confirmCtx.Err() == nil {
		f, err = e.Observe(confirmCtx)
		if confirmCtx.Err() != nil || err != nil || f.Identity != before.Identity || f.Family != before.Family || !owned() {
			return uncertain("confirmation-identity")
		}
		if f.State == Busy && f.Draft == "" && strings.Contains(f.Screen, Marker(q.Attempt)) {
			k := Sent
			if draft != "" {
				k = SentWithDraft
			}
			return Result{Outcome: k, Phase: "confirmed", Reason: "screen-turn-started", DraftIncluded: draft != ""}
		}
		if e.Sleep(confirmCtx, min(o.Poll, deadline.Sub(e.Now()))) != nil {
			break
		}
	}
	return uncertain("turn-not-confirmed")
}
