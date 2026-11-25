package cmd

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/spf13/pflag"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// hadron-cli#3038: the five polish items on `agent tasks create` (follow-ups to cli#2990 / PR #3033). Items 1-4
// are pinned here; item 5 (the test comment that named a person) is the reworded comment in
// agent_tasks_create_2990_test.go. Every server refusal the command already prints is unchanged (the
// cli#2990 pins keep covering those).

const roleTaskZeroEdges3038 = `{"data":{"createTaskNode":{"id":"n1","memoryId":"` + roleTaskMemory2990 + `","loc":"` + roleTaskLoc2990 + `","urn":"` + roleTaskURN2990 + `","name":"Restart the poller","nodeType":"task","isRunnable":true,"role":null,"updatedAt":"2026-11-22T00:00:00Z","outgoingEdges":[]}}}`

func jsonEqual3038(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func roleTaskWithLabel3038(label string) string {
	enc, _ := json.Marshal(label)
	return `{"data":{"createTaskNode":{"id":"n1","memoryId":"` + roleTaskMemory2990 + `","loc":"` + roleTaskLoc2990 + `","urn":"` + roleTaskURN2990 + `","name":"Restart the poller","nodeType":"task","isRunnable":true,"role":null,"updatedAt":"2026-11-22T00:00:00Z","outgoingEdges":[{"id":"e1","name":` + string(enc) + `,"target":{"id":"idx1","loc":"tasks","urn":"` + roleTaskIndexURN2990 + `"}}]}}}`
}

// Item 1: the server writes the index edge WITH the node, so an answer that lists none is surprising. The
// command says so on stderr and still succeeds: no failure, no CLI-side check or retry, and --json stdout is
// unchanged (an empty edges array, nothing extra).
func TestAgentTasksCreateZeroEdgeSuccessSaysSoOnStderr3038(t *testing.T) {
	for _, jsonMode := range []bool{false, true} {
		resp := roleTaskResponses2990()
		resp["CreateRoleTask"] = roleTaskZeroEdges3038
		extra := []string{}
		if jsonMode {
			extra = append(extra, "--json")
		}
		run := runRoleTask2990(t, resp, roleTaskArgs2990(t, extra...)...)
		if run.err != nil {
			t.Fatalf("json=%v: a zero-edge success must not fail: %v", jsonMode, run.err)
		}
		for _, want := range []string{"index edge", "hadron agent tasks list " + roleTaskAgentRef2990} {
			if !strings.Contains(run.errOut, want) {
				t.Errorf("json=%v: the stderr note must contain %q, got %q", jsonMode, want, run.errOut)
			}
		}
		if strings.Contains(run.out, "index edge") {
			t.Errorf("json=%v: the note belongs on stderr, not stdout: %q", jsonMode, run.out)
		}
		writes := 0
		for _, op := range run.ops {
			if op == "CreateRoleTask" {
				writes++
			}
		}
		if writes != 1 || len(run.ops) != 2 {
			t.Errorf("json=%v: no CLI-side check or retry, want GetAgent + one write, got %v", jsonMode, run.ops)
		}
		if jsonMode {
			// The WHOLE object, not a decoded subset: nothing extra may ride along on stdout.
			var got map[string]any
			if err := json.Unmarshal([]byte(run.out), &got); err != nil {
				t.Fatalf("--json stdout is not one JSON object: %v: %q", err, run.out)
			}
			want := map[string]any{"id": "n1", "loc": roleTaskLoc2990, "urn": roleTaskURN2990, "name": "Restart the poller", "edges": []any{}}
			if len(got) != len(want) {
				t.Errorf("--json stdout must keep exactly the DTO keys, got %v", got)
			}
			for k, v := range want {
				gv, ok := got[k]
				if !ok || !jsonEqual3038(gv, v) {
					t.Errorf("--json %s = %v, want %v", k, gv, v)
				}
			}
		} else if !strings.Contains(run.out, "✓ created role task") {
			t.Errorf("the success line still prints: %q", run.out)
		}
	}
	// With the edge present there is nothing to say.
	ok := runRoleTask2990(t, roleTaskResponses2990(), roleTaskArgs2990(t)...)
	if ok.err != nil || ok.errOut != "" {
		t.Errorf("an indexed success is silent on stderr: err %v stderr %q", ok.err, ok.errOut)
	}
}

// Item 2: a null mutation result is the lost-answer class the other write commands use: exit 1, a message that
// says the outcome is unknown and the task may have been recorded, and the command that shows it, before any
// retry. It is sent once and never retried.
func TestAgentTasksCreateNullResultIsTheLostAnswerClass3038(t *testing.T) {
	resp := roleTaskResponses2990()
	resp["CreateRoleTask"] = `{"data":{"createTaskNode":null}}`
	run := runRoleTask2990(t, resp, roleTaskArgs2990(t)...)
	if got := exitCodeFor(run.err); got != exitcode.Error {
		t.Fatalf("a null result exits %d, want 1 (the lost-answer class): %v", got, run.err)
	}
	for _, want := range []string{
		"the server answered createTaskNode with no node and no error",
		"so the outcome is unknown",
		"the task may have been recorded",
		"`hadron agent tasks list <agent>` before retrying",
	} {
		if run.err == nil || !strings.Contains(run.err.Error(), want) {
			t.Errorf("the lost-answer message must contain %q: %v", want, run.err)
		}
	}
	writes := 0
	for _, op := range run.ops {
		if op == "CreateRoleTask" {
			writes++
		}
	}
	if writes != 1 {
		t.Errorf("sent once, never retried: %v", run.ops)
	}
	if strings.Contains(run.out, "created") {
		t.Errorf("no success line for an unknown outcome: %q", run.out)
	}
	// Also in --json mode: the same refusal, and nothing on stdout.
	js := runRoleTask2990(t, resp, roleTaskArgs2990(t, "--json")...)
	if got := exitCodeFor(js.err); got != exitcode.Error || strings.TrimSpace(js.out) != "" {
		t.Errorf("--json: want exit 1 and an empty stdout, got exit %d stdout %q (%v)", got, js.out, js.err)
	}
	// The same message, and the write sent exactly once, in --json too (#3085).
	if js.err == nil || !strings.Contains(js.err.Error(), "the server answered createTaskNode with no node and no error") {
		t.Errorf("--json: the lost-answer message must be the same: %v", js.err)
	}
	jsWrites := 0
	for _, op := range js.ops {
		if op == "CreateRoleTask" {
			jsWrites++
		}
	}
	if jsWrites != 1 {
		t.Errorf("--json: exactly one write, never retried: %v", js.ops)
	}
}

// Item 3: ONE quoting style for the index label. The label is neutralised by output.SafeCell and printed as is,
// like every other field on the page, not wrapped in a second escaping layer.
func TestAgentTasksCreateQuotesTheLabelOnce3038(t *testing.T) {
	label := `say "hi" \x`
	resp := roleTaskResponses2990()
	resp["CreateRoleTask"] = roleTaskWithLabel3038(label)
	run := runRoleTask2990(t, resp, roleTaskArgs2990(t)...)
	if run.err != nil {
		t.Fatal(run.err)
	}
	want := "  indexed: " + label + " -> tasks (" + roleTaskIndexURN2990 + ")"
	if !strings.Contains(run.out, want) {
		t.Errorf("the label prints once, without a second escaping layer: want %q in %q", want, run.out)
	}
	if strings.Contains(run.out, `\"`) || strings.Contains(run.out, `\\`) {
		t.Errorf("no double escaping: %q", run.out)
	}
}

// Item 4: --template prints the template and creates nothing, so any other flag is refused with a usage error
// naming it, before any request; so is a positional argument (already refused).
func TestAgentTasksCreateTemplateRefusesOtherFlags3038(t *testing.T) {
	for _, tc := range []struct {
		flag string
		args []string
	}{
		{"--name", []string{"--name", "x"}},
		{"--abstract", []string{"--abstract", "x"}},
		{"--body-file", []string{"--body-file", "body.md"}},
		{"--trigger", []string{"--trigger", "x"}},
		{"--export-skill", []string{"--export-skill", "x"}},
		{"--skill-description", []string{"--skill-description", "x"}},
	} {
		t.Run(tc.flag, func(t *testing.T) {
			run := runRoleTask2990(t, map[string]string{}, append([]string{"--template"}, tc.args...)...)
			if got := exitCodeFor(run.err); got != exitcode.Usage {
				t.Fatalf("--template %s exits %d, want usage 2: %v", tc.flag, got, run.err)
			}
			if run.err == nil || !strings.Contains(run.err.Error(), tc.flag) || !strings.Contains(run.err.Error(), "--template") {
				t.Errorf("the refusal must name both --template and %s: %v", tc.flag, run.err)
			}
			if len(run.ops) != 0 {
				t.Errorf("refused before any request, got %v", run.ops)
			}
		})
	}
	// Several at once are all named.
	run := runRoleTask2990(t, map[string]string{}, "--template", "--name", "x", "--trigger", "y", "--json")
	for _, want := range []string{"--name", "--trigger", "--json"} {
		if run.err == nil || !strings.Contains(run.err.Error(), want) {
			t.Errorf("every other flag given is named (%s): %v", want, run.err)
		}
	}
}

// The label is neutralised by output.SafeCell, now its only protection (no %q): control characters never
// reach the terminal raw, and the line stays one line.
func TestAgentTasksCreateNeutralisesTheLabel3038(t *testing.T) {
	resp := roleTaskResponses2990()
	resp["CreateRoleTask"] = roleTaskWithLabel3038("a\x1b[31mred\nsplit\rcr")
	run := runRoleTask2990(t, resp, roleTaskArgs2990(t)...)
	if run.err != nil {
		t.Fatal(run.err)
	}
	for _, raw := range []string{"\x1b", "\r"} {
		if strings.Contains(run.out, raw) {
			t.Errorf("a control character %q reached the output raw: %q", raw, run.out)
		}
	}
	var indexed []string
	for _, line := range strings.Split(run.out, "\n") {
		if strings.Contains(line, "indexed:") {
			indexed = append(indexed, line)
		}
	}
	if len(indexed) != 1 || !strings.Contains(indexed[0], "-> tasks (") {
		t.Errorf("the index line stays one line, got %q in %q", indexed, run.out)
	}
}

// --template refuses --json too (a shape the template text does not have), a positional argument alone and
// with flags, and exits 2 with no request for several flags at once. --app (a global) still applies.
func TestAgentTasksCreateTemplateRefusesJSONAndArguments3038(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		args       []string
	}{
		{"json", "--json", []string{"--template", "--json"}},
		{"positional agent", "takes no agent or slug", []string{"--template", roleTaskAgentRef2990}},
		{"positional and slug", "takes no agent or slug", []string{"--template", roleTaskAgentRef2990, "restart-the-poller"}},
		{"positional with a flag", "takes no agent or slug", []string{"--template", roleTaskAgentRef2990, "--name", "x"}},
		{"several flags", "--name", []string{"--template", "--name", "x", "--trigger", "y", "--json"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := runRoleTask2990(t, map[string]string{}, tc.args...)
			if got := exitCodeFor(run.err); got != exitcode.Usage {
				t.Fatalf("exits %d, want usage 2: %v", got, run.err)
			}
			if run.err == nil || !strings.Contains(run.err.Error(), tc.want) {
				t.Errorf("the refusal must contain %q: %v", tc.want, run.err)
			}
			if len(run.ops) != 0 || run.out != "" {
				t.Errorf("refused before any request and with no output, got ops %v out %q", run.ops, run.out)
			}
		})
	}
	// Every flag of the command except --template is refused with it: derived, so a flag added later is
	// covered without touching this test.
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	create, _, err := root.Find([]string{"agent", "tasks", "create"})
	if err != nil || create == nil {
		t.Fatalf("cannot find the command: %v", err)
	}
	seen := 0
	create.LocalNonPersistentFlags().VisitAll(func(fl *pflag.Flag) {
		if fl.Name == "template" || fl.Name == "help" {
			return
		}
		seen++
		args := []string{"--template", "--" + fl.Name}
		if v, ok := flagTestValue3085(t, fl); ok {
			args = append(args, v)
		}
		run := runRoleTask2990(t, map[string]string{}, args...)
		if got := exitCodeFor(run.err); got != exitcode.Usage || len(run.ops) != 0 || run.err == nil ||
			!strings.Contains(run.err.Error(), "--"+fl.Name) || !strings.Contains(run.err.Error(), "--template prints the template") {
			t.Errorf("--template --%s: want a usage refusal naming it before any request, got exit %d ops %v err %v", fl.Name, got, run.ops, run.err)
		}
	})
	if seen < 6 {
		t.Errorf("expected at least the six create flags, saw %d", seen)
	}
	// The global read-context flags are not refused: --app is ACCEPTED here, which is all this
	// asserts. The template read is an ordinary node read that carries no App scoping on the
	// wire, so there is no request-side effect of --app to assert (#3085). --server is the
	// proof the read goes where it is pointed: runRoleTask2990 passes it, and the fake server
	// answers the read.
	content := "# Add a task to a role\n\n## Template\n\n```markdown\n# <Task name>\n```\n"
	resp := map[string]string{"GetNode": `{"data":{"node":{"id":"t1","loc":"tasks:add-role-task","urn":"hrn:node:hadronmemory.com:agent-team-system:tasks:add-role-task","name":"Add a task to a role","content":` + jsonText(content) + `}}}`}
	ok := runRoleTask2990(t, resp, "--template", "--app", "acme.com:eng")
	if ok.err != nil || !strings.Contains(ok.out, "# <Task name>") {
		t.Errorf("--template --app still prints the template: err %v out %q", ok.err, ok.out)
	}
}

// flagTestValue3085 chooses the value to pass a non-bool flag by its TYPE, so a numeric, duration or
// count flag added to the command later is not handed a string it would mis-handle (#3085).
func flagTestValue3085(t *testing.T, fl *pflag.Flag) (string, bool) {
	t.Helper()
	switch fl.Value.Type() {
	case "bool":
		return "", false
	case "string":
		return "x", true
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "count":
		return "1", true
	case "duration":
		return "1s", true
	case "float32", "float64":
		return "1", true
	default:
		t.Fatalf("flag --%s has type %q: teach flagTestValue3085 a value for it", fl.Name, fl.Value.Type())
		return "", false
	}
}

// #3085: the refusal, the help and agentic-usage.md say what is true: --template takes no other COMMAND
// flag, and the global read-context flags (--app, --server and the unlock flags) are not refused. The old
// wording said "only the global --app and --server apply", which the persistent --unlock flags contradict.
const templateGlobalsPhrase3085 = "takes no other command flag"

func TestAgentTasksCreateTemplateRefusalSaysWhichGlobalsApply3085(t *testing.T) {
	run := runRoleTask2990(t, map[string]string{}, "--template", "--name", "x")
	if run.err == nil || exitCodeFor(run.err) != exitcode.Usage {
		t.Fatalf("want a usage refusal: %v", run.err)
	}
	msg := run.err.Error()
	for _, want := range []string{templateGlobalsPhrase3085, "the global read-context flags (--app, --server, the unlock flags) still apply"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal must say %q: %s", want, msg)
		}
	}
	if strings.Contains(msg, "(--app and --server still apply)") || strings.Contains(msg, "only") {
		t.Errorf("the old, wrong 'only --app and --server' wording must be gone: %s", msg)
	}
}

func TestAgentTasksCreateHelpAndAgenticUsageSayWhichGlobalsApply3085(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "tasks", "create", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	help := strings.Join(strings.Fields(out.String()), " ")
	usage, err := os.ReadFile("agentic/agentic-usage.md")
	if err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{"--help": help, "agentic-usage.md": string(usage)} {
		if !strings.Contains(text, "the global read-context flags") || !strings.Contains(text, "the unlock flags") {
			t.Errorf("%s must name the global read-context flags and the unlock flags", name)
		}
		if strings.Contains(text, "only the global --app and --server apply") || strings.Contains(text, "only the global `--app`/`--server` apply") {
			t.Errorf("%s still says only --app and --server apply", name)
		}
	}
}

// The set of refused flags is unchanged by the reword: the persistent unlock flags and --app are not
// refused (they are the global read-context flags), so none of them produces the --template refusal.
func TestAgentTasksCreateTemplateAcceptsTheGlobalReadContextFlags3085(t *testing.T) {
	content := "# Add a task to a role\n\n## Template\n\n```markdown\n# <Task name>\n```\n"
	resp := map[string]string{"GetNode": `{"data":{"node":{"id":"t1","loc":"tasks:add-role-task","urn":"hrn:node:hadronmemory.com:agent-team-system:tasks:add-role-task","name":"Add a task to a role","content":` + jsonText(content) + `}}}`}
	for _, args := range [][]string{
		{"--template", "--app", "acme.com:eng"},
		{"--template", "--unlock", "acme.com:secrets"},
		{"--template", "--unlock-passphrase-file", "pass.txt"},
		{"--template", "--unlock-passphrase-stdin"},
	} {
		run := runRoleTask2990(t, resp, args...)
		if run.err != nil && strings.Contains(run.err.Error(), templateGlobalsPhrase3085) {
			t.Errorf("%v is a global read-context flag and must not be refused as another command flag: %v", args, run.err)
		}
	}
}
