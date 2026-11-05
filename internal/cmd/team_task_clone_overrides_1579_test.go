package cmd

import (
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/hadron-memory/hadron-cli/internal/api"
)

// cli#1579: the six remaining cloneAgentTask overrides on `team task clone`
// (tags, references, properties, requirements, owner, project) and the two
// folded cli#1584 pins (matchReasonRE boundaries, api.ExtensionString branches).

// Per flag: the exact CloneAgentTaskInput field sent.
func TestTaskCloneOverrideWires1579(t *testing.T) {
	for name, tc := range map[string]struct {
		args []string
		key  string
		want string
	}{
		"tags":             {[]string{"--tag", "a", "--tag", "b"}, "tags", `["a","b"]`},
		"refs":             {[]string{"--ref", "issue=https://x/1|one", "--ref", "pull-request=https://x/2"}, "references", ""},
		"properties":       {[]string{"--properties", `{"k":1}`}, "properties", `{"k":1}`},
		"require-role":     {[]string{"--require-role", "qa"}, "requirements", `{"role":"qa"}`},
		"min-tier":         {[]string{"--min-tier", "mid"}, "requirements", `{"minTier":"mid"}`},
		"family-in":        {[]string{"--family-in", "claude,codex"}, "requirements", `{"familyIn":["claude","codex"]}`},
		"family-not-in":    {[]string{"--family-not-in", "glm"}, "requirements", `{"familyNotIn":["glm"]}`},
		"exclude-worker":   {[]string{"--exclude-worker", "Iris"}, "requirements", `{"excludeWorkers":["Iris"]}`},
		"owner worker":     {[]string{"--owner", "worker:Iris"}, "owner", `{"user":null,"worker":"Iris"}`},
		"project":          {[]string{"--project"}, "project", `true`},
		"clear tags":       {[]string{"--clear-tags"}, "tags", `[]`},
		"clear refs":       {[]string{"--clear-refs"}, "references", `[]`},
		"clear properties": {[]string{"--clear-properties"}, "properties", `null`},
		"clear reqs":       {[]string{"--clear-requirements"}, "requirements", `null`},
	} {
		t.Run(name, func(t *testing.T) {
			input, _ := cloneInput1565(t, append([]string{"AT0.12"}, tc.args...)...)
			got, ok := input[tc.key]
			if !ok {
				t.Fatalf("%s must be sent: %s", tc.key, input)
			}
			if tc.want != "" {
				if string(got) != tc.want {
					t.Fatalf("%s = %s, want %s", tc.key, got, tc.want)
				}
			} else if !strings.Contains(string(got), `"kind":"ISSUE"`) || !strings.Contains(string(got), `"label":"one"`) || !strings.Contains(string(got), `"kind":"PULL_REQUEST"`) {
				t.Fatalf("references = %s", got)
			}
			if len(input) != 1 {
				t.Fatalf("only the named override is sent: %s", input)
			}
		})
	}
}

// Per flag: an omitted flag sends no key (not null, not empty).
func TestTaskCloneOverrideKeysAbsentWhenOmitted1579(t *testing.T) {
	input, _ := cloneInput1565(t, "AT0.12", "--title", "x")
	for _, k := range []string{"tags", "references", "properties", "requirements", "owner", "project"} {
		if _, ok := input[k]; ok {
			t.Errorf("%s must be absent when its flag is omitted: %s", k, input)
		}
	}
}

// A value flag with its clear twin is exit 2 with nothing sent; so are the
// project contradictions and empty values.
func TestTaskCloneOverrideRefusedBeforeAnyCall1579(t *testing.T) {
	for name, args := range map[string][]string{
		"tag and clear-tags":          {"--tag", "a", "--clear-tags"},
		"ref and clear-refs":          {"--ref", "issue=x", "--clear-refs"},
		"properties and clear":        {"--properties", `{}`, "--clear-properties"},
		"requirement and clear":       {"--require-role", "qa", "--clear-requirements"},
		"empty tag":                   {"--tag", ""},
		"null properties":             {"--properties", "null"},
		"empty requirement":           {"--require-role", ""},
		"family-in and family-not-in": {"--family-in", "a", "--family-not-in", "b"},
		"bad owner":                   {"--owner", "Iris"},
		"empty owner":                 {"--owner", ""},
		"project and parent":          {"--project", "--parent", "AT2"},
		"project false":               {"--project=false"},
	} {
		t.Run(name, func(t *testing.T) {
			calls, _, err := runTeamTask(t, cloneResponses1565(), append([]string{"team", "task", "clone", "AT1.4"}, args...)...)
			if exitCodeFor(err) != 2 || len(callsOf1565(calls, "AgentTaskClone")) != 0 {
				t.Fatalf("err=%v calls=%v", err, *calls)
			}
		})
	}
}

func TestTaskCloneProjectWithNoParent1579(t *testing.T) {
	input, _ := cloneInput1565(t, "AT1.4", "--project", "--no-parent")
	if string(input["project"]) != "true" || string(input["parentTaskId"]) != "null" {
		t.Fatalf("project:true + parentTaskId:null expected, got %s", input)
	}
}

func TestTaskCloneOwnerRefusalMapsAsCreate1579(t *testing.T) {
	r := cloneResponses1565()
	r["AgentTaskClone"] = errorResponse1565("TASK_OWNER_INVALID", "ghost is not a possible owner in this App", map[string]any{"ref": "ghost"})
	_, _, err := runTeamTask(t, r, "team", "task", "clone", "AT0.12", "--owner", "worker:ghost")
	if exitCodeFor(err) != 2 || err == nil || !strings.Contains(err.Error(), "TASK_OWNER_INVALID") {
		t.Fatalf("err=%v exit=%d", err, exitCodeFor(err))
	}
}

// cor:agt:050:02 + :28 (server resolvers.agentTask.ts:1881-1890): a caller who
// may not set properties and names --properties gets the clone made WITHOUT
// them, never a refusal. The CLI maps no refusal and implies none.
func TestTaskClonePropertiesNoRightIsNoRefusal1579(t *testing.T) {
	calls, out, err := runTeamTask(t, cloneResponses1565(), "team", "task", "clone", "AT0.12", "--properties", `{"k":1}`)
	if err != nil {
		t.Fatalf("the clone succeeds with properties absent: %v", err)
	}
	if n := len(callsOf1565(calls, "AgentTaskClone")); n != 1 {
		t.Fatalf("one request expected, got %d", n)
	}
	if strings.Contains(out, "PROPERTIES_") || !strings.Contains(out, "cloned task") {
		t.Fatalf("output = %q", out)
	}
	_, help, _ := runTeamTask(t, cloneResponses1565(), "team", "task", "clone", "--help")
	flat := strings.Join(strings.Fields(help), " ")
	if strings.Contains(flat, "not exposed yet") {
		t.Error("help still says the overrides are not exposed")
	}
	if strings.Contains(flat, "may not set") && strings.Contains(flat, "properties") && strings.Contains(flat, "refused for lack") {
		t.Error("help must not claim a properties refusal for lack of right")
	}
	if !strings.Contains(flat, "without a refusal") {
		t.Error("help must say a caller who may not set properties gets the clone without them, without a refusal")
	}
}

// Folded cli#1584 P3 pins.
func TestMatchReasonBoundariesInTheBatchRefusal1579(t *testing.T) {
	p := writeBatch1565(t, distinctBatch1567)
	for name, reason := range map[string]string{
		"lowercase":    "nobody_free",
		"control char": "NOBODY\x1bFREE",
		"65 chars":     "A" + strings.Repeat("B", 64),
		"empty":        "",
	} {
		t.Run(name, func(t *testing.T) {
			r := batchResponses1565(2, "")
			r["AgentTaskCreateBatch"] = errorResponse1565("TASK_MATCH_FAILED", "no match", map[string]any{"position": 2, "reason": reason})
			_, _, err := runTeamTask(t, r, "team", "task", "create", "--batch-file", p, "--distinct-assignees")
			if err == nil || exitCodeFor(err) != 5 {
				t.Fatalf("err=%v", err)
			}
			if reason != "" && strings.Contains(err.Error(), reason) {
				t.Fatalf("the rejected reason must not be echoed: %v", err)
			}
		})
	}
	ok := "A" + strings.Repeat("B", 63) // exactly 64: the longest accepted
	r := batchResponses1565(2, "")
	r["AgentTaskCreateBatch"] = errorResponse1565("TASK_MATCH_FAILED", "no match", map[string]any{"position": 2, "reason": ok})
	_, _, err := runTeamTask(t, r, "team", "task", "create", "--batch-file", p, "--distinct-assignees")
	if err == nil || !strings.Contains(err.Error(), ok) {
		t.Fatalf("a 64-character code is accepted: %v", err)
	}
}

func TestExtensionStringBranches1579(t *testing.T) {
	ext := func(v any) gqlerror.List {
		return gqlerror.List{{Message: "x", Extensions: map[string]any{"reason": v}}}
	}
	if s, ok := api.ExtensionString(ext("NOBODY_FREE"), "reason"); !ok || s != "NOBODY_FREE" {
		t.Errorf("string: %q %t", s, ok)
	}
	for name, v := range map[string]any{"non-string": 3, "empty": "", "nil": nil} {
		if s, ok := api.ExtensionString(ext(v), "reason"); ok {
			t.Errorf("%s must not read as a string, got %q", name, s)
		}
	}
	if _, ok := api.ExtensionString(gqlerror.List{{Message: "x"}}, "reason"); ok {
		t.Error("a missing key must not read as a string")
	}
	multi := gqlerror.List{
		{Message: "a", Extensions: map[string]any{"reason": ""}},
		{Message: "b", Extensions: map[string]any{"reason": 7}},
		{Message: "c", Extensions: map[string]any{"reason": "SECOND"}},
		{Message: "d", Extensions: map[string]any{"reason": "THIRD"}},
	}
	if s, ok := api.ExtensionString(multi, "reason"); !ok || s != "SECOND" {
		t.Errorf("first non-empty across several errors: %q %t", s, ok)
	}
}
