package cmd

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

// cli#3109 (hadron-server#4997 merged as #5053, cor:agt:150 "A project",
// cor:agt:050:01 "Project marker" / :02 "What a clone does"): the explicit
// project marker, set at creation only. `--is-project` on task create and
// clone sends the isProject input — presence, not value: --is-project=false
// is sent as false, an omitted flag omits the key entirely (omitempty) — and
// the server's refusals (a contradiction such as project:true with
// isProject:false) come back verbatim, never re-implemented client-side.
// task get and task list show the marker, text and --json. Batch items decode
// into the generated CreateAgentTaskInput, so an item's isProject passes
// through unchanged with no batch-specific code.

// createInput3109 runs a single task create and returns the sent input object
// with its keys exactly as they went to the wire.
func createInput3109(t *testing.T, args ...string) map[string]json.RawMessage {
	t.Helper()
	calls, _, err := runTeamTask(t, taskResponses(), append([]string{"team", "task", "create"}, args...)...)
	if err != nil {
		t.Fatalf("create %v: %v", args, err)
	}
	return mutationInput3109(t, c2Mutation(t, calls, "AgentTaskCreate").RawVars)
}

// mutationInput3109 pulls the "input" object out of a recorded mutation's
// variables as raw JSON keys.
func mutationInput3109(t *testing.T, raw json.RawMessage) map[string]json.RawMessage {
	t.Helper()
	var v struct {
		Input json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("vars=%s: %v", raw, err)
	}
	var input map[string]json.RawMessage
	if err := json.Unmarshal(v.Input, &input); err != nil {
		t.Fatalf("input=%s: %v", v.Input, err)
	}
	return input
}

// taskMarked3109 is taskFieldsJSON with (or without) the project marker.
func taskMarked3109(marked bool) string {
	if !marked {
		return taskFieldsJSON
	}
	return strings.Replace(taskFieldsJSON, `"status":"PENDING"`, `"status":"PENDING","isProject":true`, 1)
}

func TestTaskCreateIsProject3109(t *testing.T) {
	t.Run("given sends true", func(t *testing.T) {
		input := createInput3109(t, "mark it", "--is-project")
		if string(input["isProject"]) != "true" {
			t.Fatalf("isProject = %s, want true; input=%s", input["isProject"], input)
		}
	})
	t.Run("false is sent as false, not omitted", func(t *testing.T) {
		input := createInput3109(t, "mark it", "--is-project=false")
		if string(input["isProject"]) != "false" {
			t.Fatalf("isProject = %s, want false; input=%s", input["isProject"], input)
		}
	})
	t.Run("omitted omits the key entirely", func(t *testing.T) {
		input := createInput3109(t, "plain create")
		if _, ok := input["isProject"]; ok {
			t.Fatalf("isProject must be absent when the flag is omitted: %s", input)
		}
	})
	t.Run("given with --project sends both", func(t *testing.T) {
		input := createInput3109(t, "top-level and marked", "--project", "--is-project")
		if string(input["isProject"]) != "true" || string(input["project"]) != "true" {
			t.Fatalf("both keys must be sent; input=%s", input)
		}
	})
}

// The server owns the contradiction refusal (project:true with isProject:false
// is BAD_USER_INPUT before any read): the request must go out as given and the
// refusal must come back verbatim, with its code.
func TestTaskCreateIsProjectRefusalPassesThrough3109(t *testing.T) {
	r := taskResponses()
	r["AgentTaskCreate"] = errorResponse1565("BAD_USER_INPUT", "project: true with isProject: false marks nothing", nil)
	calls, _, err := runTeamTask(t, r, "team", "task", "create", "contradiction", "--project", "--is-project=false")
	if got := len(callsOf1565(calls, "AgentTaskCreate")); got != 1 {
		t.Fatalf("the request must be sent as given, never refused client-side: %d calls, err=%v", got, err)
	}
	if exitCodeFor(err) != 2 || err == nil || !strings.Contains(err.Error(), "BAD_USER_INPUT") || !strings.Contains(err.Error(), "project: true with isProject: false marks nothing") {
		t.Fatalf("the refusal must come back verbatim with its code: err=%v exit=%d", err, exitCodeFor(err))
	}
}

func TestTaskCloneIsProject3109(t *testing.T) {
	t.Run("given sends true", func(t *testing.T) {
		input, _ := cloneInput1565(t, "AT0.12", "--is-project")
		if string(input["isProject"]) != "true" {
			t.Fatalf("isProject = %s, want true; input=%s", input["isProject"], input)
		}
	})
	// Unlike --project (two-state on clone: =false is refused), the marker
	// override is three-state: false unmarks the clone outright.
	t.Run("false is sent as false", func(t *testing.T) {
		input, _ := cloneInput1565(t, "AT0.12", "--is-project=false")
		if string(input["isProject"]) != "false" {
			t.Fatalf("isProject = %s, want false; input=%s", input["isProject"], input)
		}
	})
	t.Run("omitted omits the key entirely", func(t *testing.T) {
		input, _ := cloneInput1565(t, "AT0.12", "--title", "x")
		if _, ok := input["isProject"]; ok {
			t.Fatalf("isProject must be absent when the flag is omitted: %s", input)
		}
	})
	// A nested project is legal (the marker is not the top-level rule), so
	// --is-project must not refuse beside --parent.
	t.Run("given with --parent sends both", func(t *testing.T) {
		input, _ := cloneInput1565(t, "AT0.12", "--is-project", "--parent", "AT2")
		if string(input["isProject"]) != "true" || len(input["parentTaskRef"]) == 0 {
			t.Fatalf("isProject and parentTaskRef must both be sent; input=%s", input)
		}
	})
	// The project/isProject contradiction is the server's refusal, not ours.
	t.Run("false with --project sends both", func(t *testing.T) {
		input, _ := cloneInput1565(t, "AT0.12", "--project", "--is-project=false")
		if string(input["project"]) != "true" || string(input["isProject"]) != "false" {
			t.Fatalf("both keys must be sent; input=%s", input)
		}
	})
}

// A batch item decodes strictly into the generated CreateAgentTaskInput, so an
// item's isProject reaches createAgentTasks as written — pinned here because
// the passthrough is the whole ask (no batch-specific code may appear).
func TestTaskBatchIsProjectPassesThrough3109(t *testing.T) {
	p := writeBatch1565(t, `[{"task":{"title":"a","isProject":true}},{"task":{"title":"b"}}]`)
	calls, _, err := runTeamTask(t, batchResponses1565(2, ""), "team", "task", "create", "--batch-file", p)
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Tasks []json.RawMessage `json:"tasks"`
	}
	if err := json.Unmarshal(c2Mutation(t, calls, "AgentTaskCreateBatch").RawVars, &v); err != nil {
		t.Fatal(err)
	}
	if len(v.Tasks) != 2 || !strings.Contains(string(v.Tasks[0]), `"isProject":true`) {
		t.Fatalf("the item's isProject must pass through unchanged: %s", v.Tasks[0])
	}
	if strings.Contains(string(v.Tasks[1]), "isProject") {
		t.Fatalf("an item without the key must not grow one: %s", v.Tasks[1])
	}
}

// --is-project stays refused beside --batch-file (the existing single-create
// flag allowlist), before any call.
func TestTaskBatchRefusesIsProjectFlag3109(t *testing.T) {
	calls, _, err := runTeamTask(t, batchResponses1565(1, ""), "team", "task", "create",
		"--is-project", "--batch-file", writeBatch1565(t, `[{"task":{"title":"a"}}]`))
	if exitCodeFor(err) != 2 || !strings.Contains(errText(err), "--is-project") || len(callsOf1565(calls, "AgentTaskCreateBatch")) != 0 {
		t.Fatalf("err=%v exit=%d calls=%v", err, exitCodeFor(err), *calls)
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func TestTaskGetShowsProjectMarker3109(t *testing.T) {
	t.Run("marked: text and --json", func(t *testing.T) {
		r := taskResponses()
		r["AgentTaskGet"] = `{"data":{"agentTask":` + taskMarked3109(true) + `}}`
		_, out, err := runTeamTask(t, r, "team", "task", "get", "AT0.12")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(strings.Join(strings.Fields(out), " "), "project marker: yes") {
			t.Fatalf("text must show the marker: %s", out)
		}
		var dto map[string]any
		r["AgentTaskGet"] = `{"data":{"agentTask":` + taskMarked3109(true) + `}}`
		_, outJSON, err := runTeamTask(t, r, "--json", "team", "task", "get", "AT0.12")
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(outJSON), &dto); err != nil {
			t.Fatal(err)
		}
		if dto["isProject"] != true {
			t.Fatalf("isProject = %v, want true", dto["isProject"])
		}
	})
	t.Run("unmarked: no text line, --json false", func(t *testing.T) {
		r := taskResponses()
		_, out, err := runTeamTask(t, r, "team", "task", "get", "AT0.12")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(strings.Join(strings.Fields(out), " "), "project marker") {
			t.Fatalf("an unmarked task must not show a marker line: %s", out)
		}
		var dto map[string]any
		_, outJSON, err := runTeamTask(t, r, "--json", "team", "task", "get", "AT0.12")
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(outJSON), &dto); err != nil {
			t.Fatal(err)
		}
		if dto["isProject"] != false {
			t.Fatalf("isProject = %v, want false", dto["isProject"])
		}
	})
}

func TestTaskListShowsProjectMarker3109(t *testing.T) {
	marked := strings.Replace(taskWithID2734("task9"), `"status":"PENDING"`, `"status":"PENDING","isProject":true`, 1)
	rows := func(body string) map[string]string {
		r := taskResponses()
		r["AgentTaskList"] = taskPage2734(body, 2, false)
		return r
	}
	// tableCellSplit is the column boundary of NewTable's output: tabwriter
	// pads every column but the last with at least two spaces (padding 2),
	// and no fixture cell carries a double space — the title has single
	// spaces only.
	var tableCellSplit = regexp.MustCompile(` {2,}`)
	// The table is read by column, never by whole-line substrings. A row is
	// located by an exact match on its first cell, the ID column, so an id
	// that is a substring of another row's id or of a title cannot match;
	// and the header must END with IS PROJECT, which pins the marker as the
	// final column before any cell is read — a column appended later fails
	// that pin loudly instead of misreading whatever became the last cell.
	// (Header and rows share one tabwriter.Writer, so the header's column
	// offsets do stay aligned with every row's — offset slicing would
	// work. It is not used because it would re-encode tabwriter's padding
	// rules in the test; matching cells against the ID column needs no
	// offset arithmetic.)
	rowCells := func(line string) []string {
		return tableCellSplit.Split(strings.TrimRight(line, " "), -1)
	}
	assertMarkerTable := func(t *testing.T, out string) {
		t.Helper()
		lines := strings.Split(out, "\n")
		header := ""
		for _, line := range lines {
			if strings.Contains(line, "TITLE") {
				header = strings.TrimRight(line, " ")
			}
		}
		if !strings.HasSuffix(header, "IS PROJECT") {
			t.Fatalf("IS PROJECT must be the table's last column, header %q: %s", header, out)
		}
		row := func(id string) []string {
			for _, line := range lines {
				if cells := rowCells(line); len(cells) > 1 && cells[0] == id {
					return cells
				}
			}
			t.Fatalf("no row whose ID cell is %s: %s", id, out)
			return nil
		}
		marked, plain := row("task9"), row("task2")
		if marked[len(marked)-1] != "yes" || plain[len(plain)-1] != "—" {
			t.Fatalf("the marked row's last cell must be yes, the unmarked's —:\n%s\n%s", strings.Join(marked, "|"), strings.Join(plain, "|"))
		}
		// Whatever the ids' lengths, the rows must be identical except the id
		// and marker cells: marking a task moves no other column.
		if len(marked) != len(plain) {
			t.Fatalf("rows must share their column count, %d vs %d: %s", len(marked), len(plain), out)
		}
		for i := 1; i < len(marked)-1; i++ {
			if marked[i] != plain[i] {
				t.Fatalf("rows must differ only in the id and marker cells, column %d is %q vs %q:\n%s\n%s", i, marked[i], plain[i], strings.Join(marked, "|"), strings.Join(plain, "|"))
			}
		}
	}
	t.Run("a marked row adds the IS PROJECT column", func(t *testing.T) {
		r := rows(marked + "," + taskWithID2734("task2"))
		_, out, err := runTeamTask(t, r, "team", "task", "list")
		if err != nil {
			t.Fatal(err)
		}
		assertMarkerTable(t, out)
		var page struct {
			Tasks []map[string]any `json:"tasks"`
		}
		_, outJSON, err := runTeamTask(t, rows(marked+","+taskWithID2734("task2")), "--json", "team", "task", "list")
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(outJSON), &page); err != nil {
			t.Fatal(err)
		}
		if len(page.Tasks) != 2 || page.Tasks[0]["isProject"] != true || page.Tasks[1]["isProject"] != false {
			t.Fatalf("isProject must ride every task: %s", outJSON)
		}
	})
	t.Run("mine shares the renderer and the column", func(t *testing.T) {
		_, out, err := runTeamTask(t, rows(marked+","+taskWithID2734("task2")), "team", "task", "mine")
		if err != nil {
			t.Fatal(err)
		}
		assertMarkerTable(t, out)
	})
	t.Run("list --team keeps its own table, no marker column", func(t *testing.T) {
		r := rows(marked + "," + taskWithID2734("task2"))
		r["TeamReportingLines"] = r["TeamRoster"]
		_, out, err := runTeamTask(t, r, "team", "task", "list", "--team=worker:wkr1")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "task9") {
			t.Fatalf("the team table must render the row: %s", out)
		}
		if strings.Contains(out, "IS PROJECT") {
			t.Fatalf("the --team ops table deliberately shows no marker column: %s", out)
		}
	})
	t.Run("no marked row, no column", func(t *testing.T) {
		r := taskResponses()
		_, out, err := runTeamTask(t, r, "team", "task", "list")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out, "IS PROJECT") {
			t.Fatalf("an unmarked page must not add the IS PROJECT column: %s", out)
		}
	})
}

// The #4997 ruling: wording about the flag and the marker stays domain-neutral
// ("task", "project" — never "epic", "PR" or "review seat"). The pin scopes to
// the lines that describe them, because other commands legitimately name pull
// requests (link-defect links defects to "the PR build task"); "PR" on a
// marker line would spell the marker as something it is not.
func TestTaskIsProjectHelpWording3109(t *testing.T) {
	for _, args := range [][]string{{"team", "task", "create", "--help"}, {"team", "task", "clone", "--help"}, {"team", "task", "list", "--help"}, {"team", "task", "get", "--help"}} {
		_, out, err := runTeamTask(t, taskResponses(), args...)
		if err != nil {
			t.Fatal(err)
		}
		var markerLines []string
		pr := regexp.MustCompile(`\bpr\b`)
		for _, line := range strings.Split(out, "\n") {
			lower := strings.ToLower(line)
			if strings.Contains(lower, "--is-project") || strings.Contains(lower, "project marker") || strings.Contains(lower, "isproject") {
				markerLines = append(markerLines, line)
				for _, banned := range []string{"epic", "review seat"} {
					if strings.Contains(lower, banned) {
						t.Fatalf("%v must not describe the marker with %q:\n%s", args, banned, line)
					}
				}
				// "PR" only as a standalone word — "project" shares letters
				// with it, and the marker wording says project.
				if pr.MatchString(lower) {
					t.Fatalf("%v must not describe the marker with \"PR\":\n%s", args, line)
				}
			}
		}
		if len(markerLines) == 0 {
			t.Fatalf("%v help must describe --is-project or the marker:\n%s", args, out)
		}
	}
}
