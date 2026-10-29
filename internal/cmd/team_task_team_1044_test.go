package cmd

// hadron-cli#1044 slice 1: `team task list --team [worker:<ref>]` with
// --tree and --stale-over. The team is the coordinator plus every worker
// whose roster reportsTo chain reaches them, within the bound App; the rows
// are the App's tasks a team member owns or is assigned, plus every
// descendant of those, read through the one existing task read (agentTasks)
// paged to the end.

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// ---- fixtures ---------------------------------------------------------------

func worker1044(name string) string {
	return fmt.Sprintf(`{"id":"w-%s","kind":"WORKER","name":"%s","urn":null}`, name, name)
}

const user1044 = `{"id":"u1","kind":"USER","name":"holger","urn":null}`

// rosterRow1044 is a roster row whose worker reports to boss ("" for none).
func rosterRow1044(name, boss string) string {
	row := rosterRow993(name, "AVAILABLE", true, "null", "null")
	if boss == "" {
		return row
	}
	return strings.Replace(row, `"reportsTo":null`, `"reportsTo":`+worker1044(boss), 1)
}

// The App: Marco leads Jonas, who leads Cedar (a nested chain); Ada leads
// Cameron, a separate reporting subtree of the same App.
func roster1044() []string {
	return []string{
		rosterRow1044("Marco", ""),
		rosterRow1044("Jonas", "Marco"),
		rosterRow1044("Cedar", "Jonas"),
		rosterRow1044("Ada", ""),
		rosterRow1044("Cameron", "Ada"),
	}
}

type task1044 struct {
	id, status, owner, assignee, parent string
	createdBy                           string // "" = the owner created it
	createdAt, updatedAt, startedAt     string // "" renders null (createdAt is required)
}

func ts1044(s string) string {
	if s == "" {
		return "null"
	}
	return strconv.Quote(s)
}

func (t task1044) json() string {
	assignee := "null"
	if t.assignee != "" {
		assignee = t.assignee
	}
	parent := "null"
	if t.parent != "" {
		parent = strconv.Quote(t.parent)
	}
	creator := t.createdBy
	if creator == "" {
		creator = t.owner
	}
	created := t.createdAt
	if created == "" {
		created = "2026-10-20T00:00:00Z"
	}
	status := t.status
	if status == "" {
		status = "STARTED"
	}
	return fmt.Sprintf(`{"id":%q,"appId":"capp100000000000000000000","title":"title of %s","description":null,
		"status":%q,"queued":false,"waitingOnChildren":false,"owner":%s,"assignee":%s,"assignedBy":null,
		"createdBy":%s,"parentTaskId":%s,"tags":[],"source":null,"startBy":null,
		"createdAt":%q,"updatedAt":%s,"startedAt":%s,"finishedAt":null,"cancelledAt":null,
		"closeSessionWhenDone":false,"requirements":null,"result":null}`,
		t.id, t.id, status, t.owner, assignee, creator, parent, created, ts1044(t.updatedAt), ts1044(t.startedAt))
}

func taskPage1044(tasks []task1044) string {
	rows := make([]string, 0, len(tasks))
	for _, t := range tasks {
		rows = append(rows, t.json())
	}
	return `{"data":{"agentTasks":[` + strings.Join(rows, ",") + `]}}`
}

// The App's tasks, newest first as the server orders them.
func tasks1044() []task1044 {
	return []task1044{
		{id: "t1", owner: worker1044("Marco"), assignee: worker1044("Jonas")},
		// Cross-team: a team member's seat task for a worker in Ada's subtree.
		{id: "t2", owner: worker1044("Jonas"), assignee: worker1044("Cameron"), parent: "t1"},
		// Cameron's own child under it: no team party, included as a descendant.
		{id: "t3", owner: worker1044("Cameron"), assignee: worker1044("Cameron"), parent: "t2"},
		// Ada's subtree only: never in Marco's view.
		{id: "t4", owner: worker1044("Ada"), assignee: worker1044("Cameron")},
		// Nested member (Cedar reports to Jonas reports to Marco).
		{id: "t5", owner: worker1044("Cedar")},
		// A person owns it; the team member is the assignee.
		{id: "t6", owner: user1044, assignee: worker1044("Cedar")},
		// A person's task for a worker outside the team: excluded.
		{id: "t7", owner: user1044, assignee: worker1044("Cameron")},
	}
}

// server1044 answers AgentTaskList page by page (pages[i] for the i-th call;
// an empty page past the end) and the roster.
func run1044(t *testing.T, roster []string, pages [][]task1044, argv ...string) (*[]attnCall, string, string, error) {
	t.Helper()
	writeTeamBinding(t)
	responses := rosterResponses993(roster...)
	responses["TeamReportingLines"] = responses["TeamRoster"]
	responses["AgentTeamEvents"] = `{"data":{"agentTeamEvents":[]}}`
	n := 0
	setPage := func() {
		if n < len(pages) {
			responses["AgentTaskList"] = taskPage1044(pages[n])
		} else {
			responses["AgentTaskList"] = `{"data":{"agentTasks":[]}}`
		}
	}
	setPage()
	srv, calls := attnServerHook(t, responses, func(op string) {
		if op == "AgentTaskList" {
			setPage()
			n++
		}
	})
	setTeamBindingServer(t, srv.URL)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"--server", srv.URL}, argv...))
	err := root.Execute()
	return calls, out.String(), f.IOStreams.ErrOut.(*strings.Builder).String(), err
}

type team1044JSON struct {
	AppID       string `json:"appId"`
	Coordinator struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"coordinator"`
	CoordinatorSource string `json:"coordinatorSource"`
	Members           []struct {
		ID string `json:"id"`
	} `json:"members"`
	Tasks []map[string]any `json:"tasks"`
}

func decode1044(t *testing.T, out string) team1044JSON {
	t.Helper()
	var got team1044JSON
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not the --team JSON envelope: %v\n%s", err, out)
	}
	return got
}

func taskIDs1044(tasks []map[string]any) []string {
	ids := []string{}
	for _, task := range tasks {
		ids = append(ids, fmt.Sprint(task["id"]))
	}
	return ids
}

// ---- team resolution, recursion, cross-team ----------------------------------

func TestTeamTaskListTeamResolvesNestedChainAndRecursion(t *testing.T) {
	_, out, _, err := run1044(t, roster1044(), [][]task1044{tasks1044()},
		"team", "task", "list", "--team=worker:Marco", "--json")
	if err != nil {
		t.Fatal(err)
	}
	got := decode1044(t, out)
	if got.Coordinator.ID != "w-Marco" || got.CoordinatorSource != "from --team" {
		t.Fatalf("coordinator %+v source %q", got.Coordinator, got.CoordinatorSource)
	}
	members := []string{}
	for _, m := range got.Members {
		members = append(members, m.ID)
	}
	if strings.Join(members, ",") != "w-Marco,w-Jonas,w-Cedar" {
		t.Fatalf("members %v, want Marco + Jonas + nested Cedar", members)
	}
	if ids := strings.Join(taskIDs1044(got.Tasks), ","); ids != "t1,t2,t3,t5,t6" {
		t.Fatalf("tasks %s, want t1,t2,t3,t5,t6 (server order; t4/t7 belong to another subtree)", ids)
	}
	byID := map[string]map[string]any{}
	for _, task := range got.Tasks {
		byID[fmt.Sprint(task["id"])] = task
	}
	for id, want := range map[string][3]string{
		"t1": {"team", "team", "party"},
		"t2": {"team", "outside", "party"},
		"t3": {"outside", "outside", "descendant"},
		"t6": {"user", "team", "party"},
	} {
		task := byID[id]
		if task["ownerTeam"] != want[0] || task["assigneeTeam"] != want[1] || task["match"] != want[2] {
			t.Errorf("%s: ownerTeam=%v assigneeTeam=%v match=%v, want %v", id, task["ownerTeam"], task["assigneeTeam"], task["match"], want)
		}
	}
	if byID["t3"]["parentTaskId"] != "t2" {
		t.Errorf("t3 parentTaskId %v", byID["t3"]["parentTaskId"])
	}
	if byID["t5"]["assigneeTeam"] != nil {
		t.Errorf("no assignee must be null, got %v", byID["t5"]["assigneeTeam"])
	}
}

// No wholesale visibility: the view is the bound App's one task read, joined
// to the roster — another subtree's tasks never appear, and the read sends
// no filter that would widen or narrow it server-side.
func TestTeamTaskListTeamNeverShowsAnotherSubtreeWholesale(t *testing.T) {
	calls, out, _, err := run1044(t, roster1044(), [][]task1044{tasks1044()},
		"team", "task", "list", "--team=worker:Ada", "--json")
	if err != nil {
		t.Fatal(err)
	}
	got := decode1044(t, out)
	// Ada's view: her own t4, Cameron's t2/t3/t7 rows; never Marco's t1/t5/t6.
	if ids := strings.Join(taskIDs1044(got.Tasks), ","); ids != "t2,t3,t4,t7" {
		t.Fatalf("Ada's tasks %s, want t2,t3,t4,t7", ids)
	}
	for _, c := range *calls {
		if c.Op != "AgentTaskList" {
			continue
		}
		if c.Vars["appRef"] != "capp100000000000000000000" {
			t.Fatalf("read another App: %v", c.Vars["appRef"])
		}
		for _, k := range []string{"owner", "assignee", "parentTaskId", "status", "tag", "queued", "rootsOnly"} {
			if _, ok := c.Vars[k]; ok {
				t.Fatalf("--team read sent filter %s=%v", k, c.Vars[k])
			}
		}
	}
}

// A bare --team is the bound worker, and the output says so (an ambient
// scope reports its source).
func TestTeamTaskListBareTeamIsTheBoundWorker(t *testing.T) {
	roster := append(roster1044(), strings.Replace(rosterRow1044("Rufus", "Marco"), `"id":"w-Rufus"`, `"id":"wkr1"`, 1))
	_, out, _, err := run1044(t, roster, [][]task1044{tasks1044()}, "team", "task", "list", "--team")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "team: Rufus (the bound worker)") {
		t.Fatalf("bare --team must name the bound worker and say so:\n%s", out)
	}
}

func TestTeamTaskListTeamUnknownWorkerIsNotFound(t *testing.T) {
	_, _, _, err := run1044(t, roster1044(), [][]task1044{tasks1044()},
		"team", "task", "list", "--team=worker:Nobody")
	if exitOf(err) != exitcode.NotFound || !strings.Contains(fmt.Sprint(err), "Nobody") {
		t.Fatalf("unknown coordinator: exit %d %v", exitOf(err), err)
	}
}

// ---- paging ------------------------------------------------------------------

func TestTeamTaskListTeamPagesToTheEnd(t *testing.T) {
	mk := func(from, n int) []task1044 {
		out := []task1044{}
		for i := from; i < from+n; i++ {
			out = append(out, task1044{id: fmt.Sprintf("p%03d", i), owner: worker1044("Jonas")})
		}
		return out
	}
	calls, out, _, err := run1044(t, roster1044(), [][]task1044{mk(0, 200), mk(200, 200), mk(400, 50)},
		"team", "task", "list", "--team=worker:Marco", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if n := len(decode1044(t, out).Tasks); n != 450 {
		t.Fatalf("got %d tasks, want all 450 across three pages", n)
	}
	offsets := []string{}
	for _, c := range *calls {
		if c.Op == "AgentTaskList" {
			offsets = append(offsets, fmt.Sprint(c.Vars["offset"]))
			if c.Vars["limit"] != float64(200) {
				t.Fatalf("page size %v, want the 200 cap", c.Vars["limit"])
			}
		}
	}
	if strings.Join(offsets, ",") != "0,200,400" {
		t.Fatalf("offsets %v", offsets)
	}
}

// A server that answers every offset with the same full page would loop
// forever or silently repeat: refuse, show nothing.
func TestTeamTaskListTeamRefusesANonAdvancingPage(t *testing.T) {
	page := []task1044{}
	for i := 0; i < 200; i++ {
		page = append(page, task1044{id: fmt.Sprintf("r%03d", i), owner: worker1044("Jonas")})
	}
	_, out, _, err := run1044(t, roster1044(), [][]task1044{page, page, page},
		"team", "task", "list", "--team=worker:Marco")
	if exitOf(err) != exitcode.Error || !strings.Contains(fmt.Sprint(err), "did not advance") {
		t.Fatalf("non-advancing paging: exit %d %v", exitOf(err), err)
	}
	if strings.Contains(out, "r000") {
		t.Fatalf("a refused read printed rows:\n%s", out)
	}
}

// ---- stale filter --------------------------------------------------------------

func TestTeamTaskListStaleOverKeepsExactlyTheIdleOpenTasks(t *testing.T) {
	now := time.Now().UTC()
	ago := func(d time.Duration) string { return now.Add(-d).Format(time.RFC3339Nano) }
	tasks := []task1044{
		{id: "idle-started", owner: worker1044("Jonas"), updatedAt: ago(2 * time.Hour)},
		{id: "fresh-started", owner: worker1044("Jonas"), updatedAt: ago(10 * time.Minute)},
		{id: "old-finished", status: "FINISHED", owner: worker1044("Jonas"), updatedAt: ago(3 * time.Hour)},
		// updatedAt null: falls back to startedAt, then createdAt.
		{id: "idle-by-started", owner: worker1044("Jonas"), startedAt: ago(90 * time.Minute), createdAt: ago(5 * time.Minute)},
		{id: "idle-pending-by-created", status: "PENDING", owner: worker1044("Jonas"), createdAt: ago(2 * time.Hour)},
		{id: "fresh-by-started", owner: worker1044("Jonas"), startedAt: ago(5 * time.Minute), createdAt: ago(5 * time.Hour)},
		{id: "idle-backlog", status: "BACKLOG", owner: worker1044("Jonas"), updatedAt: ago(9 * time.Hour)},
	}
	_, out, errOut, err := run1044(t, roster1044(), [][]task1044{tasks},
		"team", "task", "list", "--team=worker:Marco", "--stale-over", "1h", "--json")
	if err != nil {
		t.Fatal(err)
	}
	got := decode1044(t, out)
	if ids := strings.Join(taskIDs1044(got.Tasks), ","); ids != "idle-started,idle-by-started,idle-pending-by-created" {
		t.Fatalf("stale tasks %s", ids)
	}
	from := map[string]any{}
	for _, task := range got.Tasks {
		from[fmt.Sprint(task["id"])] = task["ageFrom"]
	}
	if from["idle-started"] != "updatedAt" || from["idle-by-started"] != "startedAt" || from["idle-pending-by-created"] != "createdAt" {
		t.Fatalf("ageFrom %v", from)
	}
	if !strings.Contains(errOut, "records lag") {
		t.Fatalf("--stale-over must say the record lags real progress, stderr %q", errOut)
	}
}

// --status filters the team view client-side, AFTER the recursion: a
// descendant whose parent is finished still belongs to the team.
func TestTeamTaskListTeamStatusAppliesAfterRecursion(t *testing.T) {
	tasks := []task1044{
		{id: "parent", status: "FINISHED", owner: worker1044("Jonas"), assignee: worker1044("Cameron")},
		{id: "child", owner: worker1044("Cameron"), assignee: worker1044("Cameron"), parent: "parent"},
	}
	_, out, _, err := run1044(t, roster1044(), [][]task1044{tasks},
		"team", "task", "list", "--team=worker:Marco", "--status", "STARTED", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if ids := strings.Join(taskIDs1044(decode1044(t, out).Tasks), ","); ids != "child" {
		t.Fatalf("tasks %s, want the open descendant of the finished team task", ids)
	}
}

// ---- tree render ----------------------------------------------------------------

func TestTeamTaskListTeamTreeNestsAndMarksCrossTeam(t *testing.T) {
	_, out, _, err := run1044(t, roster1044(), [][]task1044{tasks1044()},
		"team", "task", "list", "--team=worker:Marco", "--tree")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(out, "\n")
	indent := map[string]int{}
	for _, l := range lines {
		trimmed := strings.TrimLeft(l, " ")
		if !strings.HasPrefix(trimmed, "• ") {
			continue
		}
		id := strings.Fields(trimmed)[1]
		indent[id] = len(l) - len(trimmed)
	}
	if indent["t1"] != 0 || indent["t2"] != 2 || indent["t3"] != 4 || indent["t5"] != 0 {
		t.Fatalf("tree indents %v:\n%s", indent, out)
	}
	if _, ok := indent["t4"]; ok {
		t.Fatalf("another subtree's task rendered:\n%s", out)
	}
	if !strings.Contains(out, "Cameron (outside)") {
		t.Fatalf("a cross-team party must say so:\n%s", out)
	}
	if !strings.HasPrefix(out, "app: ") || !strings.Contains(out, "team: Marco (from --team)") {
		t.Fatalf("scope lines missing:\n%s", out)
	}
}

// A view that selects nothing still says which App and team it read.
func TestTeamTaskListTeamEmptyStillNamesItsScope(t *testing.T) {
	_, out, _, err := run1044(t, roster1044(), [][]task1044{{}},
		"team", "task", "list", "--team=worker:Marco", "--tree")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"app: ", "team: Marco", "no tasks"} {
		if !strings.Contains(out, want) {
			t.Errorf("empty view missing %q:\n%s", want, out)
		}
	}
}

// ---- usage refusals ---------------------------------------------------------------

func TestTeamTaskListTeamUsageRefusals(t *testing.T) {
	for _, argv := range [][]string{
		{"team", "task", "list", "--tree"},
		{"team", "task", "list", "--stale-over", "1h"},
		{"team", "task", "list", "--team", "--owner", "worker:Jonas"},
		{"team", "task", "list", "--team", "--limit", "5"},
		{"team", "task", "list", "--team", "--roots-only"},
		{"team", "task", "list", "--team", "--stale-over", "0s"},
		{"team", "task", "list", "--team", "--stale-over", "soon"},
		{"team", "task", "list", "--team=Marco"},
		{"team", "task", "list", "--team", "worker:Marco"},
	} {
		t.Run(strings.Join(argv[3:], " "), func(t *testing.T) {
			calls, _, _, err := run1044(t, roster1044(), [][]task1044{tasks1044()}, argv...)
			if exitOf(err) != exitcode.Usage {
				t.Fatalf("exit %d (%v), want usage", exitOf(err), err)
			}
			for _, c := range *calls {
				if c.Op == "AgentTaskList" || c.Op == "TeamRoster" || c.Op == "TeamReportingLines" {
					t.Fatalf("a usage error sent %s", c.Op)
				}
			}
		})
	}
}

// ---- slice 2: the creator joins the team view -----------------------------------

// A task owned and assigned outside the team joins when a team WORKER created
// it (match stays "party"); a task a person created, or a worker outside the
// team created, does not. The one read gate is unchanged.
func TestTeamTaskListTeamCreatorJoins(t *testing.T) {
	tasks := []task1044{
		// Created by Jonas (team), owned and assigned in Ada's subtree: joins.
		{id: "c1", owner: worker1044("Cameron"), assignee: worker1044("Cameron"), createdBy: worker1044("Jonas")},
		// Its child, all parties outside: joins as a descendant.
		{id: "c2", owner: worker1044("Cameron"), assignee: worker1044("Cameron"), createdBy: worker1044("Cameron"), parent: "c1"},
		// Created by a person for Ada's subtree: out.
		{id: "c3", owner: worker1044("Cameron"), assignee: worker1044("Cameron"), createdBy: user1044},
		// Created by a worker outside the team: out.
		{id: "c4", owner: worker1044("Cameron"), assignee: worker1044("Cameron"), createdBy: worker1044("Ada")},
		// A person named Jonas must not count: kind is part of the match.
		{id: "c5", owner: worker1044("Cameron"), assignee: worker1044("Cameron"),
			createdBy: `{"id":"w-Jonas","kind":"USER","name":"Jonas","urn":null}`},
	}
	calls, out, _, err := run1044(t, roster1044(), [][]task1044{tasks},
		"team", "task", "list", "--team=worker:Marco", "--json")
	if err != nil {
		t.Fatal(err)
	}
	got := decode1044(t, out)
	if ids := strings.Join(taskIDs1044(got.Tasks), ","); ids != "c1,c2" {
		t.Fatalf("tasks %s, want c1,c2 (creator c1 + descendant c2; person/outside creators stay out)", ids)
	}
	if got.Tasks[0]["match"] != "party" || got.Tasks[1]["match"] != "descendant" {
		t.Errorf("match %v / %v, want party / descendant", got.Tasks[0]["match"], got.Tasks[1]["match"])
	}
	if got.Tasks[0]["ownerTeam"] != "outside" || got.Tasks[0]["assigneeTeam"] != "outside" {
		t.Errorf("c1 cells %v/%v: parties stay labelled outside", got.Tasks[0]["ownerTeam"], got.Tasks[0]["assigneeTeam"])
	}
	for _, c := range *calls {
		if c.Op != "AgentTaskList" {
			continue
		}
		for _, k := range []string{"owner", "assignee", "creator", "status"} {
			if _, ok := c.Vars[k]; ok {
				t.Errorf("AgentTaskList sent %q: the view must stay one unfiltered read", k)
			}
		}
	}
}
