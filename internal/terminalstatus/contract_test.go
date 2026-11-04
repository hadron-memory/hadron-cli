package terminalstatus

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestResultJSONShape(t *testing.T) {
	at := time.Date(2026, 10, 29, 16, 12, 3, 0, time.UTC)
	r := Result{Worker: "Joshua", Terminal: "term_50a6", Status: Idle, Prompt: PromptEmpty, LastTurnAt: &at, CheckedAt: t0}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"contract":"terminal-status@1","worker":"Joshua","terminal":"term_50a6","status":"idle","prompt":"empty","reason":null,"lastTurnAt":"2026-10-29T16:12:03Z","checkedAt":"2026-10-29T16:30:00Z"}`
	if string(b) != want {
		t.Fatalf("got  %s\nwant %s", b, want)
	}
}

func TestResultNullsAndDetailNeverMarshalled(t *testing.T) {
	r := Result{Worker: "Joshua", Status: Dark, Prompt: PromptEmpty, Reason: ptr(NoTerminal), Detail: "screen text must stay local", CheckedAt: t0}
	b, _ := json.Marshal(r)
	s := string(b)
	for _, must := range []string{`"terminal":null`, `"lastTurnAt":null`, `"reason":"no_terminal"`} {
		if !strings.Contains(s, must) {
			t.Errorf("%s missing %s", s, must)
		}
	}
	if strings.Contains(s, "detail") || strings.Contains(s, "screen text") {
		t.Fatalf("detail leaked into the contract: %s", s)
	}
}

func TestClosedLists(t *testing.T) {
	for _, s := range []Status{Busy, Idle, Locked, Dark} {
		if !s.Valid() {
			t.Errorf("%s invalid", s)
		}
	}
	if Status("sleeping").Valid() || Prompt("typing").Valid() || Reason("whatever").Valid() {
		t.Fatal("values outside the closed lists must be invalid")
	}
	for _, r := range []Reason{Unrecognized, MenuOrQuestion, NoTurnForDueTicker, NoTerminal, BareShell, NoAgent} {
		if !r.Valid() {
			t.Errorf("%s invalid", r)
		}
	}
}

func ptr[T any](v T) *T { return &v }
