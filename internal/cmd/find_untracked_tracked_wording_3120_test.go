package cmd

import (
	"strings"
	"testing"
)

// cli#3120: find-untracked's help defined "tracked" as "an active task holds
// it", which contradicts cor:agt:120:04 (a finished or cancelled task keeps
// its id, so the item stays tracked until a release gives it back, and only
// the releasing task's hold goes).
func TestFindUntrackedHelpDefinesTrackedPerSpec3120(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "task", "find-untracked", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	help := strings.Join(strings.Fields(out.String()), " ")
	want := `"tracked" means a task of this App, finished or cancelled included, holds it as its external id (` + "`task create --external-id`" + `). ` +
		"`task release` gives back only the releasing task's hold: the item stays tracked while any other task of this App still holds it."
	if !strings.Contains(help, want) {
		t.Errorf("help lacks the spec's definition of tracked:\n%s", want)
	}
	if strings.Contains(help, "an active task") {
		t.Errorf("help still defines tracked by an active task")
	}
}
