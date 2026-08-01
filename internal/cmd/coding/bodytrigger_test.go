package coding

import (
	"strings"
	"testing"
)

// The two conventions, in the shapes they actually appear in: hadron-server /
// hadron-cli / mmdata use the blockquote Scope form, hadron-portal the inline
// bold form.
func TestBodyTrigger(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{
			"blockquote scope, single line",
			"# Review: whatever\n\n> **Scope.** Adding or renaming a GraphQL argument that identifies an entity.\n\nBody follows.",
			"Adding or renaming a GraphQL argument that identifies an entity.",
		},
		{
			"blockquote scope, wrapped",
			"> **Scope.** Run this when authorization compares two `organizationId`s,\n> or branches on org membership, for a user-owned entity.\n\nRest.",
			"Run this when authorization compares two `organizationId`s, or branches on org membership, for a user-owned entity.",
		},
		{
			"inline applies-when keeps its marker",
			"# Title\n\n**Applies when** a parent component reads a child's bound value.\n\nCheck 1 — ...",
			"Applies when a parent component reads a child's bound value.",
		},
		{
			"inline applies-when, wrapped",
			"**Applies when** a route form\nreturns a typed message.\n\nNext para.",
			"Applies when a route form returns a typed message.",
		},
		{
			"scope without the trailing period in the marker",
			"> **Scope** Something happens.\n",
			"Something happens.",
		},
		// A third of checks state no scope at all; those must yield nothing so
		// the finding stays quiet rather than carrying a "none found" line.
		{"no marker", "# Review: x\n\nJust prose about the rule.\n", ""},
		{"empty", "", ""},
		{"marker with no text", "> **Scope.**\n\nBody.", ""},
		{"marker with only whitespace", "> **Scope.**   \n", ""},
	}
	for _, tc := range cases {
		got, ok := bodyTrigger(tc.content)
		if tc.want == "" {
			if ok || got != "" {
				t.Errorf("%s: expected no trigger, got %q", tc.name, got)
			}
			continue
		}
		if !ok {
			t.Errorf("%s: expected a trigger, found none", tc.name)
			continue
		}
		if got != tc.want {
			t.Errorf("%s:\n  got  %q\n  want %q", tc.name, got, tc.want)
		}
	}
}

// The blockquote form stops at the end of the blockquote, not at the end of the
// document — otherwise the whole body would be swallowed into the quote.
func TestBodyTriggerStopsAtParagraphEnd(t *testing.T) {
	content := "> **Scope.** First para.\n> Still first.\n\n> A later blockquote that is not the scope.\n\n## Heading\n"
	got, ok := bodyTrigger(content)
	if !ok {
		t.Fatal("expected a trigger")
	}
	if strings.Contains(got, "later blockquote") || strings.Contains(got, "Heading") {
		t.Errorf("paragraph over-ran its end: %q", got)
	}
	if got != "First para. Still first." {
		t.Errorf("got %q", got)
	}
}

func TestTruncateRunes(t *testing.T) {
	// Multi-byte input must not be cut mid-rune.
	s := strings.Repeat("é", 50)
	got := truncateRunes(s, 10)
	if len([]rune(got)) > 11 { // 10 + the ellipsis
		t.Errorf("truncated to %d runes: %q", len([]rune(got)), got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("expected an ellipsis, got %q", got)
	}
	// Short input is returned untouched.
	if got := truncateRunes("short", 160); got != "short" {
		t.Errorf("short input altered: %q", got)
	}
	// Cuts on a word boundary when one is near the limit.
	got = truncateRunes("alpha beta gamma delta epsilon", 20)
	if strings.Contains(got, "delt…") {
		t.Errorf("cut mid-word: %q", got)
	}
}

func TestTriggerHint(t *testing.T) {
	long := "> **Scope.** " + strings.Repeat("word ", 100)

	// Default truncates to roughly label size.
	h := triggerHint(long, false)
	if h == "" {
		t.Fatal("expected a hint")
	}
	if len([]rune(h)) > triggerQuoteLimit+80 {
		t.Errorf("default hint is not truncated: %d runes", len([]rune(h)))
	}
	if !strings.Contains(h, "…") {
		t.Error("expected the truncation ellipsis")
	}

	// --suggest prints it whole.
	full := triggerHint(long, true)
	if len([]rune(full)) <= len([]rune(h)) {
		t.Error("--suggest should produce a longer quote than the default")
	}
	if strings.Contains(full, "…") {
		t.Error("--suggest should not truncate")
	}

	// No scope paragraph → no hint at all, in either mode.
	if got := triggerHint("just prose", false); got != "" {
		t.Errorf("expected no hint, got %q", got)
	}
	if got := triggerHint("just prose", true); got != "" {
		t.Errorf("expected no hint with --suggest either, got %q", got)
	}
}

// The Scope marker's `>` is optional, so continuation handling must follow the
// line actually matched. Assuming blockquote silently dropped the rest of a
// plain `**Scope.**` paragraph. No live memory uses this form today — all 57
// Scope markers are blockquoted — so this guards a latent case.
func TestBodyTriggerPlainScopeWraps(t *testing.T) {
	got, ok := bodyTrigger("**Scope.** Adding an argument that identifies\nan existing entity.\n\nRest.")
	if !ok {
		t.Fatal("expected a trigger")
	}
	want := "Adding an argument that identifies an existing entity."
	if got != want {
		t.Errorf("plain Scope paragraph lost its continuation:\n  got  %q\n  want %q", got, want)
	}

	// The blockquoted form still stops at the end of the blockquote.
	got, _ = bodyTrigger("> **Scope.** First line.\nplain continuation must not be absorbed.\n")
	if strings.Contains(got, "plain continuation") {
		t.Errorf("blockquoted paragraph absorbed a non-quoted line: %q", got)
	}
}

// truncateRunes searches and thresholds in runes. strings.LastIndexAny returns
// a BYTE offset, which over-truncated multi-byte text: a CJK trigger whose only
// space sits at rune 50 has byte offset ~150, which passed a "3/4 of 160" test
// and cut 160 runes down to 50.
func TestTruncateRunesNonASCIIKeepsContext(t *testing.T) {
	s := strings.Repeat("経", 50) + " " + strings.Repeat("路", 200)
	got := []rune(truncateRunes(s, 160))
	if len(got) < 150 {
		t.Errorf("over-truncated multi-byte text to %d runes; want ~160", len(got))
	}
	// Still a valid string, still ends with the ellipsis.
	if !strings.HasSuffix(string(got), "…") {
		t.Errorf("expected an ellipsis, got %q", string(got))
	}
	// ASCII behaviour is unchanged: back off to the word boundary.
	if got := truncateRunes("alpha beta gamma delta epsilon zeta", 30); strings.Contains(got, "zet") {
		t.Errorf("expected a word-boundary cut, got %q", got)
	}
}
