package servedprompt

import (
	"bytes"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/spf13/cobra"
)

// cli#2881 (from #2875's QA, same shape as #2795/#2880): the served-prompt help text the two
// commands built by newCommand render (`run served-prompt` and `team worker served-prompt`)
// still had prose over 78 columns (the intro and description paragraphs, the 107-column
// "Variables are the recorded values..." sentence, the run-ref sentence and the worker WHICH
// RECORD block). TestLintHelpWrap78 reaches only the CURRENT LINT paragraphs, so this measures
// what each command RENDERS: every PROSE line, that is every line before cobra's "Usage:"
// (cobra's example and flag lines are not prose and stay out of scope), within 78 columns.
func TestServedPromptHelpProseWrap78_2881(t *testing.T) {
	f := &cmdutil.Factory{}
	for name, cmd := range map[string]*cobra.Command{
		"worker (team worker served-prompt)": NewWorker(f),
		"run (run served-prompt)":            NewRun(f),
	} {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			cmd.SetArgs([]string{"--help"})
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			rendered := out.String()
			i := strings.Index(rendered, "\nUsage:")
			if i < 0 {
				t.Fatalf("no Usage: line in the rendered help:\n%s", rendered)
			}
			prose := rendered[:i]
			if len(strings.Fields(prose)) < 100 {
				t.Fatalf("the prose is the description, got %d words", len(strings.Fields(prose)))
			}
			for _, bad := range linesOver78(prose) {
				t.Errorf("%s", bad)
			}
		})
	}
}
