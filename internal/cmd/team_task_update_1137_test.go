package cmd

// hadron-cli#1137 (server#2645, cor:agt:050:09) — `team task update` edits a
// task's descriptive fields. The server reads the input as a STRICT PATCH: a
// key that is present is applied (null clears), a key that is absent is left
// alone. So these tests read the wire bytes (RawVars), where an omitted key
// and an explicit null differ.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

const updatedTask1137 = `{"data":{"updateAgentTask":` + taskFieldsJSON + `}}`

func taskUpdateResponses1137() map[string]string {
	r := taskResponses()
	r["AgentTaskUpdate"] = updatedTask1137
	return r
}

// updateInput1137 runs `team task update` and returns the wire `input` object
// exactly as sent, plus the taskId variable.
func updateInput1137(t *testing.T, args ...string) (map[string]json.RawMessage, string) {
	t.Helper()
	calls, _, err := runTeamTask(t, taskUpdateResponses1137(), append([]string{"team", "task", "update"}, args...)...)
	if err != nil {
		t.Fatalf("update %v: %v", args, err)
	}
	var sent []attnCall
	for _, c := range *calls {
		if c.Op == "AgentTaskUpdate" {
			sent = append(sent, c)
		}
	}
	if len(sent) != 1 {
		t.Fatalf("AgentTaskUpdate requests = %d, want 1", len(sent))
	}
	var vars struct {
		TaskID string                     `json:"taskId"`
		Input  map[string]json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(sent[0].RawVars, &vars); err != nil {
		t.Fatal(err)
	}
	return vars.Input, vars.TaskID
}

func TestTaskUpdateWireIsAStrictPatch1137(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want map[string]string // key -> raw JSON; keys not listed must be ABSENT
	}{
		{"title only", []string{"--title", "New title"}, map[string]string{"title": `"New title"`}},
		{"description", []string{"--description", "why"}, map[string]string{"description": `"why"`}},
		{"clear description", []string{"--clear-description"}, map[string]string{"description": `null`}},
		{"tags replace", []string{"--tag", "a", "--tag", "b"}, map[string]string{"tags": `["a","b"]`}},
		{"clear tags", []string{"--clear-tags"}, map[string]string{"tags": `null`}},
		{"requirements replace", []string{"--require-role", "qa", "--family-in", "claude,codex", "--exclude-worker", "Iris", "--exclude-worker", "Uma"},
			map[string]string{"requirements": `{"excludeWorkers":["Iris","Uma"],"familyIn":["claude","codex"],"role":"qa"}`}},
		{"family-not-in alone", []string{"--family-not-in", "codex"}, map[string]string{"requirements": `{"familyNotIn":["codex"]}`}},
		{"clear requirements", []string{"--clear-requirements"}, map[string]string{"requirements": `null`}},
		{"start-by", []string{"--start-by", "2026-11-02T09:00:00Z"}, map[string]string{"startBy": `"2026-11-02T09:00:00Z"`}},
		{"clear start-by", []string{"--clear-start-by"}, map[string]string{"startBy": `null`}},
		{"close-session", []string{"--close-session"}, map[string]string{"closeSessionWhenDone": `true`}},
		{"no-close-session", []string{"--no-close-session"}, map[string]string{"closeSessionWhenDone": `false`}},
		{"several", []string{"--title", "T", "--clear-description", "--no-close-session"},
			map[string]string{"title": `"T"`, "description": `null`, "closeSessionWhenDone": `false`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input, id := updateInput1137(t, append([]string{"AT1.2"}, tc.args...)...)
			if id != "AT1.2" {
				t.Fatalf("taskId = %q, want the handle passed through", id)
			}
			if len(input) != len(tc.want) {
				t.Fatalf("input keys = %v, want exactly %v", keysOf1137(input), tc.want)
			}
			for k, want := range tc.want {
				got, ok := input[k]
				if !ok {
					t.Fatalf("key %q absent, want %s", k, want)
				}
				if string(got) != want {
					t.Fatalf("%s = %s, want %s", k, got, want)
				}
			}
		})
	}
}

func keysOf1137(m map[string]json.RawMessage) []string {
	out := []string{}
	for k, v := range m {
		out = append(out, k+"="+string(v))
	}
	return out
}

// An empty title is a supplied value (presence, not value): the server
// refuses it and the CLI passes that refusal through.
func TestTaskUpdateEmptyTitleIsSentNotDropped1137(t *testing.T) {
	input, _ := updateInput1137(t, "AT1.2", "--title", "")
	if string(input["title"]) != `""` || len(input) != 1 {
		t.Fatalf("input = %v, want title \"\" sent", keysOf1137(input))
	}
}

func TestTaskUpdateWithNoFlagIsRefusedLocally1137(t *testing.T) {
	calls, out, err := runTeamTask(t, taskUpdateResponses1137(), "team", "task", "update", "AT1.2")
	if exitCodeFor(err) != exitcode.Usage {
		t.Fatalf("no-flag update: %v", err)
	}
	if !strings.Contains(err.Error(), "at least one") {
		t.Fatalf("refusal should say what to pass: %v", err)
	}
	if out != "" {
		t.Fatalf("stdout: %q", out)
	}
	for _, c := range *calls {
		if c.Op == "AgentTaskUpdate" {
			t.Fatal("a request was sent")
		}
	}
}

func TestTaskUpdateContradictionsAreRefusedBeforeAnyRequest1137(t *testing.T) {
	for _, args := range [][]string{
		{"--description", "d", "--clear-description"},
		{"--tag", "a", "--clear-tags"},
		{"--require-role", "qa", "--clear-requirements"},
		{"--family-in", "claude", "--clear-requirements"},
		{"--family-not-in", "codex", "--clear-requirements"},
		{"--exclude-worker", "Iris", "--clear-requirements"},
		{"--family-in", "claude", "--family-not-in", "codex"},
		{"--start-by", "2026-11-02T09:00:00Z", "--clear-start-by"},
		{"--close-session", "--no-close-session"},
		{"--close-session=false"},
		{"--family-in", ","},
		{"--family-in", " , "},
		{"--family-not-in", ","},
		{"--require-role", "qa", "--family-in", ","},
		{"--exclude-worker", "Iris", "--family-not-in", ", ,"},
		{"--start-by", "2026-11-02T09:00:00+24:00"},
		{"--start-by", "2026-11-02T09:00:00+00:60"},
		{"--start-by", "2026-11-02T09:00:00,5Z"},
		{"--no-close-session=false"},
		{"--title", "T", "--close-session=false"},
		{"--title", "T", "--no-close-session=false"},
		{"--start-by", "tomorrow"},
		{"--tag", ""},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			calls, _, err := runTeamTask(t, taskUpdateResponses1137(), append([]string{"team", "task", "update", "AT1.2"}, args...)...)
			// An unknown subcommand is also a usage error: the refusal must be
			// the command's own, not the router's.
			if exitCodeFor(err) != exitcode.Usage || strings.Contains(err.Error(), "unknown command") || strings.Contains(err.Error(), "unknown flag") {
				t.Fatalf("want the update command's own usage error, got %v", err)
			}
			for _, c := range *calls {
				if c.Op == "AgentTaskUpdate" {
					t.Fatal("a request was sent")
				}
			}
		})
	}
}

func TestTaskUpdateRefusalsKeepTheServerCodeAndPrintNothing1137(t *testing.T) {
	for _, tc := range []struct {
		code string
		want int
	}{
		{"TASK_ENDED", exitcode.Conflict},
		{"TASK_NOT_OWNER", exitcode.Forbidden},
		{"TASK_NOT_FOUND", exitcode.NotFound},
		{"BAD_USER_INPUT", exitcode.Usage},
	} {
		t.Run(tc.code, func(t *testing.T) {
			r := taskUpdateResponses1137()
			r["AgentTaskUpdate"] = `{"errors":[{"message":"Exact server refusal.","extensions":{"code":"` + tc.code + `"}}]}`
			_, out, err := runTeamTask(t, r, "team", "task", "update", "AT1.2", "--title", "x")
			if err == nil || exitCodeFor(err) != tc.want {
				t.Fatalf("refusal: %v (exit %d, want %d)", err, exitCodeFor(err), tc.want)
			}
			if !strings.Contains(err.Error(), tc.code) || !strings.Contains(err.Error(), "Exact server refusal.") {
				t.Fatalf("server code/message lost: %v", err)
			}
			if !api.HasErrorCode(err, tc.code) {
				t.Fatalf("error chain lost %s", tc.code)
			}
			if out != "" {
				t.Fatalf("partial stdout: %q", out)
			}
		})
	}
}

func TestTaskUpdatePrintsTheTaskAsGetDoes1137(t *testing.T) {
	_, text, err := runTeamTask(t, taskUpdateResponses1137(), "team", "task", "update", "task1", "--title", "x")
	if err != nil {
		t.Fatal(err)
	}
	_, got, err := runTeamTask(t, taskUpdateResponses1137(), "team", "task", "get", "task1")
	if err != nil {
		t.Fatal(err)
	}
	if text != got {
		t.Fatalf("update text differs from task get:\nupdate:\n%s\nget:\n%s", text, got)
	}
	_, js, err := runTeamTask(t, taskUpdateResponses1137(), "--json", "team", "task", "update", "task1", "--title", "x")
	if err != nil {
		t.Fatal(err)
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(js), &dto); err != nil || dto["id"] != "task1" {
		t.Fatalf("--json: %v %s", err, js)
	}
}

func TestTaskUpdateHelp1137(t *testing.T) {
	out := helpText(t, "update")
	for _, want := range []string{
		"--tag replaces the whole list",
		"--require-role, --family-in, --family-not-in, --exclude-worker and --min-tier replace the whole requirements",
		"--start-by re-anchors only the not-started clock",
		"Only the flags you name are sent",
		"--close-session",
		"--no-close-session",
		"--clear-tags",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("update help missing %q:\n%s", want, out)
		}
	}
}

// With update in place the close-session choice IS changeable; create/assign
// help must stop saying it cannot be (#959's wording) and point to update.
func TestTaskUpdateSupersedesTheNoInPlaceChangeHelp1137(t *testing.T) {
	for _, verb := range []string{"create", "assign", "reassign"} {
		out := helpText(t, verb)
		if strings.Contains(out, "cannot be changed in place") || strings.Contains(out, "no in-place setter") {
			t.Errorf("%s help still says the choice cannot be changed:\n%s", verb, out)
		}
		if !strings.Contains(out, "task update") {
			t.Errorf("%s help does not point to task update:\n%s", verb, out)
		}
	}
}
