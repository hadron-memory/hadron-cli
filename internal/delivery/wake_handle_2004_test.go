package delivery

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

const hexID = "01a1d1ed541b79128c0b155f466fc53a"

func handled(event, handle, id, title string) WakeReason {
	return WakeReason{Kind: WakeTask, Task: &TaskReason{EventID: "ev-" + handle, Event: event, ID: id, Handle: handle, Title: title}}
}

// cli#2004: the TASK line leads with the handle, then the first 12 hex.
func TestTaskLinePrintsHandleThenShortID(t *testing.T) {
	got := RenderWake([]WakeReason{handled("assigned", "AT0.6984.1", hexID, "Fix it")})
	if want := "[hadron task] assigned: Fix it (AT0.6984.1, 01a1d1ed541b)"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	// A child-finished reason names the child's handle (the reason's id is the child's).
	got = RenderWake([]WakeReason{handled("child finished", "AT0.6984.1.2", hexID, "Child")})
	if want := "[hadron task] child finished: Child (AT0.6984.1.2, 01a1d1ed541b)"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	// An id shorter than 12 is not padded or cut.
	if got := RenderWake([]WakeReason{handled("assigned", "AT1", "abc", "T")}); !strings.HasSuffix(got, "(AT1, abc)") {
		t.Fatalf("short id: %q", got)
	}
}

func TestTaskLineWithoutHandleKeepsTheFullID(t *testing.T) {
	got := RenderWake([]WakeReason{handled("assigned", "", hexID, "Fix it")})
	if want := "[hadron task] assigned: Fix it (" + hexID + ")"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	// A handle that is only control characters is empty after oneLine: same fallback.
	got = RenderWake([]WakeReason{handled("assigned", "\x1b\n", hexID, "Fix it")})
	if !strings.HasSuffix(got, "("+hexID+")") {
		t.Fatalf("control-only handle: %q", got)
	}
}

func TestTaskLineHandleIsOneLine(t *testing.T) {
	got := RenderWake([]WakeReason{handled("assigned", "AT1\x1b[31m\r\n.2", hexID, "T")})
	if strings.ContainsAny(got, "\r\n\x1b") {
		t.Fatalf("control character reached the line: %q", got)
	}
}

func TestTaskLessAlarmIsUnchangedByAHandle(t *testing.T) {
	a := WakeReason{Kind: WakeTask, Task: &TaskReason{EventID: "e", Event: "alarm", AlarmType: "delivery.stuck", Handle: "AT9"}}
	if got, want := RenderWake([]WakeReason{a}), "[hadron task] alarm: delivery.stuck (no task)"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	a.Task.AlarmType = ""
	if got, want := RenderWake([]WakeReason{a}), "[hadron task] alarm (no task)"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// The handle is display only: identity and the ack path do not move.
func TestHandleDoesNotMoveTheKey(t *testing.T) {
	a := WakeReason{Kind: WakeTask, Task: &TaskReason{EventID: "ev1", ChainIndex: 2, ID: hexID}}
	b := WakeReason{Kind: WakeTask, Task: &TaskReason{EventID: "ev1", ChainIndex: 2, ID: hexID, Handle: "AT1.2"}}
	if a.Key() != "task:ev1:2" || b.Key() != a.Key() {
		t.Fatalf("keys %q %q", a.Key(), b.Key())
	}
}

// At the MaxWakeRunes bound whole reasons are cut, so every reason kept still
// ends with its handle ref. (One reason longer than the bound is cut at its
// tail, as before this change.)
func TestBoundCutsWholeReasonsSoKeptHandlesSurvive(t *testing.T) {
	var many []WakeReason
	for i := range 30 {
		many = append(many, handled("assigned", fmt.Sprintf("AT0.%d", i), hexID, strings.Repeat("t", 60)))
	}
	cut := RenderWake(many)
	if utf8.RuneCountInString(cut) > MaxWakeRunes {
		t.Fatalf("cut line is %d runes", utf8.RuneCountInString(cut))
	}
	kept := strings.Count(cut, TaskPrefix)
	if kept == 0 || kept == 30 {
		t.Fatalf("kept %d", kept)
	}
	if n := strings.Count(cut, ", 01a1d1ed541b)"); n != kept {
		t.Fatalf("%d of %d kept reasons carry a handle ref: %q", n, kept, cut)
	}
}
