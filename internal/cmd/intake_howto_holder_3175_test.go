package cmd

import (
	"strings"
	"testing"
)

// cli#3175: the adapter how-to stated the reassign and cancel calls without
// the condition the reconcile pass applies (reconcile.go heldElsewhere): both
// are skipped, and the adapter told nothing, when a DIFFERENT ACTIVE task
// holds the item now (cor:agt:120). That differs from a release, whose
// untrack is skipped while ANY task of the App, finished or cancelled
// included, still holds the id (cor:agt:120:04); the page keeps the two
// conditions apart.
func TestIntakeHowToStatesWhenReassignAndCancelAreSkipped3175(t *testing.T) {
	flat := strings.Join(strings.Fields(readHowTo3062(t)), " ")
	for _, tc := range []struct{ where, want, old string }{
		{"run step 2",
			"a reassignment calls `mark-tracked` again and a cancel calls `report` if you list it, each only when no different active task holds the item now",
			"a reassignment calls `mark-tracked` again, a release"},
		{"mark-tracked section",
			"After a reassignment the CLI calls `mark-tracked` again for the new worker, with only its `id` and `name`, unless a different active task holds the item now.",
			"with only its `id` and `name`. "},
		{"report section",
			"Called after a task holding the item was cancelled, and only if `describe` lists `report`, unless a different active task holds the item now.",
			"was cancelled, and only if `describe` lists `report`. "},
	} {
		if !strings.Contains(flat, tc.want) {
			t.Errorf("%s lacks:\n%s", tc.where, tc.want)
		}
		if strings.Contains(flat, tc.old) {
			t.Errorf("%s still has the unconditional wording %q", tc.where, tc.old)
		}
	}
	// The two conditions stay apart: the release's own condition is unchanged
	// and says ANY task, finished or cancelled included.
	if !strings.Contains(flat, "a release calls `untrack` only when no task of the App, finished or cancelled included, still holds the released id") {
		t.Error("run step 2 lost the release's any-state condition")
	}
	if !strings.Contains(flat, "and no task of the App holds it any more") {
		t.Error("the untrack section lost its condition")
	}
}
