package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

const c7Install = "hrn:app:acme.com:eng-team/hrn:agent:acme.com:support-bot"
const c7MemoryURN = "hrn:mem:acme.com:eng-team-support-bot"
const c7MemoryResponse = `{"data":{"installMemory":{"id":"imem1","urn":"` + c7MemoryURN + `","name":"Install memory"}}}`

func TestC7CustomizeReadsWithoutCreating(t *testing.T) {
	for _, body := range []string{c7MemoryResponse, `{"data":{"installMemory":null}}`} {
		t.Run(body, func(t *testing.T) {
			calls, out, err := runTeamTask(t, map[string]string{"InstallMemory": body}, "app", "agent", "customize", "hrn:app:acme.com:eng-team", "hrn:agent:acme.com:support-bot", "--json")
			if err != nil {
				t.Fatal(err)
			}
			c := c2Mutation(t, calls, "InstallMemory")
			if c.Vars["appRef"] != "hrn:app:acme.com:eng-team" || c.Vars["agentRef"] != "hrn:agent:acme.com:support-bot" {
				t.Fatalf("wrong lookup %s", c.RawVars)
			}
			if len(*calls) != 1 || !json.Valid([]byte(out)) {
				t.Fatalf("read created or wrong JSON: %v %s", *calls, out)
			}
			if strings.Contains(body, ":null") && !strings.Contains(out, `"memory": null`) {
				t.Fatalf("withheld/uncreated must remain null: %s", out)
			}
		})
	}
}

func TestC7CreateSendsInstallInsteadOfMemory(t *testing.T) {
	r := map[string]string{"CreateNode": `{"data":{"createNode":{"id":"n1","memoryId":"imem1","loc":"custom","name":"Custom","nodeType":"info","updatedAt":"2026-10-28T00:00:00Z"}}}`}
	calls, _, err := runTeamTask(t, r, "node", "add", "--install", c7Install, "--loc", "custom", "--name", "Custom")
	if err != nil {
		t.Fatal(err)
	}
	c := c2Mutation(t, calls, "CreateNode")
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(c.RawVars, &raw); err != nil {
		t.Fatal(err)
	}
	var input map[string]json.RawMessage
	if err := json.Unmarshal(raw["input"], &input); err != nil {
		t.Fatal(err)
	}
	if _, has := input["memoryId"]; has {
		t.Fatalf("install must omit memoryId: %s", c.RawVars)
	}
	if !strings.Contains(string(input["install"]), `"appRef":"hrn:app:acme.com:eng-team"`) || !strings.Contains(string(input["install"]), `"agentRef":"hrn:agent:acme.com:support-bot"`) {
		t.Fatalf("install lost: %s", c.RawVars)
	}
	if len(*calls) != 1 {
		t.Fatalf("create must let server create lazily, no pre-read: %v", *calls)
	}
}

func TestC7NullMemoryNeverBecomesAnUnscopedRead(t *testing.T) {
	for _, args := range [][]string{{"node", "get", "custom"}, {"node", "list"}, {"node", "update", "custom", "--name", "Changed"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			calls, _, err := runTeamTask(t, map[string]string{"InstallMemory": `{"data":{"installMemory":null}}`}, append(args, "--install", c7Install)...)
			if exitCodeFor(err) != 4 {
				t.Fatalf("null memory must refuse unavailable, got %v", err)
			}
			c2Mutation(t, calls, "InstallMemory")
			if len(*calls) != 1 {
				t.Fatalf("null read queried/mutated elsewhere: %v", *calls)
			}
		})
	}
}

func TestC7InvalidInstallSelectorsMakeNoRequests(t *testing.T) {
	cases := [][]string{
		{"node", "add", "--install", c7Install, "-m", c7MemoryURN, "--loc", "x", "--name", "X"},
		{"node", "list", "--install", ""},
		{"node", "get", "x", "--install", "eng-team/support-bot"},
		{"node", "get", "hrn:node:acme.com:other:x", "--install", c7Install},
		{"node", "list", "--install", c7Install, "--memory", ""},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			calls, _, err := runTeamTask(t, nil, args...)
			if exitCodeFor(err) != 2 || len(*calls) != 0 {
				t.Fatalf("invalid selector should exit2/no calls: err=%v calls=%v", err, *calls)
			}
		})
	}
}

func TestC7ExistingMemoryReadAndUpdateSelectors(t *testing.T) {
	cases := []struct {
		name string
		args []string
		op   string
	}{
		{"get", []string{"node", "get", "custom", "--body-only"}, "GetNodeRaw"},
		{"list", []string{"node", "list", "--json"}, "FindNodes"},
		{"update", []string{"node", "update", "custom", "--name", "Changed"}, "UpdateNode"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			responses := bodyOnlyStubs("stored text")
			responses["InstallMemory"] = c7MemoryResponse
			responses["FindNodes"] = `{"data":{"nodes":[]}}`
			calls, _, err := runTeamTask(t, responses, append(tc.args, "--install", c7Install)...)
			if err != nil {
				t.Fatal(err)
			}
			c2Mutation(t, calls, "InstallMemory")
			c := c2Mutation(t, calls, tc.op)
			if tc.name == "list" {
				filter, ok := c.Vars["filter"].(map[string]any)
				if !ok || !strings.Contains(stringMustJSON(t, filter["memoryIds"]), c7MemoryURN) {
					t.Fatalf("unscoped list %s", c.RawVars)
				}
			} else {
				resolve := c2Mutation(t, calls, "ResolveUrn")
				if resolve.Vars["urn"] != "hrn:node:acme.com:eng-team-support-bot:custom" {
					t.Fatalf("wrong node resolved %s", resolve.RawVars)
				}
			}
			if tc.name == "update" {
				input := c.Vars["input"].(map[string]any)
				if _, has := input["id"]; has {
					t.Fatalf("install selector must not send id %s", c.RawVars)
				}
				if _, has := input["memoryId"]; has {
					t.Fatalf("install selector must not send memoryId %s", c.RawVars)
				}
				if input["loc"] != "custom" || input["install"] == nil {
					t.Fatalf("missing native install/loc selector %s", c.RawVars)
				}
			}
		})
	}
}

func stringMustJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestC7NativeWriteRefusalsArePreserved(t *testing.T) {
	for code, want := range map[string]int{"FORBIDDEN": 8, "AGENT_NOT_INSTALLED": 2, "INSTALL_MEMORY_NOT_FOUND": 4, "INSTALL_TARGET_CHANGED": 5, "BAD_USER_INPUT": 2} {
		t.Run(code, func(t *testing.T) {
			responses := map[string]string{"CreateNode": `{"errors":[{"message":"concealed install refusal","extensions":{"code":"` + code + `"}}]}`}
			calls, _, err := runTeamTask(t, responses, "node", "add", "--install", c7Install, "--loc", "x", "--name", "X")
			if exitCodeFor(err) != want || err.Error() != "concealed install refusal" {
				t.Fatalf("wrong refusal: %v", err)
			}
			c2Mutation(t, calls, "CreateNode")
			if len(*calls) != 1 {
				t.Fatalf("refusal must not retry/create elsewhere: %v", *calls)
			}
		})
	}
}

// Install selectors must survive every existing kind door; no id-addressed
// fallback is permitted after the memory metadata read.
func TestC7EveryKindDoorPreservesInstall(t *testing.T) {
	for _, kind := range []string{"Node", "SpecNode", "ReviewNode", "TaskNode"} {
		t.Run(kind, func(t *testing.T) {
			flags := []string{}
			switch kind {
			case "SpecNode":
				flags = []string{"--role", "spec.rule"}
			case "ReviewNode":
				flags = []string{"--role", "review.check"}
			case "TaskNode":
				flags = []string{"--runnable"}
			}
			for _, verb := range []string{"Create", "Update"} {
				t.Run(verb, func(t *testing.T) {
					op := verb + kind
					field := strings.ToLower(verb[:1]) + verb[1:] + kind
					responses := bodyOnlyStubs("stored")
					responses["InstallMemory"] = c7MemoryResponse
					responses[op] = `{"data":{"` + field + `":` + nodeJSON + `}}`
					args := []string{"node", "add", "--loc", "custom", "--name", "Custom"}
					if verb == "Update" {
						args = []string{"node", "update", "custom", "--name", "Changed"}
					}
					args = append(args, flags...)
					args = append(args, "--install", c7Install)
					calls, _, err := runTeamTask(t, responses, args...)
					if err != nil {
						t.Fatal(err)
					}
					c := c2Mutation(t, calls, op)
					input := c.Vars["input"].(map[string]any)
					if input["install"] == nil || input["loc"] != "custom" {
						t.Fatalf("lost install target: %s", c.RawVars)
					}
					if _, ok := input["memoryId"]; ok {
						t.Fatalf("memory fallback: %s", c.RawVars)
					}
					if _, ok := input["id"]; ok {
						t.Fatalf("id fallback: %s", c.RawVars)
					}
					for _, call := range *calls {
						if call.Op == "UpdateNodeData" {
							t.Fatal("unexpected id-addressed merge")
						}
					}
					if verb == "Create" && len(*calls) != 1 {
						t.Fatalf("create must not pre-resolve: %v", *calls)
					}
				})
			}
		})
	}
}

func TestC7InstallMergeRefusesBeforeAnyRequests(t *testing.T) {
	for _, extra := range [][]string{nil, {"--name", "Changed"}} {
		args := []string{"node", "update", "custom", "--install", c7Install, "--data-merge", `{"k":1}`}
		responses := bodyOnlyStubs("stored")
		responses["InstallMemory"] = c7MemoryResponse
		responses["UpdateNodeData"] = `{"data":{"updateNodeData":` + nodeJSON + `}}`
		calls, _, err := runTeamTask(t, responses, append(args, extra...)...)
		if exitCodeFor(err) != 2 || len(*calls) != 0 || !strings.Contains(err.Error(), "--data") {
			t.Fatalf("must refuse before partial write: %v %v", err, *calls)
		}
	}
}

func TestC7InstallMergeFileRefusesBeforeReadingFileOrMakingRequests(t *testing.T) {
	calls, _, err := runTeamTask(t, nil, "node", "update", "custom", "--install", c7Install, "--data-merge-file", "/nonexistent-c7-merge.json", "--name", "Changed")
	if exitCodeFor(err) != 2 || len(*calls) != 0 || !strings.Contains(err.Error(), "--install cannot be combined") {
		t.Fatalf("expected early selector refusal: %v %v", err, *calls)
	}
}

func TestC7OrdinaryMemoryCreateStillSendsMemoryID(t *testing.T) {
	responses := map[string]string{"CreateNode": `{"data":{"createNode":` + nodeJSON + `}}`}
	calls, _, err := runTeamTask(t, responses, "node", "add", "-m", c7MemoryURN, "--loc", "custom", "--name", "Custom")
	if err != nil {
		t.Fatal(err)
	}
	c := c2Mutation(t, calls, "CreateNode")
	input := c.Vars["input"].(map[string]any)
	if input["memoryId"] != c7MemoryURN {
		t.Fatalf("legacy selector lost: %s", c.RawVars)
	}
	if _, ok := input["install"]; ok {
		t.Fatalf("unexpected install: %s", c.RawVars)
	}
	for _, args := range [][]string{
		{"node", "add", "--loc", "x", "--name", "X"},
		{"node", "add", "-m", "", "--loc", "x", "--name", "X"},
	} {
		calls, _, err := runTeamTask(t, nil, args...)
		if exitCodeFor(err) != 2 || len(*calls) != 0 {
			t.Fatalf("missing memory made requests: %v %v", err, *calls)
		}
	}
}
