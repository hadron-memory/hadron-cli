package teamrouter

// Ported 1:1 from router.test.ts describe('a human typing: never appended
// to; hold the token (#5473)').

import (
	"strings"
	"testing"
)

func TestTypingIdleScreensReadEmpty(t *testing.T) {
	cases := []struct {
		tail   []string
		family string
	}{
		{claudeIdle, "claude"},
		{codexIdle, "codex"},
		{[]string{"› Ask Codex to do anything", "  ? for shortcuts"}, "codex"}, // NBSP + placeholder
	}
	for _, tc := range cases {
		if !PromptLineIsEmpty(tc.tail, tc.family) {
			t.Errorf("family %s: idle screen must read empty: %q", tc.family, tc.tail)
		}
	}
}

func TestTypingTypedTextIsNotEmpty(t *testing.T) {
	if PromptLineIsEmpty(withPrompt(claudeIdle, "❯", "half-typed message"), "claude") {
		t.Error("claude: typed text on the prompt line must hold")
	}
	if PromptLineIsEmpty(withPrompt(codexIdle, "›", "half-typed message"), "codex") {
		t.Error("codex: typed text on the prompt line must hold")
	}
	// Claude: the first line is empty but a continuation line inside the box is not.
	if PromptLineIsEmpty([]string{rule, "❯", "  second line of a draft", rule}, "claude") {
		t.Error("claude: a multi-line draft must hold")
	}
	// Codex: something below the prompt that is not footer.
	if PromptLineIsEmpty([]string{"›", "  second line of a draft", "  ? for shortcuts"}, "codex") {
		t.Error("codex: a non-footer line below the prompt must hold")
	}
}

func TestTypingLiteralMarkerCaseStillDraft(t *testing.T) {
	// Exactly as Orca rendered a two-line Claude draft whose second line is ❯
	// (the literal-marker case, live 2026-10-01; @codex P2, Gene #1555).
	if PromptLineIsEmpty([]string{rule, "❯ first line", "  ❯", rule, "  ⏵⏵ bypass permissions on"}, "claude") {
		t.Error("a draft continuation line that looks like the marker must hold")
	}
	if PromptLineIsEmpty([]string{rule, "❯", "  ❯", rule}, "claude") {
		t.Error("the indented marker continuation must hold")
	}
	// Chip's QA repro (#5724), with the marker even at column 0.
	if PromptLineIsEmpty([]string{rule, "❯ half typed", "❯", rule}, "claude") {
		t.Error("a column-0 marker continuation must hold")
	}
	// A typed rule is indented too, so it cannot fake the box's opening rule.
	if PromptLineIsEmpty([]string{rule, "❯ text", "  " + rule, "❯", rule}, "claude") {
		t.Error("an indented typed rule must hold")
	}
	// Codex: a typed › continuation is indented, so it cannot pose as the prompt.
	if PromptLineIsEmpty([]string{"› half typed", "  ›", "  ? for shortcuts"}, "codex") {
		t.Error("an indented codex continuation must hold")
	}
	// Rules in earlier transcript output do not matter: the box is the LAST pair.
	if !PromptLineIsEmpty([]string{rule, "output", rule, "❯ old turn", rule, "❯", rule, claudeHint}, "claude") {
		t.Error("an earlier transcript rule must not shadow the live box")
	}
}

func TestTypingClaudeEmptyBoxNeedsEmptyInputHint(t *testing.T) {
	// Three spaces typed: the box renders a bare ❯, and only the hint vanished
	// (Chip #5756, live 2026-10-01).
	if PromptLineIsEmpty([]string{rule, "❯", rule, "  ⏵⏵ bypass permissions on (shift+tab to cycle)"}, "claude") {
		t.Error("a vanished agents hint means typing")
	}
	// The hint is present in every permission mode while empty.
	for _, mode := range []string{
		"⏵⏵ auto mode on (shift+tab to cycle)",
		"⏸ manual mode on",
		"⏵⏵ accept edits on (shift+tab to cycle)",
		"⏸ plan mode on (shift+tab to cycle)",
	} {
		if !PromptLineIsEmpty([]string{rule, "❯", rule, "  " + mode + " · ← for agents"}, "claude") {
			t.Errorf("mode %q with the agents hint must read empty", mode)
		}
		if PromptLineIsEmpty([]string{rule, "❯", rule, "  " + mode}, "claude") {
			t.Errorf("mode %q without the agents hint must hold", mode)
		}
	}
	// No status line at all cannot prove it.
	if PromptLineIsEmpty([]string{rule, "❯", rule}, "claude") {
		t.Error("no status line cannot prove empty")
	}
}

func TestTypingClaudeStatusLineIsTerminalBottom(t *testing.T) {
	// The blind-review counterexample (#1823): a shell `cat`s a captured Claude
	// idle screen and returns to its own prompt — the trailing shell prompt is
	// the evidence that the box is history, not a live composer.
	if PromptLineIsEmpty(append(append([]string{}, claudeIdle...), "orca@orca1:~/ws/eli$ "), "claude") {
		t.Error("trailing shell prompt after the hint must hold")
	}
	// Any unexplained content after the hint holds, shell-shaped or not.
	if PromptLineIsEmpty([]string{rule, "❯", rule, claudeHint, "  some trailing output"}, "claude") {
		t.Error("trailing non-chrome content must hold")
	}
	// Trailing BLANK lines are just the scroll buffer: still empty.
	if !PromptLineIsEmpty(append(append([]string{}, claudeIdle...), "", "   "), "claude") {
		t.Error("trailing blank lines are scroll buffer, still empty")
	}
}

func TestTypingCodexOnlyPlaceholderProvesEmpty(t *testing.T) {
	// Whitespace typed into Codex: the placeholder vanished, and Orca reported no draft.
	if PromptLineIsEmpty([]string{"  Got a minute and a mildly unreasonable idea?", "›", "  GPT-6-Sol high · ~/orca/workspaces/hadron-server/eli"}, "codex") {
		t.Error("a vanished placeholder must hold")
	}
	if PromptLineIsEmpty([]string{"›", "", "  ? for shortcuts"}, "codex") {
		t.Error("a bare prompt must hold")
	}
	// Codex's update menu: the cursor line is a menu option, not the composer.
	if PromptLineIsEmpty([]string{
		"  Update available · 0.159.0 → 0.159.3",
		"› 1. Update now (runs `brew upgrade --cask codex`)",
		"  2. Skip",
		"  3. Skip until next version",
		"  enter continue · esc skip",
	}, "codex") {
		t.Error("a dialog must hold")
	}
}

func TestTypingCannotProveEmptyWithoutMarkers(t *testing.T) {
	cases := []struct {
		tail   []string
		family string
		why    string
	}{
		{[]string{"plain shell $"}, "claude", "no prompt marker"},
		{[]string{rule, "❯"}, "claude", "no closing rule"},
		{codexIdle, "gemini", "unknown agent family"},
		{[]string{}, "codex", "empty tail"},
	}
	for _, tc := range cases {
		if PromptLineIsEmpty(tc.tail, tc.family) {
			t.Errorf("%s: must not prove empty", tc.why)
		}
	}
}

func TestTypingHoldsThenNudgesOnceEmpty(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"}, [2]string{"w-baloo", "Baloo"})
	f.addTerminal("eli", "claude", "", true)
	f.addTerminal("baloo", "codex", "", true)
	f.setScreen("term_baloo", screenSpec{tail: withPrompt(codexIdle, "›", "Holger typing here")})
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{}), worker("w-baloo", "Baloo", workerOpts{})}
	out := f.poll(t, c)
	if len(out.Nudged) != 1 || out.Nudged[0] != "Eli" {
		t.Fatalf("nudged = %v, want [Eli]", out.Nudged)
	}
	if len(out.Held) != 1 || out.Held["Baloo"] != "prompt not empty (text in the prompt)" {
		t.Fatalf("held = %v, want the Baloo prompt-text hold", out.Held)
	}
	if out.Adopted {
		t.Error("a held poll adopts nothing")
	}
	wantHandles(t, f, "term_eli")
	// The held reason never carries the typed text.
	if strings.Contains(strings.Join(f.logs, "\n"), "Holger typing here") {
		t.Error("the hold reason must never quote the typed text")
	}

	f.setScreen("term_baloo", screenSpec{tail: codexIdle})
	out = f.poll(t, c)
	if !contains(out.Covered, "Eli") || len(out.Nudged) != 1 || out.Nudged[0] != "Baloo" || !out.Adopted {
		t.Fatalf("second = %+v, want Eli covered, Baloo nudged, adopted", out)
	}
}

func TestTypingCodexDraftOnlyHolds(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-baloo", "Baloo"})
	f.addTerminal("baloo", "codex", "", true)
	// As observed live: Codex draws › empty while Orca reports the text as draft.
	draft := "half typed"
	f.setScreen("term_baloo", screenSpec{tail: []string{"  permissions: YOLO mode", "›", "  GPT-6-Sol high · ~/orca/workspaces/hadron-server/baloo"}, draft: &draft})
	f.attention.workers = []AttentionItem{worker("w-baloo", "Baloo", workerOpts{})}
	out := f.poll(t, c)
	if len(out.Held) != 1 || out.Held["Baloo"] != "prompt not empty (text in the composer draft)" {
		t.Fatalf("held = %v, want the draft hold", out.Held)
	}
	wantNoSend(t, f)
}

func TestTypingHoldsOnDraftNonScreenUnreadable(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"})
	f.addTerminal("eli", "codex", "", true) // a Codex draft is authoritative: its composer renders empty
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{})}
	typed := "typed in Orca"
	ws := "   "
	cases := []struct {
		screen screenSpec
		why    string
	}{
		{screenSpec{tail: codexIdle, draft: &typed}, "text in the composer draft"},
		// Whitespace is typing too (Gene, #1555): the gate promises nobody is typing.
		{screenSpec{tail: codexIdle, draft: &ws}, "text in the composer draft"},
		{screenSpec{tail: codexIdle, source: "screen-unavailable"}, "screen unreadable"},
		{screenSpec{unreadable: true}, "screen unreadable"},
	}
	for _, tc := range cases {
		f.setScreen("term_eli", tc.screen)
		out := f.poll(t, c)
		if len(out.Held) != 1 || out.Held["Eli"] != "prompt not empty ("+tc.why+")" {
			t.Fatalf("held = %v, want Eli: prompt not empty (%s)", out.Held, tc.why)
		}
		wantNoSend(t, f)
	}
}

func TestTypingClaudeDraftCorroboratedStillHolds(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"})
	f.addTerminal("eli", "claude", "", true)
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{})}
	typed := "typed words"
	f.setScreen("term_eli", screenSpec{tail: withPrompt(claudeIdle, "❯", "typed words"), draft: &typed})
	out := f.poll(t, c)
	if len(out.Held) != 1 || out.Held["Eli"] != "prompt not empty (text in the composer draft)" {
		t.Fatalf("held = %v, want the corroborated-draft hold", out.Held)
	}
	wantNoSend(t, f)
}
