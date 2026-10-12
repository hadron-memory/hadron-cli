package cmd

// hadron-cli#830 — Task Management T4: `team task`, `team roster`, `team
// events`, `team alarm ack` and `team availability`. Red-first per command:
// every verb here failed before the implementation landed (cobra: unknown
// command), which is the CHECKED gate's acceptance.

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ---- shared fixtures -------------------------------------------------------

// taskFieldsJSON is one AgentTask with every optional-but-representative
// field set, matching the AgentTaskFields fragment's selection.
const taskFieldsJSON = `{"id":"task1","appId":"capp100000000000000000000",
	"title":"Fix the flaky CI build","description":"it flakes on Mondays",
	"status":"PENDING","queued":true,"waitingOnChildren":false,
	"owner":{"id":"wkr1","kind":"WORKER","name":"Iris","urn":"hrn:worker:acme.com:eng:iris"},
	"assignee":null,"assignedBy":null,
	"createdBy":{"id":"u1","kind":"USER","name":"holger","urn":null},
	"parentTaskId":null,"tags":["ci"],"source":"https://github.com/acme/app/issues/12",
	"startBy":"2026-10-13T09:00:00Z","createdAt":"2026-10-12T00:00:00Z",
	"updatedAt":null,"startedAt":null,"finishedAt":null,"cancelledAt":null,
	"closeSessionWhenDone":true,
	"requirements":{"role":"qa","familyIn":["claude"],"familyNotIn":null,"excludeWorkerIds":["wkr9"]},
	"result":null}`

const startedTaskFieldsJSON = `{"id":"task1","appId":"capp100000000000000000000",
	"title":"Fix the flaky CI build","description":null,
	"status":"STARTED","queued":false,"waitingOnChildren":false,
	"owner":{"id":"wkr1","kind":"WORKER","name":"Iris","urn":"hrn:worker:acme.com:eng:iris"},
	"assignee":{"id":"wkr2","kind":"WORKER","name":"Rufus","urn":"hrn:worker:acme.com:eng:rufus"},
	"assignedBy":{"id":"u1","kind":"USER","name":"holger","urn":null},
	"createdBy":{"id":"u1","kind":"USER","name":"holger","urn":null},
	"parentTaskId":null,"tags":[],"source":null,"startBy":null,
	"createdAt":"2026-10-12T00:00:00Z","updatedAt":"2026-10-12T01:00:00Z",
	"startedAt":"2026-10-12T01:00:00Z","finishedAt":null,"cancelledAt":null,
	"closeSessionWhenDone":true,"requirements":null,"result":null}`

const finishedTaskFieldsJSON = `{"id":"task1","appId":"capp100000000000000000000",
	"title":"Fix the flaky CI build","description":null,
	"status":"FINISHED","queued":false,"waitingOnChildren":false,
	"owner":{"id":"wkr1","kind":"WORKER","name":"Iris","urn":"hrn:worker:acme.com:eng:iris"},
	"assignee":{"id":"wkr2","kind":"WORKER","name":"Rufus","urn":"hrn:worker:acme.com:eng:rufus"},
	"assignedBy":{"id":"u1","kind":"USER","name":"holger","urn":null},
	"createdBy":{"id":"u1","kind":"USER","name":"holger","urn":null},
	"parentTaskId":null,"tags":[],"source":null,"startBy":null,
	"createdAt":"2026-10-12T00:00:00Z","updatedAt":"2026-10-12T02:00:00Z",
	"startedAt":"2026-10-12T01:00:00Z","finishedAt":"2026-10-12T02:00:00Z","cancelledAt":null,
	"closeSessionWhenDone":true,"requirements":null,
	"result":{"outcome":"succeeded","summary":"pinned the runner","evidence":"https://ci.acme.com/1"}}`

const eventFieldsJSON = `{"id":"evt1","appId":"capp100000000000000000000","taskId":"task1",
	"type":"task.created","severity":null,"createdAt":"2026-10-12T00:00:00Z",
	"actor":{"id":"u1","kind":"USER","name":"holger","urn":null},
	"subjectWorker":null,"payload":{"title":"Fix the flaky CI build"},
	"acknowledgedAt":null,"acknowledgedBy":null,"episodeStart":null,"chain":null,"wakes":null}`

const alarmEventFieldsJSON = `{"id":"evt2","appId":"capp100000000000000000000","taskId":"task1",
	"type":"stall.no-progress","severity":"ALARM","createdAt":"2026-10-12T03:00:00Z",
	"actor":null,"subjectWorker":{"id":"wkr2","kind":"WORKER","name":"Rufus","urn":"hrn:worker:acme.com:eng:rufus"},
	"payload":{},"acknowledgedAt":null,"acknowledgedBy":null,
	"episodeStart":"2026-10-12T02:00:00Z","chain":[{"chainIndex":0}],
	"wakes":[{"chainIndex":0,"dueAt":"2026-10-12T03:20:00Z",
	"owner":{"id":"wkr1","kind":"WORKER","name":"Iris","urn":"hrn:worker:acme.com:eng:iris"},"wokenAt":null}]}`

const ackedAlarmEventFieldsJSON = `{"id":"evt2","appId":"capp100000000000000000000","taskId":"task1",
	"type":"stall.no-progress","severity":"ALARM","createdAt":"2026-10-12T03:00:00Z",
	"actor":{"id":"wkr1","kind":"WORKER","name":"Iris","urn":"hrn:worker:acme.com:eng:iris"},
	"subjectWorker":{"id":"wkr2","kind":"WORKER","name":"Rufus","urn":"hrn:worker:acme.com:eng:rufus"},
	"payload":{},"acknowledgedAt":"2026-10-12T03:05:00Z",
	"acknowledgedBy":{"id":"wkr1","kind":"WORKER","name":"Iris","urn":"hrn:worker:acme.com:eng:iris"},
	"episodeStart":"2026-10-12T02:00:00Z","chain":[{"chainIndex":0}],"wakes":[]}`

const rosterJSON = `{"appId":"capp100000000000000000000","asOf":"2026-10-12T04:00:00Z","workers":[
	{"worker":{"id":"wkr2","kind":"WORKER","name":"Rufus","urn":"hrn:worker:acme.com:eng:rufus"},
	 "state":"ASSIGNED_NOT_STARTED","role":"backend-engineer","bound":true,"liveSession":true,
	 "family":"claude","freeForWork":false,"freeFamilies":["claude"],"launchableFamilies":["claude"],
	 "unavailableFamilies":[],"unavailableReason":null,"unavailableUntil":null,
	 "lastActiveAt":"2026-10-12T01:00:00Z","lastAssignedAt":"2026-10-11T09:00:00Z",
	 "pendingTasks":["task1"],"startedTasks":[],"waitingOn":[],"waitingSince":null,
	 "unreactedChildTask":null,"alarms":[],
	 "reportsTo":{"id":"wkr1","kind":"WORKER","name":"Iris","urn":"hrn:worker:acme.com:eng:iris"},
	 "reportsToRetired":false},
	{"worker":{"id":"wkr1","kind":"WORKER","name":"Iris","urn":"hrn:worker:acme.com:eng:iris"},
	 "state":"AVAILABLE","role":"coordinator","bound":true,"liveSession":true,
	 "family":"claude","freeForWork":true,"freeFamilies":["claude"],"launchableFamilies":[],
	 "unavailableFamilies":["codex"],"unavailableReason":null,"unavailableUntil":null,
	 "lastActiveAt":"2026-10-12T03:30:00Z","lastAssignedAt":null,
	 "pendingTasks":[],"startedTasks":["task7"],"waitingOn":["task8"],"waitingSince":"2026-10-12T02:10:00Z",
	 "unreactedChildTask":"task9","alarms":["evt2"],
	 "reportsTo":null,"reportsToRetired":false}]}`

const availabilityJSON = `{"workers":[
	{"worker":{"id":"wkr2","kind":"WORKER","name":"Rufus","urn":"hrn:worker:acme.com:eng:rufus"},
	 "state":"UNAVAILABLE","reason":"OUT_OF_TOKENS","until":"2026-10-13T00:00:00Z",
	 "setAt":"2026-10-12T00:00:00Z","setByParty":{"id":"wkr2","kind":"WORKER","name":"Rufus","urn":"hrn:worker:acme.com:eng:rufus"},
	 "inEffect":true}],
	"families":[
	{"appId":"capp100000000000000000000","family":"codex","state":"UNAVAILABLE","reason":"HOST_DOWN",
	 "until":null,"setAt":"2026-10-12T00:00:00Z",
	 "setBy":{"id":"u1","kind":"USER","name":"holger","urn":null},"inEffect":true}]}`

// One worker record, as setWorkerAvailability returns it (not the team
// envelope availabilityJSON spells).
const workerAvailabilityRecordJSON = `{"worker":{"id":"wkr2","kind":"WORKER","name":"Rufus","urn":"hrn:worker:acme.com:eng:rufus"},
	"state":"UNAVAILABLE","reason":"OUT_OF_TOKENS","until":"2026-10-13T00:00:00Z",
	"setAt":"2026-10-12T04:00:00Z","setByParty":{"id":"wkr1","kind":"WORKER","name":"Iris","urn":"hrn:worker:acme.com:eng:iris"},
	"inEffect":true}`

// taskResponses is the standard server for a bound worktree: the binding's
// App answers every task-surface operation.
func taskResponses() map[string]string {
	return map[string]string{
		"AgentTaskCreate":            `{"data":{"createAgentTask":` + taskFieldsJSON + `}}`,
		"AgentTaskAssign":            `{"data":{"assignAgentTask":` + startedTaskFieldsJSON + `}}`,
		"AgentTaskReassign":          `{"data":{"reassignAgentTask":` + startedTaskFieldsJSON + `}}`,
		"AgentTaskStart":             `{"data":{"startAgentTask":` + startedTaskFieldsJSON + `}}`,
		"AgentTaskProgress":          `{"data":{"recordAgentTaskProgress":` + startedTaskFieldsJSON + `}}`,
		"AgentTaskFinish":            `{"data":{"finishAgentTask":` + finishedTaskFieldsJSON + `}}`,
		"AgentTaskCancel":            `{"data":{"cancelAgentTask":` + finishedTaskFieldsJSON + `}}`,
		"AgentTaskGet":               `{"data":{"agentTask":` + taskFieldsJSON + `}}`,
		"AgentTaskList":              `{"data":{"agentTasks":[` + taskFieldsJSON + `]}}`,
		"AgentTaskTree":              `{"data":{"agentTaskTree":[` + taskFieldsJSON + `]}}`,
		"AgentTeamEvents":            `{"data":{"agentTeamEvents":[` + eventFieldsJSON + `]}}`,
		"AlarmAcknowledge":           `{"data":{"acknowledgeAlarm":` + ackedAlarmEventFieldsJSON + `}}`,
		"TeamRoster":                 `{"data":{"teamRoster":` + rosterJSON + `}}`,
		"TeamAvailability":           `{"data":{"teamAvailability":` + availabilityJSON + `}}`,
		"WorkerAvailabilitySet":      `{"data":{"setWorkerAvailability":` + workerAvailabilityRecordJSON + `}}`,
		"ModelFamilyAvailabilitySet": `{"data":{"setModelFamilyAvailability":{"appId":"capp100000000000000000000","family":"codex","state":"AVAILABLE","reason":null,"until":null,"setAt":"2026-10-12T00:00:00Z","setBy":{"id":"wkr1","kind":"WORKER","name":"Iris","urn":"hrn:worker:acme.com:eng:iris"},"inEffect":false}}}`,
		// The scope line's decorating read (#470): best-effort, answered
		// happily here and refused in the degradation test below.
		"TeamAppIdentity": teamAppIdentityJSON,
	}
}

// runTeamTask executes argv against a bound-worktree server and returns the
// recorded calls.
func runTeamTask(t *testing.T, responses map[string]string, argv ...string) (*[]attnCall, string, error) {
	t.Helper()
	writeTeamBinding(t)
	srv, calls := attnServer(t, responses)
	setTeamBindingServer(t, srv.URL)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"--server", srv.URL}, argv...))
	err := root.Execute()
	return calls, out.String(), err
}

// ---- create ---------------------------------------------------------------

func TestTeamTaskCreateAttributesToBoundSession(t *testing.T) {
	calls, out, err := runTeamTask(t, taskResponses(),
		"team", "task", "create", "Fix the flaky CI build",
		"--description", "it flakes on Mondays",
		"--assignee", "worker:Rufus",
		"--tag", "ci",
		"--source", "https://github.com/acme/app/issues/12",
		"--start-by", "2026-10-13T09:00:00Z",
		"--require-role", "qa",
		"--family-in", "claude",
		"--exclude-worker", "wkr9")
	if err != nil {
		t.Fatal(err)
	}
	var create *attnCall
	for i := range *calls {
		if (*calls)[i].Op == "AgentTaskCreate" {
			create = &(*calls)[i]
		}
	}
	if create == nil {
		t.Fatal("no AgentTaskCreate call")
	}
	// The actor rule (#830): a bound worktree writes as its worker — the
	// X-Hadron-Session header the authored-write middleware adds.
	if create.Session != "s-new" {
		t.Fatalf("create not attributed: session header %q", create.Session)
	}
	input, _ := create.Vars["input"].(map[string]any)
	if input == nil {
		t.Fatal("no input variable")
	}
	if input["title"] != "Fix the flaky CI build" {
		t.Fatalf("title %v", input["title"])
	}
	if input["appRef"] != "capp100000000000000000000" {
		t.Fatalf("appRef %v (binding's App)", input["appRef"])
	}
	assignee, _ := input["assignee"].(map[string]any)
	if assignee == nil || assignee["worker"] != "Rufus" {
		t.Fatalf("assignee %v", input["assignee"])
	}
	reqs, _ := input["requirements"].(map[string]any)
	if reqs == nil || reqs["role"] != "qa" {
		t.Fatalf("requirements %v", input["requirements"])
	}
	if !strings.Contains(out, "✓ created task task1") {
		t.Fatalf("receipt %q", out)
	}
}

func TestTeamTaskCreateUnboundWritesAsThePerson(t *testing.T) {
	srv, calls := attnServer(t, taskResponses())
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "task", "create", "Title", "--app", "capp100000000000000000000", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, c := range *calls {
		if c.Op == "AgentTaskCreate" && c.Session != "" {
			t.Fatalf("unbound create carried a session header: %q", c.Session)
		}
	}
}

func TestTeamTaskCreatePartySyntaxRefused(t *testing.T) {
	for _, flag := range []string{"--assignee", "--owner"} {
		_, _, err := runTeamTask(t, taskResponses(),
			"team", "task", "create", "T", flag, "Rufus")
		if err == nil || !strings.Contains(err.Error(), "worker:") || !strings.Contains(err.Error(), "user:") {
			t.Fatalf("%s bare name accepted: %v", flag, err)
		}
	}
}

func TestTeamTaskCreateFamilyConstraintsRefused(t *testing.T) {
	_, _, err := runTeamTask(t, taskResponses(),
		"team", "task", "create", "T", "--family-in", "claude", "--family-not-in", "codex")
	if err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("familyIn+familyNotIn accepted: %v", err)
	}
}

// ---- lifecycle verbs ------------------------------------------------------

func TestTeamTaskLifecycleVerbsAttributed(t *testing.T) {
	cases := []struct {
		argv []string
		op   string
		rece string
	}{
		{[]string{"team", "task", "start", "task1"}, "AgentTaskStart", "✓ started task task1"},
		{[]string{"team", "task", "progress", "task1"}, "AgentTaskProgress", "✓ recorded progress on task task1"},
		{[]string{"team", "task", "cancel", "task1"}, "AgentTaskCancel", "✓ cancelled task task1"},
	}
	for _, tc := range cases {
		t.Run(tc.op, func(t *testing.T) {
			calls, out, err := runTeamTask(t, taskResponses(), tc.argv...)
			if err != nil {
				t.Fatal(err)
			}
			for _, c := range *calls {
				if c.Op == tc.op {
					if c.Session != "s-new" {
						t.Fatalf("not attributed: %q", c.Session)
					}
					if c.Vars["taskId"] != "task1" {
						t.Fatalf("taskId %v", c.Vars["taskId"])
					}
					if !strings.Contains(out, tc.rece) {
						t.Fatalf("receipt %q (want %q)", out, tc.rece)
					}
					return
				}
			}
			t.Fatalf("no %s call", tc.op)
		})
	}
}

func TestTeamTaskFinishSendsResult(t *testing.T) {
	calls, out, err := runTeamTask(t, taskResponses(),
		"team", "task", "finish", "task1",
		"--outcome", "succeeded",
		"--summary", "pinned the runner",
		"--evidence", "https://ci.acme.com/1")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range *calls {
		if c.Op == "AgentTaskFinish" {
			if c.Session != "s-new" {
				t.Fatalf("not attributed: %q", c.Session)
			}
			res, _ := c.Vars["result"].(map[string]any)
			if res == nil || res["outcome"] != "succeeded" || res["summary"] != "pinned the runner" || res["evidence"] != "https://ci.acme.com/1" {
				t.Fatalf("result %v", c.Vars["result"])
			}
			if !strings.Contains(out, "✓ finished task task1 (succeeded)") {
				t.Fatalf("receipt %q", out)
			}
			return
		}
	}
	t.Fatal("no AgentTaskFinish call")
}

func TestTeamTaskFinishRequiresOutcome(t *testing.T) {
	_, _, err := runTeamTask(t, taskResponses(), "team", "task", "finish", "task1")
	if err == nil || !strings.Contains(err.Error(), "--outcome") {
		t.Fatalf("missing outcome accepted: %v", err)
	}
}

// ---- assign / reassign ----------------------------------------------------

func TestTeamTaskAssignOmitsCloseSessionUnlessGiven(t *testing.T) {
	cases := []struct {
		name string
		flag string
		want any // nil = variable omitted entirely
	}{
		{"omitted", "", nil},
		{"true", "--close-session", true},
		{"false", "--no-close-session", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			argv := []string{"team", "task", "assign", "task1", "--assignee", "worker:Rufus"}
			if tc.flag != "" {
				argv = append(argv, tc.flag)
			}
			calls, _, err := runTeamTask(t, taskResponses(), argv...)
			if err != nil {
				t.Fatal(err)
			}
			for _, c := range *calls {
				if c.Op == "AgentTaskAssign" {
					if got, ok := c.Vars["closeSessionWhenDone"]; ok && got != tc.want {
						t.Fatalf("closeSessionWhenDone %v, want %v", got, tc.want)
					} else if !ok && tc.want != nil {
						t.Fatalf("closeSessionWhenDone omitted, want %v", tc.want)
					}
					if c.Session != "s-new" {
						t.Fatalf("not attributed: %q", c.Session)
					}
					return
				}
			}
			t.Fatal("no AgentTaskAssign call")
		})
	}
}

func TestTeamTaskReassignSendsParty(t *testing.T) {
	calls, _, err := runTeamTask(t, taskResponses(),
		"team", "task", "reassign", "task1", "--assignee", "user:holger")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range *calls {
		if c.Op == "AgentTaskReassign" {
			party, _ := c.Vars["assignee"].(map[string]any)
			if party == nil || party["user"] != "holger" {
				t.Fatalf("assignee %v", c.Vars["assignee"])
			}
			return
		}
	}
	t.Fatal("no AgentTaskReassign call")
}

// ---- get / list -----------------------------------------------------------

func TestTeamTaskGetJSONContract(t *testing.T) {
	calls, out, err := runTeamTask(t, taskResponses(), "team", "task", "get", "task1", "--json")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range *calls {
		if c.Op == "AgentTaskGet" && c.Session != "" {
			t.Fatalf("read attributed: %q", c.Session)
		}
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	for _, key := range []string{"id", "appId", "title", "description", "status", "queued", "waitingOnChildren", "owner", "assignee", "assignedBy", "createdBy", "parentTaskId", "tags", "source", "startBy", "createdAt", "updatedAt", "startedAt", "finishedAt", "cancelledAt", "closeSessionWhenDone", "requirements", "result"} {
		if _, ok := dto[key]; !ok {
			t.Fatalf("missing key %q in %s", key, out)
		}
	}
	if dto["status"] != "PENDING" || dto["queued"] != true {
		t.Fatalf("status/queued %v/%v", dto["status"], dto["queued"])
	}
	owner, _ := dto["owner"].(map[string]any)
	if owner == nil || owner["kind"] != "WORKER" || owner["name"] != "Iris" {
		t.Fatalf("owner %v", dto["owner"])
	}
	// tags must render as [], never null, when the server sends [].
	if tags, _ := dto["tags"].([]any); tags == nil {
		t.Fatalf("tags not a list: %v", dto["tags"])
	}
}

func TestTeamTaskListSendsFilters(t *testing.T) {
	calls, _, err := runTeamTask(t, taskResponses(),
		"team", "task", "list",
		"--status", "PENDING", "--status", "STARTED",
		"--tag", "ci", "--queued", "--roots-only",
		"--assignee", "worker:Rufus",
		"--limit", "10", "--offset", "5")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range *calls {
		if c.Op == "AgentTaskList" {
			if c.Vars["appRef"] != "capp100000000000000000000" {
				t.Fatalf("appRef %v", c.Vars["appRef"])
			}
			if st, _ := c.Vars["status"].([]any); len(st) != 2 || st[0] != "PENDING" {
				t.Fatalf("status %v", c.Vars["status"])
			}
			if c.Vars["tag"] != "ci" || c.Vars["queued"] != true || c.Vars["rootsOnly"] != true {
				t.Fatalf("filters %v", c.Vars)
			}
			if c.Vars["limit"] != float64(10) || c.Vars["offset"] != float64(5) {
				t.Fatalf("paging %v", c.Vars)
			}
			return
		}
	}
	t.Fatal("no AgentTaskList call")
}

func TestTeamTaskListStatusValidated(t *testing.T) {
	_, _, err := runTeamTask(t, taskResponses(), "team", "task", "list", "--status", "WOKE")
	if err == nil || !strings.Contains(err.Error(), "PENDING") {
		t.Fatalf("bad status accepted: %v", err)
	}
}

func TestTeamTaskListNeedsAnApp(t *testing.T) {
	// No binding, no --app: the loud refusal naming the remedies.
	srv, _ := attnServer(t, taskResponses())
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "task", "list", "--server", srv.URL})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "--app") {
		t.Fatalf("missing app accepted: %v", err)
	}
}

// ---- tree -----------------------------------------------------------------

func TestTeamTaskTreeRendersOpenAlarms(t *testing.T) {
	responses := taskResponses()
	responses["AgentTeamEvents"] = `{"data":{"agentTeamEvents":[` + alarmEventFieldsJSON + `]}}`
	calls, out, err := runTeamTask(t, responses, "team", "task", "tree", "task1")
	if err != nil {
		t.Fatal(err)
	}
	var events *attnCall
	for i := range *calls {
		if (*calls)[i].Op == "AgentTeamEvents" {
			events = &(*calls)[i]
		}
	}
	if events == nil {
		t.Fatal("tree did not read events")
	}
	if events.Vars["alarmsOnly"] != true {
		t.Fatalf("alarmsOnly %v", events.Vars["alarmsOnly"])
	}
	if events.Vars["appRef"] != "capp100000000000000000000" {
		t.Fatalf("appRef %v", events.Vars["appRef"])
	}
	if !strings.Contains(out, "alarm") || !strings.Contains(out, "task1") {
		t.Fatalf("render %q", out)
	}
}

// ---- events ---------------------------------------------------------------

func TestTeamEventsSendsFilters(t *testing.T) {
	calls, out, err := runTeamTask(t, taskResponses(),
		"team", "events",
		"--alarms", "--since", "2026-10-12T00:00:00Z",
		"--after", "evt0", "--task", "task1", "--type", "task.created",
		"--limit", "20")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range *calls {
		if c.Op == "AgentTeamEvents" {
			if c.Vars["alarmsOnly"] != true || c.Vars["since"] != "2026-10-12T00:00:00Z" ||
				c.Vars["after"] != "evt0" || c.Vars["taskId"] != "task1" {
				t.Fatalf("filters %v", c.Vars)
			}
			if ty, _ := c.Vars["types"].([]any); len(ty) != 1 || ty[0] != "task.created" {
				t.Fatalf("types %v", c.Vars["types"])
			}
			if c.Vars["limit"] != float64(20) {
				t.Fatalf("limit %v", c.Vars["limit"])
			}
			if c.Session != "" {
				t.Fatalf("read attributed: %q", c.Session)
			}
			if !strings.Contains(out, "task.created") {
				t.Fatalf("render %q", out)
			}
			return
		}
	}
	t.Fatal("no AgentTeamEvents call")
}

func TestTeamEventsJSONContract(t *testing.T) {
	_, out, err := runTeamTask(t, taskResponses(), "team", "events", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var events []map[string]any
	if err := json.Unmarshal([]byte(out), &events); err != nil {
		t.Fatalf("not a JSON list: %v\n%s", err, out)
	}
	if len(events) != 1 || events[0]["type"] != "task.created" {
		t.Fatalf("events %s", out)
	}
	if _, ok := events[0]["wakes"]; !ok {
		t.Fatal("missing wakes key")
	}
}

// ---- roster ---------------------------------------------------------------

func TestTeamRosterRendersServerStates(t *testing.T) {
	calls, out, err := runTeamTask(t, taskResponses(), "team", "roster")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range *calls {
		if c.Op == "TeamRoster" {
			if c.Vars["appRef"] != "capp100000000000000000000" {
				t.Fatalf("appRef %v", c.Vars["appRef"])
			}
			if c.Session != "" {
				t.Fatalf("read attributed: %q", c.Session)
			}
		}
	}
	for _, want := range []string{"Rufus", "ASSIGNED_NOT_STARTED", "AVAILABLE", "Iris"} {
		if !strings.Contains(out, want) {
			t.Fatalf("render missing %q: %q", want, out)
		}
	}
}

func TestTeamRosterHelpLabelsStatesPlanDerived(t *testing.T) {
	// The roster's states are plan-derived, not a spec (cor:agt:050 Out Of
	// Scope) — the help text must say so (#830).
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "roster", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "plan-derived") {
		t.Fatalf("help does not label the states plan-derived:\n%s", out.String())
	}
}

// ---- alarm ack ------------------------------------------------------------

func TestTeamAlarmAckAttributedAndRendered(t *testing.T) {
	calls, out, err := runTeamTask(t, taskResponses(), "team", "alarm", "ack", "evt2")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "✓ acknowledged alarm evt2") {
		t.Fatalf("receipt %q", out)
	}
	for _, c := range *calls {
		if c.Op == "AlarmAcknowledge" {
			if c.Vars["eventId"] != "evt2" {
				t.Fatalf("eventId %v", c.Vars["eventId"])
			}
			if c.Session != "s-new" {
				t.Fatalf("not attributed: %q", c.Session)
			}
			return
		}
	}
	t.Fatal("no AlarmAcknowledge call")
}

// ---- availability ---------------------------------------------------------

func TestTeamAvailabilityGetRendersBothHalves(t *testing.T) {
	calls, out, err := runTeamTask(t, taskResponses(), "team", "availability", "get", "--json")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range *calls {
		if c.Op == "TeamAvailability" && c.Session != "" {
			t.Fatalf("read attributed: %q", c.Session)
		}
	}
	var dto struct {
		Workers  []map[string]any `json:"workers"`
		Families []map[string]any `json:"families"`
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(dto.Workers) != 1 || len(dto.Families) != 1 {
		t.Fatalf("halves %d/%d", len(dto.Workers), len(dto.Families))
	}
	if dto.Workers[0]["reason"] != "OUT_OF_TOKENS" || dto.Families[0]["family"] != "codex" {
		t.Fatalf("records %s", out)
	}
}

func TestTeamAvailabilityGetHumanRender(t *testing.T) {
	_, out, err := runTeamTask(t, taskResponses(), "team", "availability", "get")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Rufus", "OUT_OF_TOKENS", "codex", "HOST_DOWN"} {
		if !strings.Contains(out, want) {
			t.Fatalf("render missing %q: %q", want, out)
		}
	}
}

func TestTeamAvailabilitySetWorkerValidation(t *testing.T) {
	cases := []struct {
		name string
		argv []string
		want string
	}{
		{"reason requires unavailable", []string{"--worker", "wkr2", "--state", "available", "--reason", "STALLED"}, "--reason"},
		{"unavailable requires reason", []string{"--worker", "wkr2", "--state", "unavailable"}, "--reason"},
		{"until requires unavailable", []string{"--worker", "wkr2", "--state", "available", "--until", "2026-10-13T00:00:00Z"}, "--until"},
		{"bad state", []string{"--worker", "wkr2", "--state", "sleepy"}, "available"},
		{"worker xor family", []string{"--worker", "wkr2", "--family", "claude", "--state", "available"}, "--worker"},
		{"neither", []string{"--state", "available"}, "--worker"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := runTeamTask(t, taskResponses(), append([]string{"team", "availability", "set"}, tc.argv...)...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("accepted: %v", err)
			}
		})
	}
}

func TestTeamAvailabilitySetWorkerSendsInput(t *testing.T) {
	calls, out, err := runTeamTask(t, taskResponses(),
		"team", "availability", "set",
		"--worker", "Rufus", "--state", "unavailable",
		"--reason", "OUT_OF_TOKENS", "--until", "2026-10-13T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range *calls {
		if c.Op == "WorkerAvailabilitySet" {
			if c.Session != "s-new" {
				t.Fatalf("not attributed: %q", c.Session)
			}
			input, _ := c.Vars["input"].(map[string]any)
			if input == nil || input["worker"] != "Rufus" || input["state"] != "UNAVAILABLE" ||
				input["reason"] != "OUT_OF_TOKENS" || input["until"] != "2026-10-13T00:00:00Z" {
				t.Fatalf("input %v", c.Vars["input"])
			}
			if input["appRef"] != "capp100000000000000000000" {
				t.Fatalf("appRef %v (needed for a worker NAME)", input["appRef"])
			}
			if !strings.Contains(out, "✓") {
				t.Fatalf("receipt %q", out)
			}
			return
		}
	}
	t.Fatal("no WorkerAvailabilitySet call")
}

func TestTeamAvailabilitySetFamilySendsInput(t *testing.T) {
	calls, _, err := runTeamTask(t, taskResponses(),
		"team", "availability", "set",
		"--family", "codex", "--state", "available")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range *calls {
		if c.Op == "ModelFamilyAvailabilitySet" {
			input, _ := c.Vars["input"].(map[string]any)
			if input == nil || input["family"] != "codex" || input["state"] != "AVAILABLE" {
				t.Fatalf("input %v", c.Vars["input"])
			}
			if input["appRef"] != "capp100000000000000000000" {
				t.Fatalf("appRef %v", input["appRef"])
			}
			return
		}
	}
	t.Fatal("no ModelFamilyAvailabilitySet call")
}

func TestTeamTaskListScopeLineDegradesToTheRef(t *testing.T) {
	// The scope line decorates, never gates (#470): a server that cannot
	// answer the App-identity read still gets its task list, with the raw
	// ref standing in for the readable label.
	responses := taskResponses()
	responses["TeamAppIdentity"] = gqlErrorJSON("INTERNAL_SERVER_ERROR")
	calls, out, err := runTeamTask(t, responses, "team", "task", "list")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range *calls {
		if c.Op == "AgentTaskList" && c.Session != "" {
			t.Fatalf("read attributed: %q", c.Session)
		}
	}
	if !strings.Contains(out, "app: capp100000000000000000000") {
		t.Fatalf("degraded scope line missing the ref: %q", out)
	}
	if !strings.Contains(out, "Fix the flaky CI build") {
		t.Fatalf("rows lost to the decoration failure: %q", out)
	}
}

// ---- cycle 1 (Diego #9094, confirmed live by Colin #9102) ------------------

// writeCountingWriter fails from the nth Write on — the earlier writes stand
// in for the render lines that precede the table whose Flush error the P3-2
// test proves is no longer discarded.
type writeCountingWriter struct{ failFrom, writes int }

func (w *writeCountingWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writes >= w.failFrom {
		return 0, errors.New("write failed late")
	}
	return len(p), nil
}

func TestTeamAvailabilityGetFlushErrorPropagates(t *testing.T) {
	// Diego P3-2: the availability tables discarded their Flush errors, so a
	// broken pipe could report success. The app line (write 1) and the first
	// table's header (write 2) succeed; the failure lands inside the tables,
	// where the discarded error used to vanish.
	writeTeamBinding(t)
	srv, _ := attnServer(t, taskResponses())
	setTeamBindingServer(t, srv.URL)
	f, _ := testFactory(t)
	f.IOStreams.Out = &writeCountingWriter{failFrom: 3}
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "availability", "get", "--server", srv.URL})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "write failed late") {
		t.Fatalf("flush error discarded: %v", err)
	}
}

func TestTeamEventsLimitValidated(t *testing.T) {
	// Diego P2-2: --limit 0 panicked on the empty page (0==0 indexed -1);
	// --limit 201 silently clamped to 200 server-side and suppressed the
	// continuation hint. Both refuse loudly now, naming the cap.
	for _, bad := range []string{"0", "-1", "201", "9999"} {
		t.Run(bad, func(t *testing.T) {
			_, _, err := runTeamTask(t, taskResponses(), "team", "events", "--limit", bad)
			if err == nil || !strings.Contains(err.Error(), "200") {
				t.Fatalf("--limit %s accepted: %v", bad, err)
			}
		})
	}
}

func TestTeamTaskListLimitValidated(t *testing.T) {
	for _, bad := range []string{"0", "201"} {
		t.Run(bad, func(t *testing.T) {
			_, _, err := runTeamTask(t, taskResponses(), "team", "task", "list", "--limit", bad)
			if err == nil || !strings.Contains(err.Error(), "200") {
				t.Fatalf("--limit %s accepted: %v", bad, err)
			}
		})
	}
}

func TestTeamEventsFullPageAtTheCapStillHints(t *testing.T) {
	// The hint must compare against the EFFECTIVE page size, not the raw
	// flag: exactly 200 events at the cap name the next cursor.
	responses := taskResponses()
	many := strings.Repeat(`{"id":"x","appId":"a","taskId":null,"type":"task.created","severity":null,"createdAt":"2026-10-12T00:00:00Z","actor":null,"subjectWorker":null,"payload":{},"acknowledgedAt":null,"acknowledgedBy":null,"episodeStart":null,"chain":null,"wakes":null},`, 200)
	responses["AgentTeamEvents"] = `{"data":{"agentTeamEvents":[` + many[:len(many)-1] + `]}}`
	_, out, err := runTeamTask(t, responses, "team", "events", "--limit", "200")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "continue with --after") {
		t.Fatalf("full-at-cap page carried no continuation hint: %q", out)
	}
}

// alarmPagingServer answers the tree's alarm reads page-aware: the first
// AgentTeamEvents (no after) returns a FULL page of acknowledged, unrelated
// alarms; every continuation returns the one OPEN alarm on task1. The
// one-page implementation Diego's P2-1 names never sees the open alarm.
func alarmPagingServer(t *testing.T) (*httptest.Server, *[]attnCall) {
	t.Helper()
	calls := &[]attnCall{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string         `json:"operationName"`
			Variables     map[string]any `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		*calls = append(*calls, attnCall{Op: body.OperationName, Vars: body.Variables, Session: r.Header.Get("X-Hadron-Session")})
		w.Header().Set("Content-Type", "application/json")
		switch body.OperationName {
		case "AgentTaskTree":
			_, _ = w.Write([]byte(`{"data":{"agentTaskTree":[` + taskFieldsJSON + `]}}`))
		case "AgentTeamEvents":
			if body.Variables["after"] == nil {
				page := strings.TrimSuffix(strings.Repeat(ackedAlarmRow+",", 200), ",")
				_, _ = w.Write([]byte(`{"data":{"agentTeamEvents":[` + page + `]}}`))
			} else {
				_, _ = w.Write([]byte(`{"data":{"agentTeamEvents":[` + alarmEventFieldsJSON + `]}}`))
			}
		default:
			t.Errorf("unexpected operation %q", body.OperationName)
			_, _ = w.Write([]byte(`{"errors":[{"message":"unexpected operation"}]}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, calls
}

const ackedAlarmRow = `{"id":"acked-a","appId":"capp100000000000000000000","taskId":"task9",
	"type":"stall.no-progress","severity":"ALARM","createdAt":"2026-10-01T00:00:00Z",
	"actor":null,"subjectWorker":null,"payload":{},"acknowledgedAt":"2026-10-01T01:00:00Z",
	"acknowledgedBy":null,"episodeStart":null,"chain":null,"wakes":null}`

func TestTeamTaskTreePaginatesAlarmPages(t *testing.T) {
	writeTeamBinding(t)
	srv, calls := alarmPagingServer(t)
	setTeamBindingServer(t, srv.URL)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "task", "tree", "task1", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	rendered := out.String()
	pages := 0
	var continuation any
	for _, c := range *calls {
		if c.Op == "AgentTeamEvents" {
			pages++
			continuation = c.Vars["after"]
		}
	}
	if pages < 2 {
		t.Fatalf("alarms read stopped at page 1 (%d AgentTeamEvents calls) — the open alarm on page 2 was never seen", pages)
	}
	if continuation == nil {
		t.Fatal("continuation page carried no after cursor")
	}
	if !strings.Contains(rendered, "1 open alarm") {
		t.Fatalf("render %q", rendered)
	}
}

func TestTeamAvailabilitySetWorkerUrnWithoutApp(t *testing.T) {
	// Diego P3-1: a worker id or URN needs no App — the server takes it
	// self-sufficient — but the implementation demanded scope regardless.
	srv, calls := attnServer(t, taskResponses())
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "availability", "set",
		"--worker", "hrn:worker:acme.com:eng:rufus",
		"--state", "unavailable", "--reason", "STALLED",
		"--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, c := range *calls {
		if c.Op == "WorkerAvailabilitySet" {
			input, _ := c.Vars["input"].(map[string]any)
			if input == nil || input["appRef"] != nil {
				t.Fatalf("appRef %v — a URN names its worker without one", c.Vars["input"])
			}
			return
		}
	}
	t.Fatal("no WorkerAvailabilitySet call")
}

func TestTeamAvailabilitySetWorkerNameStillNeedsTheApp(t *testing.T) {
	// The strictness control beside P3-1: a NAME is ambiguous without the
	// App, and widening the URN path must not widen this one.
	srv, _ := attnServer(t, taskResponses())
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "availability", "set",
		"--worker", "Rufus", "--state", "unavailable", "--reason", "STALLED",
		"--server", srv.URL})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "--app") {
		t.Fatalf("bare name without an App accepted: %v", err)
	}
}
