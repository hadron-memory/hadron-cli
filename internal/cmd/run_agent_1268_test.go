package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1268 (hadron-server#2849): every manual-run caller can name the Agent the
// run is served as. --agent is passed through as agentRef; omitted, the key is
// absent, so the server picks the App's single installed Agent.

const runAgentURN = "hrn:agent:acme.com:coach"

// op is the operation sent without --agent, opAgent the one sent with it:
// task run switches documents (RunTaskWithAgent), the other two do not.
type runAgentCase struct {
	name, op, opAgent, resp string
	args                    []string
}

func runAgentCases() []runAgentCase {
	importResp := `{"data":{"importNode":{"status":"FETCH_PENDING","jobId":"run_xyz","node":{` +
		`"id":"n1","memoryId":"mem1","loc":"clips:p","name":"P","nodeType":"webpage","updatedAt":"2026-06-11T00:00:00Z"}}}}`
	return []runAgentCase{
		{"task run", "RunTask", "RunTaskWithAgent", `{"data":{"runTask":"run_abc"}}`,
			[]string{"task", "run", nodeURN, "--app", "acme.com:ops"}},
		{"run trigger", "TriggerAppRun", "TriggerAppRun", `{"data":{"triggerAppRun":` + appRunJSON + `}}`,
			[]string{"run", "trigger", "--app", "capp100000000000000000000", "--entry", "hrn:node:acme.com::ops::x"}},
		{"node import --task", "ImportNode", "ImportNode", importResp,
			[]string{"node", "import", "--url", "https://ex.com/p", "-m", "acme.com:kb", "--loc", "clips:p", "--task", "acme.com::kb::tasks:distill", "--yes"}},
	}
}

// agentRefOf returns the agentRef the operation sent and whether the key was
// present at all: top-level for the runTask documents, inside input for the
// other two.
func agentRefOf(t *testing.T, raw json.RawMessage, op string) (any, bool) {
	t.Helper()
	var vars map[string]any
	if err := json.Unmarshal(raw, &vars); err != nil {
		t.Fatalf("%s variables: %v (%s)", op, err, raw)
	}
	if op != "RunTask" && op != "RunTaskWithAgent" {
		vars, _ = vars["input"].(map[string]any)
	}
	v, ok := vars["agentRef"]
	return v, ok
}

func runAgentCmd(t *testing.T, c runAgentCase, resp string, extra ...string) (map[string]json.RawMessage, error) {
	t.Helper()
	gql, captured := captureGraphQL(t, map[string]string{"ResolveUrn": resolveNodeJSON, c.op: resp, c.opAgent: resp})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(append(append([]string{}, c.args...), extra...), "--server", gql.URL))
	return captured, root.Execute()
}

func TestRunAgentFlagSendsAgentRef(t *testing.T) {
	for _, c := range runAgentCases() {
		t.Run(c.name, func(t *testing.T) {
			captured, err := runAgentCmd(t, c, c.resp, "--agent", runAgentURN)
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			if v, ok := agentRefOf(t, captured[c.opAgent], c.opAgent); !ok || v != runAgentURN {
				t.Errorf("agentRef = %v (present %v), want %q", v, ok, runAgentURN)
			}
		})
	}
}

func TestRunAgentOmittedHasNoAgentRefKey(t *testing.T) {
	for _, c := range runAgentCases() {
		t.Run(c.name, func(t *testing.T) {
			captured, err := runAgentCmd(t, c, c.resp)
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			if v, ok := agentRefOf(t, captured[c.op], c.op); ok {
				t.Errorf("no --agent must omit agentRef (null would be an explicit choice), got %v", v)
			}
		})
	}
}

// RUN_AGENT_REQUIRED is a refused input (exit 2), and the message names the
// flag that fixes it: the server's own sentence names the wire arg, agentRef.
func TestRunAgentRequiredNamesTheFlag(t *testing.T) {
	refusal := `{"errors":[{"message":"This App has several installed Agents: pass agentRef to name the one the run is served as.","extensions":{"code":"RUN_AGENT_REQUIRED"}}]}`
	for _, c := range runAgentCases() {
		t.Run(c.name, func(t *testing.T) {
			_, err := runAgentCmd(t, c, refusal)
			if err == nil || exitCodeFor(err) != exitcode.Usage {
				t.Fatalf("want exit %d, got %v (code %d)", exitcode.Usage, err, exitCodeFor(err))
			}
			if msg := err.Error(); !strings.Contains(msg, "several installed Agents") || !strings.Contains(msg, "--agent") || !strings.Contains(msg, "hadron app agent list") {
				t.Errorf("message must keep the server's sentence and name --agent, got %q", msg)
			}
		})
	}
}

// An unknown or foreign Agent is the server's BAD_USER_INPUT, shown as its own
// message (exit 2); the CLI adds no --agent remedy, since --agent was given.
func TestRunAgentForeignAgentShowsServerMessage(t *testing.T) {
	refusal := `{"errors":[{"message":"agentRef does not name an agent installed in this App.","extensions":{"code":"BAD_USER_INPUT"}}]}`
	for _, c := range runAgentCases() {
		t.Run(c.name, func(t *testing.T) {
			_, err := runAgentCmd(t, c, refusal, "--agent", runAgentURN)
			if err == nil || exitCodeFor(err) != exitcode.Usage || err.Error() != "agentRef does not name an agent installed in this App." {
				t.Fatalf("want the server's message at exit %d, got %v (code %d)", exitcode.Usage, err, exitCodeFor(err))
			}
		})
	}
}

// Local refusals, before any request: --agent only shapes an executed run, so
// task run needs --app and node import needs --task; a blank --agent would
// read on the server as omitted, so it is refused rather than dropped.
func TestRunAgentLocalRefusals(t *testing.T) {
	cases := runAgentCases()
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"task run without --app", []string{"task", "run", nodeURN, "--agent", runAgentURN}, "--agent requires --app"},
		{"node import without --task", []string{"node", "import", "--url", "https://ex.com/p", "-m", "acme.com:kb", "--loc", "clips:p", "--agent", runAgentURN}, "--agent requires --task"},
		{"task run blank", append(append([]string{}, cases[0].args...), "--agent", " "), "--agent is empty"},
		{"run trigger blank", append(append([]string{}, cases[1].args...), "--agent", ""), "--agent is empty"},
		{"node import blank", append(append([]string{}, cases[2].args...), "--agent", ""), "--agent is empty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gql, captured := captureGraphQL(t, map[string]string{})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(tc.args, "--server", gql.URL))
			err := root.Execute()
			if err == nil || exitCodeFor(err) != exitcode.Usage || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want usage error containing %q, got %v (code %d)", tc.want, err, exitCodeFor(err))
			}
			if len(captured) != 0 {
				t.Errorf("nothing may be sent on a usage error, got operations %v", captured)
			}
		})
	}
}

// --agent is a content-mode flag on node import: restore and recursive modes
// refuse it like --task.
func TestNodeImportAgentRejectedOutsideContentMode(t *testing.T) {
	for name, args := range map[string][]string{
		"restore":   {"node", "import", "doc.md", "--agent", runAgentURN},
		"recursive": {"node", "import", "-r", "somedir", "--agent", runAgentURN},
	} {
		t.Run(name, func(t *testing.T) {
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(args, "--server", "http://127.0.0.1:1"))
			err := root.Execute()
			if err == nil || exitCodeFor(err) != exitcode.Usage || !strings.Contains(err.Error(), "--agent") || !strings.Contains(err.Error(), "appl") {
				t.Fatalf("want a usage refusal naming --agent, got %v (code %d)", err, exitCodeFor(err))
			}
		})
	}
}

// A plain task run must keep working on a server that predates agentRef: an
// argument named in the document is refused there even with its variable
// omitted, so only RunTaskWithAgent may name it.
func TestRunTaskPlainDocumentHasNoAgentRef(t *testing.T) {
	if strings.Contains(gen.RunTask_Operation, "agentRef") {
		t.Fatalf("RunTask must not name agentRef:\n%s", gen.RunTask_Operation)
	}
	if !strings.Contains(gen.RunTaskWithAgent_Operation, "agentRef: $agentRef") {
		t.Fatalf("RunTaskWithAgent must send agentRef:\n%s", gen.RunTaskWithAgent_Operation)
	}
}
