package skilldoc

import (
	"strings"
	"testing"
)

// cli#722 / server#1341 — the description rule Cowork's upload validator
// applies, pinned to the 20 cases of Holger's probe upload (2026-09-25):
// every refused case must fire skill-description-markup for Claude, every
// accepted one must not, and Codex (which accepts all 20) never fires it.

func descNode(desc string) Node {
	decl := map[string]any{"name": "probe", "description": desc, "enable": true}
	return Node{
		URN: "hrn:node:example.com:demo:tasks:probe", Loc: "tasks:probe", MemoryURN: "hrn:mem:example.com:demo",
		IsRunnable: true, Content: "# Body",
		Properties: map[string]any{ExportsKey: map[string]any{HostClaudeSkill: decl, HostCodexSkill: decl}},
	}
}

func markupFired(t *testing.T, desc, host string) (bool, string) {
	t.Helper()
	for _, f := range LintFor(descNode(desc), hostRow(t, host)) {
		if f.Rule == "skill-description-markup" {
			return true, f.Message
		}
	}
	return false, ""
}

// The probe's descriptions, EXACTLY as uploaded: Eli's synthetic
// eli-tagprobe.zip (server#1341, measurement step 2), keyed by its skill
// names. Cowork refused the first 12 and accepted the last 8.
var (
	probeRefused = map[string]string{
		"p02-placeholder":      "Use when adding a node to <memory>.",
		"p03-capital-tag":      "Use when working as <Name>.",
		"p04-hyphen-tag":       "Use when given a <memory-urn>.",
		"p07-lt-and-gt-spaced": "Use when 1 < 2 and 3 > 2.",
		"p08-closing-tag":      "Use when the block ends with </end>.",
		"p09-self-closing":     "Use when a <br/> appears.",
		"p12-shift":            "Use when shifting x << 2 or y >> 1.",
		"p14-digit-tag":        "Use when step <1> is next.",
		"p15-two-words":        "Use when <two words> appear.",
		"p16-email":            "Use when mailing <user@example.com>.",
		"p17-html-comment":     "Use when a <!-- note --> appears.",
		"p19-url":              "Use when linking <https://example.com>.",
	}
	probeAccepted = map[string]string{
		"p01-clean-control":  "Does a thing. Use when you need it.",
		"p05-lone-lt":        "Use when a < b.",
		"p06-lone-gt":        "Use when b > a.",
		"p10-arrows":         "Use when mapping a -> b or x => y.",
		"p11-heart":          "Use when you <3 it.",
		"p13-entities":       "Use when text shows &lt;memory&gt; escaped.",
		"p18-empty-brackets": "Use when <> appears.",
		"p20-lt-letter":      "Use when a<b holds.",
	}
)

func TestDescriptionMarkupMatchesTheCoworkProbe(t *testing.T) {
	if len(probeRefused) != 12 || len(probeAccepted) != 8 {
		t.Fatalf("the probe had 12 refused and 8 accepted cases; the table has %d and %d", len(probeRefused), len(probeAccepted))
	}
	for name, desc := range probeRefused {
		if fired, _ := markupFired(t, desc, HostClaudeSkill); !fired {
			t.Errorf("%s: Cowork refused %q, so claudeSkill must fire skill-description-markup", name, desc)
		}
	}
	for name, desc := range probeAccepted {
		if fired, msg := markupFired(t, desc, HostClaudeSkill); fired {
			t.Errorf("%s: Cowork accepted %q, so it must not fire: %s", name, desc, msg)
		}
	}
}

func TestDescriptionMarkupIsClaudeOnly(t *testing.T) {
	// Codex's loader has no such check (codex-cli rust-v0.153.0 source) and
	// loads a `<worker>` description (team-chat #1127); the 20 probe cases
	// were not run against Codex.
	for name, desc := range probeRefused {
		if fired, _ := markupFired(t, desc, HostCodexSkill); fired {
			t.Errorf("%s: codexSkill must not fire skill-description-markup", name)
		}
	}
	if !hostRow(t, HostClaudeSkill).RejectsDescriptionMarkup || hostRow(t, HostCodexSkill).RejectsDescriptionMarkup {
		t.Error("only claudeSkill rejects description markup")
	}
}

func TestDescriptionMarkupIsAnErrorNamingWhatToChange(t *testing.T) {
	for _, f := range LintFor(descNode("Use when adding a node to <memory>."), hostRow(t, HostClaudeSkill)) {
		if f.Rule != "skill-description-markup" {
			continue
		}
		if f.Severity != SevError {
			t.Errorf("severity %q, want %q — a refused description fails the whole upload", f.Severity, SevError)
		}
		if !strings.Contains(f.Message, `"<memory>"`) || !strings.Contains(f.Message, "claudeSkill") {
			t.Errorf("the message must name the offending text and the host: %s", f.Message)
		}
		// @codex on #730: the message states the rule the server will copy, so
		// it must not describe `<>` (which passes) as refused.
		if !strings.Contains(f.Message, "at least one character between") {
			t.Errorf("the message must state the non-empty span condition: %s", f.Message)
		}
		return
	}
	t.Fatal("skill-description-markup did not fire")
}

// It is reported beside the length rule, not instead of it, so fixing one
// does not reveal the other on the next run.
func TestDescriptionMarkupIsReportedBesideTooLong(t *testing.T) {
	long := "Use when <memory> " + strings.Repeat("x", MaxDescriptionLen)
	got := rules(LintFor(descNode(long), hostRow(t, HostClaudeSkill)))
	if got["skill-description-markup"] != SevError || got["skill-description-too-long"] != SevError {
		t.Errorf("want both skill-description-markup and skill-description-too-long, got %v", got)
	}
}

// UNMEASURED, and pinned on purpose. Two predicates fit all 20 probe cases:
// `<[^>]+>` and the narrower `<[^<>]+>`. They differ only on a nested `<<>`,
// which the probe did not include. This encodes the broader one, so `<<>` is
// refused; if Cowork is ever measured to accept it, this test is the one to
// change, together with descriptionMarkupRE.
func TestDescriptionMarkupRefusesTheUnmeasuredNestedCase(t *testing.T) {
	if fired, _ := markupFired(t, "Use when a <<> appears.", HostClaudeSkill); !fired {
		t.Error("the broader predicate refuses a nested <<> (unmeasured, conservative)")
	}
}
