package cmd

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// cli#1283 (CHECKED T1): start, progress, cancel, park and ready surface the
// server's response warnings exactly as create/assign/reassign/finish do
// (cli#962) — one stderr line each in text mode, an always-present `warnings`
// array in --json — with the stdout receipt and the exit code unchanged.
// task.cancel.open-children is the warning observed live and dropped.

var c1283Mutations = []struct {
	name string
	argv []string
	op   string
	key  string
}{
	{"start", []string{"team", "task", "start", "task1"}, "AgentTaskStart", "startAgentTask"},
	{"progress", []string{"team", "task", "progress", "task1"}, "AgentTaskProgress", "recordAgentTaskProgress"},
	{"cancel", []string{"team", "task", "cancel", "task1"}, "AgentTaskCancel", "cancelAgentTask"},
	{"park", []string{"team", "task", "park", "task1"}, "AgentTaskPark", "parkAgentTask"},
	{"ready", []string{"team", "task", "ready", "task1"}, "AgentTaskReady", "makeAgentTaskReady"},
}

func TestC1283CancelOpenChildrenWarning(t *testing.T) {
	w := `[{"path":"cancelAgentTask","code":"task.cancel.open-children","text":"This task still has open child tasks.","register":{"state":"unmapped"}}]`
	argv := []string{"team", "task", "cancel", "task1"}
	baseOut, _, err := runTeamTaskStreams(t, taskResponses(), argv...)
	if err != nil {
		t.Fatal(err)
	}
	out, errOut, err := runTeamTaskStreams(t, c962With("AgentTaskCancel", w), argv...)
	if err != nil {
		t.Fatalf("a warning changed the exit: %v", err)
	}
	if out != baseOut {
		t.Fatalf("stdout receipt changed:\n got %q\nwant %q", out, baseOut)
	}
	if want := "warning: [task.cancel.open-children] This task still has open child tasks. (no task mapped)\n"; errOut != want {
		t.Fatalf("stderr %q\nwant   %q", errOut, want)
	}

	jsonOut, jsonErr, err := runTeamTaskStreams(t, c962With("AgentTaskCancel", w), append(argv, "--json")...)
	if err != nil {
		t.Fatal(err)
	}
	if jsonErr != "" {
		t.Errorf("--json wrote stderr: %q", jsonErr)
	}
	var dto struct {
		ID       string            `json:"id"`
		Warnings []c962WarningJSON `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &dto); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, jsonOut)
	}
	want := []c962WarningJSON{{Code: "task.cancel.open-children", Text: "This task still has open child tasks.", Register: map[string]any{"state": "unmapped", "taskLoc": nil, "taskUrn": nil}}}
	if dto.ID == "" || !reflect.DeepEqual(dto.Warnings, want) {
		t.Fatalf("got id %q warnings %+v\nwant %+v", dto.ID, dto.Warnings, want)
	}
}

func TestC1283SimpleVerbsSurfaceWarnings(t *testing.T) {
	for _, m := range c1283Mutations {
		t.Run(m.name, func(t *testing.T) {
			baseOut, _, err := runTeamTaskStreams(t, taskResponses(), m.argv...)
			if err != nil {
				t.Fatal(err)
			}
			out, errOut, err := runTeamTaskStreams(t, c962With(m.op, c962Warnings(m.key)), m.argv...)
			if err != nil {
				t.Fatalf("a warning changed the exit: %v", err)
			}
			if out != baseOut {
				t.Fatalf("stdout receipt changed:\n got %q\nwant %q", out, baseOut)
			}
			want := "warning: [task.assign.assignee-busy] The assignee already has an open task. → hrn:node:acme.com:eng-system:tasks:rebalance\n" +
				"warning: [task.future.unknown] A code this CLI has never seen.\n"
			if errOut != want {
				t.Fatalf("stderr %q\nwant   %q", errOut, want)
			}

			jsonOut, _, err := runTeamTaskStreams(t, c962With(m.op, c962Warnings(m.key)), append(m.argv, "--json")...)
			if err != nil {
				t.Fatal(err)
			}
			var dto struct {
				ID       string            `json:"id"`
				Warnings []c962WarningJSON `json:"warnings"`
			}
			if err := json.Unmarshal([]byte(jsonOut), &dto); err != nil {
				t.Fatalf("not JSON: %v\n%s", err, jsonOut)
			}
			if dto.ID == "" || len(dto.Warnings) != 2 || dto.Warnings[1].Code != "task.future.unknown" {
				t.Fatalf("--json %s", jsonOut)
			}
		})
	}
}

func TestC1283SimpleVerbsAbsentWarningsAreEmpty(t *testing.T) {
	for _, m := range c1283Mutations {
		t.Run(m.name, func(t *testing.T) {
			out, errOut, err := runTeamTaskStreams(t, taskResponses(), append(m.argv, "--json")...)
			if err != nil {
				t.Fatal(err)
			}
			var dto map[string]any
			if err := json.Unmarshal([]byte(out), &dto); err != nil {
				t.Fatalf("not JSON: %v\n%s", err, out)
			}
			if list, ok := dto["warnings"].([]any); !ok || len(list) != 0 {
				t.Fatalf("warnings must be [] when absent, got %v in %s", dto["warnings"], out)
			}
			if strings.Contains(errOut, "warning:") {
				t.Fatalf("stderr %q", errOut)
			}
		})
	}
}
