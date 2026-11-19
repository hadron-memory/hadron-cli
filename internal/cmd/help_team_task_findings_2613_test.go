package cmd

import (
	"strings"
	"testing"
)

// cli#2613: the team task group page tells the findings workflow in one
// paragraph: finish with --findings-file, read with findings, record with
// dispose-finding, list with finding-dispositions, in that order.
func TestTeamTaskHelpTellsFindingsWorkflow2613(t *testing.T) {
	help := flatHelp1053S3(t, "team", "task")
	start := strings.Index(help, "Findings:")
	if start < 0 {
		t.Fatal(`team task help has no paragraph starting "Findings:"`)
	}
	para := help[start:]
	last := -1
	for _, want := range []string{
		"hadron team task finish", "--findings-file",
		"hadron team task findings",
		"hadron team task dispose-finding",
		"hadron team task finding-dispositions",
	} {
		i := strings.Index(para, want)
		if i < 0 {
			t.Fatalf("the findings paragraph lacks %q: %q", want, para)
		}
		if i < last {
			t.Errorf("%q comes before an earlier step in the findings paragraph: %q", want, para)
		}
		last = i
	}
}
