package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2990: `hadron agent tasks create` is a THIN client of hadron-server#4864 (ccfac717): the existing task
// door, extended with an optional tasks-index edge (index) and an opt-in skill export (skill). The server
// writes the node and its index edge atomically and owns every rule; the CLI resolves the Agent's system
// memory with an ordinary read, makes ONE write call, maps refusals through api.MapError, and adds no
// pre-check or local lint of its own.

const (
	roleTaskAgentRef2990 = "hrn:agent:acme.com:support-bot"
	roleTaskMemory2990   = "mem-support-bot-system"
	roleTaskLoc2990      = "tasks:restart-the-poller"
	roleTaskURN2990      = "hrn:node:acme.com:support-bot-system:tasks:restart-the-poller"
	roleTaskIndexURN2990 = "hrn:node:acme.com:support-bot-system:tasks"
)

func roleTaskAgentJSON2990() string {
	return strings.Replace(agentJSON, `"systemMemoryId":null`, `"systemMemoryId":"`+roleTaskMemory2990+`"`, 1)
}

const roleTaskCreated2990 = `{"data":{"createTaskNode":{"id":"n1","memoryId":"` + roleTaskMemory2990 + `","loc":"` + roleTaskLoc2990 + `","urn":"` + roleTaskURN2990 + `","name":"Restart the poller","nodeType":"task","isRunnable":true,"role":null,"updatedAt":"2026-11-22T00:00:00Z","outgoingEdges":[{"id":"e1","name":"when the poller stalls","target":{"id":"idx1","loc":"tasks","urn":"` + roleTaskIndexURN2990 + `"}}]}}}`

func roleTaskBody2990(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(p, []byte("# Restart the poller\n\n## Purpose\nBring it back.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

type roleTaskRun2990 struct {
	out, errOut string
	err         error
	ops         []string
	vars        map[string]json.RawMessage
}

func runRoleTask2990(t *testing.T, responses map[string]string, args ...string) roleTaskRun2990 {
	t.Helper()
	var ops []string
	gql, captured := captureGraphQLFunc(t, func(op string) string {
		ops = append(ops, op)
		return responses[op]
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"agent", "tasks", "create", "--server", gql.URL}, args...))
	err := root.Execute()
	errOut := ""
	if sb, ok := f.IOStreams.ErrOut.(*strings.Builder); ok {
		errOut = sb.String()
	}
	return roleTaskRun2990{out: out.String(), errOut: errOut, err: err, ops: ops, vars: captured}
}

func roleTaskResponses2990() map[string]string {
	return map[string]string{
		"GetAgent":       `{"data":{"agent":` + roleTaskAgentJSON2990() + `}}`,
		"CreateRoleTask": roleTaskCreated2990,
	}
}

func roleTaskArgs2990(t *testing.T, extra ...string) []string {
	return append([]string{roleTaskAgentRef2990, "restart-the-poller", "--name", "Restart the poller", "--abstract", "Bring the poller back when it stalls.",
		"--body-file", roleTaskBody2990(t), "--trigger", "when the poller stalls"}, extra...)
}

func decodeVars2990(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("variables: %v: %s", err, raw)
	}
	return v
}

// A valid create reads the Agent once, then makes ONE write: the Agent, slug-derived loc, name, abstract, body
// and trigger in a single call, with the Agent's own system memory. It prints the created node's loc, URN and
// the index edge.
func TestAgentTasksCreateIsOneWriteCall2990(t *testing.T) {
	run := runRoleTask2990(t, roleTaskResponses2990(), roleTaskArgs2990(t)...)
	if run.err != nil {
		t.Fatalf("create: %v\n%s", run.err, run.errOut)
	}
	if strings.Join(run.ops, ",") != "GetAgent,CreateRoleTask" {
		t.Fatalf("one read of the Agent, then ONE write: %v", run.ops)
	}
	v := decodeVars2990(t, run.vars["CreateRoleTask"])
	in, _ := v["input"].(map[string]any)
	want := map[string]any{"memoryId": roleTaskMemory2990, "loc": roleTaskLoc2990, "name": "Restart the poller",
		"abstract": "Bring the poller back when it stalls.", "nodeType": "task", "isRunnable": true}
	for k, w := range want {
		if in[k] != w {
			t.Errorf("input.%s = %v, want %v", k, in[k], w)
		}
	}
	if c, _ := in["content"].(string); !strings.Contains(c, "## Purpose\nBring it back.") {
		t.Errorf("input.content must carry the body file: %q", c)
	}
	if _, has := in["role"]; has {
		t.Errorf("no role is sent (cor:acl:130:05: no door accepts one): %v", in["role"])
	}
	idx, _ := v["index"].(map[string]any)
	if idx["agent"] != roleTaskAgentRef2990 || idx["trigger"] != "when the poller stalls" {
		t.Errorf("index = %v", idx)
	}
	if _, has := v["skill"]; has {
		t.Errorf("no export block by default (Holger, 2026-10-28): skill = %v", v["skill"])
	}
	for _, want := range []string{roleTaskLoc2990, roleTaskURN2990, "when the poller stalls", roleTaskIndexURN2990} {
		if !strings.Contains(run.out, want) {
			t.Errorf("the output must show %q:\n%s", want, run.out)
		}
	}
}

// --json is the created node's loc, URN and edge as a structure; a list is [] never null; nothing else is on
// stdout.
func TestAgentTasksCreateJSON2990(t *testing.T) {
	resp := roleTaskResponses2990()
	run := runRoleTask2990(t, resp, append([]string{"--json"}, roleTaskArgs2990(t)...)...)
	if run.err != nil {
		t.Fatal(run.err)
	}
	var dto struct {
		ID    string `json:"id"`
		Loc   string `json:"loc"`
		URN   string `json:"urn"`
		Name  string `json:"name"`
		Edges []struct {
			ID        string `json:"id"`
			Label     string `json:"label"`
			TargetLoc string `json:"targetLoc"`
			TargetURN string `json:"targetUrn"`
		} `json:"edges"`
	}
	if err := json.Unmarshal([]byte(run.out), &dto); err != nil {
		t.Fatalf("stdout must be exactly one JSON document: %v\n%s", err, run.out)
	}
	if dto.Loc != roleTaskLoc2990 || dto.URN != roleTaskURN2990 || len(dto.Edges) != 1 || dto.Edges[0].Label != "when the poller stalls" || dto.Edges[0].TargetURN != roleTaskIndexURN2990 {
		t.Fatalf("dto = %+v", dto)
	}
	// An answer with no outgoing edge still renders [] (never null).
	resp["CreateRoleTask"] = strings.Replace(roleTaskCreated2990, `"outgoingEdges":[{"id":"e1","name":"when the poller stalls","target":{"id":"idx1","loc":"tasks","urn":"`+roleTaskIndexURN2990+`"}}]`, `"outgoingEdges":[]`, 1)
	run = runRoleTask2990(t, resp, append([]string{"--json"}, roleTaskArgs2990(t)...)...)
	if run.err != nil || !strings.Contains(run.out, `"edges": []`) {
		t.Fatalf("an empty edge list is [], not null: err=%v\n%s", run.err, run.out)
	}
}

// The skill export is opt-in: with --export-skill the request carries the declaration (name and description)
// and nothing else about it; without both flags together the CLI refuses before any request.
func TestAgentTasksCreateSkillExportIsOptIn2990(t *testing.T) {
	run := runRoleTask2990(t, roleTaskResponses2990(), roleTaskArgs2990(t, "--export-skill", "hadron-restart-the-poller", "--skill-description", "Restart the poller when it stalls")...)
	if run.err != nil {
		t.Fatal(run.err)
	}
	v := decodeVars2990(t, run.vars["CreateRoleTask"])
	skill, _ := v["skill"].(map[string]any)
	if skill["name"] != "hadron-restart-the-poller" || skill["description"] != "Restart the poller when it stalls" || len(skill) != 2 {
		t.Fatalf("skill = %v", v["skill"])
	}
	for _, extra := range [][]string{{"--export-skill", "n"}, {"--skill-description", "d"}} {
		run := runRoleTask2990(t, roleTaskResponses2990(), roleTaskArgs2990(t, extra...)...)
		if exitCodeFor(run.err) != exitcode.Usage || len(run.ops) != 0 {
			t.Errorf("%v: the two skill flags go together: exit %d, ops %v", extra, exitCodeFor(run.err), run.ops)
		}
	}
}

// The inputs the verb needs are named before anything is sent.
func TestAgentTasksCreateNeedsItsInputs2990(t *testing.T) {
	full := roleTaskArgs2990(t)
	for _, drop := range []string{"--name", "--abstract", "--body-file", "--trigger"} {
		var args []string
		for i := 0; i < len(full); i++ {
			if full[i] == drop {
				i++
				continue
			}
			args = append(args, full[i])
		}
		run := runRoleTask2990(t, roleTaskResponses2990(), args...)
		if exitCodeFor(run.err) != exitcode.Usage || len(run.ops) != 0 || !strings.Contains(run.err.Error(), drop) {
			t.Errorf("without %s: want a usage refusal naming it before any request, got exit %d ops %v err %v", drop, exitCodeFor(run.err), run.ops, run.err)
		}
	}
	run := runRoleTask2990(t, roleTaskResponses2990(), roleTaskAgentRef2990)
	if exitCodeFor(run.err) != exitcode.Usage || len(run.ops) != 0 {
		t.Errorf("without a slug: exit %d ops %v", exitCodeFor(run.err), run.ops)
	}
}

// Each server refusal maps through api.MapError to its exit code and prints the server's message; the write is
// never retried and nothing is made up locally. An unknown Agent is refused by the READ, before any write.
func TestAgentTasksCreateRefusalsMapThroughMapError2990(t *testing.T) {
	for _, tc := range []struct {
		name, op, code string
		want           int
	}{
		{"existing slug", "CreateRoleTask", "NodeLocConflictError", exitcode.Conflict},
		{"not allowed", "CreateRoleTask", "FORBIDDEN", exitcode.Forbidden},
		{"agent with no tasks index", "CreateRoleTask", "BAD_USER_INPUT", exitcode.Usage},
		{"unknown agent", "GetAgent", "AGENT_NOT_FOUND", exitcode.NotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := roleTaskResponses2990()
			resp[tc.op] = `{"errors":[{"message":"server says ` + tc.code + `","extensions":{"code":"` + tc.code + `"}}]}`
			run := runRoleTask2990(t, resp, roleTaskArgs2990(t)...)
			if got := exitCodeFor(run.err); got != tc.want {
				t.Fatalf("exit %d, want %d: %v", got, tc.want, run.err)
			}
			if !strings.Contains(run.err.Error(), "server says "+tc.code) {
				t.Errorf("the server's own message surfaces: %v", run.err)
			}
			writes := 0
			for _, op := range run.ops {
				if op == "CreateRoleTask" {
					writes++
				}
			}
			if tc.op == "GetAgent" && writes != 0 {
				t.Errorf("an unknown Agent is refused before any write: %v", run.ops)
			}
			if tc.op == "CreateRoleTask" && writes != 1 {
				t.Errorf("one write, never retried: %v", run.ops)
			}
			if strings.Contains(run.out, "created") {
				t.Errorf("no success line after a refusal: %q", run.out)
			}
		})
	}
}

// --template prints the body template from the template NODE by an ordinary read (the server read was
// dropped, #4864 R2): the fenced block under "## Template". It creates nothing.
func TestAgentTasksCreateTemplatePrintsTheTemplateNodesBlock2990(t *testing.T) {
	content := "# Add a task to a role\n\nintro\n\n## Template\n\n```markdown\n# <Task name>\n\n## Purpose / When to use\nWhat it does.\n```\n\n## Procedure\n1. step\n"
	resp := map[string]string{"GetNode": `{"data":{"node":{"id":"t1","loc":"tasks:add-role-task","urn":"hrn:node:hadronmemory.com:agent-team-system:tasks:add-role-task","name":"Add a task to a role","content":` + jsonText(content) + `}}}`}
	run := runRoleTask2990(t, resp, "--template")
	if run.err != nil {
		t.Fatalf("--template: %v\n%s", run.err, run.errOut)
	}
	if want := "# <Task name>\n\n## Purpose / When to use\nWhat it does.\n"; run.out != want {
		t.Fatalf("the template block, exactly:\n%q\nwant\n%q", run.out, want)
	}
	for _, op := range run.ops {
		if op == "CreateRoleTask" || op == "GetAgent" {
			t.Fatalf("--template creates nothing and reads no Agent: %v", run.ops)
		}
	}
}

// The help and the agent contract carry the authoring guidance (the add-role-task node's): write after doing
// the work once; a rule only one team follows belongs in that team's process.
func TestAgentTasksCreateHelpSaysWhenToWriteATask2990(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "tasks", "create", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	help := strings.Join(strings.Fields(out.String()), " ")
	// The contract is judged on the `agent tasks create` entry alone (its one line), never the whole file: a
	// phrase elsewhere must not satisfy the check, nor an unrelated mention fail it.
	raw := string(readContract(t))
	const anchor = "\nhadron agent tasks create <agentRef>"
	if n := strings.Count(raw, anchor); n != 1 {
		t.Fatalf("agentic-usage must carry exactly ONE agent tasks create entry, starting its line (found %d): a second one could satisfy or break this pin", n)
	}
	at := strings.Index(raw, anchor) + 1 // the entry is ONE physical line: judged up to its newline
	entry := raw[at:]
	if nl := strings.Index(entry, "\n"); nl >= 0 {
		entry = entry[:nl]
	}
	entry = strings.Join(strings.Fields(entry), " ")
	texts := []struct{ name, text string }{{"help", help}, {"agentic-usage entry", entry}}
	for _, tx := range texts {
		lower := strings.ToLower(tx.text)
		if strings.Contains(lower, "lint advice") {
			t.Errorf("%s promises only what the CLI does (the server's attached advice goes to stderr); it says nothing about lint advice", tx.name)
		}
		// One statement of the CLI's scope in both places: no content rule or lint of its own, not "no pre-check".
		for _, want := range []string{
			"no content rule or lint of its own",
			"after doing the work once, never speculatively",
			"a rule only one team follows belongs in that team's process, not a shared role memory",
			"hadron agent tasks create",
			"any advice the server attaches to its answer",
		} {
			if !strings.Contains(lower, want) {
				t.Errorf("%s must say %q", tx.name, want)
			}
		}
	}
}

// An Agent with no system memory is the SERVER's refusal to make (no tasks index / no pointer): the CLI makes
// no pre-check, so the write is still sent once, without a memory id, and the server's answer decides.
func TestAgentTasksCreateMakesNoPreCheckOfItsOwn2990(t *testing.T) {
	resp := roleTaskResponses2990()
	resp["GetAgent"] = `{"data":{"agent":` + agentJSON + `}}` // systemMemoryId: null
	resp["CreateRoleTask"] = `{"errors":[{"message":"The Agent has no tasks index.","extensions":{"code":"BAD_USER_INPUT"}}]}`
	run := runRoleTask2990(t, resp, roleTaskArgs2990(t)...)
	if strings.Join(run.ops, ",") != "GetAgent,CreateRoleTask" {
		t.Fatalf("the write is sent and the server decides: %v", run.ops)
	}
	in, _ := decodeVars2990(t, run.vars["CreateRoleTask"])["input"].(map[string]any)
	if _, has := in["memoryId"]; has {
		t.Errorf("an Agent with no system memory sends no memory id: %v", in["memoryId"])
	}
	if exitCodeFor(run.err) != exitcode.Usage || !strings.Contains(run.err.Error(), "The Agent has no tasks index.") {
		t.Errorf("the server's refusal maps through MapError: exit %d %v", exitCodeFor(run.err), run.err)
	}
}

// The server's advice rides the response's extensions.warnings (the existing node-write warning sink): it
// prints to stderr, stdout is unchanged and the create is never failed by it. (The help promises exactly this
// and nothing about the server's lint beyond it.)
func TestAgentTasksCreatePrintsTheServersAdviceToStderr2990(t *testing.T) {
	plain := runRoleTask2990(t, roleTaskResponses2990(), roleTaskArgs2990(t)...)
	resp := roleTaskResponses2990()
	resp["CreateRoleTask"] = strings.TrimSuffix(roleTaskCreated2990, "}") + `,"extensions":{"warnings":[{"path":"createTaskNode","code":"skill.lint.warning","text":"The skill description is long."},{"path":"updateNode","code":"wrong.root","text":"must stay silent"}]}}`
	run := runRoleTask2990(t, resp, roleTaskArgs2990(t)...)
	if run.err != nil || run.out != plain.out {
		t.Fatalf("advice never fails the create nor changes stdout: err=%v\n%s\nvs\n%s", run.err, run.out, plain.out)
	}
	if !strings.Contains(run.errOut, "warning: [skill.lint.warning] The skill description is long.") || strings.Contains(run.errOut, "must stay silent") {
		t.Fatalf("the server's advice for this write is printed to stderr, another root's is not: %q", run.errOut)
	}
}

// A template node that holds no fenced block under "## Template" is a clear error, not an empty print.
func TestAgentTasksCreateTemplateWithoutABlockIsAnError2990(t *testing.T) {
	resp := map[string]string{"GetNode": `{"data":{"node":{"id":"t1","loc":"tasks:add-role-task","urn":"hrn:node:hadronmemory.com:agent-team-system:tasks:add-role-task","name":"x","content":"# Add a task\n\nno template here\n"}}}`}
	run := runRoleTask2990(t, resp, "--template")
	if exitCodeFor(run.err) != exitcode.Error || run.out != "" || !strings.Contains(run.err.Error(), "## Template") {
		t.Fatalf("want exit 1, nothing on stdout, naming the section: exit %d out %q err %v", exitCodeFor(run.err), run.out, run.err)
	}
}
