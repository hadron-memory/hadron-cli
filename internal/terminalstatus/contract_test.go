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
	const want = `{"contract":"terminal-status@1","worker":"Joshua","terminal":"term_50a6","status":"idle","prompt":"empty","reason":null,"lastTurnAt":"2026-10-29T16:12:03Z","checkedAt":"2026-10-29T16:30:00Z","resetAt":null}`
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

// cli#2676: rate_limited is on the closed list, and resetAt is an additive
// key inside @1: always present (null when not known), UTC, every @1 key kept.
func TestRateLimitedAndResetAtAdditiveKey2676(t *testing.T) {
	for _, r := range []Reason{"no_screen", "multiple_terminals"} {
		if !r.Valid() {
			t.Errorf("%q must be on the closed list (cli#2677; hadron-server#4558 added both)", r)
		}
	}
	if !RateLimited.Valid() {
		t.Fatal("rate_limited must be on the closed list")
	}
	reset := time.Date(2026, 11, 20, 16, 45, 32, 0, time.FixedZone("x", 8*3600))
	b, _ := json.Marshal(Result{Worker: "Gil", Status: Locked, Prompt: PromptUnknown, Reason: ptr(RateLimited), ResetAt: &reset, CheckedAt: t0})
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got["contract"] != "terminal-status@1" || got["reason"] != "rate_limited" || got["resetAt"] != "2026-11-20T08:45:32Z" {
		t.Fatalf("%s", b)
	}
	for _, k := range []string{"worker", "terminal", "status", "prompt", "reason", "lastTurnAt", "checkedAt"} {
		if _, ok := got[k]; !ok {
			t.Fatalf("@1 key %q must stay: %s", k, b)
		}
	}
	// Not limited: the key is still present, null.
	b, _ = json.Marshal(Result{Worker: "Gil", Status: Idle, Prompt: PromptEmpty, CheckedAt: t0})
	if !strings.Contains(string(b), `"resetAt":null`) {
		t.Fatalf("resetAt must always be present: %s", b)
	}
	// A consumer that knows only the @1 keys still parses the new output.
	var old struct {
		Contract string  `json:"contract"`
		Worker   *string `json:"worker"`
		Terminal *string `json:"terminal"`
		Status   Status  `json:"status"`
		Prompt   Prompt  `json:"prompt"`
		Reason   *Reason `json:"reason"`
	}
	b, _ = json.Marshal(Result{Worker: "Gil", Status: Locked, Prompt: PromptUnknown, Reason: ptr(RateLimited), ResetAt: &reset, CheckedAt: t0})
	if err := json.Unmarshal(b, &old); err != nil || old.Status != Locked || old.Reason == nil || *old.Reason != RateLimited {
		t.Fatalf("an @1 consumer must still parse: %v %+v", err, old)
	}
}
