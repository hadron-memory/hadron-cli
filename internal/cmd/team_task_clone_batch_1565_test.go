package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cli#1565: `team task clone`, `team task create --batch-file` and the clone
// links on `team task get`.

func cloneResponses1565() map[string]string {
	r := taskResponses()
	r["AgentTaskClone"] = `{"data":{"cloneAgentTask":` + taskFieldsJSON + `}}`
	return r
}

func batchResponses1565(n int, extensions string) map[string]string {
	r := taskResponses()
	tasks := make([]string, n)
	for i := range tasks {
		tasks[i] = taskFieldsJSON
	}
	r["AgentTaskCreateBatch"] = `{"data":{"createAgentTasks":[` + strings.Join(tasks, ",") + `]}` + extensions + `}`
	return r
}

func errorResponse1565(code, message string, ext map[string]any) string {
	e := map[string]any{"code": code}
	for k, v := range ext {
		e[k] = v
	}
	b, _ := json.Marshal(map[string]any{"errors": []any{map[string]any{"message": message, "extensions": e}}})
	return string(b)
}

func callsOf1565(calls *[]attnCall, op string) []attnCall {
	var got []attnCall
	for _, c := range *calls {
		if c.Op == op {
			got = append(got, c)
		}
	}
	return got
}

func cloneInput1565(t *testing.T, args ...string) (map[string]json.RawMessage, string) {
	t.Helper()
	calls, _, err := runTeamTask(t, cloneResponses1565(), append([]string{"team", "task", "clone"}, args...)...)
	if err != nil {
		t.Fatalf("clone %v: %v", args, err)
	}
	call := c2Mutation(t, calls, "AgentTaskClone")
	var v struct {
		TaskID string                     `json:"taskId"`
		Input  map[string]json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(call.RawVars, &v); err != nil {
		t.Fatal(err)
	}
	return v.Input, v.TaskID
}

func TestTaskCloneSendsOnlyNamedOverrides1565(t *testing.T) {
	input, id := cloneInput1565(t, "AT0.12")
	if id != "AT0.12" || len(input) != 0 {
		t.Fatalf("a bare clone must send an empty input, got %s for %s", input, id)
	}
	input, _ = cloneInput1565(t, "AT0.12", "--title", "T2", "--description", "", "--priority", "3",
		"--status", "BACKLOG", "--source", "https://x/y", "--assignee", "worker:Iris",
		"--start-by", "2099-01-01T00:00:00Z", "--finish-by", "2099-02-01T00:00:00Z", "--no-close-session")
	want := map[string]string{
		"title": `"T2"`, "description": `""`, "priority": `3`, "status": `"BACKLOG"`, "source": `"https://x/y"`,
		"startBy": `"2099-01-01T00:00:00Z"`, "finishBy": `"2099-02-01T00:00:00Z"`, "closeSessionWhenDone": `false`,
	}
	for k, w := range want {
		if string(input[k]) != w {
			t.Errorf("%s = %s, want %s", k, input[k], w)
		}
	}
	if _, ok := input["assignee"]; !ok {
		t.Errorf("assignee not sent: %s", input)
	}
	if _, ok := input["parentTaskId"]; ok {
		t.Errorf("parentTaskId sent unasked: %s", input)
	}
}

func TestTaskCloneParentIsThreeState1565(t *testing.T) {
	// omitted: the source's parent is kept, so no key at all.
	input, _ := cloneInput1565(t, "AT1.4", "--title", "x")
	if _, ok := input["parentTaskId"]; ok {
		t.Fatalf("omitted parent must send no key: %s", input)
	}
	// --no-parent: explicit null (the parent is dropped).
	input, _ = cloneInput1565(t, "AT1.4", "--no-parent")
	if got, ok := input["parentTaskId"]; !ok || string(got) != "null" {
		t.Fatalf("--no-parent must send parentTaskId null, got %q present=%t", got, ok)
	}
	// --parent: the named task.
	input, _ = cloneInput1565(t, "AT1.4", "--parent", "AT2")
	if string(input["parentTaskId"]) != `"AT2"` {
		t.Fatalf("--parent AT2 must send the id, got %s", input["parentTaskId"])
	}
}

func TestTaskCloneRefusedBeforeAnyCall1565(t *testing.T) {
	for name, args := range map[string][]string{
		"parent and no-parent": {"clone", "AT1.4", "--parent", "AT2", "--no-parent"},
		"no-parent false":      {"clone", "AT1.4", "--no-parent=false"},
		"empty parent":         {"clone", "AT1.4", "--parent", ""},
		"close and no-close":   {"clone", "AT1.4", "--close-session", "--no-close-session"},
		"bad status":           {"clone", "AT1.4", "--status", "started"},
		"bad assignee":         {"clone", "AT1.4", "--assignee", "Iris"},
		"bad start-by":         {"clone", "AT1.4", "--start-by", "tomorrow"},
		"bad finish-by":        {"clone", "AT1.4", "--finish-by", "2099-01-01"},
		"no task":              {"clone"},
		"two tasks":            {"clone", "AT1", "AT2"},
	} {
		t.Run(name, func(t *testing.T) {
			calls, _, err := runTeamTask(t, cloneResponses1565(), append([]string{"team", "task"}, args...)...)
			if exitCodeFor(err) != 2 || len(callsOf1565(calls, "AgentTaskClone")) != 0 {
				t.Fatalf("err=%v calls=%v", err, *calls)
			}
		})
	}
}

func TestTaskCloneRefusalsMapToTheDocumentedExits1565(t *testing.T) {
	for _, tc := range []struct {
		code string
		exit int
	}{
		{"TASK_NOT_FOUND", 4}, {"BAD_USER_INPUT", 2}, {"TASK_NOT_ASSIGNEE", 8}, {"TASK_NOT_OWNER", 8}, {"TASK_ENDED", 5},
	} {
		t.Run(tc.code, func(t *testing.T) {
			r := cloneResponses1565()
			r["AgentTaskClone"] = errorResponse1565(tc.code, "refused", nil)
			calls, out, err := runTeamTask(t, r, "team", "task", "clone", "AT0.12")
			if exitCodeFor(err) != tc.exit || out != "" || len(callsOf1565(calls, "AgentTaskClone")) != 1 {
				t.Fatalf("err=%v exit=%d out=%q", err, exitCodeFor(err), out)
			}
		})
	}
}

func TestTaskCloneReceiptAndJSON1565(t *testing.T) {
	_, out, err := runTeamTask(t, cloneResponses1565(), "team", "task", "clone", "AT0.12")
	if err != nil || !strings.Contains(out, "cloned task AT0.12 as") {
		t.Fatalf("err=%v out=%q", err, out)
	}
	_, out, err = runTeamTask(t, cloneResponses1565(), "--json", "team", "task", "clone", "AT0.12")
	var got map[string]any
	if err != nil || json.Unmarshal([]byte(out), &got) != nil || got["id"] != "task1" || got["warnings"] == nil {
		t.Fatalf("err=%v out=%q", err, out)
	}
}

// ---- batch ----------------------------------------------------------------

func writeBatch1565(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "batch.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

type batchVars1565 struct {
	AppRef string            `json:"appRef"`
	Tasks  []json.RawMessage `json:"tasks"`
}

func TestTaskBatchSendsOneCallInFileOrderWithAbsentKeysAbsent1565(t *testing.T) {
	p := writeBatch1565(t, `[
	  {"task":{"title":"first"}},
	  {"task":{"title":"second","description":"d","priority":2,"requirements":{"roles":["qa"]}},"blockedByPositions":[1]}
	]`)
	calls, out, err := runTeamTask(t, batchResponses1565(2, ""), "team", "task", "create", "--batch-file", p)
	if err != nil {
		t.Fatal(err)
	}
	call := c2Mutation(t, calls, "AgentTaskCreateBatch")
	var v batchVars1565
	if err := json.Unmarshal(call.RawVars, &v); err != nil {
		t.Fatal(err)
	}
	if v.AppRef == "" || len(v.Tasks) != 2 {
		t.Fatalf("vars: %s", call.RawVars)
	}
	// Item 1: only the keys the file named. No null for description, source, tags, …
	if string(v.Tasks[0]) != `{"task":{"title":"first"}}` {
		t.Fatalf("item 1 = %s", v.Tasks[0])
	}
	var second struct {
		Task               map[string]json.RawMessage `json:"task"`
		BlockedByPositions []int                      `json:"blockedByPositions"`
	}
	if err := json.Unmarshal(v.Tasks[1], &second); err != nil {
		t.Fatal(err)
	}
	if len(second.BlockedByPositions) != 1 || second.BlockedByPositions[0] != 1 {
		t.Fatalf("blockedByPositions must be sent verbatim (1-based): %s", v.Tasks[1])
	}
	for _, k := range []string{"title", "description", "priority", "requirements"} {
		if _, ok := second.Task[k]; !ok {
			t.Errorf("item 2 lost %s: %s", k, v.Tasks[1])
		}
	}
	if len(second.Task) != 4 {
		t.Errorf("item 2 gained keys: %s", v.Tasks[1])
	}
	if !strings.Contains(out, "created 2 tasks") {
		t.Fatalf("out=%q", out)
	}
}

func TestTaskBatchRefusedBeforeAnyCall1565(t *testing.T) {
	twentySix := "[" + strings.TrimSuffix(strings.Repeat(`{"task":{"title":"t"}},`, 26), ",") + "]"
	for name, c := range map[string]struct {
		body string
		args []string
	}{
		"empty list":       {body: `[]`},
		"26 items":         {body: twentySix},
		"not an array":     {body: `{"task":{"title":"t"}}`},
		"unknown key":      {body: `[{"task":{"titel":"t"}}]`},
		"unknown item key": {body: `[{"task":{"title":"t"},"blockers":[1]}]`},
		"mis-cased key":    {body: `[{"task":{"Title":"t"}}]`},
		"duplicate key":    {body: `[{"task":{"title":"a","title":"b"}}]`},
		"no task":          {body: `[{"blockedByPositions":[1]}]`},
		"wrong value type": {body: `[{"task":{"title":"t","priority":"high"}}]`},
		"trailing garbage": {body: `[{"task":{"title":"t"}}] x`},
		"with a flag":      {body: `[{"task":{"title":"t"}}]`, args: []string{"--priority", "2"}},
		"with a title":     {body: `[{"task":{"title":"t"}}]`, args: []string{"T"}},
		"with --match":     {body: `[{"task":{"title":"t"}}]`, args: []string{"--match"}},
	} {
		t.Run(name, func(t *testing.T) {
			p := writeBatch1565(t, c.body)
			argv := append([]string{"team", "task", "create", "--batch-file", p}, c.args...)
			calls, _, err := runTeamTask(t, batchResponses1565(1, ""), argv...)
			if exitCodeFor(err) != 2 || len(callsOf1565(calls, "AgentTaskCreateBatch")) != 0 {
				t.Fatalf("err=%v exit=%d calls=%v", err, exitCodeFor(err), *calls)
			}
		})
	}
	t.Run("empty path", func(t *testing.T) {
		calls, _, err := runTeamTask(t, batchResponses1565(1, ""), "team", "task", "create", "--batch-file", "")
		if exitCodeFor(err) != 2 || len(callsOf1565(calls, "AgentTaskCreateBatch")) != 0 {
			t.Fatalf("err=%v calls=%v", err, *calls)
		}
	})
	t.Run("missing file", func(t *testing.T) {
		calls, _, err := runTeamTask(t, batchResponses1565(1, ""), "team", "task", "create", "--batch-file", filepath.Join(t.TempDir(), "nope.json"))
		if exitCodeFor(err) != 2 || len(callsOf1565(calls, "AgentTaskCreateBatch")) != 0 {
			t.Fatalf("err=%v calls=%v", err, *calls)
		}
	})
	t.Run("stdin from a terminal", func(t *testing.T) {
		calls, _, err := runTeamTask(t, batchResponses1565(1, ""), "team", "task", "create", "--batch-file", "-")
		if exitCodeFor(err) != 2 || len(callsOf1565(calls, "AgentTaskCreateBatch")) != 0 {
			t.Fatalf("err=%v calls=%v", err, *calls)
		}
	})
}

func TestTaskBatchPersistentFlagsStillCombine1565(t *testing.T) {
	p := writeBatch1565(t, `[{"task":{"title":"t"}}]`)
	calls, out, err := runTeamTask(t, batchResponses1565(1, ""), "--json", "team", "task", "create", "--batch-file", p)
	if err != nil || len(callsOf1565(calls, "AgentTaskCreateBatch")) != 1 {
		t.Fatalf("err=%v calls=%v", err, *calls)
	}
	var got struct {
		Tasks    []map[string]any `json:"tasks"`
		Warnings []any            `json:"warnings"`
	}
	if json.Unmarshal([]byte(out), &got) != nil || len(got.Tasks) != 1 || got.Warnings == nil || len(got.Warnings) != 0 {
		t.Fatalf("--json must carry tasks and warnings [] : %q", out)
	}
}

func TestTaskBatchExitCodesAndPosition1565(t *testing.T) {
	p := writeBatch1565(t, `[{"task":{"title":"a"}},{"task":{"title":"b"}},{"task":{"title":"c"}}]`)
	for _, tc := range []struct {
		code string
		exit int
	}{
		{"TASK_BATCH_TOO_LARGE", 2}, {"TASK_BLOCKER_CYCLE", 5}, {"BAD_USER_INPUT", 2}, {"TASK_NOT_FOUND", 4},
		{"APP_NOT_FOUND", 4}, {"TASK_NOT_OWNER", 8}, {"TASK_NOT_ASSIGNEE", 8}, {"TASK_ENDED", 5},
	} {
		t.Run(tc.code, func(t *testing.T) {
			r := batchResponses1565(3, "")
			r["AgentTaskCreateBatch"] = errorResponse1565(tc.code, "refused", nil)
			calls, out, err := runTeamTask(t, r, "team", "task", "create", "--batch-file", p)
			if exitCodeFor(err) != tc.exit || out != "" {
				t.Fatalf("err=%v exit=%d out=%q", err, exitCodeFor(err), out)
			}
			// All or none, one request, no retry and no fall-back to N single creates.
			if n := len(callsOf1565(calls, "AgentTaskCreateBatch")); n != 1 || len(callsOf1565(calls, "AgentTaskCreate")) != 0 {
				t.Fatalf("batch calls=%d, single creates=%d", n, len(callsOf1565(calls, "AgentTaskCreate")))
			}
			if strings.Contains(err.Error(), "(task ") {
				t.Fatalf("a refusal with no position must not name one: %v", err)
			}
		})
	}
	t.Run("position kept in text", func(t *testing.T) {
		r := batchResponses1565(3, "")
		r["AgentTaskCreateBatch"] = errorResponse1565("BAD_USER_INPUT", "blockedByPositions out of range", map[string]any{"position": 3})
		_, _, err := runTeamTask(t, r, "team", "task", "create", "--batch-file", p)
		if exitCodeFor(err) != 2 || !strings.Contains(err.Error(), "(task 3 of 3)") {
			t.Fatalf("err=%v", err)
		}
	})
}

func TestTaskBatchPositionInJSONErrorEnvelope1565(t *testing.T) {
	p := writeBatch1565(t, `[{"task":{"title":"a"}},{"task":{"title":"b"}},{"task":{"title":"c"}}]`)
	run := func(ext map[string]any) map[string]any {
		r := batchResponses1565(3, "")
		r["AgentTaskCreateBatch"] = errorResponse1565("TASK_BLOCKER_CYCLE", "cycle", ext)
		writeTeamBinding(t)
		srv, _ := attnServer(t, r)
		setTeamBindingServer(t, srv.URL)
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"--server", srv.URL, "--json", "team", "task", "create", "--batch-file", p})
		renderError(f, root.Execute())
		var env struct {
			Error map[string]any `json:"error"`
		}
		if err := json.Unmarshal([]byte(out.String()), &env); err != nil {
			t.Fatalf("envelope %q: %v", out.String(), err)
		}
		return env.Error
	}
	withPos := run(map[string]any{"position": 3})
	if withPos["position"] != float64(3) || withPos["serverCode"] != "TASK_BLOCKER_CYCLE" || withPos["code"] != float64(5) {
		t.Fatalf("envelope = %v", withPos)
	}
	if _, ok := run(nil)["position"]; ok {
		t.Fatal("a refusal without a position must have no position key")
	}
}

func TestTaskBatchWarningsByPosition1565(t *testing.T) {
	ext := `,"extensions":{"warnings":[{"path":"createAgentTasks.2","code":"task.create.no-source","text":"no source"}]}`
	p := writeBatch1565(t, `[{"task":{"title":"a"}},{"task":{"title":"b"}}]`)
	_, out, err := runTeamTask(t, batchResponses1565(2, ext), "--json", "team", "task", "create", "--batch-file", p)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Warnings []struct {
			Position *int   `json:"position"`
			Code     string `json:"code"`
		} `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || len(got.Warnings) != 1 || got.Warnings[0].Position == nil || *got.Warnings[0].Position != 2 || got.Warnings[0].Code != "task.create.no-source" {
		t.Fatalf("warnings = %+v err=%v out=%s", got, err, out)
	}
}

// ---- get ------------------------------------------------------------------

func getResponses1565(links string) map[string]string {
	r := taskResponses()
	r["AgentTaskGet"] = `{"data":{"agentTask":` + strings.TrimSuffix(taskFieldsJSON, "}") + links + `}}}`
	return r
}

func TestTaskGetShowsCloneLinks1565(t *testing.T) {
	links := `,"clonedFrom":{"id":"src1","handle":"AT0.3","title":"the old one","status":"FINISHED"},
	  "clones":{"total":3,"hasMore":true,"items":[{"id":"c1","handle":"AT0.20","title":"copy","status":"PENDING"}]}`
	calls, out, err := runTeamTask(t, getResponses1565(links), "team", "task", "get", "task1", "--clones-limit", "1", "--clones-offset", "2")
	if err != nil {
		t.Fatal(err)
	}
	v := callsOf1565(calls, "AgentTaskGet")[0].Vars
	if v["clonesLimit"] != float64(1) || v["clonesOffset"] != float64(2) {
		t.Fatalf("paging vars = %v", v)
	}
	for _, want := range []string{"replaces: AT0.3", "clones: 1 shown of 3 (more: true)", "AT0.20"} {
		if !strings.Contains(out, want) {
			t.Errorf("text missing %q:\n%s", want, out)
		}
	}
	// A pending source reads "cloned from".
	live := strings.Replace(links, `"FINISHED"`, `"STARTED"`, 1)
	_, out, err = runTeamTask(t, getResponses1565(live), "team", "task", "get", "task1")
	if err != nil || !strings.Contains(out, "cloned from: AT0.3") {
		t.Fatalf("err=%v out=%s", err, out)
	}
}

func TestTaskGetCloneLinksJSONShape1565(t *testing.T) {
	_, out, err := runTeamTask(t, getResponses1565(`,"clonedFrom":null,"clones":{"total":0,"hasMore":false,"items":[]}`), "--json", "team", "task", "get", "task1")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if string(got["clonedFrom"]) != "null" {
		t.Errorf("clonedFrom = %s, want null", got["clonedFrom"])
	}
	var page struct {
		Items   []any `json:"items"`
		Total   int   `json:"total"`
		HasMore bool  `json:"hasMore"`
	}
	if err := json.Unmarshal(got["clones"], &page); err != nil || page.Items == nil || len(page.Items) != 0 {
		t.Fatalf("clones = %s (items must be [] not null)", got["clones"])
	}
	if got["id"] == nil {
		t.Errorf("the task fields must stay flat: %s", out)
	}
	// No link for a task that is no clone and has none: text prints nothing extra.
	_, text, err := runTeamTask(t, getResponses1565(`,"clonedFrom":null,"clones":{"total":0,"hasMore":false,"items":[]}`), "team", "task", "get", "task1")
	if err != nil || strings.Contains(text, "clones:") || strings.Contains(text, "cloned from") {
		t.Fatalf("err=%v text=%s", err, text)
	}
}

func TestTaskGetClonePagingRefusedBeforeAnyCall1565(t *testing.T) {
	for _, args := range [][]string{{"--clones-limit", "0"}, {"--clones-limit", "201"}, {"--clones-offset", "-1"}} {
		calls, _, err := runTeamTask(t, getResponses1565(`,"clonedFrom":null,"clones":null`), append([]string{"team", "task", "get", "task1"}, args...)...)
		if exitCodeFor(err) != 2 || len(callsOf1565(calls, "AgentTaskGet")) != 0 {
			t.Fatalf("%v: err=%v calls=%v", args, err, *calls)
		}
	}
}
