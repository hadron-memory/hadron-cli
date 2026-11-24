package cmd

import (
	"strings"
	"testing"
)

// cli#2913: `question amend|withdraw|purge --help` state the exit 7 unknown outcome, with a per-verb
// read-back, for parity with answer and retract (cli#2854, #2908). (ask's sentence is #2994's.) The help
// says what the command really does: the read-back it names is the one the error carries.
func TestQuestionExit7HelpForAmendWithdrawPurge2913(t *testing.T) {
	for _, tc := range []struct {
		verb string
		// the noun the unknown outcome is about, and the read-back the help must name
		noun, readBack string
	}{
		{"amend", "the amend may have been recorded", "question get"},
		{"withdraw", "the withdrawal may have been recorded", "question get"},
		{"purge", "the purge may have happened", "question get"},
	} {
		t.Run(tc.verb, func(t *testing.T) {
			help := strings.Join(strings.Fields(cmdHelpOut(t, "question", tc.verb)), " ")
			for _, want := range []string{
				"Exit 7 is an unknown outcome",
				"the reply never arrived",
				tc.noun,
				"Nothing is resent",
				tc.readBack,
				"could not reach the server",
			} {
				if !strings.Contains(help, want) {
					t.Errorf("question %s --help must state the exit 7 unknown outcome, lacks %q:\n%s", tc.verb, want, help)
				}
			}
			if tc.verb == "purge" && !strings.Contains(help, "not found") {
				t.Errorf("purge help must say a purged question reads as not found:\n%s", help)
			}
			// no issue number or spec citation in user help (#2508)
			if strings.Contains(help, "#2854") || strings.Contains(help, "#2908") || strings.Contains(help, "cor:") {
				t.Errorf("no issue number or citation in user help:\n%s", help)
			}
		})
	}
}
