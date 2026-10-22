package cmd

// hadron-cli#988 (R182, hadron-server#2249) — the server default for
// closeSessionWhenDone is now NO: a task ends the assignee's worker session
// only when the assigner asks. create gains --close-session (the yes), keeps
// --no-close-session as an explicit no, and the help teaches the new default.

import (
	"os"
	"strings"
	"testing"
)

func TestC988CreateMapsCloseSessionFlags(t *testing.T) {
	cases := []struct {
		name string
		flag []string
		want any // nil = key absent or null on the wire (server default applies)
	}{
		{"neither", nil, nil},
		{"close", []string{"--close-session"}, true},
		{"no-close", []string{"--no-close-session"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			argv := append([]string{"team", "task", "create", "T"}, tc.flag...)
			calls, _, err := runTeamTask(t, taskResponses(), argv...)
			if err != nil {
				t.Fatal(err)
			}
			for _, c := range *calls {
				if c.Op == "AgentTaskCreate" {
					input, _ := c.Vars["input"].(map[string]any)
					if got := input["closeSessionWhenDone"]; got != tc.want {
						t.Fatalf("closeSessionWhenDone %v, want %v", got, tc.want)
					}
					return
				}
			}
			t.Fatal("no AgentTaskCreate call")
		})
	}
}

func TestC988CreateRefusesContradictingFlags(t *testing.T) {
	calls, _, err := runTeamTask(t, taskResponses(),
		"team", "task", "create", "T", "--close-session", "--no-close-session")
	if err == nil || !strings.Contains(err.Error(), "contradict") {
		t.Fatalf("contradicting flags accepted: %v", err)
	}
	for _, c := range *calls {
		if c.Op == "AgentTaskCreate" {
			t.Fatal("create sent despite contradicting flags")
		}
	}
}

func TestC988HelpTeachesTheNewDefault(t *testing.T) {
	for _, verb := range []string{"create", "assign", "reassign"} {
		out := helpText(t, verb)
		for _, want := range []string{
			"finishing or cancelling does not ask the assignee to end its worker session",
			"pass --close-session at create",
			"or on the assign that first names the assignee of a queued task",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("%s help missing %q:\n%s", verb, want, out)
			}
		}
		if strings.Contains(out, "By default finishing or cancelling asks") ||
			strings.Contains(out, "pass --no-close-session at create") {
			t.Errorf("%s help still teaches the old default:\n%s", verb, out)
		}
	}
}

func TestC988AgenticUsageDocumentsCloseSession(t *testing.T) {
	data, err := os.ReadFile("agentic/agentic-usage.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "hadron team task create ") {
			if !strings.Contains(strings.SplitN(line, " | assign", 2)[0], "[--close-session|--no-close-session]") {
				t.Error("agentic-usage create synopsis lacks [--close-session|--no-close-session]")
			}
			return
		}
	}
	t.Error("agentic-usage has no `hadron team task create` synopsis line")
}

// The no-ask default is a creation default: assign/reassign with neither flag
// keep the stored value (Jane/Andi P3s on #989).
func TestC988AssignHelpQualifiesTheDefaultAsCreationOnly(t *testing.T) {
	for _, verb := range []string{"assign", "reassign"} {
		out := helpText(t, verb)
		if !strings.Contains(out, "The default of not asking applies at creation") {
			t.Errorf("%s help does not qualify the default:\n%s", verb, out)
		}
	}
}

func TestC988AgenticUsageStatesTheDefaultAndPreserve(t *testing.T) {
	data, err := os.ReadFile("agentic/agentic-usage.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "close-when-done defaults to NO at create") {
		t.Error("agentic-usage does not state the create default")
	}
}
