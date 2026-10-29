package orcaterm

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// The 2026-10-18 scratch-terminal captures (cli#907; testdata/scratch-2026-10-18
// and its README): real Claude Code 2.1.303 and Codex CLI 0.163.0 screens,
// judged by the code that delivery runs.

func scratchScreen(t *testing.T, name string) *Screen {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "scratch-2026-10-18", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	sc := NewClient(func(context.Context, []string) Result { return Result{Stdout: string(b)} }).ReadScreen(context.Background(), "h")
	if sc == nil {
		t.Fatalf("%s: unreadable capture", name)
	}
	return sc
}

func TestScratchCaptures2026_10_18(t *testing.T) {
	for _, c := range []struct {
		name, family string
		want         Composer
	}{
		// Claude: the empty composer is a bare box over ONE mode footer, with
		// or without the transient "· ← for agents" (it shows only on the
		// first screen after start), and Orca reporting no draft.
		{"c01-start", "claude", ComposerEmpty},
		{"c30-restart", "claude", ComposerEmpty},
		{"c33-bs-on-empty", "claude", ComposerEmpty},
		{"c13-ml3-esc-esc", "claude", ComposerEmpty},
		{"c18-after-enter-1", "claude", ComposerEmpty},
		// Orca blanks the composer in the tail and reports its text as draft.
		{"c02-draft", "claude", ComposerOwn},
		{"c09-multiline", "claude", ComposerOwn},
		{"c14-empty-esc-esc", "claude", ComposerQuestion},
		{"c20-askuser", "claude", ComposerQuestion},
		{"x01-start", "codex", ComposerEmpty},
		{"x20-working", "codex", ComposerEmpty},
		{"x21-done", "codex", ComposerEmpty},
		{"x02-draft", "codex", ComposerOwn},
		{"x04-ml", "codex", ComposerOwn},
		{"x24-model-menu", "codex", ComposerQuestion},
		// Codex quit to the shell by one Ctrl+C on an empty composer.
		{"x13-single-ctrl-c-empty", "codex", ComposerUnknown},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got, _ := ReadComposer(scratchScreen(t, c.name), c.family, "SCRATCH-907"); got != c.want {
				t.Fatalf("composer %q, want %q", got, c.want)
			}
		})
	}
}

// The box alone never proves a Claude composer empty: Orca blanks the text
// it holds, so a bare box with a draft is a draft, and a footer that is not
// the mode line is not proof.
func TestClaudeEmptyNeedsTheModeFooter(t *testing.T) {
	for name, below := range map[string]string{
		"no footer":       "",
		"another line":    "  ctrl+g to edit in Vim",
		"two footer rows": "  ⏵⏵ auto mode on (shift+tab to cycle)\n  esc to interrupt",
		// Fail closed: only a mode phrase ending in " on" is the mode line.
		"unknown phrase":       "  ⏵⏵ something else entirely",
		"bare marker":          "  ⏵⏵",
		"marker and space":     "  ⏵⏵ ",
		"two hintless footers": "  ⏵⏵ bypass permissions on · 1 shell\n  ⏵⏵ auto mode on · 2 shells",
		// The mode phrase cannot swallow a " · " segment (BLIND P2, #1189).
		"later segment ends on": "  ⏵⏵ something else entirely · notification on",
		"truncated hint":        "  ⏵⏵ auto mode on (shift+tab",
		"text after mode":       "  ⏵⏵ auto mode on trailing",
		"empty mode phrase":     "  ⏵⏵  on",
	} {
		t.Run(name, func(t *testing.T) {
			tail := []string{rule, "❯ ", rule}
			if below != "" {
				tail = append(tail, splitLines(below)...)
			}
			if PromptLineIsEmpty(tail, "claude") {
				t.Fatalf("judged empty: %q", tail)
			}
		})
	}
	for _, footer := range []string{
		"  ⏵⏵ bypass permissions on (shift+tab to cycle)",
		"  ⏵⏵ auto mode on (shift+tab to cycle)",
		"  ⏵⏵ auto mode on (shift+tab to cycle) · ← for agents",
		claudeHint,
		// A live worker's footer (fleet read, 2026-10-18).
		"  ⏵⏵ bypass permissions on (shift+tab to cycle) · PR #435 · ← for agents",
		// A background shell drops the cycle hint (cli#1155; server#2695).
		"  ⏵⏵ bypass permissions on · 1 shell",
		"  ⏵⏵ auto mode on · 2 shells · ← for agents",
		"  ⏵⏵ auto mode on",
	} {
		if !PromptLineIsEmpty([]string{rule, "❯ ", rule, footer}, "claude") {
			t.Errorf("%q not judged empty", footer)
		}
	}
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := range s {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

// Orca accepts --wait-submit (and so --retry-request) only with --text AND
// --enter and without --interrupt; anything else is refused invalid_argument
// before it reaches the terminal (scratch capture, cli#907).
func TestSendWaitsOnlyWhereOrcaAllows(t *testing.T) {
	for name, c := range map[string]struct {
		opts SendOptions
		wait bool
	}{
		"text and Enter": {SendOptions{}, true},
		"Enter-only":     {SendOptions{EnterOnly: true}, false},
		"no Enter":       {SendOptions{NoEnter: true}, false},
		"interrupt":      {SendOptions{Interrupt: true}, false},
	} {
		t.Run(name, func(t *testing.T) {
			r := &recorder{replies: []string{`{"ok":false,"error":{"code":"transport","requestId":"req-9"}}`, accepted}}
			NewClient(r.run).Send(context.Background(), "term_1", "[hadron chat #1] x", c.opts)
			if got := slices.Contains(r.first(t), "--wait-submit"); got != c.wait {
				t.Fatalf("--wait-submit=%v, want %v: %v", got, c.wait, r.calls[0])
			}
			retried := len(r.calls) == 2
			if retried != c.wait {
				t.Fatalf("retry=%v, want %v (a retry id is valid only with --wait-submit): %v", retried, c.wait, r.calls)
			}
		})
	}
}

// cli#906 row 2: Claude's steady-state liveness on the saved captures. Idle
// needs positive evidence: a completed turn, or a fresh launch's banner with
// only its own chrome below it. A thinking line is busy before its timer
// appears. Anything else stays unknown (deferred to busyDeadline, no later).
func TestClaudeIdleOnScratchCaptures(t *testing.T) {
	for name, want := range map[string]Liveness{
		"c01-start":         Idle, // fresh launch, update notice below the banner
		"c13-ml3-esc-esc":   Idle,
		"c30-restart":       Idle, // effort indicator below the banner
		"c33-bs-on-empty":   Idle,
		"c18-after-enter-1": Busy, // "✢ Noodling…", no timer yet
		"c18-after-enter-4": Idle,
		"c21-after-answer":  Idle, // completion line under the effort indicator
		"c14-empty-esc-esc": Unknown,
	} {
		t.Run(name, func(t *testing.T) {
			if got, why := IdleProof(scratchScreen(t, name).Tail, "claude"); got != want {
				t.Fatalf("liveness %q (%s), want %q", got, why, want)
			}
		})
	}
}

func TestClaudeWeakChromeNeverSubstitutesForEvidence(t *testing.T) {
	box := []string{rule, "❯ ", rule, "  ⏵⏵ bypass permissions on (shift+tab to cycle)"}
	for name, above := range map[string][]string{
		"effort indicator alone":  {"                                    ◐ medium · /effort"},
		"update notice alone":     {"  Updated to latest. Got 37 features, 227 bugfixes, and 65 other changes.", "  code.claude.com/docs/en/changelog for details"},
		"logo without its banner": {"▝▜██████▀  Sonnet 5.5 · Claude Max"},
		"ordinary output":         {"● You picked Alpha.", "◐ medium · /effort"},
	} {
		t.Run(name, func(t *testing.T) {
			if got, _ := IdleProof(append(append([]string{}, above...), box...), "claude"); got == Idle {
				t.Fatalf("judged idle on chrome alone: %q", above)
			}
		})
	}
	thinking := append([]string{"❯ do a thing", "✶ Pondering…"}, box...)
	if got, _ := IdleProof(thinking, "claude"); got != Busy {
		t.Fatalf("a thinking line without a timer: %q, want busy", got)
	}
}
