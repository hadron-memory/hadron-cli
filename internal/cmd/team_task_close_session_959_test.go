package cmd

// hadron-cli#959 — the help teaches the one supported path for a task whose
// assignee's session must stay open: --no-close-session at CREATE. The
// close-when-done choice has no in-place setter, and same-party reassignment
// is refused without touching it (Chip's intake on #959), so the help says so
// rather than leaving reassign-to-self looking like a way to clear it. It
// does not advertise cancel-and-recreate as a remedy (Xylon's CHECKED,
// Ada #29224).

import (
	"strings"
	"testing"
)

func helpText(t *testing.T, verb string) string {
	t.Helper()
	_, out, err := runTeamTask(t, taskResponses(), "team", "task", verb, "--help")
	if err != nil {
		t.Fatalf("%s --help: %v", verb, err)
	}
	// Join the wrapped lines so a phrase is found across a line break.
	return strings.Join(strings.Fields(out), " ")
}

func TestC959CreateHelpStatesTheGracePeriodAndNoInPlaceChange(t *testing.T) {
	out := helpText(t, "create")
	for _, want := range []string{
		"grace period, 10 minutes by default, set per App",
		"cannot be changed in place",
		"pass --no-close-session at create",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("create help missing %q:\n%s", want, out)
		}
	}
	// The grace is per-App configurable (cor:agt:050:10, sessionEndGraceMinutes);
	// a flat "10-minute grace" states the default as if it were fixed (#982
	// Andi/Jack).
	if strings.Contains(out, "10-minute") {
		t.Errorf("create help states the default grace as fixed:\n%s", out)
	}
}

func TestC959AssignHelpRulesOutSamePartyReassignment(t *testing.T) {
	for _, verb := range []string{"assign", "reassign"} {
		out := helpText(t, verb)
		for _, want := range []string{
			"no in-place setter",
			"reassigning a task to its current assignee is refused",
			"--no-close-session at create",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("%s help missing %q:\n%s", verb, want, out)
			}
		}
	}
}

func TestC959HelpDoesNotAdvertiseRecreateAsARemedy(t *testing.T) {
	for _, verb := range []string{"create", "assign", "reassign"} {
		out := strings.ToLower(helpText(t, verb))
		for _, banned := range []string{"recreate", "re-create", "cancel and create", "cancel it and"} {
			if strings.Contains(out, banned) {
				t.Errorf("%s help advertises %q:\n%s", verb, banned, out)
			}
		}
	}
}
