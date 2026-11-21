// Package terminalstatus is the `terminal-status@1` output contract of
// `hadron orca terminal status` (cli#1187) and the dark rule behind it.
//
// Nothing here knows about Orca: the team health sweep (#1181) consumes the
// JSON this package defines and never imports a classifier, and a later
// terminal environment would ship its own command emitting the same
// contract. The closed lists below match the server's Worker.terminal reason
// enum (hadron-server#2771).
package terminalstatus

import (
	"encoding/json"
	"time"
)

// Contract names and versions the JSON this package marshals.
const Contract = "terminal-status@1"

// Status is exactly one of busy | idle | locked | dark.
type Status string

const (
	// Idle: an empty prompt, safe to push one line.
	Idle Status = "idle"
	// Busy: a turn is running.
	Busy Status = "busy"
	// Locked: the agent cannot take work for an identifiable (or, fail
	// closed, unrecognized) reason.
	Locked Status = "locked"
	// Dark: a broken state, a ticker owed this worker a turn and none came,
	// or there is no live agent terminal at all.
	Dark Status = "dark"
)

// Valid reports whether s is on the closed list.
func (s Status) Valid() bool { return s == Idle || s == Busy || s == Locked || s == Dark }

// Prompt is the terminal-level fact, separate from Status.
type Prompt string

const (
	PromptEmpty    Prompt = "empty"
	PromptNotEmpty Prompt = "not_empty"
	PromptMenu     Prompt = "menu"
	PromptUnknown  Prompt = "unknown"
)

// Valid reports whether p is on the closed list.
func (p Prompt) Valid() bool {
	return p == PromptEmpty || p == PromptNotEmpty || p == PromptMenu || p == PromptUnknown
}

// Reason is a stable machine code, set only for Locked and Dark.
type Reason string

const (
	Unrecognized       Reason = "unrecognized"
	MenuOrQuestion     Reason = "menu_or_question"
	NoTurnForDueTicker Reason = "no_turn_for_due_ticker"
	NoTerminal         Reason = "no_terminal"
	BareShell          Reason = "bare_shell"
	NoAgent            Reason = "no_agent"
	// RateLimited: the harness shows a usage-limit screen (cli#2676). Locked:
	// the agent is waiting on a reset, not stopped; Result.ResetAt carries it.
	RateLimited Reason = "rate_limited"
	// NoScreen: Orca gave no rendered screen for the terminal (a read whose
	// source is screen-unavailable), so nothing could be matched. Locked, as
	// unrecognized is (cli#2677; hadron-server#4558).
	NoScreen Reason = "no_screen"
	// MultipleTerminals: more than one agent terminal is bound to the worker, so
	// no single terminal can be judged. Locked (cli#2677; hadron-server#4558).
	MultipleTerminals Reason = "multiple_terminals"
)

// Deferred reasons (token_budget_exhausted, guardrail_triggered,
// model_at_capacity, shed_staged_not_relaunched) have no captured screen yet:
// until one exists such a screen answers Locked/Unrecognized, never a guess.

// Valid reports whether r is on the closed list.
func (r Reason) Valid() bool {
	switch r {
	case Unrecognized, MenuOrQuestion, NoTurnForDueTicker, NoTerminal, BareShell, NoAgent, RateLimited, NoScreen, MultipleTerminals:
		return true
	}
	return false
}

// Result is one worker's terminal status, the JSON element of the contract.
// Detail is local only: it is never marshalled, so a sweep can never send
// screen text anywhere.
type Result struct {
	Worker     string
	Terminal   string
	Status     Status
	Prompt     Prompt
	Reason     *Reason
	LastTurnAt *time.Time
	CheckedAt  time.Time
	Detail     string
	// ResetAt is when a rate_limited screen says the limit resets (cli#2676):
	// marshalled as "resetAt", RFC 3339 UTC, null when not known. An additive
	// key, so the contract stays @1.
	ResetAt *time.Time
	// ResetAssumedLocal: the screen's reset time carried no UTC offset, so
	// ResetAt reads it as the host's local time. Text output says so; never
	// marshalled.
	ResetAssumedLocal bool
}

// MarshalJSON writes the documented shape: every key present, nulls for the
// absent ones, no detail.
func (r Result) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Contract   string     `json:"contract"`
		Worker     *string    `json:"worker"`
		Terminal   *string    `json:"terminal"`
		Status     Status     `json:"status"`
		Prompt     Prompt     `json:"prompt"`
		Reason     *Reason    `json:"reason"`
		LastTurnAt *time.Time `json:"lastTurnAt"`
		CheckedAt  time.Time  `json:"checkedAt"`
		ResetAt    *time.Time `json:"resetAt"`
	}{Contract, nonEmpty(r.Worker), nonEmpty(r.Terminal), r.Status, r.Prompt, r.Reason, utc(r.LastTurnAt), r.CheckedAt.UTC().Truncate(time.Second), utc(r.ResetAt)})
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}
