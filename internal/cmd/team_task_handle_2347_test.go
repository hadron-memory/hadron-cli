package cmd

import (
	"strings"
	"testing"
)

// #2347 (follow-ups to #2345): a malformed AT0 handle keeps the raw parent:
// line, and the tree tests cover a real project row and a hoisted child with
// its own subtask.

func TestTaskGetMalformedAT0HandleKeepsTheRawParent2347(t *testing.T) {
	for _, handle := range []string{"AT0.0", "AT0.01", "AT0.x", ""} {
		t.Run("handle="+handle, func(t *testing.T) {
			child := strings.Replace(at0ChildFieldsJSON1320, `"handle":"AT0.7"`, `"handle":"`+handle+`"`, 1)
			responses := taskResponses()
			responses["AgentTaskGet"] = `{"data":{"agentTask":` + child + `}}`
			_, out, err := runTeamTask(t, responses, "team", "task", "get", "task7")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, "  parent: at0id\n") || strings.Contains(out, "project: none") {
				t.Fatalf("a malformed handle %q must keep the raw parent: line, never \"project: none\":\n%s", handle, out)
			}
		})
	}
}

// A real project (AT1, id proj1) in the chain: AT1.2 nests under it, indented.
func TestTaskTreeRealProjectRowNestsItsChild2347(t *testing.T) {
	project := strings.NewReplacer(`"id":"at0id"`, `"id":"proj1"`, `"handle":"AT0"`, `"handle":"AT1"`, `"title":"No project"`, `"title":"Calendar"`).Replace(at0ProjectFieldsJSON1320)
	responses := taskResponses()
	responses["AgentTaskTree"] = `{"data":{"agentTaskTree":[` + project + `,` + realProjectChildFieldsJSON1320 + `]}}`
	_, out, err := runTeamTask(t, responses, "team", "task", "tree", "AT1.2")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "• AT1 proj1 [BACKLOG]") || !strings.Contains(out, "\n  • AT1.2 task12 [PENDING]") {
		t.Fatalf("AT1.2 must be indented under the real project AT1:\n%s", out)
	}
}

// A child of AT0 is hoisted to the root; its own subtask stays nested under it.
func TestTaskTreeHoistedChildKeepsItsSubtask2347(t *testing.T) {
	task5 := strings.NewReplacer(`"id":"task7"`, `"id":"task5"`, `"handle":"AT0.7"`, `"handle":"AT0.5"`).Replace(at0ChildFieldsJSON1320)
	responses := taskResponses()
	responses["AgentTaskTree"] = `{"data":{"agentTaskTree":[` + at0ProjectFieldsJSON1320 + `,` + task5 + `,` + at0SubtaskFieldsJSON1320 + `]}}`
	_, out, err := runTeamTask(t, responses, "team", "task", "tree", "AT0.5")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "No project") {
		t.Fatalf("the reserved row is not drawn:\n%s", out)
	}
	if !strings.Contains(out, "• AT0.5 task5 ") || strings.Contains(out, "\n  • AT0.5 ") || strings.HasPrefix(out, "  ") {
		t.Fatalf("AT0.5 is a root:\n%s", out)
	}
	if !strings.Contains(out, "\n  • AT0.5.3 task53 ") {
		t.Fatalf("its own subtask stays nested beneath it:\n%s", out)
	}
}
