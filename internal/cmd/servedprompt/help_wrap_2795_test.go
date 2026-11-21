package servedprompt

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/spf13/cobra"
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
		for _, bad := range linesOver78(text) {
			t.Errorf("%s: %s", name, bad)
		}
	}
}

// linesOver78 reports each line wider than 78 COLUMNS. A column is a rune, not a byte, so a
// non-ASCII character in a help string is judged as one column (cli#2880 item 1).
func linesOver78(text string) []string {
	var bad []string
	for i, line := range strings.Split(text, "\n") {
		if n := utf8.RuneCountInString(line); n > 78 {
			bad = append(bad, fmt.Sprintf("line %d is %d columns (want <=78): %s", i+1, n, line))
		}
	}
	return bad
}

// cli#2880 item 1: a line of exactly 78 non-ASCII characters is 78 columns, though it is more
// bytes, and a line of 79 is not.
func TestLinesOver78CountsRunesNotBytes2880(t *testing.T) {
	if got := linesOver78(strings.Repeat("é", 78)); len(got) != 0 {
		t.Errorf("78 runes is within the limit, got %v", got)
	}
	if got := linesOver78(strings.Repeat("é", 79)); len(got) != 1 {
		t.Errorf("79 runes is over the limit, got %v", got)
	}
	if utf8.RuneCountInString(strings.Repeat("é", 78)) != 78 {
		t.Fatal("test setup")
	}
}

// cli#2880 item 2: the constants are measured above; this measures what each command RENDERS.
// Every line of the paragraph a command embeds must appear in its --help output as a whole line
// (so cobra or a later wrapper has not re-flowed it), in order, within 78 columns.
func TestRenderedHelpKeepsTheLintParagraphWrapped2880(t *testing.T) {
	f := &cmdutil.Factory{}
	for name, tc := range map[string]struct {
		cmd       *cobra.Command
		paragraph string
	}{
		"worker":  {NewWorker(f), currentLintHelp(true)},
		"run":     {NewRun(f), currentLintHelp(false)},
		"chat":    {NewChat(f), historyLintHelp},
		"session": {NewSession(f), historyLintHelp},
	} {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			tc.cmd.SetOut(&out)
			tc.cmd.SetErr(&out)
			tc.cmd.SetArgs([]string{"--help"})
			if err := tc.cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			rendered := out.String()
			var want []string
			for _, l := range strings.Split(tc.paragraph, "\n") {
				if strings.TrimSpace(l) != "" {
					want = append(want, l)
				}
			}
			if len(want) < 3 {
				t.Fatalf("the paragraph is lines, got %d", len(want))
			}
			lines := strings.Split(rendered, "\n")
			next := 0
			for _, l := range lines {
				if next < len(want) && l == want[next] {
					next++
				}
			}
			if next != len(want) {
				t.Errorf("rendered --help lost or re-flowed the line %q (matched %d of %d lines in order)", want[next], next, len(want))
			}
			for _, bad := range linesOver78(strings.Join(want, "\n")) {
				t.Errorf("%s", bad)
			}
		})
	}
}
