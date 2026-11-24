package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// cli#3124 (hadron-server#5080): the batch file's task object is
// CreateAgentTaskInput by its API names, and parentTaskId is now parentTaskRef,
// with no alias. A file that still says parentTaskId is refused before any
// call (exit 2) with a message that names the new key; a file that says
// parentTaskRef is sent unchanged.
func TestTaskBatchParentTaskRefIsSentUnchanged3124(t *testing.T) {
	p := writeBatch1565(t, `[{"task":{"title":"child","parentTaskRef":"AT0.5"}}]`)
	calls, _, err := runTeamTask(t, batchResponses1565(1, ""), "team", "task", "create", "--batch-file", p)
	if err != nil {
		t.Fatal(err)
	}
	call := c2Mutation(t, calls, "AgentTaskCreateBatch")
	var v batchVars1565
	if err := json.Unmarshal(call.RawVars, &v); err != nil {
		t.Fatal(err)
	}
	if len(v.Tasks) != 1 || string(v.Tasks[0]) != `{"task":{"parentTaskRef":"AT0.5","title":"child"}}` &&
		string(v.Tasks[0]) != `{"task":{"title":"child","parentTaskRef":"AT0.5"}}` {
		t.Fatalf("parentTaskRef must reach the server unchanged: %s", call.RawVars)
	}
}

func TestTaskBatchRefusesTheRenamedParentTaskIdAndNamesTheNewKey3124(t *testing.T) {
	p := writeBatch1565(t, `[{"task":{"title":"ok"}},{"task":{"title":"child","parentTaskId":"AT0.5"}}]`)
	calls, _, err := runTeamTask(t, batchResponses1565(2, ""), "team", "task", "create", "--batch-file", p)
	if exitCodeFor(err) != 2 || len(callsOf1565(calls, "AgentTaskCreateBatch")) != 0 {
		t.Fatalf("want exit 2 and no call: err=%v exit=%d calls=%v", err, exitCodeFor(err), *calls)
	}
	for _, want := range []string{"task 2", "parentTaskId", "parentTaskRef", "renamed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must contain %q: %v", want, err)
		}
	}
}
