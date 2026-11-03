package team

import (
	"context"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/delivery"
)

// hadron-cli#1534: the host poller's DueWakes read selects DueWake.tickText and
// WakeReason.action and maps them verbatim onto delivery.Wake.TickText and
// WakeReason.Action (cor:agt:080:23 / :26). The adapter never trims, repairs,
// cuts or resolves anything: the typed line is the server's, whole, or the
// existing composed fallback.

func oneTickWake(tickText, action string) string {
	return `{"data":{"dueWakes":{"status":"OK","withheld":0,"dormant":[],
 "wakes":[{"proof":"WAKE-PROOF-1-SECRET","worker":{"id":"wkr1"},` + tickText + `
  "deliverAfter":"2026-10-19T11:58:00Z","busyDeadline":"2026-10-19T12:08:00Z","channels":[],
  "reasons":[{"kind":"TICK","ticker":{"id":"tk1","name":"Base tick","lastTickAt":"2026-10-19T11:55:00Z"},"task":null` + action + `}]}],
 "pageInfo":{"hasNextPage":false,"endCursor":null}}}}`
}

func firstWake(t *testing.T, body string) delivery.Wake {
	t.Helper()
	s, _ := deliveryFake(t, map[string][]string{"DueWakes": {body}})
	page, err := s.Wakes(context.Background())
	if err != nil || len(page.Wakes) != 1 {
		t.Fatalf("page %+v err %v", page, err)
	}
	return page.Wakes[0]
}

func TestWakesMapTickTextVerbatim(t *testing.T) {
	// Leading/trailing space and inner spacing are the server's: no trim.
	const line = "  [hadron tick] Base tick: read the team chat.  "
	w := firstWake(t, oneTickWake(`"tickText":"`+line+`",`, ""))
	if w.TickText != line {
		t.Fatalf("TickText = %q, want it verbatim %q", w.TickText, line)
	}
}

func TestWakesMapActionVerbatimWithWithheldDistinct(t *testing.T) {
	for state, want := range map[string]delivery.TickAction{
		"MAPPED":     {Code: "cor:agt:110:01", State: "MAPPED", TaskLoc: "tasks:base-tick", TaskURN: "hrn:node:acme.com:team:tasks:base-tick"},
		"UNMAPPED":   {Code: "cor:agt:110:01", State: "UNMAPPED"},
		"UNRESOLVED": {Code: "cor:agt:110:01", State: "UNRESOLVED", TaskLoc: "tasks:gone"},
		"WITHHELD":   {Code: "cor:agt:110:01", State: "WITHHELD"},
	} {
		t.Run(state, func(t *testing.T) {
			act := `,"action":{"code":"` + want.Code + `","state":"` + state + `"`
			if want.TaskLoc != "" {
				act += `,"taskLoc":"` + want.TaskLoc + `"`
			}
			if want.TaskURN != "" {
				act += `,"taskUrn":"` + want.TaskURN + `"`
			}
			act += `}`
			w := firstWake(t, oneTickWake("", act))
			got := w.Reasons[0].Action
			if got == nil || *got != want {
				t.Fatalf("Action = %+v, want %+v", got, want)
			}
		})
	}
}

// An omitted or null VALUE (on a schema that has the fields) is the existing
// composed fallback: no text, no action, nothing invented.
func TestWakesNullOrOmittedTickTextAndActionFallBack(t *testing.T) {
	for name, body := range map[string]string{
		"omitted": oneTickWake("", ""),
		"null":    oneTickWake(`"tickText":null,`, `,"action":null`),
		"empty":   oneTickWake(`"tickText":"",`, ""),
	} {
		t.Run(name, func(t *testing.T) {
			w := firstWake(t, body)
			if w.TickText != "" || w.Reasons[0].Action != nil {
				t.Fatalf("TickText %q Action %+v, want none", w.TickText, w.Reasons[0].Action)
			}
			composed := delivery.RenderWake(w.Reasons)
			if got := delivery.RenderWakeWith(w.Reasons, w.TickText); got != composed {
				t.Fatalf("fallback line %q, want the composed %q", got, composed)
			}
		})
	}
}

// The adapter hands a control-character line through untouched; the existing
// renderer drops it WHOLE and types the composed notice (cor:agt:080:26).
func TestWakesControlCharacterLineIsDroppedWholeNotRepaired(t *testing.T) {
	w := firstWake(t, oneTickWake(`"tickText":"[hadron tick] a\u001b[31mred\nb",`, ""))
	if !strings.ContainsRune(w.TickText, '\x1b') || !strings.Contains(w.TickText, "\n") {
		t.Fatalf("adapter must not repair: %q", w.TickText)
	}
	got := delivery.RenderWakeWith(w.Reasons, w.TickText)
	if got != delivery.RenderWake(w.Reasons) || strings.ContainsFunc(got, func(r rune) bool { return r < ' ' }) {
		t.Fatalf("line = %q, want the composed fallback, no control characters", got)
	}
}

// A server whose SDL lacks the fields refuses the query at validation. That is
// a refusal like any other: one call, an error, nothing typed, no retry
// without the selection (fail closed).
func TestWakesSchemaValidationRefusalFailsClosed(t *testing.T) {
	refusal := `{"errors":[{"message":"Cannot query field \"tickText\" on type \"DueWake\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`
	s, calls := deliveryFake(t, map[string][]string{"DueWakes": {refusal}})
	page, err := s.Wakes(context.Background())
	if err == nil || len(page.Wakes) != 0 {
		t.Fatalf("page %+v err %v, want a refusal and nothing typed", page, err)
	}
	if cs := calls(); len(cs) != 1 {
		t.Fatalf("calls = %d, want exactly one (no silent retry)", len(cs))
	}
}
