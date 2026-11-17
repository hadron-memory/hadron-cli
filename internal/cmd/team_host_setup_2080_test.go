package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// hadron-cli#2080 over hadron-server#4241 (S1a): `team host assign|unassign|
// setup`. Every test runs in a worktree bound to a worker (session s-new), so
// the attribution of each call is pinned where it can go wrong: reads carry no
// session, a write is the person's unless --instruction claims the holder's.

const assignmentJSON2080 = `{"id":"a1","hostId":"orca1","workerId":"wkr1","worker":{"name":"Jonas"},
	"workdir":"jonas-3","family":"claude","model":"opus-5.5",
	"createdAt":"2026-11-15T10:00:00Z","updatedAt":"2026-11-15T10:00:00Z"}`

func responses2080() map[string]string {
	r := taskResponses()
	r["TeamHostAssign"] = `{"data":{"assignHostWorker":` + assignmentJSON2080 + `}}`
	r["TeamHostAssignInstructed"] = r["TeamHostAssign"]
	r["TeamHostUnassign"] = `{"data":{"unassignHostWorker":true}}`
	r["TeamHostUnassignInstructed"] = r["TeamHostUnassign"]
	r["TeamHostSetSource"] = `{"data":{"setHostSetupSource":"SERVER"}}`
	r["TeamHostSetSourceInstructed"] = r["TeamHostSetSource"]
	r["TeamHostSetup"] = `{"data":{"hostSetup":{"source":"SERVER","assignments":[` + assignmentJSON2080 + `]}}}`
	// The S1a-era fixtures model a server that predates S1b: the profile-bearing
	// documents are refused at validation, so `team host setup` falls back to the
	// S1a document these tests were written against (cli#2157).
	r["TeamHostSetupWithProfile"] = oldServerProfileRefusal2157
	r["TeamHostAssignWithProfile"] = oldServerProfileRefusal2157
	r["TeamHostAssignWithProfileInstructed"] = oldServerProfileRefusal2157
	return r
}

func assignArgs2080(extra ...string) []string {
	return append([]string{"team", "host", "assign", "Jonas", "--host", "orca1", "--workdir", "jonas-3", "--family", "claude"}, extra...)
}

func noCall2080(t *testing.T, calls *[]attnCall, ops ...string) {
	t.Helper()
	for _, c := range *calls {
		for _, op := range ops {
			if c.Op == op {
				t.Fatalf("%s was sent, want nothing", op)
			}
		}
	}
}

func TestTeamHostAssign2080OneMutationWithTheDocumentedInput(t *testing.T) {
	calls, out, err := runTeamTask(t, responses2080(), assignArgs2080()...)
	if err != nil {
		t.Fatal(err)
	}
	c := findCall(t, calls, "TeamHostAssign")
	in := c.Vars["input"].(map[string]any)
	want := map[string]any{"appRef": "capp100000000000000000000", "worker": "Jonas", "hostId": "orca1", "workdir": "jonas-3", "family": "claude"}
	if len(in) != len(want) {
		t.Fatalf("input = %v, want exactly %v (model and replace omitted, not null)", in, want)
	}
	for k, v := range want {
		if in[k] != v {
			t.Fatalf("input[%s] = %v, want %v", k, in[k], v)
		}
	}
	if c.Session != "" || c.Vars["instruction"] != nil {
		t.Fatalf("a plain write is the person's: session %q instruction %v", c.Session, c.Vars["instruction"])
	}
	noCall2080(t, calls, "TeamHostAssignInstructed")
	for _, w := range []string{"✓ assigned Jonas to host orca1", "workdir jonas-3", "family claude", "model opus-5.5"} {
		if !strings.Contains(out, w) {
			t.Fatalf("text missing %q:\n%s", w, out)
		}
	}
}

func TestTeamHostAssign2080ModelAndReplaceAreSentOnlyWhenGiven(t *testing.T) {
	calls, _, err := runTeamTask(t, responses2080(), assignArgs2080("--model", "opus-5.5", "--replace")...)
	if err != nil {
		t.Fatal(err)
	}
	in := findCall(t, calls, "TeamHostAssign").Vars["input"].(map[string]any)
	if in["model"] != "opus-5.5" || in["replace"] != true {
		t.Fatalf("input = %v", in)
	}
}

func TestTeamHostAssign2080JSON(t *testing.T) {
	_, out, err := runTeamTask(t, responses2080(), append(assignArgs2080(), "--json")...)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	for k, w := range map[string]any{"id": "a1", "hostId": "orca1", "workerId": "wkr1", "workerName": "Jonas", "workdir": "jonas-3", "family": "claude", "model": "opus-5.5"} {
		if got[k] != w {
			t.Fatalf("%s = %v, want %v in %v", k, got[k], w, got)
		}
	}
	// The two S1b profile keys are always present, null when unset (#2157).
	for _, k := range []string{"effort", "accountProfile"} {
		if v, ok := got[k]; !ok || v != nil {
			t.Fatalf("%s = %v (present %v), want an explicit null", k, v, ok)
		}
	}
	if len(got) != 11 {
		t.Fatalf("keys = %v", got)
	}
}

// A path, an option or a command never reaches the server: the token shape is
// checked first, with the server's own pattern, and nothing is sent.
func TestTeamHostAssign2080TokensAreRefusedBeforeAnyRequest(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"absolute path", []string{"team", "host", "assign", "Jonas", "--host", "orca1", "--workdir", "/home/orca/x", "--family", "claude"}},
		{"parent", []string{"team", "host", "assign", "Jonas", "--host", "orca1", "--workdir", "../x", "--family", "claude"}},
		{"option", []string{"team", "host", "assign", "Jonas", "--host", "orca1", "--workdir", "-rf", "--family", "claude"}},
		{"double slash", []string{"team", "host", "assign", "Jonas", "--host", "orca1", "--workdir", "a//b", "--family", "claude"}},
		{"trailing slash", []string{"team", "host", "assign", "Jonas", "--host", "orca1", "--workdir", "a/", "--family", "claude"}},
		{"command as model", assignArgs2080("--model", "bash -c x")},
		{"flag as model", assignArgs2080("--model", "--dangerously")},
		{"long model", assignArgs2080("--model", strings.Repeat("m", 129))},
		{"bad family", []string{"team", "host", "assign", "Jonas", "--host", "orca1", "--workdir", "jonas-3", "--family", "cl aude"}},
		{"bad host", []string{"team", "host", "assign", "Jonas", "--host", "or ca1", "--workdir", "jonas-3", "--family", "claude"}},
		{"no workdir", []string{"team", "host", "assign", "Jonas", "--host", "orca1", "--family", "claude"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls, out, err := runTeamTask(t, responses2080(), tc.args...)
			if err == nil || exitCodeFor(err) != exitcode.Usage {
				t.Fatalf("want exit %d, got %v (exit %d)", exitcode.Usage, err, exitCodeFor(err))
			}
			if len(*calls) != 0 {
				t.Fatalf("a refused token must send no request at all, got %v", *calls)
			}
			if strings.Contains(out, "✓") {
				t.Fatalf("receipt before refusal: %q", out)
			}
		})
	}
}

// The family that was validated is the family that is sent (BLIND P2, Jamir).
func TestTeamHostAssign2080FamilyIsSentAsValidated(t *testing.T) {
	calls, _, err := runTeamTask(t, responses2080(), "team", "host", "assign", "Jonas", "--host", "orca1", "--workdir", "jonas-3", "--family", " CLAUDE ")
	if err != nil {
		t.Fatal(err)
	}
	if in := findCall(t, calls, "TeamHostAssign").Vars["input"].(map[string]any); in["family"] != "claude" {
		t.Fatalf("family = %v, want the validated lowercase token", in["family"])
	}
}

// A null or missing hostSetup is a server failure (exit 1), never a panic and
// never a partial success (QA F1, Colin; BLIND P2, Jamir).
func TestTeamHostSetup2080NullResultIsAServerFailure(t *testing.T) {
	for name, resp := range map[string]string{"null": `{"data":{"hostSetup":null}}`, "missing": `{"data":{}}`} {
		t.Run(name, func(t *testing.T) {
			r := responses2080()
			r["TeamHostSetup"] = resp
			_, out, err := runTeamTask(t, r, "team", "host", "setup", "--json")
			if err == nil || exitCodeFor(err) != exitcode.Error {
				t.Fatalf("want exit %d, got %v (exit %d)", exitcode.Error, err, exitCodeFor(err))
			}
			if strings.TrimSpace(out) != "" {
				t.Fatalf("partial output: %q", out)
			}
		})
	}
}

func TestTeamHostAssign2080ServerRefusalsMapToExitCodes(t *testing.T) {
	for _, tc := range []struct {
		code string
		want int
	}{
		{"BAD_HOST_ASSIGNMENT_ARGS", exitcode.Usage},
		{"HOST_ASSIGNMENT_NOT_PERMITTED", exitcode.Forbidden},
		{"WORKER_NOT_FOUND", exitcode.NotFound},
		{"WORKER_RETIRED", exitcode.Conflict},
		{"HOST_ASSIGNMENT_EXISTS", exitcode.Conflict},
	} {
		t.Run(tc.code, func(t *testing.T) {
			r := responses2080()
			r["TeamHostAssign"] = `{"errors":[{"message":"refused ` + tc.code + `.","extensions":{"code":"` + tc.code + `"}}]}`
			_, out, err := runTeamTask(t, r, assignArgs2080()...)
			if err == nil || exitCodeFor(err) != tc.want || !strings.Contains(err.Error(), "refused "+tc.code) {
				t.Fatalf("want exit %d with the server's message, got %v (exit %d)", tc.want, err, exitCodeFor(err))
			}
			if strings.Contains(out, "✓") || strings.Contains(out, "assigned") {
				t.Fatalf("partial output before refusal: %q", out)
			}
		})
	}
}

func TestTeamHostAssign2080InstructionGoesThroughTheWorkerSession(t *testing.T) {
	calls, _, err := runTeamTask(t, responses2080(), assignArgs2080("--instruction", "task:hrn:task:acme.com:eng:7")...)
	if err != nil {
		t.Fatal(err)
	}
	c := findCall(t, calls, "TeamHostAssignInstructed")
	if c.Session != "s-new" {
		t.Fatalf("session = %q, want the bound worker's", c.Session)
	}
	if ins, _ := c.Vars["instruction"].(map[string]any); ins["kind"] == nil || ins["ref"] != "hrn:task:acme.com:eng:7" {
		t.Fatalf("instruction = %v", c.Vars["instruction"])
	}
	noCall2080(t, calls, "TeamHostAssign")
}

func TestTeamHostAssign2080BadInstructionIsRefusedBeforeAnyRequest(t *testing.T) {
	calls, _, err := runTeamTask(t, responses2080(), assignArgs2080("--instruction", "nonsense:x")...)
	if err == nil || exitCodeFor(err) != exitcode.Usage {
		t.Fatalf("want usage, got %v", err)
	}
	noCall2080(t, calls, "TeamHostAssign", "TeamHostAssignInstructed")
}

func TestTeamHostAssign2080RefusedInstructedWriteIsNeverRetriedAsThePerson(t *testing.T) {
	r := responses2080()
	r["TeamHostAssignInstructed"] = `{"errors":[{"message":"no.","extensions":{"code":"HOST_ASSIGNMENT_NOT_PERMITTED"}}]}`
	calls, _, err := runTeamTask(t, r, assignArgs2080("--instruction", "task:hrn:task:acme.com:eng:7")...)
	if err == nil || exitCodeFor(err) != exitcode.Forbidden {
		t.Fatalf("want forbidden, got %v", err)
	}
	noCall2080(t, calls, "TeamHostAssign")
}

func TestTeamHostUnassign2080RemovedAndThereWasNone(t *testing.T) {
	for _, tc := range []struct {
		name, resp, text string
		removed          bool
	}{
		{"removed", `true`, "✓ removed the assignment of Jonas to host orca1", true},
		{"none", `false`, "there was no assignment of Jonas to host orca1 to remove", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := responses2080()
			r["TeamHostUnassign"] = `{"data":{"unassignHostWorker":` + tc.resp + `}}`
			calls, out, err := runTeamTask(t, r, "team", "host", "unassign", "Jonas", "--host", "orca1")
			if err != nil {
				t.Fatalf("neither answer is an error: %v", err)
			}
			c := findCall(t, calls, "TeamHostUnassign")
			if in := c.Vars["input"].(map[string]any); in["worker"] != "Jonas" || in["hostId"] != "orca1" || in["appRef"] != "capp100000000000000000000" || len(in) != 3 {
				t.Fatalf("input = %v", in)
			}
			if c.Session != "" {
				t.Fatalf("session = %q on a plain write", c.Session)
			}
			if !strings.Contains(out, tc.text) {
				t.Fatalf("text missing %q:\n%s", tc.text, out)
			}
			_, js, err := runTeamTask(t, r, "team", "host", "unassign", "Jonas", "--host", "orca1", "--json")
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			if err := json.Unmarshal([]byte(js), &got); err != nil || got["removed"] != tc.removed || got["hostId"] != "orca1" || got["worker"] != "Jonas" {
				t.Fatalf("json = %s (%v)", js, err)
			}
		})
	}
}

func TestTeamHostUnassign2080RefusalsAndInstruction(t *testing.T) {
	r := responses2080()
	r["TeamHostUnassign"] = `{"errors":[{"message":"no.","extensions":{"code":"HOST_ASSIGNMENT_NOT_PERMITTED"}}]}`
	_, out, err := runTeamTask(t, r, "team", "host", "unassign", "Jonas", "--host", "orca1")
	if err == nil || exitCodeFor(err) != exitcode.Forbidden || strings.Contains(out, "removed") {
		t.Fatalf("got %v exit %d out %q", err, exitCodeFor(err), out)
	}
	calls, _, err := runTeamTask(t, responses2080(), "team", "host", "unassign", "Jonas", "--host", "orca1", "--instruction", "task:hrn:task:acme.com:eng:7")
	if err != nil {
		t.Fatal(err)
	}
	if c := findCall(t, calls, "TeamHostUnassignInstructed"); c.Session != "s-new" {
		t.Fatalf("session = %q", c.Session)
	}
	calls, _, err = runTeamTask(t, responses2080(), "team", "host", "unassign", "Jonas")
	if err == nil || exitCodeFor(err) != exitcode.Usage {
		t.Fatalf("--host is required: %v", err)
	}
	noCall2080(t, calls, "TeamHostUnassign")
}

func TestTeamHostSetup2080ReadCarriesNoSession(t *testing.T) {
	calls, out, err := runTeamTask(t, responses2080(), "team", "host", "setup")
	if err != nil {
		t.Fatal(err)
	}
	c := findCall(t, calls, "TeamHostSetup")
	if c.Session != "" {
		t.Fatalf("a read carries no worker session, got %q", c.Session)
	}
	if _, has := c.Vars["hostId"]; has || c.Vars["appRef"] != "capp100000000000000000000" {
		t.Fatalf("vars = %v (hostId omitted when not given)", c.Vars)
	}
	for _, w := range []string{"source: SERVER", "HOST", "WORKER", "Jonas", "jonas-3", "claude", "opus-5.5"} {
		if !strings.Contains(out, w) {
			t.Fatalf("text missing %q:\n%s", w, out)
		}
	}
}

func TestTeamHostSetup2080HostFilterAndEmptyJSON(t *testing.T) {
	r := responses2080()
	r["TeamHostSetup"] = `{"data":{"hostSetup":{"source":"HOST","assignments":[]}}}`
	calls, out, err := runTeamTask(t, r, "team", "host", "setup", "--host", "orca2", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if c := findCall(t, calls, "TeamHostSetup"); c.Vars["hostId"] != "orca2" {
		t.Fatalf("vars = %v", c.Vars)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if string(got["assignments"]) != "[]" || string(got["source"]) != `"HOST"` || string(got["hostId"]) != `"orca2"` {
		t.Fatalf("json = %s", out)
	}
	_, out, err = runTeamTask(t, r, "team", "host", "setup")
	if err != nil || !strings.Contains(out, "no assignments") {
		t.Fatalf("empty text: %v %q", err, out)
	}
}

func TestTeamHostSetup2080SetSource(t *testing.T) {
	calls, out, err := runTeamTask(t, responses2080(), "team", "host", "setup", "--source", "server")
	if err != nil {
		t.Fatal(err)
	}
	c := findCall(t, calls, "TeamHostSetSource")
	if c.Vars["source"] != "SERVER" || c.Vars["appRef"] != "capp100000000000000000000" || c.Session != "" {
		t.Fatalf("call = %+v", c)
	}
	noCall2080(t, calls, "TeamHostSetup")
	if !strings.Contains(out, "host setup source is now SERVER") {
		t.Fatalf("text:\n%s", out)
	}
	_, js, err := runTeamTask(t, responses2080(), "team", "host", "setup", "--source", "SERVER", "--json")
	if err != nil || !strings.Contains(js, `"source": "SERVER"`) && !strings.Contains(js, `"source":"SERVER"`) {
		t.Fatalf("json: %v %s", err, js)
	}
	calls, _, err = runTeamTask(t, responses2080(), "team", "host", "setup", "--source", "HOST", "--instruction", "task:hrn:task:acme.com:eng:7")
	if err != nil {
		t.Fatal(err)
	}
	if c := findCall(t, calls, "TeamHostSetSourceInstructed"); c.Session != "s-new" || c.Vars["source"] != "HOST" {
		t.Fatalf("call = %+v", c)
	}
}

func TestTeamHostSetup2080UsageErrorsSendNothing(t *testing.T) {
	for _, argv := range [][]string{
		{"team", "host", "setup", "--source", "BOTH"},
		{"team", "host", "setup", "--source", "SERVER", "--host", "orca1"},
		{"team", "host", "setup", "--instruction", "task:hrn:task:acme.com:eng:7"},
		{"team", "host", "setup", "--host", "or ca"},
	} {
		calls, _, err := runTeamTask(t, responses2080(), argv...)
		if err == nil || exitCodeFor(err) != exitcode.Usage {
			t.Fatalf("%v: want usage, got %v", argv, err)
		}
		noCall2080(t, calls, "TeamHostSetSource", "TeamHostSetSourceInstructed", "TeamHostSetup")
	}
}

func TestTeamHostSetup2080SourceRefusalMapsAndPrintsNothing(t *testing.T) {
	r := responses2080()
	r["TeamHostSetSource"] = `{"errors":[{"message":"no.","extensions":{"code":"HOST_ASSIGNMENT_NOT_PERMITTED"}}]}`
	_, out, err := runTeamTask(t, r, "team", "host", "setup", "--source", "SERVER")
	if err == nil || exitCodeFor(err) != exitcode.Forbidden || strings.Contains(out, "now") {
		t.Fatalf("got %v out %q", err, out)
	}
}
