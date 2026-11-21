package cmd

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// cli#2264 (server#4217, cor:agt:120:02 as amended 2026-11-14): an external id
// is held by at most ONE ACTIVE task (backlog, pending, started) per App; a
// finished or cancelled task keeps its id but does not block a new claim. So
// `task list --external-id` can list several tasks, newest first, and the
// holder is the one that is still active. The CLI says so, and the active-only
// view is the existing --status filter.

const startedExternalIDTaskJSON = `{"id":"task2","appId":"capp100000000000000000000","parentTaskId":null,"title":"Current claim","description":null,"status":"STARTED","priority":3,"blocked":false,"owner":{"id":"wkr1","kind":"WORKER","name":"Iris","urn":"hrn:worker:acme.com:eng:iris"},"assignee":{"id":"wkr1","kind":"WORKER","name":"Iris","urn":"hrn:worker:acme.com:eng:iris"},"createdBy":{"id":"wkr1","kind":"WORKER","name":"Iris","urn":"hrn:worker:acme.com:eng:iris"},"createdAt":"2026-11-14T12:00:00Z","updatedAt":"2026-11-14T12:00:00Z"}`

func taskIDsFromJSON(t *testing.T, out string) []string {
	t.Helper()
	// cli#2734: `task list --json` is the page object {tasks, total, hasMore}
	// (was a bare array; the cli#1919 precedent).
	var page struct {
		Tasks []struct {
			ID string `json:"id"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(out), &page); err != nil {
		t.Fatalf("not a task page: %v\n%s", err, out)
	}
	rows := page.Tasks
	ids := []string{}
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	return ids
}

// Two tasks, one external id (newest first, as the server returns them): the
// list prints both, in that order, in text and --json.
func TestTaskListExternalIDShowsHistoryNewestFirst2264(t *testing.T) {
	r := taskResponses()
	both := taskPage2734(startedExternalIDTaskJSON+","+finishedTaskFieldsJSON, 2, false)
	r["AgentTaskListFiltered"], r["AgentTaskList"] = both, both
	calls, out, err := runTeamTask(t, r, "team", "task", "list", "--external-id", "https://github.com/acme/repo/issues/1", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if ids := taskIDsFromJSON(t, out); len(ids) != 2 || ids[0] != "task2" || ids[1] != "task1" {
		t.Fatalf("ids = %v, want [task2 task1] (newest first, none dropped, none re-sorted)", ids)
	}
	sent := false
	for _, c := range *calls {
		if c.Op == "AgentTaskListFiltered" {
			sent = true
			if c.Vars["externalId"] != "https://github.com/acme/repo/issues/1" {
				t.Fatalf("the external id did not reach the server: %v", c.Vars)
			}
			if _, has := c.Vars["status"]; has {
				t.Fatalf("a bare --external-id must not narrow by status: %v", c.Vars)
			}
		}
	}
	if !sent {
		t.Fatal("no filtered request")
	}
	_, text, err := runTeamTask(t, r, "team", "task", "list", "--external-id", "x")
	if err != nil {
		t.Fatal(err)
	}
	if i, j := strings.Index(text, "task2"), strings.Index(text, "task1"); i < 0 || j < 0 || i > j {
		t.Fatalf("text does not list both, newest first:\n%s", text)
	}
}

// The active holder is one call: --external-id with the three active statuses.
// The status filter is sent as given and the CLI never picks "the first row".
func TestTaskListExternalIDActiveHolderIsTheStatusFilter2264(t *testing.T) {
	r := taskResponses()
	only := taskPage2734(startedExternalIDTaskJSON, 1, false)
	r["AgentTaskListFiltered"], r["AgentTaskList"] = only, only
	calls, out, err := runTeamTask(t, r, "team", "task", "list", "--external-id", "x",
		"--status", "BACKLOG", "--status", "PENDING", "--status", "STARTED", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if ids := taskIDsFromJSON(t, out); len(ids) != 1 || ids[0] != "task2" {
		t.Fatalf("ids = %v, want only the STARTED holder", ids)
	}
	for _, c := range *calls {
		if c.Op == "AgentTaskListFiltered" {
			if c.Vars["externalId"] != "x" {
				t.Fatalf("the external id did not reach the server: %v", c.Vars)
			}
			raw, _ := json.Marshal(c.Vars["status"])
			var got []string
			if err := json.Unmarshal(raw, &got); err != nil || strings.Join(got, ",") != "BACKLOG,PENDING,STARTED" {
				t.Fatalf("status = %s (%v), want BACKLOG,PENDING,STARTED", raw, err)
			}
			return
		}
	}
	t.Fatal("no filtered request")
}

// With no active holder the answer is an empty list, never the newest ended
// task.
func TestTaskListExternalIDNoActiveHolderIsEmpty2264(t *testing.T) {
	r := taskResponses()
	none := taskPage2734("", 0, false)
	r["AgentTaskListFiltered"], r["AgentTaskList"] = none, none
	calls, out, err := runTeamTask(t, r, "team", "task", "list", "--external-id", "x", "--status", "BACKLOG", "--status", "PENDING", "--status", "STARTED", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if ids := taskIDsFromJSON(t, out); len(ids) != 0 {
		t.Fatalf("ids = %v, want none", ids)
	}
	for _, c := range *calls {
		if c.Op == "AgentTaskListFiltered" {
			raw, _ := json.Marshal(c.Vars["status"])
			if c.Vars["externalId"] != "x" || string(raw) != `["BACKLOG","PENDING","STARTED"]` {
				t.Fatalf("the empty-result request lost its filters: %v", c.Vars)
			}
			return
		}
	}
	t.Fatal("no filtered request")
}

// The help tells the active rule and no longer the old one-per-App rule.
func TestTaskExternalIDHelpStatesTheActiveRule2264(t *testing.T) {
	for _, argv := range [][]string{{"team", "task", "list", "--help"}, {"team", "task", "create", "--help"}} {
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(argv)
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		help := out.String()
		for _, stale := range []string{"at most one per App", "unique per App and set once", "unique per App, set once", "the task claiming exactly this external id"} {
			if strings.Contains(help, stale) {
				t.Errorf("%v help still says %q", argv, stale)
			}
		}
		for _, want := range []string{"ACTIVE", "finished", "cancelled"} {
			if !strings.Contains(help, want) {
				t.Errorf("%v help does not say %q about the external id", argv, want)
			}
		}
	}
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "task", "list", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	list := out.String()
	if !strings.Contains(list, "--status BACKLOG --status PENDING --status STARTED") || !strings.Contains(list, "newest first") {
		t.Errorf("list help does not show the active-holder lookup and the order:\n%s", list)
	}
	// Jibril's P2s: an empty result establishes only that nothing matched the
	// App and filters used, and --unresolved lists finished tasks only.
	for _, bad := range []string{"nobody holds it now"} {
		if strings.Contains(list, bad) {
			t.Errorf("list help overclaims (%q):\n%s", bad, list)
		}
	}
	for _, want := range []string{"under these filters", "same App as the refused create", "--unresolved", "cannot find an active holder"} {
		if !strings.Contains(strings.Join(strings.Fields(list), " "), want) {
			t.Errorf("list help does not say %q:\n%s", want, list)
		}
	}
}

// Jibril's P2s in the shipped reference and the query comments, scoped
// (cli#2339): the reference is asserted inside the `task create --external-id`
// paragraph, not anywhere in the file, and each query's qualification is
// asserted in the comment attached to its own `$externalId` declaration.
// cor:agt:120:02 holds an outside item per App, by at most one ACTIVE task, so
// neither narrowed query finds "the live holder".
func TestTaskExternalIDReferenceAndQueryCommentsAreQualified2264(t *testing.T) {
	ref, err := os.ReadFile("agentic/agentic-usage.md")
	if err != nil {
		t.Fatal(err)
	}
	const head = "- **`task create --external-id <id>`**"
	doc := string(ref)
	i := strings.Index(doc, head)
	if i < 0 {
		t.Fatalf("the %q paragraph is not in agentic-usage.md", head)
	}
	para := doc[i+len(head):]
	if j := strings.Index(para, "\n- **"); j >= 0 {
		para = para[:j]
	}
	flat := strings.Join(strings.Fields(para), " ")
	if strings.Contains(flat, "nobody holds it now") {
		t.Error("the task create --external-id paragraph overclaims what an empty external-id lookup means")
	}
	for _, want := range []string{"under these filters", "same App as the refused create", "no other narrowing filter"} {
		if !strings.Contains(flat, want) {
			t.Errorf("the task create --external-id paragraph does not say %q", want)
		}
	}
	q, err := os.ReadFile("../api/queries/agenttasks.graphql")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ name, want string }{
		{"AgentTaskListUnresolved", "FINISHED tasks"},
		{"AgentTaskListCreator", "narrowed to one creator"},
	} {
		i := strings.Index(string(q), "query "+c.name+"(")
		if i < 0 {
			t.Fatalf("query %s not found", c.name)
		}
		block := string(q)[i:]
		if j := strings.Index(block, "\n}\n"); j > 0 {
			block = block[:j]
		}
		if strings.Contains(block, "finds the live holder") {
			t.Errorf("%s's comment promises a live-holder lookup its scope cannot give", c.name)
		}
		// The comment lines directly above the $externalId declaration.
		lines := strings.Split(block, "\n")
		decl := -1
		for k, l := range lines {
			if strings.HasPrefix(strings.TrimSpace(l), "$externalId:") {
				decl = k
			}
		}
		if decl < 0 {
			t.Fatalf("%s has no $externalId declaration", c.name)
		}
		var cm []string
		for k := decl - 1; k >= 0 && strings.HasPrefix(strings.TrimSpace(lines[k]), "#"); k-- {
			cm = append(cm, lines[k])
		}
		comment := strings.Join(strings.Fields(strings.Join(cm, " ")), " ")
		if !strings.Contains(comment, c.want) || !strings.Contains(comment, "not the live-holder lookup") {
			t.Errorf("%s: the comment at $externalId must say %q and %q, got: %s", c.name, c.want, "not the live-holder lookup", comment)
		}
	}
}
