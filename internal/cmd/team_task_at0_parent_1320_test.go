package cmd

// hadron-cli#1320 (server#2553, cor:agt:050:01; option A, Holger 2026-11-04):
// a task made without --parent is a child of the App's reserved AT0 "No
// project" project. The CLI never said so, so `team task get` printed the
// reserved project's raw id as a parent nobody asked for. Option A: when the
// parent is AT0, text output omits the `parent:` line and prints
// `project: none` in its place — the task's own handle AT0.<n> already says
// it. The same rule applies wherever text shows a parent: `task tree`'s
// nesting and the team list tree. `--json` keeps the raw `parentTaskId`, a
// real project parent and a real parent task still print their `parent:`
// line, and nothing about the output is platform-conditional.

import (
	"encoding/json"
	"strings"
	"testing"
)

// at0Project1320 is the reserved row itself: handle AT0, no parent.
const at0ProjectFieldsJSON1320 = `{"id":"at0id","handle":"AT0","appId":"capp100000000000000000000",
	"title":"No project","description":null,"status":"BACKLOG","queued":false,
	"waitingOnChildren":false,"owner":null,"assignee":null,"assignedBy":null,
	"createdBy":null,"parentTaskId":null,"tags":[],"source":null,"startBy":null,
	"createdAt":"2026-10-12T00:00:00Z","updatedAt":null,"startedAt":null,
	"finishedAt":null,"cancelledAt":null,"closeSessionWhenDone":false,
	"requirements":null,"result":null}`

// at0Child1320 is the issue's case: created with no --parent, so the server
// parents it on the reserved AT0 and numbers it AT0.7.
const at0ChildFieldsJSON1320 = `{"id":"task7","handle":"AT0.7","appId":"capp100000000000000000000",
	"title":"Fix alpha's clock sync","description":null,"status":"PENDING","queued":true,
	"waitingOnChildren":false,
	"owner":{"id":"wkr1","kind":"WORKER","name":"Iris","urn":"hrn:worker:acme.com:eng:iris"},
	"assignee":null,"assignedBy":null,
	"createdBy":{"id":"u1","kind":"USER","name":"holger","urn":null},
	"parentTaskId":"at0id","tags":[],"source":null,"startBy":null,
	"createdAt":"2026-10-12T00:00:00Z","updatedAt":null,"startedAt":null,
	"finishedAt":null,"cancelledAt":null,"closeSessionWhenDone":false,
	"requirements":null,"result":null}`

// realProjectChild1320 has a real project parent (AT1), which keeps its line.
const realProjectChildFieldsJSON1320 = `{"id":"task12","handle":"AT1.2","appId":"capp100000000000000000000",
	"title":"Ship the calendar","description":null,"status":"PENDING","queued":true,
	"waitingOnChildren":false,
	"owner":{"id":"wkr1","kind":"WORKER","name":"Iris","urn":"hrn:worker:acme.com:eng:iris"},
	"assignee":null,"assignedBy":null,
	"createdBy":{"id":"u1","kind":"USER","name":"holger","urn":null},
	"parentTaskId":"proj1","tags":[],"source":null,"startBy":null,
	"createdAt":"2026-10-12T00:00:00Z","updatedAt":null,"startedAt":null,
	"finishedAt":null,"cancelledAt":null,"closeSessionWhenDone":false,
	"requirements":null,"result":null}`

// at0Subtask1320 sits UNDER a task (AT0.5), not under the reserved project:
// its parent is a real task even though the project is AT0.
const at0SubtaskFieldsJSON1320 = `{"id":"task53","handle":"AT0.5.3","appId":"capp100000000000000000000",
	"title":"Seat three","description":null,"status":"PENDING","queued":true,
	"waitingOnChildren":false,
	"owner":{"id":"wkr1","kind":"WORKER","name":"Iris","urn":"hrn:worker:acme.com:eng:iris"},
	"assignee":null,"assignedBy":null,
	"createdBy":{"id":"u1","kind":"USER","name":"holger","urn":null},
	"parentTaskId":"task5","tags":[],"source":null,"startBy":null,
	"createdAt":"2026-10-12T00:00:00Z","updatedAt":null,"startedAt":null,
	"finishedAt":null,"cancelledAt":null,"closeSessionWhenDone":false,
	"requirements":null,"result":null}`

// ---- task get -------------------------------------------------------------

func TestTaskGetHidesReservedAT0Parent1320(t *testing.T) {
	responses := taskResponses()
	responses["AgentTaskGet"] = `{"data":{"agentTask":` + at0ChildFieldsJSON1320 + `}}`
	_, out, err := runTeamTask(t, responses, "team", "task", "get", "AT0.7")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "  project: none\n") {
		t.Fatalf("reserved AT0 parent must print \"  project: none\" in place of the parent line:\n%s", out)
	}
	if strings.Contains(out, "parent:") {
		t.Fatalf("reserved AT0 parent must not print a parent: line:\n%s", out)
	}
	if strings.Contains(out, "at0id") {
		t.Fatalf("the reserved project's raw id must not appear:\n%s", out)
	}
}

func TestTaskGetRealProjectParentStillPrints1320(t *testing.T) {
	responses := taskResponses()
	responses["AgentTaskGet"] = `{"data":{"agentTask":` + realProjectChildFieldsJSON1320 + `}}`
	_, out, err := runTeamTask(t, responses, "team", "task", "get", "AT1.2")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "  parent: proj1\n") {
		t.Fatalf("a real project parent keeps its parent: line:\n%s", out)
	}
	if strings.Contains(out, "project: none") {
		t.Fatalf("a real project parent is not \"no project\":\n%s", out)
	}
}

func TestTaskGetSubtaskUnderAT0TaskStillPrints1320(t *testing.T) {
	responses := taskResponses()
	responses["AgentTaskGet"] = `{"data":{"agentTask":` + at0SubtaskFieldsJSON1320 + `}}`
	_, out, err := runTeamTask(t, responses, "team", "task", "get", "AT0.5.3")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "  parent: task5\n") {
		t.Fatalf("a parent that is a task (not the reserved project) keeps its parent: line:\n%s", out)
	}
	if strings.Contains(out, "project: none") {
		t.Fatalf("a real parent task is not \"no project\":\n%s", out)
	}
}

func TestTaskGetJSONKeepsRawParentTaskId1320(t *testing.T) {
	responses := taskResponses()
	responses["AgentTaskGet"] = `{"data":{"agentTask":` + at0ChildFieldsJSON1320 + `}}`
	_, out, err := runTeamTask(t, responses, "team", "task", "get", "AT0.7", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if dto["parentTaskId"] != "at0id" {
		t.Fatalf("--json keeps the raw parentTaskId, got %v", dto["parentTaskId"])
	}
	if dto["handle"] != "AT0.7" {
		t.Fatalf("handle %v", dto["handle"])
	}
}

// ---- task tree ------------------------------------------------------------

func TestTaskTreeHidesReservedAT0Ancestor1320(t *testing.T) {
	responses := taskResponses()
	responses["AgentTaskTree"] = `{"data":{"agentTaskTree":[` + at0ProjectFieldsJSON1320 + `,` + at0ChildFieldsJSON1320 + `]}}`
	_, out, err := runTeamTask(t, responses, "team", "task", "tree", "AT0.7")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "No project") || strings.Contains(out, "at0id") {
		t.Fatalf("the reserved AT0 row is not drawn as a parent node:\n%s", out)
	}
	if !strings.Contains(out, "\n• AT0.7 task7 ") && !strings.HasPrefix(out, "• AT0.7 task7 ") {
		t.Fatalf("the AT0-parented task renders as a root, at depth 0:\n%s", out)
	}
	if strings.Contains(out, "\n  • ") {
		t.Fatalf("nothing nests under a hidden reserved parent:\n%s", out)
	}
}

func TestTaskTreeExplicitAT0RootStillRenders1320(t *testing.T) {
	responses := taskResponses()
	responses["AgentTaskTree"] = `{"data":{"agentTaskTree":[` + at0ProjectFieldsJSON1320 + `,` + at0ChildFieldsJSON1320 + `]}}`
	_, out, err := runTeamTask(t, responses, "team", "task", "tree", "AT0")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "• AT0 at0id [BACKLOG]") {
		t.Fatalf("a root asked for BY NAME still renders:\n%s", out)
	}
	if !strings.Contains(out, "\n  • AT0.7 task7 ") {
		t.Fatalf("its children still nest under the asked-for root:\n%s", out)
	}
}

func TestTaskTreeRealProjectChainUnchanged1320(t *testing.T) {
	responses := taskResponses()
	responses["AgentTaskTree"] = `{"data":{"agentTaskTree":[` + at0ProjectFieldsJSON1320 + `,` + realProjectChildFieldsJSON1320 + `]}}`
	// The reserved row is present in the chain but is nobody's parent here:
	// proj1 is not in the result, so AT1.2 is an orphan-root, AT0 a bare root.
	_, out, err := runTeamTask(t, responses, "team", "task", "tree", "AT1.2")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "• AT1.2 task12 [PENDING]") {
		t.Fatalf("the real-project task renders:\n%s", out)
	}
}

// ---- team list --tree -----------------------------------------------------

// The team view can select the reserved row itself (here: a team member owns
// it). The row keeps rendering — it was selected on its own merits — but
// nothing nests under it: its children are roots.
func TestTeamListTreeHoistsAT0Children1320(t *testing.T) {
	pages := [][]task1044{{
		{id: "at0id", handle: "AT0", status: "BACKLOG", owner: worker1044("Marco")},
		{id: "task7", handle: "AT0.7", owner: worker1044("Marco"), assignee: worker1044("Jonas"), parent: "at0id"},
	}}
	_, tree, _, err := run1044(t, roster1044(), pages, "team", "task", "list", "--team=worker:Marco", "--tree")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tree, "• AT0.7 task7 [") || !strings.Contains(tree, "• AT0 at0id [") {
		t.Fatalf("both selected rows render:\n%s", tree)
	}
	if strings.Contains(tree, "\n  • ") {
		t.Fatalf("nothing nests under the reserved row:\n%s", tree)
	}
	// The hoisted child is a ROOT: drawn unindented, like the reserved row.
	for _, line := range strings.Split(tree, "\n") {
		if strings.Contains(line, "• AT0.7 task7") || strings.Contains(line, "• AT0 at0id") {
			if strings.TrimLeft(line, " ") != line {
				t.Fatalf("row must be unindented: %q", line)
			}
		}
	}
}
