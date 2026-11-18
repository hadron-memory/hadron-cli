package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api"
)

func priorityTask1135() string {
	return strings.Replace(taskFieldsJSON, `"status":"PENDING"`, `"priority":2,"blocked":true,"blockedBy":{"items":[{"id":"b1","handle":"AT1.2","title":"Dependency","status":"PENDING"}],"total":1,"hasMore":false},"blocks":{"items":[],"total":0,"hasMore":false},"status":"PENDING"`, 1)
}
func TestTaskPriorityCreateAndGet1135(t *testing.T) {
	srv, calls := captureGraphQL(t, map[string]string{"AgentTaskCreate": `{"data":{"createAgentTask":` + priorityTask1135() + `}}`})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "task", "create", "Dependent", "--app", "acme.com:eng-team", "--priority", "2", "--blocked-by", "AT1.2", "--blocked-by", "AT1.3", "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var vars struct{ Input map[string]json.RawMessage }
	if err := json.Unmarshal(calls["AgentTaskCreate"], &vars); err != nil {
		t.Fatal(err)
	}
	if string(vars.Input["priority"]) != "2" || string(vars.Input["blockedBy"]) != `["hrn:agenttask:acme.com:eng-team:AT1.2","hrn:agenttask:acme.com:eng-team:AT1.3"]` {
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
			if !api.HasErrorCode(err, tc.code) {
				t.Fatalf("error chain lost %s: %v", tc.code, err)
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
	r := taskResponses()
	r["AgentTaskGet"] = `{"data":{"agentTask":` + priorityTask1135() + `}}`
	r["AgentTaskList"] = `{"data":{"agentTasks":[` + priorityTask1135() + `,` + strings.Replace(priorityTask1135(), `"id":"task1"`, `"id":"task2"`, 1) + `]}}`

	// get, text: the priority, blocked state and both pages are printed exactly,
	// as consecutive lines (cli#1182: not a substring that a timestamp or a
	// header would satisfy).
	_, out, err := runTeamTask(t, r, "team", "task", "get", "AT1.4")
	if err != nil {
		t.Fatal(err)
	}
	if want := "  priority: 2 (1 = highest); blocked: true\n  blocked by: 1 shown of 1 (more: false)\n    AT1.2 (b1) PENDING: Dependency\n  blocks: 0 shown of 0 (more: false)\n"; !strings.Contains(out, want) {
		t.Fatalf("get text lacks the exact priority/blocked/dependency block %q:\n%s", want, out)
	}

	// list, text: the header names the columns and each row's own cells carry the
	// priority, blocked flag and the blocker summary, in the server's order.
	_, out, err = runTeamTask(t, r, "team", "task", "list")
	if err != nil {
		t.Fatal(err)
	}
	var header string
	var rows [][]string
	for _, line := range strings.Split(out, "\n") {
		switch f := strings.Fields(line); {
		case strings.HasPrefix(line, "ID "):
			header = line
		case len(f) > 0 && strings.HasPrefix(f[0], "task"):
			rows = append(rows, f)
		}
	}
	if !strings.Contains(header, "PRIORITY") || !strings.Contains(header, "BLOCKED") || !strings.Contains(header, "BLOCKED BY") || !strings.Contains(header, "BLOCKS") {
		t.Fatalf("list header lacks the priority/blocked columns: %q", header)
	}
	if len(rows) != 2 || rows[0][0] != "task1" || rows[1][0] != "task2" {
		t.Fatalf("server order changed or rows missing: %v\n%s", rows, out)
	}
	for _, row := range rows {
		if got := row[:5]; strings.Join(got, " ") != row[0]+" PENDING 2 true AT1.2" {
			t.Fatalf("row cells %v, want [id PENDING 2 true AT1.2 ...]", got)
		}
	}

	// --json: the exact values and page shapes.
	for _, cmd := range []string{"get", "list"} {
		args := []string{"team", "task", cmd, "--json"}
		if cmd == "get" {
			args = []string{"team", "task", "get", "AT1.4", "--json"}
		}
		_, out, err := runTeamTask(t, r, args...)
		if err != nil {
			t.Fatal(err)
		}
		var task struct {
			Priority  int  `json:"priority"`
			Blocked   bool `json:"blocked"`
			BlockedBy struct {
				Items   []map[string]string `json:"items"`
				Total   int                 `json:"total"`
				HasMore bool                `json:"hasMore"`
			} `json:"blockedBy"`
			Blocks struct {
				Items   []any `json:"items"`
				Total   int   `json:"total"`
				HasMore bool  `json:"hasMore"`
			} `json:"blocks"`
		}
		if cmd == "list" {
			var tasks []json.RawMessage
			if err := json.Unmarshal([]byte(out), &tasks); err != nil || len(tasks) != 2 {
				t.Fatalf("list json: %v\n%s", err, out)
			}
			out = string(tasks[0])
		}
		if err := json.Unmarshal([]byte(out), &task); err != nil {
			t.Fatal(err)
		}
		if task.Priority != 2 || !task.Blocked || task.BlockedBy.Total != 1 || task.BlockedBy.HasMore || len(task.BlockedBy.Items) != 1 ||
			task.BlockedBy.Items[0]["id"] != "b1" || task.BlockedBy.Items[0]["handle"] != "AT1.2" || task.BlockedBy.Items[0]["title"] != "Dependency" || task.BlockedBy.Items[0]["status"] != "PENDING" ||
			task.Blocks.Total != 0 || task.Blocks.HasMore || task.Blocks.Items == nil || len(task.Blocks.Items) != 0 {
			t.Fatalf("%s json fields wrong: %+v\n%s", cmd, task, out)
		}
	}
}

// A realistic late page: 21 blockers in all, asked for offset 20 limit 1, so the
// one item returned is the 21st and nothing more remains.
func TestTaskDependenciesPageForwarding1135(t *testing.T) {
	r := taskResponses()
	r["AgentTaskDependencies"] = `{"data":{"agentTask":{"id":"task1","blockedBy":{"items":[{"id":"b21","handle":"AT1.22","title":"The last blocker","status":"STARTED"}],"total":21,"hasMore":false},"blocks":{"items":[],"total":0,"hasMore":false}}}}`
	calls, out, err := runTeamTask(t, r, "team", "task", "dependencies", "AT1.4", "--limit", "1", "--offset", "20", "--json")
	if err != nil {
		t.Fatal(err)
	}
	c := c2Mutation(t, calls, "AgentTaskDependencies")
	if c.Vars["taskRef"] != "AT1.4" || c.Vars["limit"] != float64(1) || c.Vars["offset"] != float64(20) {
		t.Fatal(c.Vars)
	}
	var got struct {
		ID        string `json:"id"`
		BlockedBy struct {
			Items   []map[string]string `json:"items"`
			Total   int                 `json:"total"`
			HasMore bool                `json:"hasMore"`
		} `json:"blockedBy"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != "task1" || got.BlockedBy.Total != 21 || got.BlockedBy.HasMore || len(got.BlockedBy.Items) != 1 || got.BlockedBy.Items[0]["handle"] != "AT1.22" || got.BlockedBy.Items[0]["status"] != "STARTED" {
		t.Fatalf("the late page was not rendered as returned: %+v\n%s", got, out)
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

func TestTaskPriorityExplicitCreateValues1135(t *testing.T) {
	for _, value := range []string{"1", "0", "6"} {
		t.Run(value, func(t *testing.T) {
			r := taskResponses()
			if value != "1" {
				r["AgentTaskCreate"] = `{"errors":[{"message":"priority must be an integer from 1 to 5.","extensions":{"code":"BAD_USER_INPUT"}}]}`
			}
			calls, out, err := runTeamTask(t, r, "team", "task", "create", "new", "--priority", value, "--json")
			if value == "1" {
				if err != nil {
					t.Fatal(err)
				}
			} else if exitCodeFor(err) != 2 || out != "" {
				t.Fatalf("out=%q err=%v", out, err)
			}
			n := 0
			for _, c := range *calls {
				if c.Op == "AgentTaskCreate" {
					n++
					in := c.Vars["input"].(map[string]any)
					if fmt.Sprint(in["priority"]) != value {
						t.Fatalf("explicit priority changed: %s", c.RawVars)
					}
				}
			}
			if n != 1 {
				t.Fatalf("create count=%d", n)
			}
		})
	}
}

func TestTaskPriorityHelpReferenceScope1135(t *testing.T) {
	for _, verb := range []string{"set-priority", "add-blocker", "remove-blocker", "dependencies"} {
		t.Run(verb, func(t *testing.T) {
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"team", "task", verb, "--help"})
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(out.String(), "active App") || !strings.Contains(out.String(), "--app does not") || !strings.Contains(out.String(), "hrn:agenttask:example.com:team:AT1.2") {
				t.Fatalf("misleading task ref scope: %s", out.String())
			}
		})
	}
}
