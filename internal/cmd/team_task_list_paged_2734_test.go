package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2734 (hadron-server#4622): the task list reads adopt agentTasksPage —
// {items, total, hasMore} — so "is there more" is the server's answer, not a
// full-page guess. One page per invocation; --json becomes the page object;
// the whole-App reads (--team, the intake adapter) page to exhaustion; a
// server without the field refuses loudly, with no fallback to agentTasks.

// taskPage2734 is one agentTasksPage answer.
func taskPage2734(items string, total int, hasMore bool) string {
	return fmt.Sprintf(`{"data":{"agentTasksPage":{"items":[%s],"total":%d,"hasMore":%t}}}`, items, total, hasMore)
}

// taskWithID2734 is taskFieldsJSON under a different id, so a paged walk can
// be seen to accumulate distinct tasks.
func taskWithID2734(id string) string {
	return strings.ReplaceAll(taskFieldsJSON, `"task1"`, `"`+id+`"`)
}

// taskRows2734 is n distinct tasks as an items array body.
func taskRows2734(n int) string {
	rows := make([]string, n)
	for i := range rows {
		rows[i] = taskWithID2734(fmt.Sprintf("task%d", i))
	}
	return strings.Join(rows, ",")
}

// runTeamTaskStaged2734 is runTeamTask with a hook fired as each operation
// arrives (attnServerHook), so a test can answer page 2 of one operation with
// a different body than page 1.
func runTeamTaskStaged2734(t *testing.T, responses map[string]string, onOp func(op string), argv ...string) (*[]attnCall, string, string, error) {
	t.Helper()
	writeTeamBinding(t)
	srv, calls := attnServerHook(t, responses, onOp)
	setTeamBindingServer(t, srv.URL)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"--server", srv.URL}, argv...))
	err := root.Execute()
	return calls, out.String(), f.IOStreams.ErrOut.(*strings.Builder).String(), err
}

// ---- the list reads the paged root field; --json is the page object ------

func TestTaskListUsesAgentTasksPageAndJSONWrapper2734(t *testing.T) {
	r := taskResponses()
	r["AgentTaskList"] = taskPage2734(taskFieldsJSON, 5, true)
	calls, out, err := runTeamTask(t, r, "team", "task", "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	c := taskListCall(t, calls)
	if !strings.Contains(c.Query, "agentTasksPage(") {
		t.Errorf("the list reads agentTasksPage, not the legacy array: %s", c.Query)
	}
	if strings.Contains(c.Query, "agentTasks(") {
		t.Errorf("the legacy array read is gone from the document: %s", c.Query)
	}
	var page struct {
		Tasks   []json.RawMessage `json:"tasks"`
		Total   int               `json:"total"`
		HasMore bool              `json:"hasMore"`
	}
	if err := json.Unmarshal([]byte(out), &page); err != nil {
		t.Fatalf("--json is {tasks,total,hasMore}, never a bare array: %v: %s", err, out)
	}
	if len(page.Tasks) != 1 || page.Total != 5 || !page.HasMore {
		t.Fatalf("page echoed as served: %+v", page)
	}
}

func TestTaskMineJSONIsPageObject2734(t *testing.T) {
	r := taskResponses()
	r["AgentTaskList"] = taskPage2734(startedTaskFieldsJSON, 1, false)
	_, out, err := runTeamTask(t, r, "team", "task", "mine", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		Tasks   []json.RawMessage `json:"tasks"`
		Total   int               `json:"total"`
		HasMore bool              `json:"hasMore"`
	}
	if err := json.Unmarshal([]byte(out), &page); err != nil {
		t.Fatalf("mine --json is the same page object: %v: %s", err, out)
	}
	if len(page.Tasks) != 1 || page.Total != 1 || page.HasMore {
		t.Fatalf("got %+v", page)
	}
}

// ---- text output: the truth is said only when the server says more -------

func TestTaskListTextContinuationOnlyWhenHasMore2734(t *testing.T) {
	// hasMore: the line names the total and the exact --offset to continue at —
	// offset + the ROWS SERVED (cor:api:120:04), so a short page (one row of a
	// 20-row page, more remain) continues at 1, not at the page size. The line
	// is on stderr, with the other continuation lines; stdout is the table.
	r := taskResponses()
	r["AgentTaskList"] = taskPage2734(taskFieldsJSON, 5, true)
	_, out, errOut, err := runTeamTaskStaged2734(t, r, nil, "team", "task", "list")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"more", "5", "--offset 1"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("continuation line missing %q from stderr: %q", want, errOut)
		}
	}
	if strings.Contains(out, "--offset") {
		t.Errorf("the continuation is not on stdout: %q", out)
	}
	if strings.Contains(errOut, "Showing a full page") {
		t.Errorf("the old full-page guess is gone from stderr: %q", errOut)
	}

	// A full page the server says is the end is the end.
	r = taskResponses()
	r["AgentTaskList"] = taskPage2734(taskRows2734(20), 20, false)
	_, out, errOut, err = runTeamTaskStaged2734(t, r, nil, "team", "task", "list")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "--offset") || strings.Contains(out, "more") {
		t.Errorf("hasMore false: no continuation is invented: %q", out)
	}
	if errOut != "" {
		t.Errorf("hasMore false: stderr is silent: %q", errOut)
	}

	// An explicit --limit does not suppress the server's truth.
	r = taskResponses()
	r["AgentTaskList"] = taskPage2734(taskRows2734(20), 40, true)
	_, _, errOut, err = runTeamTaskStaged2734(t, r, nil, "team", "task", "list", "--limit", "20")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "--offset 20") {
		t.Errorf("an explicit limit is the user's page, and more is still said: %q", errOut)
	}
}

// ---- an untrustworthy page refuses, never an empty success ---------------

func TestTaskListMalformedPageRefuses2734(t *testing.T) {
	for name, resp := range map[string]string{
		"no page object": `{"data":{}}`,
		"null page":      `{"data":{"agentTasksPage":null}}`,
		"items null":     `{"data":{"agentTasksPage":{"items":null,"total":0,"hasMore":false}}}`,
		"null item":      `{"data":{"agentTasksPage":{"items":[null],"total":1,"hasMore":false}}}`,
		"empty, more":    `{"data":{"agentTasksPage":{"items":[],"total":5,"hasMore":true}}}`,
	} {
		r := taskResponses()
		r["AgentTaskList"] = resp
		_, _, _, err := runTeamTaskStaged2734(t, r, nil, "team", "task", "list", "--json")
		if err == nil {
			t.Errorf("%s: a malformed page must refuse, not render an empty success", name)
			continue
		}
		if code := exitCodeFor(err); code != exitcode.Error {
			t.Errorf("%s: exit %d, want %d (a server failure, not usage): %v", name, code, exitcode.Error, err)
		}
		if !strings.Contains(err.Error(), "malformed agentTasksPage") {
			t.Errorf("%s: the refusal names the malformed page: %v", name, err)
		}
	}
}

// ---- an old server refuses; the CLI does not fall back -------------------

func TestTaskListOldServerRefusesWithNoFallback2734(t *testing.T) {
	r := taskResponses()
	r["AgentTaskList"] = `{"data":null,"errors":[{"message":"Cannot query field \"agentTasksPage\" on type \"Query\". Did you mean \"agentTasks\"?","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`
	calls, out, _, err := runTeamTaskStaged2734(t, r, nil, "team", "task", "list")
	if err == nil || !strings.Contains(err.Error(), "agentTasksPage") {
		t.Fatalf("a server without the paged field refuses loudly: %v", err)
	}
	if strings.Contains(out, "No tasks") {
		t.Errorf("a refusal is not an empty list: %q", out)
	}
	if len(*calls) != 1 {
		t.Errorf("no fallback read follows the refusal: %+v", *calls)
	}
}

// ---- the --team view walks to !hasMore ------------------------------------

func TestTeamViewWalksHasMore2734(t *testing.T) {
	r := taskResponses()
	r["TeamReportingLines"] = r["TeamRoster"]
	r["AgentTaskList"] = taskPage2734(strings.Join([]string{
		taskWithID2734("v1"), taskWithID2734("v2"), taskWithID2734("v3"),
	}, ","), 4, true)
	page2 := taskPage2734(taskWithID2734("v4"), 4, false)
	seen := 0
	// onOp fires before the arriving request's own response lookup: restage on
	// the SECOND arrival, so page 1 answers page 1 and page 2 answers page 2.
	stage := func(op string) {
		if op != "AgentTaskList" {
			return
		}
		seen++
		if seen == 2 {
			setAttnResponse(r, "AgentTaskList", page2)
		}
	}
	calls, out, _, err := runTeamTaskStaged2734(t, r, stage, "team", "task", "list", "--team")
	if err != nil {
		t.Fatal(err)
	}
	reads := []attnCall{}
	for _, c := range *calls {
		if c.Op == "AgentTaskList" {
			reads = append(reads, c)
		}
	}
	if len(reads) != 2 {
		t.Fatalf("the view walks to !hasMore, %d reads: %+v", len(reads), *calls)
	}
	// The walk advances by the rows served (cor:api:120:04): page 1 returned
	// three rows, so page 2 continues at offset 3 — not at the 200 page size,
	// which would skip everything a short page leaves behind.
	if off, _ := reads[1].Vars["offset"].(float64); off != 3 {
		t.Errorf("page 2 continues at the rows served: offset %v", reads[1].Vars["offset"])
	}
	if n := strings.Count(out, "Fix the flaky CI build"); n != 4 {
		t.Errorf("all four tasks render once: %d rows", n)
	}
	if strings.Contains(out, "--offset") {
		t.Errorf("the whole-App view ends complete, no continuation line: %q", out)
	}
}

func TestTeamViewRefusesANonAdvancingHasMore2734(t *testing.T) {
	r := taskResponses()
	r["TeamReportingLines"] = r["TeamRoster"]
	dup := taskPage2734(strings.Join([]string{
		taskWithID2734("v1"), taskWithID2734("v2"), taskWithID2734("v3"),
	}, ","), 6, true)
	r["AgentTaskList"] = dup
	seen := 0
	stage := func(op string) {
		if op != "AgentTaskList" {
			return
		}
		seen++
		if seen == 1 {
			setAttnResponse(r, "AgentTaskList", dup) // same page again, hasMore still true
		}
	}
	_, _, _, err := runTeamTaskStaged2734(t, r, stage, "team", "task", "list", "--team")
	if err == nil || !strings.Contains(err.Error(), "did not advance") {
		t.Fatalf("hasMore with no new task is a server ignoring the offset: %v", err)
	}
}

// ---- the intake adapter's held read pages (server#4622 does not widen) ---

func run1961Staged2734(t *testing.T, path string, responses map[string]string, onOp func(op string), argv ...string) (*[]attnCall, string, string, error) {
	t.Helper()
	return runTeamTaskStaged2734(t, responses, onOp, append([]string{"team", "task", "find-untracked", "--adapter", path}, argv...)...)
}

func TestAdapterHeldReadFollowsHasMore2734(t *testing.T) {
	list := `echo '{"items":[` + item1961(itemA1961, "one") + `,` + item1961(itemB1961, "two") + `]}'`
	path, _ := fakeAdapter1961(t, "", list, "")
	page1 := `{"data":{"agentTasksPage":{"items":[{"id":"held-0","externalId":` + fmt.Sprintf("%q", itemA1961) + `}],"total":2,"hasMore":true}}}`
	page2 := `{"data":{"agentTasksPage":{"items":[{"id":"held-1","externalId":` + fmt.Sprintf("%q", itemB1961) + `}],"total":2,"hasMore":false}}}`
	r := withEmptyChanges1962(map[string]string{"IntakeTasksByExternalIds": page1})
	seen := 0
	// onOp fires when a request arrives, BEFORE that request's response lookup:
	// restage on the SECOND arrival so page 1 answers page 1 and page 2 page 2
	// (restaging on the first would hand page 2's content to page 1).
	stage := func(op string) {
		if op != "IntakeTasksByExternalIds" {
			return
		}
		seen++
		if seen == 2 {
			setAttnResponse(r, "IntakeTasksByExternalIds", page2)
		}
	}
	calls, out, _, err := run1961Staged2734(t, path, r, stage, "--json")
	if err != nil {
		ops := []string{}
		for _, c := range *calls {
			ops = append(ops, c.Op)
		}
		t.Fatal(err, "ops:", strings.Join(ops, ","), "out:", out)
	}
	reads := opsOf1961(calls, "IntakeTasksByExternalIds")
	if len(reads) != 2 {
		t.Fatalf("the held read follows hasMore to the end: %d reads", len(reads))
	}
	// Rows served, not the 20-row page size: page 1 held one row, so page 2
	// continues at offset 1.
	if off, _ := reads[1].Vars["offset"].(float64); off != 1 {
		t.Errorf("page 2 continues at the rows served: %v", reads[1].Vars["offset"])
	}
	var dto struct {
		Items    []struct{ ExternalId string }
		Complete bool
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if len(dto.Items) != 0 || !dto.Complete {
		t.Fatalf("both items are held; page 2's holder must not be missed: %+v", dto)
	}
}

// TestAdapterHeldReadRefusesEmptyPageWithMore2734: items [] with hasMore true
// can never advance the held read (it steps by rows served), so it is refused
// on the first page as a server failure instead of paging to the bound.
func TestAdapterHeldReadRefusesEmptyPageWithMore2734(t *testing.T) {
	list := `echo '{"items":[` + item1961(itemA1961, "one") + `]}'`
	path, _ := fakeAdapter1961(t, "", list, "")
	r := withEmptyChanges1962(map[string]string{
		"IntakeTasksByExternalIds": `{"data":{"agentTasksPage":{"items":[],"total":5,"hasMore":true}}}`,
	})
	_, _, _, err := run1961Staged2734(t, path, r, nil, "--json")
	if err == nil || !strings.Contains(err.Error(), "empty page claims more remain") {
		t.Fatalf("an empty page with hasMore is a refused server failure: %v", err)
	}
}
