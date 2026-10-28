package cmd

import (
 "encoding/json"
 "testing"

 "github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// A schema refresh must not introduce a new input key to existing task creates.
func TestTaskCreateOmitsUnsupportedProjectInput(t *testing.T) {
 calls, _, err := runTeamTask(t, taskResponses(), "team", "task", "create", "compatibility control", "--json")
 if err != nil { t.Fatal(err) }
 call := c2Mutation(t, calls, "AgentTaskCreate")
 input := call.Vars["input"].(map[string]any)
 if value, exists := input["project"]; exists { t.Fatalf("new field sent to old provider: project=%v, vars=%s", value, call.RawVars) }
 for _, value := range []bool{false, true} {
  raw, err := json.Marshal(gen.CreateAgentTaskInput{Title:"explicit control", Project:&value})
  if err != nil { t.Fatal(err) }
  var fields map[string]json.RawMessage
  if err := json.Unmarshal(raw, &fields); err != nil { t.Fatal(err) }
  want := "false"; if value { want="true" }
  if string(fields["project"]) != want { t.Fatalf("explicit input lost: %s", raw) }
 }
}
