package skilldoc

import (
	"strings"
	"testing"
)

// server#1315 — a skill name the HOST reserves is refused for that host only,
// ported from hadron-server's hosts.reserved.test.ts so the offline lint agrees
// with the planner.

func reservedNode(name string) Node {
	decl := map[string]any{"name": name, "description": "Does a thing. Use when you need it.", "enable": true}
	return Node{
		URN: "hrn:node:example.com:demo:tasks:x", Loc: "tasks:x", MemoryURN: "hrn:mem:example.com:demo",
		IsRunnable: true, Content: "# Body",
		Properties: map[string]any{ExportsKey: map[string]any{HostClaudeSkill: decl, HostCodexSkill: decl}},
	}
}

func hostRow(t *testing.T, key string) Host {
	t.Helper()
	h, ok := HostFor(key)
	if !ok {
		t.Fatalf("no host %q", key)
	}
	return h
}

func reservedFor(t *testing.T, name, host string) bool {
	t.Helper()
	_, ok := rules(LintFor(reservedNode(name), hostRow(t, host)))["skill-name-reserved"]
	return ok
}

func TestReservedNamesRegistry(t *testing.T) {
	if got := hostRow(t, HostClaudeSkill).ReservedNames; len(got) != 1 || got[0] != "synced" {
		t.Errorf("claudeSkill reserves %q, want [synced]", got)
	}
	if got := hostRow(t, HostCodexSkill).ReservedNames; len(got) != 0 {
		t.Errorf("codexSkill reserves %q, want nothing", got)
	}
}

func TestSyncedIsAnErrorForClaudeOnly(t *testing.T) {
	fs := LintFor(reservedNode("synced"), hostRow(t, HostClaudeSkill))
	if sev := rules(fs)["skill-name-reserved"]; sev != SevError {
		t.Fatalf("claudeSkill: skill-name-reserved severity %q, want %q (findings %+v)", sev, SevError, fs)
	}
	// hadron-server's text, word for word (src/lib/skilldoc/lint.ts, #1318).
	want := `skill name "synced" is reserved by claudeSkill: the host keeps its own skills in a folder of that name and skips a skill authored there, so the exported file would never load — rename properties.exports.claudeSkill.name`
	for _, f := range fs {
		if f.Rule == "skill-name-reserved" && f.Message != want {
			t.Errorf("message\n got %q\nwant %q", f.Message, want)
		}
	}
	if reservedFor(t, "synced", HostCodexSkill) {
		t.Error("codexSkill: the reservation is Claude's, not a global ban")
	}
}

func TestReservedMatchesTheWholeNameNotASubstring(t *testing.T) {
	for _, name := range []string{"synced-notes", "my-synced", "unsynced", "sync"} {
		if reservedFor(t, name, HostClaudeSkill) {
			t.Errorf("%q flagged as reserved", name)
		}
	}
}

func TestClaudeAndAnthropicAreNotReserved(t *testing.T) {
	// An upload-surface rule, not one Claude Code applies (server#1315).
	for _, name := range []string{"hadron-export-task-as-claude-skill", "anthropic-helper"} {
		for _, host := range []string{HostClaudeSkill, HostCodexSkill} {
			if reservedFor(t, name, host) {
				t.Errorf("%s@%s flagged as reserved", name, host)
			}
		}
	}
}

func TestReservedIsFoldedLikeAFilesystemName(t *testing.T) {
	// The host reserves the name "in any capitalization"; an alias landing on
	// the same directory is the same directory. hadron-server folds with an
	// ICU collator at base sensitivity, and so does this.
	for _, name := range []string{
		"Synced", "SYNCED", " synced ",
		"\u017fynced",                          // long s
		"sy\u0301nced",                         // decomposed accent
		"s\u00fdnced",                          // precomposed accent
		"\uff53\uff59\uff4e\uff43\uff45\uff44", // fullwidth
	} {
		if !reservedFor(t, name, HostClaudeSkill) {
			t.Errorf("%q not flagged as reserved", name)
		}
	}
}

func TestReservedIsReportedBesideTheGrammarError(t *testing.T) {
	// `Synced` gets both, so fixing the case does not reveal a second error.
	for _, name := range []string{"Synced", "SYNCED"} {
		got := rules(LintFor(reservedNode(name), hostRow(t, HostClaudeSkill)))
		if got["skill-name-invalid"] != SevError || got["skill-name-reserved"] != SevError {
			t.Errorf("%q: findings %v, want both skill-name-invalid and skill-name-reserved", name, got)
		}
	}
}

func TestOrdinaryNameHasNoNameFinding(t *testing.T) {
	// CONTROL: the rule is not firing on everything.
	for rule := range rules(LintFor(reservedNode("plain-skill"), hostRow(t, HostClaudeSkill))) {
		if strings.HasPrefix(rule, "skill-name-") {
			t.Errorf("plain-skill produced %s", rule)
		}
	}
}
