package orcaterm

import (
	"testing"
	"time"

	ts "github.com/hadron-memory/hadron-cli/internal/terminalstatus"
)

var now = time.Date(2026, 10, 29, 16, 41, 40, 0, time.UTC)

func agent(family string) Terminal {
	return Terminal{Handle: "term_1", AgentIdentity: &family, Connected: true, Writable: true}
}

func fix(tail []string) *Screen { return &Screen{Source: "screen", Tail: tail} }

func TestClassify(t *testing.T) {
	menu := []string{"Do you want to proceed?", "❯ 1. Yes", "  2. No, and tell Claude what to do differently (esc)"}
	shell := Terminal{Handle: "term_sh", Connected: true, Writable: true}
	gone := agent("claude")
	gone.Connected = false
	for name, c := range map[string]struct {
		term   Terminal
		sc     *Screen
		status ts.Status
		prompt ts.Prompt
		reason ts.Reason
	}{
		"claude idle":                 {agent("claude"), fix(claudeIdle), ts.Idle, ts.PromptEmpty, ""},
		"claude busy at empty prompt": {agent("claude"), fix(claudeBusyEmpty), ts.Busy, ts.PromptEmpty, ""},
		"codex idle":                  {agent("codex"), fix(codexIdle), ts.Idle, ts.PromptEmpty, ""},
		"codex busy at empty prompt":  {agent("codex"), fix(codexBusyEmpty), ts.Busy, ts.PromptEmpty, ""},
		"codex idle after a recap":    {agent("codex"), fix(codexIdleRecapDiego), ts.Idle, ts.PromptEmpty, ""},
		"claude permission dialog":    {agent("claude"), fix(menu), ts.Locked, ts.PromptMenu, ts.MenuOrQuestion},
		"claude ask-user (capture)":   {agent("claude"), scratchScreen(t, "c20-askuser"), ts.Locked, ts.PromptMenu, ts.MenuOrQuestion},
		"codex model menu (capture)":  {agent("codex"), scratchScreen(t, "x24-model-menu"), ts.Locked, ts.PromptMenu, ts.MenuOrQuestion},
		"codex working (capture)":     {agent("codex"), scratchScreen(t, "x20-working"), ts.Busy, ts.PromptEmpty, ""},
		"claude start (capture)":      {agent("claude"), scratchScreen(t, "c01-start"), ts.Idle, ts.PromptEmpty, ""},
		// The launch art pushes Codex's banner out of IdleProof's window, so a
		// fresh start has an empty prompt but no positive idle proof: it holds
		// (delivery holds it too) rather than guess.
		"codex start (capture)":          {agent("codex"), scratchScreen(t, "x01-start"), ts.Locked, ts.PromptEmpty, ts.Unrecognized},
		"bare shell (capture)":           {shell, scratchScreen(t, "x13-single-ctrl-c-empty"), ts.Dark, ts.PromptUnknown, ts.BareShell},
		"agent not connected":            {gone, fix(claudeIdle), ts.Dark, ts.PromptUnknown, ts.NoAgent},
		"empty prompt, no proof":         {agent("codex"), fix(codexUnknownStatusNoRecap), ts.Locked, ts.PromptEmpty, ts.Unrecognized},
		"unreadable screen fails closed": {agent("claude"), nil, ts.Locked, ts.PromptUnknown, ts.Unrecognized},
		"partial source fails closed":    {agent("claude"), &Screen{Source: "scrollback", Tail: claudeIdle}, ts.Locked, ts.PromptUnknown, ts.Unrecognized},
		"bare codex prompt is not idle":  {agent("codex"), fix([]string{"  Worked for 5s • 4:04 PM", "› ", "  ? for shortcuts"}), ts.Locked, ts.PromptUnknown, ts.Unrecognized},
	} {
		t.Run(name, func(t *testing.T) {
			got := Classify(c.term, c.sc, now)
			if got.Status != c.status || got.Prompt != c.prompt || got.Reason != c.reason {
				t.Fatalf("got %s/%s/%q, want %s/%s/%q", got.Status, got.Prompt, got.Reason, c.status, c.prompt, c.reason)
			}
		})
	}
}

// Orca blanks a composer's text in the tail and reports it as the draft: a
// typed draft is a non-empty prompt and never idle.
func TestClassifyDraftIsNotIdle(t *testing.T) {
	got := Classify(agent("claude"), scratchScreen(t, "c02-draft"), now)
	if got.Prompt != ts.PromptNotEmpty || got.Status == ts.Idle {
		t.Fatalf("draft classified %s/%s", got.Status, got.Prompt)
	}
}

// A screen showing a different agent than Orca names is two disagreeing
// signals: held, never idle.
func TestClassifyFamilyDisagreementFailsClosed(t *testing.T) {
	got := Classify(agent("codex"), fix(claudeIdle), now)
	if got.Status == ts.Idle {
		t.Fatalf("disagreeing identity classified idle")
	}
}

// Idle is the only status a send-only-on-idle caller types into: every
// screen in the repo's fixture set that is not positively idle must not be.
func TestClassifyNeverIdleOnQuestionScreens(t *testing.T) {
	for _, tail := range [][]string{
		{"Do you want to proceed?", "❯ 1. Yes"},
		{"Would you like to run this command?", "› 1. Yes, proceed"},
	} {
		for _, fam := range []string{"claude", "codex"} {
			if got := Classify(agent(fam), fix(tail), now); got.Status == ts.Idle {
				t.Errorf("%s %q classified idle", fam, tail[0])
			}
		}
	}
}

func TestLastTurnAt(t *testing.T) {
	at := func(h, m int, d int) *time.Time {
		v := time.Date(2026, 10, d, h, m, 0, 0, time.UTC)
		return &v
	}
	for name, c := range map[string]struct {
		family string
		tail   []string
		want   *time.Time
	}{
		"claude done stamp (pm)":              {"claude", claudeIdleRecapYuri, at(15, 54, 29)},
		"codex worked stamp (am)":             {"codex", codexIdleRecapDiego, at(3, 49, 29)},
		"codex worked stamp (24h)":            {"codex", codexIdle, at(16, 32, 29)},
		"newest stamp wins":                   {"codex", append([]string{"  Worked for 1s • 1:00 PM"}, codexIdle...), at(16, 32, 29)},
		"a stamp ahead of now is yesterday's": {"codex", []string{"  Worked for 5s • 11:50 PM", "› Ask Codex to do anything", "  ? for shortcuts"}, at(23, 50, 28)},
		"no stamp on screen is null":          {"claude", claudeBusyEmpty, nil},
		"codex banner without a turn is null": {"codex", []string{"› Ask Codex to do anything", "  ? for shortcuts"}, nil},
		"unknown family is null":              {"", claudeIdle, nil},
	} {
		t.Run(name, func(t *testing.T) {
			got := LastTurnAt(fix(c.tail), c.family, now)
			switch {
			case got == nil && c.want == nil:
			case got == nil || c.want == nil || !got.Equal(*c.want):
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

// The dark rule is blind to lastTurnAt: the same screen classifies the same
// with and without a readable turn time.
func TestClassifyStatusIndependentOfLastTurnAt(t *testing.T) {
	// Same completed-turn marker; only the stamp's readability differs.
	unreadable := append([]string{}, codexIdle...)
	unreadable[0] = "  Worked for 33s • later"
	with := Classify(agent("codex"), fix(codexIdle), now)
	without := Classify(agent("codex"), fix(unreadable), now)
	if with.LastTurnAt == nil || without.LastTurnAt != nil {
		t.Fatalf("fixtures do not isolate the stamp: %v / %v", with.LastTurnAt, without.LastTurnAt)
	}
	if with.Status != ts.Idle || without.Status != ts.Idle {
		t.Fatalf("status depends on the stamp: %s vs %s", with.Status, without.Status)
	}
}

// BLIND P2-3: a terminal that cannot be typed into is never sendable.
func TestClassifyNonWritableIsNeverIdle(t *testing.T) {
	tm := agent("claude")
	tm.Writable = false
	got := Classify(tm, fix(claudeIdle), now)
	if got.Status == ts.Idle || got.Status != ts.Locked || got.Reason != ts.Unrecognized {
		t.Fatalf("non-writable classified %s/%q", got.Status, got.Reason)
	}
}

// LOCAL P2-1: both families' chrome at once is ambiguity, and Orca's identity
// must not break the tie: held, never idle.
func TestClassifyMixedChromeFailsClosed(t *testing.T) {
	mixed := append([]string{"OpenAI Codex (v1.0)"}, claudeIdle...)
	for _, fam := range []string{"claude", "codex"} {
		if got := Classify(agent(fam), fix(mixed), now); got.Status != ts.Locked || got.Reason != ts.Unrecognized {
			t.Errorf("identity %s: mixed chrome classified %s/%q", fam, got.Status, got.Reason)
		}
	}
}

// LOCAL P2-1: a bare shell needs POSITIVE shell evidence (a shell prompt as
// the last line); arbitrary unrecognized output is not proof of a shell.
func TestClassifyBareShellNeedsPositiveEvidence(t *testing.T) {
	shell := Terminal{Handle: "term_sh", Connected: true, Writable: true}
	for name, tail := range map[string][]string{
		"bash prompt": {"user@host:~/w$"},
		"root prompt": {"root@host:/# "},
		"zsh prompt":  {"host% "},
	} {
		if got := Classify(shell, fix(tail), now); got.Status != ts.Dark || got.Reason != ts.BareShell {
			t.Errorf("%s: %s/%q", name, got.Status, got.Reason)
		}
	}
	for name, tail := range map[string][]string{
		"unrecognized output": {"Compiling... 42%", "some tui frame"},
		"empty screen":        {},
	} {
		if got := Classify(shell, fix(tail), now); got.Status != ts.Locked || got.Reason != ts.Unrecognized {
			t.Errorf("%s: %s/%q", name, got.Status, got.Reason)
		}
	}
}
