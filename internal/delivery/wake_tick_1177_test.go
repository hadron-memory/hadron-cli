package delivery

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

// cli#1177 (T-C1): the server writes the tick line (DueWake.tickText) and the
// host types it unchanged after its own prefix, ahead of the TASK parts
// (cor:agt:080:26: no reader rebuilds, wraps or re-renders it).

const serverTick = "tick a-tick: run hrn:node:x:y:tasks:on-base-tick"

func TestRenderWakeTypesTheServersTickTextFirst(t *testing.T) {
	got := RenderWakeWith([]WakeReason{task("ev9", 0, "Fix"), tick("tk1", tickAt)}, serverTick)
	want := "[hadron tick] " + serverTick + "; [hadron task] assigned: Fix (task-ev9)"
	if got != want {
		t.Fatalf("\n got %q\nwant %q", got, want)
	}
	if strings.Contains(got, "scheduled check-in") {
		t.Fatal("the generic advice must not be appended to the server's line")
	}
}

func TestRenderWakeEmptyTickTextIsTodaysLine(t *testing.T) {
	reasons := []WakeReason{tick("tk1", tickAt), task("ev9", 0, "Fix")}
	if got, want := RenderWakeWith(reasons, ""), RenderWake(reasons); got != want {
		t.Fatalf("\n got %q\nwant %q", got, want)
	}
	if got := RenderWakeWith([]WakeReason{tick("tk1", tickAt)}, ""); got != tickLine {
		t.Fatalf("%q", got)
	}
	// Tick text with no TICK reason is not typed: it belongs to a tick.
	if got := RenderWakeWith([]WakeReason{task("ev9", 0, "Fix")}, serverTick); strings.Contains(got, "a-tick") {
		t.Fatalf("tick text typed without a TICK reason: %q", got)
	}
}

func TestRenderWakeNeverComposesALineFromAction(t *testing.T) {
	r := tick("tk1", tickAt)
	r.Action = &TickAction{Code: "tick.base", State: "MAPPED", TaskLoc: "tasks:x", TaskURN: "hrn:node:x:y:tasks:x"}
	got := RenderWakeWith([]WakeReason{r}, "")
	if got != tickLine || strings.Contains(got, "hrn:node:x:y:tasks:x") {
		t.Fatalf("composed from the action: %q", got)
	}
}

func TestRenderWakeTickTextIsOneLineAndNeverCut(t *testing.T) {
	got := RenderWakeWith([]WakeReason{tick("tk1", tickAt)}, "tick a\nb\r\x1b[31m c")
	if strings.ContainsAny(got, "\r\n\x1b") {
		t.Fatalf("control characters typed: %q", got)
	}
	var tasks []WakeReason
	for i := range 30 {
		tasks = append(tasks, task("ev"+string(rune('a'+i)), 0, strings.Repeat("t", 60)))
	}
	long := strings.Repeat("l", 400)
	cut := RenderWakeWith(append([]WakeReason{tick("tk1", tickAt)}, tasks...), long)
	if !strings.HasPrefix(cut, "[hadron tick] "+long) || utf8.RuneCountInString(cut) > MaxWakeRunes {
		t.Fatalf("tick text cut or line too long (%d): %q", utf8.RuneCountInString(cut), cut[:60])
	}
	if !strings.Contains(cut, "more reasons)") {
		t.Fatal("TASK parts are cut first and say how many")
	}
	huge := strings.Repeat("h", MaxWakeRunes*2)
	whole := RenderWakeWith(append([]WakeReason{tick("tk1", tickAt)}, tasks...), huge)
	if !strings.HasPrefix(whole, "[hadron tick] "+huge) {
		t.Fatal("a tick text longer than MaxWakeRunes is typed whole")
	}
}

func TestWakeIdentityIgnoresActionAndTickText(t *testing.T) {
	a, b := tick("tk1", tickAt), tick("tk1", tickAt)
	b.Action = &TickAction{Code: "tick.base", State: "WITHHELD"}
	if a.Key() != b.Key() {
		t.Fatal("Key includes Action")
	}
	h := newHarness(t)
	w := h.wake(tick("tk1", tickAt))
	w.TickText = "tick SECRETTEXT: run it"
	h.deliverWake(w)
	if len(h.orca.sends) != 1 || h.orca.sends[0].text != "[hadron tick] tick SECRETTEXT: run it" {
		t.Fatalf("sends %+v", h.orca.sends)
	}
	w2 := h.wake(tick("tk1", tickAt))
	w2.TickText = "tick a different text"
	h.deliverWake(w2)
	if len(h.orca.sends) != 1 {
		t.Fatal("a re-read whose TickText differs must not be retyped")
	}
	if w.Span() != "wake:tick=1,task=0,mention=0" {
		t.Fatalf("span %q", w.Span())
	}
}

func TestTickTextReachesNoRecordOrEvent(t *testing.T) {
	h := newHarness(t)
	w := h.wake(tick("tk1", tickAt))
	w.TickText = "tick SECRETTEXT: run it"
	h.deliverWake(w)
	for _, e := range h.events {
		if strings.Contains(strings.ToLower(strings.TrimSpace(fmt.Sprintf("%+v", e))), "secrettext") {
			t.Fatalf("an event carries the tick text: %s", fmt.Sprintf("%+v", e))
		}
	}
	for k := range h.wakeRecords() {
		if strings.Contains(strings.ToLower(k), "secrettext") {
			t.Fatalf("a record carries the tick text: %s", k)
		}
	}
}

func TestPartialReTypeTypesTheWholeTickTextOnce(t *testing.T) {
	h := newHarness(t)
	first := h.wake(tick("tk1", tickAt))
	first.NamesChannels = true
	first.TickText = "tick one"
	h.deliverWake(first) // typed and held: tk1 is recorded
	both := h.wake(tick("tk1", tickAt), tick("tk2", tickAt))
	both.TickText = "tick one · tick two"
	h.deliverWake(both)
	if len(h.orca.sends) != 2 || h.orca.sends[1].text != "[hadron tick] tick one · tick two" {
		t.Fatalf("sends %+v", h.orca.sends)
	}
	h.wantWakeRecord(tick("tk2", tickAt).Key(), WakeAcked)
}
