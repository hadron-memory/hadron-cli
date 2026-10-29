package cmd

import (
	"encoding/json"
	"fmt"
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

func TestTaskPriorityDefaultOmissionAndBoundActor1135(t *testing.T) {
	for _, tc := range []struct {
		args      []string
		op, field string
	}{
		{[]string{"create", "ordinary"}, "AgentTaskCreate", "createAgentTask"},
		{[]string{"set-priority", "AT1.4", "1"}, "AgentTaskSetPriority", "setAgentTaskPriority"},
		{[]string{"add-blocker", "AT1.4", "AT1.2"}, "AgentTaskAddBlocker", "addAgentTaskBlocker"},
		{[]string{"remove-blocker", "AT1.4", "AT1.2"}, "AgentTaskRemoveBlocker", "removeAgentTaskBlocker"},
	} {
		t.Run(tc.op, func(t *testing.T) {
			r := taskResponses()
			r[tc.op] = `{"data":{"` + tc.field + `":` + priorityTask1135() + `}}`
			calls, _, err := runTeamTask(t, r, append([]string{"team", "task"}, tc.args...)...)
			if err != nil {
				t.Fatal(err)
			}
			found := 0
			for _, c := range *calls {
				if c.Op == tc.op {
					found++
					if c.Session != "s-new" {
						t.Fatalf("lost actor:%+v", c)
					}
					if tc.op == "AgentTaskCreate" {
						in := c.Vars["input"].(map[string]any)
						for _, key := range []string{"priority", "blockedBy", "project"} {
							if _, ok := in[key]; ok {
								t.Fatalf("omission changed:%s", c.RawVars)
							}
						}
					}
				}
			}
			if found != 1 {
				t.Fatalf("writes=%d", found)
			}
		})
	}
}
func TestTaskPriorityRefusalsHaveCodeAndNoOutput1135(t *testing.T) {
	for _, tc := range []struct {
		args     []string
		op, code string
		want     int
	}{
		{[]string{"set-priority", "AT1.1", "6"}, "AgentTaskSetPriority", "BAD_USER_INPUT", 2},
		{[]string{"set-priority", "AT1.1", "0"}, "AgentTaskSetPriority", "BAD_USER_INPUT", 2},
		{[]string{"add-blocker", "AT1.1", "AT1.2"}, "AgentTaskAddBlocker", "TASK_BLOCKER_CYCLE", 5},
		{[]string{"add-blocker", "AT1.1", "foreign"}, "AgentTaskAddBlocker", "TASK_NOT_FOUND", 4},
		{[]string{"remove-blocker", "AT1.1", "AT1.2"}, "AgentTaskRemoveBlocker", "TASK_INVALID_TRANSITION", 5},
		{[]string{"start", "AT1.1"}, "AgentTaskStart", "TASK_BLOCKED", 5},
		{[]string{"create", "new", "--priority", "2"}, "AgentTaskCreate", "TASK_NOT_OWNER", 8},
	} {
		t.Run(tc.op+tc.code+strings.Join(tc.args, "-"), func(t *testing.T) {
			r := taskResponses()
			r[tc.op] = `{"errors":[{"message":"Exact server refusal.","extensions":{"code":"` + tc.code + `"}}]}`
			calls, out, err := runTeamTask(t, r, append([]string{"team", "task"}, tc.args...)...)
			if exitCodeFor(err) != tc.want || !strings.Contains(err.Error(), tc.code) || !strings.Contains(err.Error(), "Exact server refusal.") {
				t.Fatalf("refusal:%v", err)
			}
			if out != "" {
				t.Fatalf("partial stdout:%s", out)
			}
			n := 0
			for _, c := range *calls {
				if c.Op == tc.op {
					n++
				}
			}
			if n != 1 {
				t.Fatalf("mutation requests:%d", n)
			}
		})
	}
}
func TestTaskPriorityGetAndListRenderReturnedOrder1135(t *testing.T) {
	for _, jsonMode := range []bool{false, true} {
		for _, cmd := range []string{"get", "list"} {
			t.Run(cmd+fmt.Sprint(jsonMode), func(t *testing.T) {
				r := taskResponses()
				r["AgentTaskGet"] = `{"data":{"agentTask":` + priorityTask1135() + `}}`
				r["AgentTaskList"] = `{"data":{"agentTasks":[` + priorityTask1135() + `,` + strings.Replace(priorityTask1135(), `"id":"task1"`, `"id":"task2"`, 1) + `]}}`
				args := []string{"team", "task", cmd}
				if cmd == "get" {
					args = append(args, "AT1.4")
				}
				if jsonMode {
					args = append(args, "--json")
				}
				_, out, err := runTeamTask(t, r, args...)
				if err != nil {
					t.Fatal(err)
				}
				for _, w := range []string{"2", "AT1.2"} {
					if !strings.Contains(out, w) {
						t.Fatalf("missing%s:%s", w, out)
					}
				}
				if jsonMode {
					for _, w := range []string{`"priority": 2`, `"blocked": true`, `"items": []`, `"hasMore": false`} {
						if !strings.Contains(out, w) {
							t.Fatalf("missing%s:%s", w, out)
						}
					}
				} else if !strings.Contains(strings.ToLower(out), "blocked") {
					t.Fatal(out)
				}
				if cmd == "list" && strings.Index(out, "task1") > strings.Index(out, "task2") {
					t.Fatalf("server order changed:%s", out)
				}
			})
		}
	}
}
func TestTaskDependenciesPageForwarding1135(t *testing.T) {
	r := taskResponses()
	r["AgentTaskDependencies"] = `{"data":{"agentTask":{"id":"task1","blockedBy":{"items":[],"total":21,"hasMore":false},"blocks":{"items":[],"total":0,"hasMore":false}}}}`
	calls, out, err := runTeamTask(t, r, "team", "task", "dependencies", "AT1.4", "--limit", "1", "--offset", "20", "--json")
	if err != nil {
		t.Fatal(err)
	}
	c := c2Mutation(t, calls, "AgentTaskDependencies")
	if c.Vars["taskRef"] != "AT1.4" || c.Vars["limit"] != float64(1) || c.Vars["offset"] != float64(20) {
		t.Fatal(c.Vars)
	}
	if !strings.Contains(out, `"total": 21`) || !strings.Contains(out, `"items": []`) {
		t.Fatal(out)
	}
}

func TestTaskPriorityBadLocalInputDoesNotWrite1135(t *testing.T) {
	for _, args := range [][]string{
		{"create", "new", "--blocked-by="},
		{"create", "new", "--blocked-by", "  "},
		{"set-priority", "AT1.2", "not-an-integer"},
		{"add-blocker", "AT1.2", "  "},
		{"remove-blocker", "  ", "AT1.2"},
		{"dependencies", "AT1.2", "--offset", "-1"},
	} {
		t.Run(strings.Join(args, "-"), func(t *testing.T) {
			calls, out, err := runTeamTask(t, taskResponses(), append([]string{"team", "task"}, args...)...)
			if exitCodeFor(err) != 2 || out != "" {
				t.Fatalf("out=%q err=%v", out, err)
			}
			for _, c := range *calls {
				if strings.Contains(c.Op, "Create") || strings.Contains(c.Op, "Priority") || strings.Contains(c.Op, "Blocker") {
					t.Fatalf("unexpected write: %+v", c)
				}
			}
		})
	}
}
