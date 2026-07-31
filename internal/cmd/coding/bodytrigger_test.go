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
