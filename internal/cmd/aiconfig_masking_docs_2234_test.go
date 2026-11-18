package cmd

import (
	"strings"
	"testing"
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
	for _, want := range []string{
		"short or common",
		"legitimate substrings",
		"presentation tradeoff",
		"no length or word-boundary exemption",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("masking paragraph lacks %q:\n%s", want, flat)
		}
	}
	for _, line := range strings.Split(doc, "\n") {
		if strings.Contains(line, "an older server is refused (GRAPHQL_VALIDATION_FAILED") && len(line) > 100 {
			t.Errorf("old-server chat-probe sentence is not wrapped (%d columns): %q", len(line), line)
		}
	}
}
