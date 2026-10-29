package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

func priorityTask1135() string {
	return strings.Replace(taskFieldsJSON, `"status":"PENDING"`, `"priority":2,"blocked":true,"blockedBy":{"items":[{"id":"b1","handle":"AT1.2","title":"Dependency","status":"PENDING"}],"total":1,"hasMore":false},"blocks":{"items":[],"total":0,"hasMore":false},"status":"PENDING"`, 1)
}
func TestTaskPriorityCreateAndGet1135(t *testing.T) {
	srv, calls := captureGraphQL(t, map[string]string{"AgentTaskCreate": `{"data":{"createAgentTask":` + priorityTask1135() + `}}`})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "task", "create", "Dependent", "--app", "capp100000000000000000000", "--priority", "2", "--blocked-by", "AT1.2", "--blocked-by", "AT1.3", "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var vars struct{ Input map[string]json.RawMessage }
	if err := json.Unmarshal(calls["AgentTaskCreate"], &vars); err != nil {
		t.Fatal(err)
	}
	if string(vars.Input["priority"]) != "2" || string(vars.Input["blockedBy"]) != `["AT1.2","AT1.3"]` {
		t.Fatalf("variables:%s", calls["AgentTaskCreate"])
	}
	var dto map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatal(err)
	}
	if string(dto["priority"]) != "2" || string(dto["blocked"]) != "true" || !strings.Contains(string(dto["blockedBy"]), "AT1.2") {
		t.Fatal(out.String())
	}
}
func TestTaskPriorityCommands1135(t *testing.T) {
	for _, tc := range []struct {
		args      []string
		op, field string
	}{
		{[]string{"set-priority", "AT1.4", "3"}, "AgentTaskSetPriority", "setAgentTaskPriority"},
		{[]string{"add-blocker", "AT1.4", "AT1.2"}, "AgentTaskAddBlocker", "addAgentTaskBlocker"},
		{[]string{"remove-blocker", "AT1.4", "AT1.2"}, "AgentTaskRemoveBlocker", "removeAgentTaskBlocker"},
	} {
		t.Run(tc.op, func(t *testing.T) {
			srv, calls := captureGraphQL(t, map[string]string{tc.op: `{"data":{"` + tc.field + `":` + priorityTask1135() + `}}`})
			f, _ := testFactory(t)
			r := NewRootCmd(f)
			r.SetArgs(append(append([]string{"team", "task"}, tc.args...), "--server", srv.URL))
			if err := r.Execute(); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(calls[tc.op]), `"taskRef":"AT1.4"`) {
				t.Fatal(calls)
			}
		})
	}
}
