package cmd

// hadron-cli#1118 (server#2420, cor:agt:050:28) — `--properties <json|@file>` /
// `--clear-properties` on `team task create|update`, and `properties` on
// `task get`. The server reads an omitted key as "leave it" and an explicit
// null as "clear", so the wire bytes (RawVars) are what is asserted.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

const propsObject1118 = `{"review":{"tier":"T1","seats":["blind","local"]},"complexity":3}`

// withProperties1118 is taskFieldsJSON carrying a properties object.
func withProperties1118(props string) string {
	return strings.Replace(taskFieldsJSON, `"result":null}`, `"result":null,"properties":`+props+`}`, 1)
}

func taskPropsResponses1118(props string) map[string]string {
	r := taskResponses()
	r["AgentTaskUpdate"] = `{"data":{"updateAgentTask":` + withProperties1118(props) + `}}`
	r["AgentTaskCreate"] = `{"data":{"createAgentTask":` + withProperties1118(props) + `}}`
	r["AgentTaskGet"] = `{"data":{"agentTask":` + withProperties1118(props) + `}}`
	return r
}

func sentInput1118(t *testing.T, op string, calls *[]attnCall) map[string]json.RawMessage {
	t.Helper()
	var sent []attnCall
	for _, c := range *calls {
		if c.Op == op {
			sent = append(sent, c)
		}
	}
	if len(sent) != 1 {
		t.Fatalf("%s requests = %d, want 1", op, len(sent))
	}
	var vars struct {
		Input map[string]json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(sent[0].RawVars, &vars); err != nil {
		t.Fatal(err)
	}
	return vars.Input
}

func TestTaskUpdatePropertiesWire1118(t *testing.T) {
	file := filepath.Join(t.TempDir(), "props.json")
	if err := os.WriteFile(file, []byte("\n  "+propsObject1118+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		args []string
		want string // the raw "properties" key; "" means it must be ABSENT
	}{
		{"object replaces", []string{"--properties", propsObject1118}, propsObject1118},
		{"from a file", []string{"--properties", "@" + file}, propsObject1118},
		{"clear sends null", []string{"--clear-properties"}, `null`},
		{"empty object is a value, not a clear", []string{"--properties", "{}"}, `{}`},
		{"omitted flag leaves it absent", []string{"--title", "T"}, ``},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls, _, err := runTeamTask(t, taskPropsResponses1118(`null`), append([]string{"team", "task", "update", "AT1.2"}, tc.args...)...)
			if err != nil {
				t.Fatal(err)
			}
			input := sentInput1118(t, "AgentTaskUpdate", calls)
			got, present := input["properties"]
			if tc.want == "" {
				if present {
					t.Fatalf("properties must be omitted, got %s", got)
				}
				return
			}
			if !present || string(got) != tc.want {
				t.Fatalf("properties = %s (present %v), want %s", got, present, tc.want)
			}
		})
	}
}

func TestTaskPropertiesUsageRefusalsSendNothing1118(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.json")
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"set and clear", []string{"--properties", "{}", "--clear-properties"}},
		{"not json", []string{"--properties", "{review:"}},
		{"empty value", []string{"--properties", "  "}},
		{"literal null points at --clear-properties", []string{"--properties", "null"}},
		{"bare at sign", []string{"--properties", "@"}},
		{"unreadable file", []string{"--properties", "@" + missing}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls, _, err := runTeamTask(t, taskPropsResponses1118(`null`), append([]string{"team", "task", "update", "AT1.2"}, tc.args...)...)
			if exitCodeFor(err) != exitcode.Usage || strings.Contains(err.Error(), "unknown flag") {
				t.Fatalf("want the command's own usage error, got %v", err)
			}
			for _, c := range *calls {
				if c.Op == "AgentTaskUpdate" {
					t.Fatal("a request was sent")
				}
			}
		})
	}
	// The same check guards create.
	calls, _, err := runTeamTask(t, taskPropsResponses1118(`null`), "team", "task", "create", "T", "--properties", "{oops")
	if exitCodeFor(err) != exitcode.Usage || strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("create: want usage error, got %v", err)
	}
	for _, c := range *calls {
		if c.Op == "AgentTaskCreate" {
			t.Fatal("create sent a request")
		}
	}
}

func TestTaskCreatePropertiesWire1118(t *testing.T) {
	calls, _, err := runTeamTask(t, taskPropsResponses1118(propsObject1118), "team", "task", "create", "T", "--properties", propsObject1118)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(sentInput1118(t, "AgentTaskCreate", calls)["properties"]); got != propsObject1118 {
		t.Fatalf("create properties = %s", got)
	}
	// Omitted flag: the key is absent, never null.
	calls, _, err = runTeamTask(t, taskPropsResponses1118(`null`), "team", "task", "create", "T")
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := sentInput1118(t, "AgentTaskCreate", calls)["properties"]; ok {
		t.Fatalf("an unnamed --properties must be omitted, sent %s", got)
	}
}

func TestTaskGetShowsProperties1118(t *testing.T) {
	_, out, err := runTeamTask(t, taskPropsResponses1118(propsObject1118), "team", "task", "get", "task1", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Properties json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	// --json is indented as a whole; compare the value, not its whitespace.
	var compact bytes.Buffer
	if err := json.Compact(&compact, got.Properties); err != nil || compact.String() != propsObject1118 {
		t.Fatalf("--json properties = %s (%v)", got.Properties, err)
	}
	// Unset is an explicit null in the contract, not a missing key.
	_, out, err = runTeamTask(t, taskPropsResponses1118(`null`), "team", "task", "get", "task1", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatal(err)
	}
	if v, ok := raw["properties"]; !ok || string(v) != "null" {
		t.Fatalf("unset properties = %s (present %v), want null", v, ok)
	}
	_, text, err := runTeamTask(t, taskPropsResponses1118(propsObject1118), "team", "task", "get", "task1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "  properties: "+propsObject1118) {
		t.Fatalf("text row missing:\n%s", text)
	}
	// An unset value prints no row.
	_, text, _ = runTeamTask(t, taskPropsResponses1118(`null`), "team", "task", "get", "task1")
	if strings.Contains(text, "properties:") {
		t.Fatalf("unset value must print no row:\n%s", text)
	}
}

func TestTaskPropertiesRefusalsRenderCodeAndExit1118(t *testing.T) {
	for _, tc := range []struct {
		code string
		want int
	}{
		{"PROPERTIES_NOT_AN_OBJECT", exitcode.Usage},
		{"PROPERTIES_TOO_LARGE", exitcode.Usage},
		{"PROPERTIES_SCHEMA_VIOLATION", exitcode.Usage},
		{"PROPERTIES_SCHEMA_UNUSABLE", exitcode.Conflict},
		{"TASK_ENDED", exitcode.Conflict},
		// An assignee combining --properties with another field.
		{"TASK_NOT_OWNER", exitcode.Forbidden},
	} {
		for _, verb := range []string{"update", "create"} {
			t.Run(tc.code+" "+verb, func(t *testing.T) {
				r := taskPropsResponses1118(`null`)
				op := "AgentTaskUpdate"
				args := []string{"team", "task", "update", "AT1.2", "--properties", "{}"}
				if verb == "create" {
					op, args = "AgentTaskCreate", []string{"team", "task", "create", "T", "--properties", "{}"}
				}
				r[op] = `{"errors":[{"message":"Exact server refusal.","extensions":{"code":"` + tc.code + `"}}]}`
				_, out, err := runTeamTask(t, r, args...)
				if err == nil || exitCodeFor(err) != tc.want {
					t.Fatalf("%v (exit %d, want %d)", err, exitCodeFor(err), tc.want)
				}
				if !strings.Contains(err.Error(), tc.code) || !strings.Contains(err.Error(), "Exact server refusal.") {
					t.Fatalf("code or server message lost: %v", err)
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
}
