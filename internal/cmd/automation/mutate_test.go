package automation

import "testing"

// cli#3114: the re-read command names the kind of selection, never what was typed.
func TestApprovalHintNeverRepeatsTheSelection3114(t *testing.T) {
	byRun := &selection{Trigger: triggerDTO{Ref: "run-typed"}}
	if got, want := approvalHint(byRun), "`hadron automation approval --run <run-id> --app <app-ref>`"; got != want {
		t.Errorf("run hint = %q, want %q", got, want)
	}
	byName := &selection{Trigger: triggerDTO{Ref: "r", Name: "typed"}}
	if got, want := approvalHint(byName), "`hadron automation approval <name> --app <app-ref>`"; got != want {
		t.Errorf("name hint = %q, want %q", got, want)
	}
}
