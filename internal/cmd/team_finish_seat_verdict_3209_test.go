package cmd

import (
	"strings"
	"testing"
)

// cli#3209: `team task finish` help names SEAT_VERDICT_REQUIRED (exit 5) and the
// order: record the verdict while STARTED, then finish.
func TestTeamTaskFinishHelpNamesSeatVerdictRequired(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "task", "finish", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	help := strings.Join(strings.Fields(out.String()), " ")
	for _, w := range []string{"SEAT_VERDICT_REQUIRED", "team pr verdict", "exits 5"} {
		if !strings.Contains(help, w) {
			t.Errorf("finish help should say %q:\n%s", w, out.String())
		}
	}
}
