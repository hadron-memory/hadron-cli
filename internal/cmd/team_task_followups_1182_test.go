package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1182 — follow-ups from the #1179 reviews on the task dependency pages.

// The dependency pages declare non-null items ([AgentTask!]!). A null item, or
// one with no id, is a malformed answer: the command refuses it (the same exit
// class as every other "server returned ..." refusal), names the page and the
// item, and prints nothing. Before, a null item was silently dropped, so
// len(items) disagreed with the page and a bad answer looked like a good one.

const dependencyItem1182 = `{"id":"b1","handle":"AT1.2","title":"Dependency","status":"PENDING"}`

// taskWithPageItem1182 returns the priority task with item planted in one of its
// dependency pages: "blockedBy" and "blocks" are the fragment's pages, "clones"
// is get's own extra page. An unknown page, or a replacement that finds nothing
// to replace, fails the test at once, so a fixture can never come back unchanged.
func taskWithPageItem1182(t *testing.T, page, item string) string {
	t.Helper()
	task := priorityTask1135()
	var out string
	switch page {
	case "blockedBy":
		out = strings.Replace(task, dependencyItem1182, item, 1)
	case "blocks":
		out = strings.Replace(task, `"blocks":{"items":[]`, `"blocks":{"items":[`+item+`]`, 1)
	case "clones":
		out = strings.TrimSuffix(task, "}") + `,"clones":{"items":[` + item + `],"total":1,"hasMore":false}}`
	default:
		t.Fatalf("unknown dependency page %q", page)
	}
	if out == task {
		t.Fatalf("planting %q in %s changed nothing: the fixture no longer has that page", item, page)
	}
	return out
}

func TestMalformedDependencyItemIsRefusedNotSkipped1182(t *testing.T) {
	items := map[string]string{
		"null item":       `null`,
		"item with no id": `{"id":"","handle":"AT1.9","title":"No id","status":"PENDING"}`,
		"empty object":    `{}`,
	}
	type surface struct {
		name, page string
		args       []string
		responses  func(task string) map[string]string
		jsonToo    bool
	}
	surfaces := []surface{
		{"get", "blockedBy", []string{"team", "task", "get", "AT1.4"}, func(task string) map[string]string {
			r := taskResponses()
			r["AgentTaskGet"] = `{"data":{"agentTask":` + task + `}}`
			return r
		}, true},
		{"get", "blocks", []string{"team", "task", "get", "AT1.4"}, func(task string) map[string]string {
			r := taskResponses()
			r["AgentTaskGet"] = `{"data":{"agentTask":` + task + `}}`
			return r
		}, true},
		{"get", "clones", []string{"team", "task", "get", "AT1.4"}, func(task string) map[string]string {
			r := taskResponses()
			r["AgentTaskGet"] = `{"data":{"agentTask":` + task + `}}`
			return r
		}, true},
		{"list", "blockedBy", []string{"team", "task", "list"}, func(task string) map[string]string {
			r := taskResponses()
			r["AgentTaskList"] = `{"data":{"agentTasks":[` + priorityTask1135() + `,` + task + `]}}`
			return r
		}, true},
		{"list", "blocks", []string{"team", "task", "list"}, func(task string) map[string]string {
			r := taskResponses()
			r["AgentTaskList"] = `{"data":{"agentTasks":[` + priorityTask1135() + `,` + task + `]}}`
			return r
		}, true},
		{"set-priority", "blockedBy", []string{"team", "task", "set-priority", "AT1.4", "2"}, func(task string) map[string]string {
			r := taskResponses()
			r["AgentTaskSetPriority"] = `{"data":{"setAgentTaskPriority":` + task + `}}`
			return r
		}, true},
		{"add-blocker", "blocks", []string{"team", "task", "add-blocker", "AT1.4", "AT1.2"}, func(task string) map[string]string {
			r := taskResponses()
			r["AgentTaskAddBlocker"] = `{"data":{"addAgentTaskBlocker":` + task + `}}`
			return r
		}, true},
		{"remove-blocker", "blockedBy", []string{"team", "task", "remove-blocker", "AT1.4", "AT1.2"}, func(task string) map[string]string {
			r := taskResponses()
			r["AgentTaskRemoveBlocker"] = `{"data":{"removeAgentTaskBlocker":` + task + `}}`
			return r
		}, true},
	}
	for _, s := range surfaces {
		for iname, item := range items {
			for _, jsonMode := range []bool{false, true} {
				name := s.name + "/" + s.page + "/" + iname
				args := append([]string{}, s.args...)
				if jsonMode {
					args = append(args, "--json")
					name += "/json"
				}
				t.Run(name, func(t *testing.T) {
					_, out, err := runTeamTask(t, s.responses(taskWithPageItem1182(t, s.page, item)), args...)
					if err == nil {
						t.Fatalf("a malformed %s item was accepted:\n%s", s.page, out)
					}
					if code := exitCodeFor(err); code != exitcode.Error {
						t.Fatalf("exit %d, want %d (%v)", code, exitcode.Error, err)
					}
					if out != "" {
						t.Fatalf("a refused answer printed partial output:\n%s", out)
					}
					if msg := err.Error(); !strings.Contains(msg, s.page) || !strings.Contains(msg, "item 1") {
						t.Fatalf("the refusal must name the page and the item: %q", msg)
					}
				})
			}
		}
	}
}

// `task dependencies` reads both pages together; a bad item in either refuses.
func TestMalformedDependencyItemIsRefusedByTheDependenciesRead1182(t *testing.T) {
	for _, page := range []string{"blockedBy", "blocks"} {
		for iname, item := range map[string]string{"null item": `null`, "item with no id": `{"id":"","title":"x","status":"PENDING"}`} {
			t.Run(page+"/"+iname, func(t *testing.T) {
				blockedBy, blocks := dependencyItem1182, ``
				if page == "blockedBy" {
					blockedBy = item
				} else {
					blocks = item
				}
				r := taskResponses()
				r["AgentTaskDependencies"] = `{"data":{"agentTask":{"id":"task1","blockedBy":{"items":[` + blockedBy + `],"total":1,"hasMore":false},"blocks":{"items":[` + blocks + `],"total":0,"hasMore":false}}}}`
				_, out, err := runTeamTask(t, r, "team", "task", "dependencies", "AT1.4", "--json")
				if err == nil || exitCodeFor(err) != exitcode.Error || out != "" || !strings.Contains(err.Error(), page) {
					t.Fatalf("want a refusal naming %s, no output; got err=%v out=%q", page, err, out)
				}
			})
		}
	}
}

// The refusal does not change what valid or empty pages do: an empty items list
// (a page past the end, or no dependencies) is legitimate and unchanged.
func TestValidAndEmptyDependencyPagesAreUnchanged1182(t *testing.T) {
	r := taskResponses()
	// One shared offset of 20: 21 blockers leave exactly the last one on this page,
	// and a single blocked task is already past it.
	r["AgentTaskDependencies"] = `{"data":{"agentTask":{"id":"task1","blockedBy":{"items":[` + dependencyItem1182 + `],"total":21,"hasMore":false},"blocks":{"items":[],"total":1,"hasMore":false}}}}`
	_, out, err := runTeamTask(t, r, "team", "task", "dependencies", "AT1.4", "--offset", "20")
	if err != nil {
		t.Fatalf("a legitimate page was refused: %v", err)
	}
	if want := "  blocked by: 1 shown of 21 (more: false)\n    AT1.2 (b1) PENDING: Dependency\n  blocks: 0 shown of 1 (more: false)\n"; out != want {
		t.Fatalf("output changed:\nwant %q\ngot  %q", want, out)
	}
}

// Server warnings on the dedicated dependency mutations (set-priority,
// add-blocker, remove-blocker). They match the simple task mutations, NOT the
// warning-enabled create/assign/reassign/finish: the advice is not shown (no
// stderr line in text mode, no "warnings" key in --json), and the result is
// exactly what it is without the advice. A warning-enabled verb (assign) shows
// the same kind of advice, so this is a characterised difference, not an
// oversight in the harness. (No new warning behaviour is added here.)
func TestDependencyMutationsDropServerWarningsLikeSimpleMutations1182(t *testing.T) {
	warn := func(path string) string {
		return `,"extensions":{"warnings":[{"path":"` + path + `","code":"task.advice.sample","text":"sample advice"}]}`
	}
	type verb struct {
		name, op, key string
		args          []string
	}
	for _, v := range []verb{
		{"set-priority", "AgentTaskSetPriority", "setAgentTaskPriority", []string{"team", "task", "set-priority", "AT1.4", "2"}},
		{"add-blocker", "AgentTaskAddBlocker", "addAgentTaskBlocker", []string{"team", "task", "add-blocker", "AT1.4", "AT1.2"}},
		{"remove-blocker", "AgentTaskRemoveBlocker", "removeAgentTaskBlocker", []string{"team", "task", "remove-blocker", "AT1.4", "AT1.2"}},
	} {
		for _, jsonMode := range []bool{false, true} {
			name := v.name
			args := append([]string{}, v.args...)
			if jsonMode {
				args = append(args, "--json")
				name += "/json"
			}
			t.Run(name, func(t *testing.T) {
				run := func(withWarning bool) (string, string) {
					r := taskResponses()
					body := `{"data":{"` + v.key + `":` + priorityTask1135() + `}`
					if withWarning {
						body += warn(v.key)
					}
					r[v.op] = body + `}`
					_, out, errOut, err := runTeamTaskAll1524(t, r, args...)
					if err != nil {
						t.Fatal(err)
					}
					return out, errOut
				}
				plainOut, plainErr := run(false)
				warnedOut, warnedErr := run(true)
				if warnedOut != plainOut || warnedErr != plainErr {
					t.Fatalf("a server warning changed the result:\nwithout: %q / %q\nwith:    %q / %q", plainOut, plainErr, warnedOut, warnedErr)
				}
				if strings.Contains(warnedErr, "warning:") || strings.Contains(warnedOut, "sample advice") || strings.Contains(warnedOut, `"warnings"`) {
					t.Fatalf("the dependency mutation showed server advice: out=%q err=%q", warnedOut, warnedErr)
				}
			})
		}
	}
	// Control: the same advice on a warning-enabled verb IS shown, in both modes.
	r := taskResponses()
	r["AgentTaskAssign"] = `{"data":{"assignAgentTask":` + priorityTask1135() + `}` + warn("assignAgentTask") + `}`
	_, out, errOut, err := runTeamTaskAll1524(t, r, "team", "task", "assign", "AT1.4", "--assignee", "worker:Iris")
	if err != nil || !strings.Contains(errOut, "warning: [task.advice.sample] sample advice") || strings.Contains(out, "sample advice") {
		t.Fatalf("control: assign must print its warning on stderr only: err=%v out=%q stderr=%q", err, out, errOut)
	}
	_, jout, _, err := runTeamTaskAll1524(t, r, "team", "task", "assign", "AT1.4", "--assignee", "worker:Iris", "--json")
	var got struct {
		Warnings []struct{ Code, Text string } `json:"warnings"`
	}
	if err != nil || json.Unmarshal([]byte(jout), &got) != nil || len(got.Warnings) != 1 || got.Warnings[0].Code != "task.advice.sample" {
		t.Fatalf("control: assign --json must carry the warning: err=%v out=%s", err, jout)
	}
}

// A page the provider did not return at all (an older provider, or a nil page)
// reads as "unavailable", never as an empty or complete list: text and --json,
// on get, list and dependencies (cli#1182 item 6).
func TestUnavailableDependencyPagesRender1182(t *testing.T) {
	noPages := strings.Replace(priorityTask1135(),
		`"blockedBy":{"items":[`+dependencyItem1182+`],"total":1,"hasMore":false},"blocks":{"items":[],"total":0,"hasMore":false}`,
		`"blockedBy":null,"blocks":null`, 1)
	if noPages == priorityTask1135() {
		t.Fatal("fixture did not remove the pages")
	}
	r := taskResponses()
	r["AgentTaskGet"] = `{"data":{"agentTask":` + noPages + `}}`
	r["AgentTaskList"] = `{"data":{"agentTasks":[` + noPages + `]}}`
	r["AgentTaskDependencies"] = `{"data":{"agentTask":{"id":"task1","blockedBy":null,"blocks":null}}}`

	for _, cmd := range [][]string{{"team", "task", "get", "AT1.4"}, {"team", "task", "dependencies", "AT1.4"}} {
		_, out, err := runTeamTask(t, r, cmd...)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "  blocked by: unavailable\n  blocks: unavailable\n") {
			t.Fatalf("%v: text must say unavailable, not an empty list:\n%s", cmd, out)
		}
		if strings.Contains(out, "shown of") {
			t.Fatalf("%v: an unavailable page was rendered as a count:\n%s", cmd, out)
		}
		_, jout, err := runTeamTask(t, r, append(append([]string{}, cmd...), "--json")...)
		if err != nil {
			t.Fatal(err)
		}
		var got struct {
			BlockedBy json.RawMessage `json:"blockedBy"`
			Blocks    json.RawMessage `json:"blocks"`
		}
		if err := json.Unmarshal([]byte(jout), &got); err != nil || string(got.BlockedBy) != "null" || string(got.Blocks) != "null" {
			t.Fatalf("%v: json pages must be null: %v %s", cmd, err, jout)
		}
	}

	_, out, err := runTeamTask(t, r, "team", "task", "list")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Fields(line); len(f) > 5 && f[0] == "task1" {
			if f[4] != "unavailable" || f[5] != "unavailable" {
				t.Fatalf("list cells %v, want unavailable summaries", f[:6])
			}
			return
		}
	}
	t.Fatalf("no task row in:\n%s", out)
}

// Help and docs polish (cli#1182 items 3, 5 and 7), each pinned to the
// behaviour it documents so the wording cannot drift from it.

// cobra appends "(default N)" to an int flag's usage by itself, so the usage
// text must not state the default a second time.
func TestDependencyPagingHelpStatesTheDefaultOnce1182(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	cmd, _, err := root.Find([]string{"team", "task", "dependencies"})
	if err != nil || cmd == nil {
		t.Fatalf("no dependencies command: %v", err)
	}
	usages := cmd.Flags().FlagUsages()
	for _, line := range strings.Split(usages, "\n") {
		if strings.Contains(line, "--limit") {
			if got := strings.Count(line, "default 20"); got != 1 {
				t.Fatalf("--limit states the default %d times, want once: %q", got, line)
			}
			if !strings.Contains(line, "cap 200") {
				t.Fatalf("--limit lost the cap: %q", line)
			}
			return
		}
	}
	t.Fatalf("no --limit usage in:\n%s", usages)
}

// The task priority/dependency section is a subsection like its siblings (##),
// not a top-level heading of its own.
func TestAgenticUsageTaskPrioritySectionIsASubsection1182(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agentic-usage"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	doc := out.String()
	if !strings.Contains(doc, "\n## Task priority and dependencies (cli#1135)\n") {
		t.Fatal("the task priority section is missing, or is not a ## subsection")
	}
	if strings.Contains(doc, "\n# Task priority and dependencies") {
		t.Fatal("the task priority section is still a top-level (#) heading")
	}
}

// `task list --team` prints priority and blocked but NOT the dependency summary
// columns that plain `task list` has; the docs say so, and this pin keeps the
// behaviour and the sentence from drifting apart. (The --json of both carries
// the pages.)
func TestTeamListColumnsMatchTheTaskDocs1182(t *testing.T) {
	_, out, _, err := run1044(t, roster1044(), [][]task1044{tasks1044()}, "team", "task", "list", "--team=worker:Marco")
	if err != nil {
		t.Fatal(err)
	}
	var header string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "ID ") {
			header = line
		}
	}
	if !strings.Contains(header, "PRIORITY") || !strings.Contains(header, "BLOCKED") {
		t.Fatalf("list --team lost its priority/blocked columns: %q", header)
	}
	if strings.Contains(header, "BLOCKED BY") || strings.Contains(header, "BLOCKS") {
		t.Fatalf("list --team now prints the dependency summaries; the docs must say so: %q", header)
	}

	f, docOut := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agentic-usage"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	doc := docOut.String()
	start := strings.Index(doc, "## Task priority and dependencies (cli#1135)")
	if start < 0 {
		t.Fatal("the task priority section is missing")
	}
	section := doc[start:]
	if end := strings.Index(section[3:], "\n## "); end >= 0 {
		section = section[:end+3]
	}
	flat := strings.Join(strings.Fields(section), " ")
	if !strings.Contains(flat, "list --team") || !strings.Contains(flat, "without the dependency summaries") {
		t.Fatalf("the docs must say that `list --team` prints priority and blocked without the dependency summaries:\n%s", flat)
	}
}

// Every task command projects its answer through the same DTO, and each call
// site handles the refusal itself, so every verb is pinned. A null blocker item
// in the answer is refused (exit 1, naming the page) with nothing printed. The
// commands that WROTE refuse as an UNCERTAIN write: they say it may already have
// taken effect, name the task, and say to check before retrying (a retried create
// would duplicate); the commands that only READ must not say that.
func TestEveryTaskVerbRefusesAMalformedDependencyPage1182(t *testing.T) {
	good := priorityTask1135()
	type verb struct {
		name  string
		args  func(t *testing.T) []string
		op    string
		write bool
		// body builds the whole GraphQL response from the malformed task.
		body func(bad string) string
		base func() map[string]string
	}
	fixed := func(args ...string) func(*testing.T) []string { return func(*testing.T) []string { return args } }
	one := func(key string) func(string) string {
		return func(bad string) string { return `{"data":{"` + key + `":` + bad + `}}` }
	}
	many := func(key string) func(string) string {
		return func(bad string) string { return `{"data":{"` + key + `":[` + good + `,` + bad + `]}}` }
	}
	verbs := []verb{
		{"create", fixed("team", "task", "create", "Fix the flaky CI build"), "AgentTaskCreate", true, one("createAgentTask"), taskResponses},
		{"assign", fixed("team", "task", "assign", "task1", "--assignee", "worker:Rufus"), "AgentTaskAssign", true, one("assignAgentTask"), taskResponses},
		{"reassign", fixed("team", "task", "reassign", "task1", "--assignee", "user:holger"), "AgentTaskReassign", true, one("reassignAgentTask"), taskResponses},
		{"start", fixed("team", "task", "start", "task1"), "AgentTaskStart", true, one("startAgentTask"), taskResponses},
		{"progress", fixed("team", "task", "progress", "task1"), "AgentTaskProgress", true, one("recordAgentTaskProgress"), taskResponses},
		{"cancel", fixed("team", "task", "cancel", "task1"), "AgentTaskCancel", true, one("cancelAgentTask"), taskResponses},
		{"park", fixed("team", "task", "park", "task1"), "AgentTaskPark", true, one("parkAgentTask"), taskResponses},
		{"ready", fixed("team", "task", "ready", "task1"), "AgentTaskReady", true, one("makeAgentTaskReady"), taskResponses},
		{"finish", fixed("team", "task", "finish", "task1", "--outcome", "succeeded", "--summary", "s", "--evidence", "https://ci.acme.com/1"), "AgentTaskFinish", true, one("finishAgentTask"), taskResponses},
		{"hand-back", fixed("team", "task", "hand-back", "task1", "--code", "refused"), "AgentTaskHandBack", true, one("handBackAgentTask"), taskResponses},
		{"release", fixed("team", "task", "release", "task1"), "AgentTaskRelease", true, one("releaseAgentTaskExternalId"), releaseResponses1962},
		{"set-priority", fixed("team", "task", "set-priority", "AT1.4", "2"), "AgentTaskSetPriority", true, one("setAgentTaskPriority"), taskResponses},
		{"add-blocker", fixed("team", "task", "add-blocker", "AT1.4", "AT1.2"), "AgentTaskAddBlocker", true, one("addAgentTaskBlocker"), taskResponses},
		{"remove-blocker", fixed("team", "task", "remove-blocker", "AT1.4", "AT1.2"), "AgentTaskRemoveBlocker", true, one("removeAgentTaskBlocker"), taskResponses},
		{"clone", fixed("team", "task", "clone", "AT0.12"), "AgentTaskClone", true, one("cloneAgentTask"), cloneResponses1565},
		{"update", fixed("team", "task", "update", "task1", "--title", "renamed"), "AgentTaskUpdate", true, one("updateAgentTask"), taskResponses},
		{"create --batch-file", func(t *testing.T) []string {
			return []string{"team", "task", "create", "--batch-file", writeBatch1565(t, `[{"task":{"title":"first"}}]`)}
		}, "AgentTaskCreateBatch", true, func(bad string) string { return `{"data":{"createAgentTasks":[` + bad + `]}}` }, taskResponses},
		{"get", fixed("team", "task", "get", "AT1.4"), "AgentTaskGet", false, func(bad string) string { return `{"data":{"agentTask":` + bad + `}}` }, taskResponses},
		{"list", fixed("team", "task", "list"), "AgentTaskList", false, many("agentTasks"), taskResponses},
		{"mine", fixed("team", "task", "mine"), "AgentTaskList", false, many("agentTasks"), taskResponses},
		{"list --text", fixed("team", "task", "list", "--text", "flaky"), "AgentTaskListText", false, many("agentTasks"), taskResponses},
		{"list --created-after", fixed("team", "task", "list", "--created-after", "2026-01-01T00:00:00Z"), "AgentTaskListFiltered", false, many("agentTasks"), taskResponses},
		{"list --unresolved", fixed("team", "task", "list", "--unresolved"), "AgentTaskListUnresolved", false, many("agentTasks"), taskResponses},
		{"list --creator", fixed("team", "task", "list", "--creator", "user:holger"), "AgentTaskListCreator", false, many("agentTasks"), taskResponses},
		{"tree", fixed("team", "task", "tree", "task1"), "AgentTaskTree", false, many("agentTaskTree"), taskResponses},
	}
	for _, v := range verbs {
		for _, jsonMode := range []bool{false, true} {
			name := v.name
			if jsonMode {
				name += "/json"
			}
			t.Run(name, func(t *testing.T) {
				args := v.args(t)
				if jsonMode {
					args = append(append([]string{}, args...), "--json")
				}
				r := v.base()
				r[v.op] = v.body(taskWithPageItem1182(t, "blockedBy", `null`))
				_, out, err := runTeamTask(t, r, args...)
				if err == nil {
					t.Fatalf("a malformed blockedBy item was accepted:\n%s", out)
				}
				if code := exitCodeFor(err); code != exitcode.Error {
					t.Fatalf("exit %d, want %d (%v)", code, exitcode.Error, err)
				}
				msg := err.Error()
				if !strings.Contains(msg, "blockedBy") || !strings.Contains(msg, "item 1") {
					t.Fatalf("the refusal must name the page and the item: %q", msg)
				}
				if out != "" {
					t.Fatalf("a refused answer printed partial output:\n%s", out)
				}
				uncertain := strings.Contains(msg, "the write may already have taken effect") && strings.Contains(msg, "task1") && strings.Contains(msg, "before retrying")
				if v.write && !uncertain {
					t.Fatalf("a write's refused answer must say the write may have taken effect, name task1 and say to check before retrying: %q", msg)
				}
				if !v.write && strings.Contains(msg, "may already have taken effect") {
					t.Fatalf("a read claimed a write may have taken effect: %q", msg)
				}
			})
		}
	}
}

// The untracked-item claim writes too (it starts the tasks it returns): a
// refused answer names every task it returned and says to check before retrying.
func TestUntrackedClaimRefusesAMalformedDependencyPageAsUncertain1182(t *testing.T) {
	bad := strings.Replace(claimedTaskJSON1524, `"status":"STARTED"`, `"blockedBy":{"items":[null],"total":1,"hasMore":false},"status":"STARTED"`, 1)
	if bad == claimedTaskJSON1524 {
		t.Fatal("fixture did not gain the page")
	}
	_, out, _, err := runFind1524(t, map[string]string{"ClaimUntrackedTaskItems": claimResult1524(bad, true, "", "")}, "--integration", "github", "--assign-to-me", "--json")
	if err == nil || exitCodeFor(err) != exitcode.Error || out != "" {
		t.Fatalf("want an exit-1 refusal and no output; got err=%v out=%q", err, out)
	}
	if msg := err.Error(); !strings.Contains(msg, "malformed blockedBy page") || !strings.Contains(msg, "the write may already have taken effect") || !strings.Contains(msg, "task1") {
		t.Fatalf("the refusal must name the page and the claimed task and say the write may have taken effect: %q", msg)
	}
}

// A page whose items list is null or missing is malformed too (the field is
// non-null), and is never rendered as an empty page; a decoded empty array is
// the one legitimate empty page. Through the real client, on get and dependencies.
func TestNullOrMissingDependencyItemsAreRefusedButEmptyIsNot1182(t *testing.T) {
	pages := map[string]string{
		"null items":    `"blockedBy":{"items":null,"total":0,"hasMore":false}`,
		"missing items": `"blockedBy":{"total":0,"hasMore":false}`,
	}
	empty := `"blockedBy":{"items":[],"total":0,"hasMore":false}`
	original := `"blockedBy":{"items":[` + dependencyItem1182 + `],"total":1,"hasMore":false}`
	for name, page := range pages {
		t.Run(name, func(t *testing.T) {
			task := strings.Replace(priorityTask1135(), original, page, 1)
			if task == priorityTask1135() {
				t.Fatal("fixture was not changed")
			}
			r := taskResponses()
			r["AgentTaskGet"] = `{"data":{"agentTask":` + task + `}}`
			r["AgentTaskDependencies"] = `{"data":{"agentTask":{"id":"task1",` + page + `,"blocks":{"items":[],"total":0,"hasMore":false}}}}`
			for _, args := range [][]string{{"team", "task", "get", "AT1.4"}, {"team", "task", "dependencies", "AT1.4"}} {
				_, out, err := runTeamTask(t, r, args...)
				if err == nil || exitCodeFor(err) != exitcode.Error || out != "" || !strings.Contains(err.Error(), "blockedBy page: items is missing or null") {
					t.Fatalf("%v: want a refusal of the null/missing items, no output; got err=%v out=%q", args, err, out)
				}
			}
		})
	}
	task := strings.Replace(priorityTask1135(), original, empty, 1)
	r := taskResponses()
	r["AgentTaskGet"] = `{"data":{"agentTask":` + task + `}}`
	r["AgentTaskDependencies"] = `{"data":{"agentTask":{"id":"task1",` + empty + `,"blocks":{"items":[],"total":0,"hasMore":false}}}}`
	for _, args := range [][]string{{"team", "task", "get", "AT1.4"}, {"team", "task", "dependencies", "AT1.4"}} {
		if _, _, err := runTeamTask(t, r, args...); err != nil {
			t.Fatalf("%v: a decoded empty array is a legitimate empty page: %v", args, err)
		}
	}
}

// finish with findings has already written when it reads the answer, so a
// malformed dependency page there is refused as an UNCERTAIN finish (the same
// wrapper as every other unusable answer on that path): it says the finish state
// may have changed and points at the findings read, and prints nothing.
func TestFinishWithFindingsRefusesAMalformedDependencyPageAsUncertain1182(t *testing.T) {
	var reply map[string]any
	if err := json.Unmarshal([]byte(finishReply1550(t, `{"items":[],"total":0,"hasMore":false}`)), &reply); err != nil {
		t.Fatal(err)
	}
	task := reply["data"].(map[string]any)["finishAgentTask"].(map[string]any)
	task["blockedBy"] = map[string]any{"items": []any{nil}, "total": 1, "hasMore": false}
	body, err := json.Marshal(reply)
	if err != nil {
		t.Fatal(err)
	}
	_, out, err := runTeamTask(t, map[string]string{"AgentTaskFinishWithFindings": string(body)}, "team", "task", "finish", "AT0.7", "--outcome", "succeeded", "--findings-file", findingsFile1550(t, `[]`))
	if err == nil || exitCodeFor(err) != exitcode.Error {
		t.Fatalf("want an exit-1 refusal, got %v", err)
	}
	id, _ := task["id"].(string)
	if msg := err.Error(); id == "" || !strings.Contains(msg, "finish state may have changed") || !strings.Contains(msg, "malformed blockedBy page") || !strings.Contains(msg, "task "+id) {
		t.Fatalf("the refusal must say the finish state is uncertain, name the page and the task %q: %q", id, msg)
	}
	if out != "" {
		t.Fatalf("a refused answer printed output:\n%s", out)
	}
}
