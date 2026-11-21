package cmd

import (
	"strings"
	"testing"
)

// cli#2893 (option 1, docs): `team wake-settings set --instruction` names no App, so the
// CLI cannot compare the bound worker's App with the target's (the server resolves the
// target, cli#2774). The help must not promise that check: it says the SERVER judges the
// App match for this command, and keeps the fallback wording for the checks the CLI does
// make (an incomplete binding or a binding that changed). Wording only: no wire change.
func TestWakeSettingsSetHelpNamesTheServerAsTheJudgeOfTheApp2893(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "wake-settings", "set", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	help := strings.Join(strings.Fields(out.String()), " ")
	if strings.Contains(help, "for this server and the worker's own App") {
		t.Errorf("the help promises a CLI-side App check this command cannot make:\n%s", help)
	}
	for _, want := range []string{
		// The remedy: the server is the judge of the App for this command.
		"server resolves the target",
		"CLI cannot check the worker's App",
		// The fallback wording for what the CLI CAN check stays as it was.
		"when that binding is complete",
		"unchanged since the command started",
		"otherwise it goes out as the signed-in person with the claim still sent (a note on stderr says why)",
		"Who may change them is the server's rule",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("the help lacks %q:\n%s", want, help)
		}
	}
}
