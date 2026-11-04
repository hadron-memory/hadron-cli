package cmd

import (
	"strings"
	"testing"
)

// cli#640: `agent list` help sends a reader who wants an App's installed
// agents to the command that reads the AppAgent join, `app agent list
// <app-ref>`, the same one the --app stderr note names. `team roster` lists
// the team's workers, not its installed agents.
func TestAgentListHelpPointsAtAppAgentList(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetOut(out)
	root.SetArgs([]string{"agent", "list", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	help := strings.Join(strings.Fields(out.String()), " ")
	if !strings.Contains(help, "hadron app agent list <app-ref>") {
		t.Errorf("agent list --help must name `hadron app agent list <app-ref>` for an App's installed agents:\n%s", out.String())
	}
	if strings.Contains(help, "team roster") {
		t.Errorf("agent list --help must not send readers to `team roster` (it lists workers):\n%s", out.String())
	}
}
