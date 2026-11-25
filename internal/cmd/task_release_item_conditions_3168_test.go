package cmd

import (
	"os"
	"strings"
	"testing"
)

// cli#3168: the release help and docs said a release frees or untracks the item
// without saying when. A create and the untracked list have DIFFERENT
// conditions. A create is refused only while an ACTIVE task holds the id
// (cor:agt:120:02, S1a). The item stays tracked, so no claim finds it and
// untrack is not called, while ANY task of the App holds it, finished or
// cancelled included (cor:agt:120:04).
func assertWording3168(t *testing.T, where, text string, want, unwanted []string) {
	t.Helper()
	flat := strings.Join(strings.Fields(text), " ")
	for _, s := range want {
		if !strings.Contains(flat, s) {
			t.Errorf("%s lacks:\n%s", where, s)
		}
	}
	for _, s := range unwanted {
		if strings.Contains(flat, s) {
			t.Errorf("%s still has the unqualified wording:\n%s", where, s)
		}
	}
}

func TestTaskReleaseHelpSaysWhoCanTakeTheItem3168(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "task", "release", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	assertWording3168(t, "release help", out.String(), []string{
		"The next `task create --external-id` naming it gets it only when no other active task (backlog, pending or started) of the App holds it; " +
			"a finished or cancelled task that holds it does not block the create.",
		"The next `team task find-untracked` claim gets it only when no other task of the App, finished or cancelled included, still holds it.",
	}, []string{"claim, gets it"})
}

func TestAgenticUsageReleaseEntrySaysWhoCanTakeTheItem3168(t *testing.T) {
	b, err := os.ReadFile("agentic/agentic-usage.md")
	if err != nil {
		t.Fatal(err)
	}
	assertWording3168(t, "agentic-usage release entry", string(b), []string{
		"give the task's outside item back: the next create naming it gets it only when no other active task (backlog, pending or started) of the App holds it (cor:agt:120:02), " +
			"and the next find-untracked claim gets it only when no other task of the App, finished or cancelled included, still holds it (cor:agt:120:04);",
	}, []string{"so the next create or find-untracked claim gets it"})
}

func TestAgenticUsageReconcileQualifiesUntrack3168(t *testing.T) {
	b, err := os.ReadFile("agentic/agentic-usage.md")
	if err != nil {
		t.Fatal(err)
	}
	assertWording3168(t, "agentic-usage RECONCILE", string(b), []string{
		"RELEASED -> `untrack` {externalId} with the RELEASED id the feed names (the released task no longer holds it), " +
			"only when no task of the App, finished or cancelled included, still holds it;",
	}, []string{"(the task holds none)"})
}

func TestIntakeHowToRunStepQualifiesUntrack3168(t *testing.T) {
	b, err := os.ReadFile(howToPath3062)
	if err != nil {
		t.Fatal(err)
	}
	assertWording3168(t, "intake how-to run step 2", string(b), []string{
		"a release calls `untrack` only when no task of the App, finished or cancelled included, still holds the released id,",
	}, []string{"a release calls `untrack`, a cancel"})
}
