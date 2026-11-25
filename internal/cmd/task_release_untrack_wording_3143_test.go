package cmd

import (
	"strings"
	"testing"
)

// cli#3143: the release help said the next reconcile pass always calls untrack.
// A release gives back only the releasing task's hold, and the pass calls
// untrack only when no other task of the App, in any state, holds the item
// (cor:agt:120:04, reconcile.go heldElsewhere).
func TestTaskReleaseHelpQualifiesUntrack3143(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "task", "release", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	help := strings.Join(strings.Fields(out.String()), " ")
	want := "The next `find-untracked` reconcile pass tells the team's tracker adapter (`untrack`) that the item was released " +
		"only when no other task of the App, finished or cancelled included, still holds it."
	if !strings.Contains(help, want) {
		t.Errorf("release help lacks the qualified untrack sentence:\n%s", want)
	}
	if strings.Contains(help, "that the item was released.") {
		t.Error("release help says a release untracks unconditionally")
	}
}
