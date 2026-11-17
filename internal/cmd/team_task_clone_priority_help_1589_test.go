package cmd

import (
	"strings"
	"testing"
)

func TestTaskClonePriorityHelpInherits1589(t *testing.T) {
	_, help, err := runTeamTask(t, cloneResponses1565(), "team", "task", "clone", "--help")
	if err != nil {
		t.Fatal(err)
	}
	flat := strings.Join(strings.Fields(help), " ")
	if !strings.Contains(flat, "omitted: inherit the source priority") {
		t.Fatalf("help must describe inherited priority, got:\n%s", help)
	}
	if strings.Contains(flat, "(default 1)") || strings.Contains(flat, "(default 0)") {
		t.Fatalf("priority has no fixed numeric default:\n%s", help)
	}
}

func TestTaskClonePriorityOmissionAndExplicitWire1589(t *testing.T) {
	input, _ := cloneInput1565(t, "AT0.12")
	if _, ok := input["priority"]; ok {
		t.Fatalf("omission must inherit, not send a default: %s", input)
	}
	for _, priority := range []string{"1", "5", "0"} {
		input, _ = cloneInput1565(t, "AT0.12", "--priority", priority)
		if string(input["priority"]) != priority {
			t.Fatalf("explicit %s must be sent for the server to judge, got %s", priority, input["priority"])
		}
	}
}
