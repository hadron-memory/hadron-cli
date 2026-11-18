package cmd

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// cli#2234 items 1 and 2: agentic-usage documents the exact-value masking
// tradeoff for the submitted override key (no exemption), and the old-server
// chat-probe sentence is wrapped like its neighbours.
func TestAgenticUsageAiConfigMaskingTradeoff2234(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agentic-usage"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	doc := out.String()
	start := strings.Index(doc, "The submitted override key is redacted locally")
	if start < 0 {
		t.Fatal("ai-config test redaction paragraph is missing")
	}
	para := doc[start:]
	if end := strings.Index(para, "When a nonempty override key is supplied"); end >= 0 {
		para = para[:end]
	}
	flat := strings.Join(strings.Fields(para), " ")
	// Only the two clauses that state the contract are pinned: masking reaches
	// legitimate substrings, and there is no length or word-boundary exemption.
	// The surrounding wording may be edited freely (cli#2364 F6).
	for _, want := range []string{
		"legitimate substrings",
		"no length or word-boundary exemption",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("masking paragraph lacks %q:\n%s", want, flat)
		}
	}
	for _, line := range strings.Split(doc, "\n") {
		// Columns are runes, not bytes: a multi-byte character is one column.
		if cols := utf8.RuneCountInString(line); strings.Contains(line, "an older server is refused (GRAPHQL_VALIDATION_FAILED") && cols > 100 {
			t.Errorf("old-server chat-probe sentence is not wrapped (%d columns): %q", cols, line)
		}
	}
}
