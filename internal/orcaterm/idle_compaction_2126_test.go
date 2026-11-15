package orcaterm

import (
	"testing"

	ts "github.com/hadron-memory/hadron-cli/internal/terminalstatus"
)

// cli#2126 (Charles's repro of Hugo's #108970): Claude draws its idle
// compaction banner with a ● glyph as well as ✻. Above an empty prompt
// either is positive idle proof. The match is narrow: the ● form counts only
// as the whole banner sentence at the start of the line, because ● also
// starts Claude's ordinary output lines.
const idleCompactionBanner = `Compacted while idle, before the prompt cache expired · turn off with "idleCompaction": false in settings.json`

func TestClassifyIdleCompactionBanner2126(t *testing.T) {
	tail := func(first string) []string { return []string{first, rule, "❯ ", rule, claudeHint} }
	for name, c := range map[string]struct {
		line   string
		status ts.Status
		reason ts.Reason
	}{
		"✻ banner":                          {"✻ " + idleCompactionBanner, ts.Idle, ""},
		"● banner":                          {"● " + idleCompactionBanner, ts.Idle, ""},
		"● banner, short form":              {"● Compacted while idle, before the prompt cache expired", ts.Idle, ""},
		"● output line naming the banner":   {"● I saw \"Compacted while idle, before the prompt cache expired\" earlier", ts.Locked, ts.Unrecognized},
		"● only the first words":            {"● Compacted while idle", ts.Locked, ts.Unrecognized},
		"another glyph before the sentence": {"◆ " + idleCompactionBanner, ts.Locked, ts.Unrecognized},
	} {
		t.Run(name, func(t *testing.T) {
			got := Classify(agent("claude"), fix(tail(c.line)), now)
			if got.Status != c.status || got.Prompt != ts.PromptEmpty || got.Reason != c.reason {
				t.Fatalf("got %s/%s/%q, want %s/%s/%q", got.Status, got.Prompt, got.Reason, c.status, ts.PromptEmpty, c.reason)
			}
		})
	}
}
