package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// cli#2514 (hadron-server#4414, cor:agt:050:30): `task finish --worked-from REF` records the role tasks the
// assignee followed; the stored {nodeId, urn, revision} refs come back on the receipt, `task get` and --json.

const (
	refA2514 = "hrn:node:acme.com:eng-system:tasks:build-it"
	refB2514 = "hrn:node:acme.com:eng-system:tasks:review-it"
)

// finishedWithRefs2514 is the finished task, with the refs the server stored on its result.
func finishedWithRefs2514(t *testing.T, refs ...map[string]any) string {
	t.Helper()
	var task map[string]any
	if err := json.Unmarshal([]byte(finishedTaskFieldsJSON), &task); err != nil {
		t.Fatal(err)
	}
	if refs != nil {
		task["result"].(map[string]any)["workedFromRoleTaskRefs"] = refs
	}
	b, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func storedRefs2514() []map[string]any {
	return []map[string]any{
		{"nodeId": "node-a", "urn": refA2514, "revision": 3},
		{"nodeId": "node-b", "urn": refB2514, "revision": 12},
	}
}

func finishResponses2514(t *testing.T, refs ...map[string]any) map[string]string {
	t.Helper()
	r := taskResponses()
	r["AgentTaskFinish"] = `{"data":{"finishAgentTask":` + finishedWithRefs2514(t, refs...) + `}}`
	return r
}

func TestTaskFinishWorkedFromSendsRefsInOrder2514(t *testing.T) {
	calls, out, err := runTeamTask(t, finishResponses2514(t, storedRefs2514()...),
		"team", "task", "finish", "task1", "--outcome", "succeeded", "--worked-from", refA2514, "--worked-from", refB2514)
	if err != nil {
		t.Fatal(err)
	}
	var got []any
	for _, c := range *calls {
		if c.Op == "AgentTaskFinish" {
			res, _ := c.Vars["result"].(map[string]any)
			got, _ = res["workedFromRoleTaskRefs"].([]any)
		}
	}
	if len(got) != 2 || got[0] != refA2514 || got[1] != refB2514 {
		t.Fatalf("refs sent %v, want [%s %s] in order", got, refA2514, refB2514)
	}
	for _, want := range []string{"worked from", refA2514, "r3", refB2514, "r12"} {
		if !strings.Contains(out, want) {
			t.Errorf("receipt lacks %q:\n%s", want, out)
		}
	}
}

func TestTaskFinishWorkedFromJSONShowsStoredRefs2514(t *testing.T) {
	_, out, err := runTeamTask(t, finishResponses2514(t, storedRefs2514()...),
		"team", "task", "finish", "task1", "--outcome", "succeeded", "--worked-from", refA2514, "--worked-from", refB2514, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Result struct {
			Refs []struct {
				NodeID   string `json:"nodeId"`
				URN      string `json:"urn"`
				Revision int    `json:"revision"`
			} `json:"workedFromRoleTaskRefs"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	r := env.Result.Refs
	if len(r) != 2 || r[0].NodeID != "node-a" || r[0].URN != refA2514 || r[0].Revision != 3 || r[1].NodeID != "node-b" || r[1].Revision != 12 {
		t.Fatalf("result.workedFromRoleTaskRefs = %+v", r)
	}
}

func TestTaskFinishWorkedFromElevenRefsRefuseLocally2514(t *testing.T) {
	argv := []string{"team", "task", "finish", "task1", "--outcome", "succeeded"}
	for i := 0; i < 11; i++ {
		argv = append(argv, "--worked-from", "hrn:node:acme.com:eng-system:tasks:t"+string(rune('a'+i)))
	}
	calls, out, err := runTeamTask(t, taskResponses(), argv...)
	if err == nil || exitCodeFor(err) != 2 {
		t.Fatalf("want exit 2, got %v (%d)", err, exitCodeFor(err))
	}
	for _, c := range *calls {
		if c.Op == "AgentTaskFinish" || c.Op == "AgentTaskFinishWithFindings" {
			t.Fatalf("a refused finish must send no mutation, got %s", c.Op)
		}
	}
	if out != "" {
		t.Fatalf("no output on a refusal, got %q", out)
	}
}

func TestTaskFinishWorkedFromTenRefsAreSent2514(t *testing.T) {
	argv := []string{"team", "task", "finish", "task1", "--outcome", "succeeded"}
	for i := 0; i < 10; i++ {
		argv = append(argv, "--worked-from", "hrn:node:acme.com:eng-system:tasks:t"+string(rune('a'+i)))
	}
	if _, _, err := runTeamTask(t, taskResponses(), argv...); err != nil {
		t.Fatalf("ten refs are the cap, not over it: %v", err)
	}
}

func TestTaskFinishWorkedFromEmptyRefRefuses2514(t *testing.T) {
	calls, _, err := runTeamTask(t, taskResponses(), "team", "task", "finish", "task1", "--outcome", "succeeded", "--worked-from", "  ")
	if err == nil || exitCodeFor(err) != 2 {
		t.Fatalf("want exit 2, got %v", err)
	}
	for _, c := range *calls {
		if c.Op == "AgentTaskFinish" {
			t.Fatal("a refused finish must send no mutation")
		}
	}
}

// No flag: the request names no workedFromRoleTaskRefs at all (omitted, not null), and the output is as before.
func TestTaskFinishWithoutWorkedFromIsUnchanged2514(t *testing.T) {
	calls, out, err := runTeamTask(t, taskResponses(), "team", "task", "finish", "task1", "--outcome", "succeeded")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range *calls {
		if c.Op == "AgentTaskFinish" && strings.Contains(string(c.RawVars), "workedFromRoleTaskRefs") {
			t.Fatalf("request must not carry the field: %s", c.RawVars)
		}
	}
	if strings.Contains(out, "worked from") {
		t.Fatalf("receipt gained a line: %q", out)
	}
	_, jsonOut, err := runTeamTask(t, taskResponses(), "team", "task", "finish", "task1", "--outcome", "succeeded", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(jsonOut, "workedFromRoleTaskRefs") {
		t.Fatalf("--json gained a key: %s", jsonOut)
	}
}

func TestTaskFinishWorkedFromRefusalNamesNoRef2514(t *testing.T) {
	r := taskResponses()
	// The server's message could echo whatever it likes; the CLI prints its own fixed words.
	r["AgentTaskFinish"] = `{"errors":[{"message":"role task node-secret-777 is not readable","extensions":{"code":"TASK_ROLE_TASK_REFS_INVALID"}}]}`
	_, out, err := runTeamTask(t, r, "team", "task", "finish", "task1", "--outcome", "succeeded", "--worked-from", "node-secret-777")
	if err == nil {
		t.Fatal("want a refusal")
	}
	msg := err.Error()
	if !strings.Contains(msg, "TASK_ROLE_TASK_REFS_INVALID") || !strings.Contains(msg, "--worked-from") {
		t.Fatalf("fixed message lacks the code or the flag: %q", msg)
	}
	if strings.Contains(msg, "node-secret-777") || strings.Contains(out, "node-secret-777") {
		t.Fatalf("a reference was echoed: %q / %q", msg, out)
	}
	if exitCodeFor(err) != 2 {
		t.Fatalf("a refused request exits with the usage code, got %d", exitCodeFor(err))
	}
}

func TestTaskFinishWorkedFromWithFindings2514(t *testing.T) {
	calls, _, err := runTeamTask(t, map[string]string{"AgentTaskFinishWithFindings": finishReply1550(t, `{"items":[],"total":0,"hasMore":false}`)},
		"team", "task", "finish", "AT0.7", "--outcome", "succeeded", "--findings-file", findingsFile1550(t, `[]`), "--worked-from", refA2514, "--worked-from", refB2514)
	if err != nil {
		t.Fatal(err)
	}
	var got []any
	for _, c := range *calls {
		if c.Op == "AgentTaskFinishWithFindings" {
			res, _ := c.Vars["workedFrom"].([]any)
			got = res
		}
	}
	if len(got) != 2 || got[0] != refA2514 || got[1] != refB2514 {
		t.Fatalf("findings finish sent %v", got)
	}
}

func TestTaskFinishWithFindingsWithoutWorkedFromOmitsIt2514(t *testing.T) {
	calls, _, err := runTeamTask(t, map[string]string{"AgentTaskFinishWithFindings": finishReply1550(t, `{"items":[],"total":0,"hasMore":false}`)},
		"team", "task", "finish", "AT0.7", "--outcome", "succeeded", "--findings-file", findingsFile1550(t, `[]`))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range *calls {
		if c.Op == "AgentTaskFinishWithFindings" && strings.Contains(string(c.RawVars), "workedFrom") {
			t.Fatalf("request must omit the variable: %s", c.RawVars)
		}
	}
}

func TestTaskGetShowsWorkedFromRefs2514(t *testing.T) {
	r := taskResponses()
	r["AgentTaskGet"] = `{"data":{"agentTask":` + finishedWithRefs2514(t, storedRefs2514()...) + `}}`
	_, out, err := runTeamTask(t, r, "team", "task", "get", "task1")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"worked from", refA2514, "r3", refB2514, "r12"} {
		if !strings.Contains(out, want) {
			t.Errorf("task get lacks %q:\n%s", want, out)
		}
	}
	// A task finished without refs shows nothing new.
	r["AgentTaskGet"] = `{"data":{"agentTask":` + finishedWithRefs2514(t) + `}}`
	_, plain, err := runTeamTask(t, r, "team", "task", "get", "task1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain, "worked from") {
		t.Fatalf("task get gained a line: %s", plain)
	}
	_, plainJSON, err := runTeamTask(t, r, "team", "task", "get", "task1", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plainJSON, "workedFromRoleTaskRefs") {
		t.Fatalf("--json gained a key: %s", plainJSON)
	}
}
