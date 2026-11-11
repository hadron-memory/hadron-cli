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

// cli#1856: alive Claude agents at an empty composer read locked/unrecognized
// because the classifier failed closed on two screen shapes. Each fixture is
// drawn in its RED position — the one that failed before the fix:
//   - a line BELOW the composer box that is not the mode footer (PromptLineIsEmpty
//     wants exactly one footer there): the update notice and the /clear hint;
//   - a permission-mode statusline ABOVE the box (IdleProof judges the last
//     non-weak line above it): the auto-mode·PR and bypass·agent footers.
func TestClassifyAliveFooters1856(t *testing.T) {
	const (
		updateInstalled = "✔ Update installed · Restart to update"
		newTask         = "new task? /clear to save 128k tokens"
		autoModePR      = "⏵⏵ auto mode on (shift+tab to cycle) · PR #3701"
		bypassAgent     = "⏵⏵ bypass permissions on (shift+tab to cycle) · ← 1 agent"
	)
	box := func(above []string, below ...string) []string {
		out := append(append([]string{}, above...), rule, "❯ ", rule)
		return append(out, below...)
	}
	done := []string{"⏺ Done.", "✻ Cogitated for 1m 3s"}
	for name, tail := range map[string][]string{
		"update installed below the box, alone":            box(done, "  "+updateInstalled),
		"update installed below the box, after the footer": box(done, claudeHint, "  "+updateInstalled),
		"new task hint below the box, alone":               box(done, "  "+newTask),
		"new task hint below the box, after the footer":    box(done, claudeHint, "  "+newTask),
		"auto mode PR footer above the box":                box(append(append([]string{}, done...), "  "+autoModePR), claudeHint),
		"bypass agent footer above the box":                box(append(append([]string{}, done...), "  "+bypassAgent), claudeHint),
	} {
		t.Run(name, func(t *testing.T) {
			got := Classify(agent("claude"), fix(tail), now)
			if got.Status != ts.Idle || got.Prompt != ts.PromptEmpty || got.Reason != "" {
				t.Fatalf("got %s/%s/%q, want idle/empty", got.Status, got.Prompt, got.Reason)
			}
		})
	}

	// Green controls: the relaxed reads must never make a menu, a dialog, a
	// typed draft or an unknown line below the box look idle or empty.
	t.Run("controls stay held", func(t *testing.T) {
		menu := []string{"Do you want to proceed?", "❯ 1. Yes", "  2. No, and tell Claude what to do differently (esc)"}
		for name, c := range map[string]struct {
			term   Terminal
			tail   []string
			status ts.Status
			prompt ts.Prompt
			reason ts.Reason
		}{
			"trust dialog":               {agent("claude"), menu, ts.Locked, ts.PromptMenu, ts.MenuOrQuestion},
			"bare shell":                 {Terminal{Handle: "term_sh", Connected: true, Writable: true}, []string{"joshua@host:~/w$"}, ts.Dark, ts.PromptUnknown, ts.BareShell},
			"unknown line below the box": {agent("claude"), box(done, claudeHint, "  something else entirely"), ts.Locked, ts.PromptUnknown, ts.Unrecognized},
			"unknown line above the box": {agent("claude"), box(append(append([]string{}, done...), "  ⏵⏵ not a mode line"), claudeHint), ts.Locked, ts.PromptEmpty, ts.Unrecognized},
			"notice with typed text":     {agent("claude"), []string{"⏺ Done.", rule, "❯ half typed", rule, "  " + updateInstalled}, ts.Locked, ts.PromptNotEmpty, ts.Unrecognized},
			"statusline above a dialog":  {agent("claude"), append([]string{"  " + autoModePR}, menu...), ts.Locked, ts.PromptMenu, ts.MenuOrQuestion},
		} {
			t.Run(name, func(t *testing.T) {
				got := Classify(c.term, fix(c.tail), now)
				if got.Status != c.status || got.Prompt != c.prompt || got.Reason != c.reason {
					t.Fatalf("got %s/%s/%q, want %s/%s/%q", got.Status, got.Prompt, got.Reason, c.status, c.prompt, c.reason)
				}
			})
		}
	})
}

// cli#1856: an unrecognized read says which line it could not match, in the
// local Detail only (never IdleProof's returned reason, which stays a code).
func TestClassifyUnrecognizedDetailNamesTheLine1856(t *testing.T) {
	below := Classify(agent("claude"), fix([]string{"⏺ Done.", rule, "❯ ", rule, claudeHint, "  odd line below"}), now)
	if below.Reason != ts.Unrecognized || below.Detail != "could not match: odd line below" {
		t.Errorf("below the box: %s %q", below.Reason, below.Detail)
	}
	above := Classify(agent("claude"), fix([]string{"⏺ Done.", "  odd line above", rule, "❯ ", rule, claudeHint}), now)
	if above.Reason != ts.Unrecognized || above.Detail != "could not match: odd line above" {
		t.Errorf("above the box: %s %q", above.Reason, above.Detail)
	}
	if idle := Classify(agent("claude"), fix(claudeIdle), now); idle.Detail != "" {
		t.Errorf("an idle read carries no detail: %q", idle.Detail)
	}
	if _, reason := IdleProof([]string{"⏺ Done.", "  odd line above", rule, "❯ ", rule, claudeHint}, "claude"); reason != "unrecognized-chrome" {
		t.Errorf("IdleProof must keep returning a code, got %q", reason)
	}
}
