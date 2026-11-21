package servedprompt

import (
	"strings"
	"testing"
)

// cli#2795 (from #2778's LOCAL P3): the CURRENT LINT help paragraphs hard-wrap
// at 78 columns. The retry sentence landed as one long line and broke the wrap;
// this pin holds every rendered line of both paragraphs — currentLintHelp in
// both its variants and the history const — within 78 columns.
func TestLintHelpWrap78(t *testing.T) {
	texts := map[string]string{
		"currentLintHelp(worker)": currentLintHelp(true),
		"currentLintHelp(run)":    currentLintHelp(false),
		"historyLintHelp":         historyLintHelp,
	}
	for name, text := range texts {
		for i, line := range strings.Split(text, "\n") {
			if len(line) > 78 {
				t.Errorf("%s line %d is %d columns (want <=78):\n%s", name, i+1, len(line), line)
			}
		}
	}
}
