package cmd

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func TestTaskMatching1549Wire(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		role  any
		roles any
		match any
		team  any
	}{
		{name: "omitted"},
		{name: "false", args: []string{"--match=false"}},
		{name: "single", args: []string{"--require-role", "qa"}, role: "qa"},
		{name: "single raw", args: []string{"--require-role", " QA "}, role: " QA "},
		{name: "roles", args: []string{"--require-role", "qa,cli", "--require-role", "server"}, roles: []any{"qa", "cli", "server"}},
		{name: "match team", args: []string{"--match", "--team", "hrn:worker:acme.com:eng:iris"}, match: true, team: "hrn:worker:acme.com:eng:iris"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"team", "task", "create", "example", "--json"}, tc.args...)
			calls, _, err := runTeamTask(t, taskResponses(), args...)
			if err != nil {
				t.Fatal(err)
			}
			var input map[string]any
			for _, c := range *calls {
				if c.Op == "AgentTaskCreate" {
					input = c.Vars["input"].(map[string]any)
				}
			}
			if input == nil {
				t.Fatal("no create")
			}
			if _, present := input["match"]; tc.match == nil && present {
				t.Fatal("absent/false match must omit the key")
			}
			if input["match"] != tc.match {
				t.Fatalf("match=%#v want %#v", input["match"], tc.match)
			}
			req, _ := input["requirements"].(map[string]any)
			for key, want := range map[string]any{"role": tc.role, "roles": tc.roles, "team": tc.team} {
				got := req[key]
				if _, present := req[key]; want == nil && (key != "role" || tc.roles != nil) && present {
					t.Errorf("unset %s must be omitted", key)
				}
				a, _ := json.Marshal(got)
				b, _ := json.Marshal(want)
				if string(a) != string(b) {
					t.Errorf("%s=%s want %s", key, a, b)
				}
			}
		})
	}
}

func TestTaskMatching1549EmptyAndTeamConflict(t *testing.T) {
	for _, args := range [][]string{
		{"create", "example", "--require-role", ""}, {"create", "example", "--require-role", ","},
		{"list", "--team", "--match-failed"}, {"list", "--team=worker:Iris", "--match-failed=false"},
	} {
		calls, _, err := runTeamTask(t, taskResponses(), append([]string{"team", "task"}, args...)...)
		if exitCodeFor(err) != 2 || len(*calls) != 0 {
			t.Errorf("%v: err=%v calls=%v", args, err, *calls)
		}
	}
}

func matchingFields1549(t *testing.T) string {
	t.Helper()
	var row map[string]any
	if err := json.Unmarshal([]byte(taskFieldsJSON), &row); err != nil {
		t.Fatal(err)
	}
	row["requirements"].(map[string]any)["roles"] = []string{"qa", "cli"}
	row["requirements"].(map[string]any)["teamWorkerId"] = "wkr1"
	{
		row["matchFailure"] = map[string]any{"reason": "FUTURE_REASON", "failedAt": "2026-11-02T20:00:00Z"}
	}
	b, _ := json.Marshal(row)
	return string(b)
}

func TestTaskMatching1549FailureReceipt(t *testing.T) {
	for _, jsonMode := range []bool{false, true} {
		r := taskResponses()
		r["AgentTaskCreate"] = `{"data":{"createAgentTask":` + matchingFields1549(t) + `}}`
		args := []string{"team", "task", "create", "example", "--match"}
		if jsonMode {
			args = append(args, "--json")
		}
		calls, out, err := runTeamTask(t, r, args...)
		if exitCodeFor(err) != 5 || !errors.Is(err, exitcode.ErrSilent) {
			t.Fatalf("want silent5, err=%v output=%s", err, out)
		}
		if !strings.Contains(out, "FUTURE_REASON") || !strings.Contains(out, "2026-11-02T20:00:00Z") {
			t.Fatal(out)
		}
		n := 0
		for _, c := range *calls {
			if c.Op == "AgentTaskCreate" {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("creates=%d", n)
		}
		if jsonMode {
			var row map[string]any
			if err := json.Unmarshal([]byte(out), &row); err != nil {
				t.Fatal(err)
			}
			if row["id"] != "task1" || row["matchFailure"] == nil {
				t.Fatal(row)
			}
		} else if strings.Contains(out, "platform's matcher picks one") {
			t.Fatal("failed task advertised for auto matching")
		}
	}
}

func TestTaskMatching1549ReadAndFilter(t *testing.T) {
	r := taskResponses()
	row := matchingFields1549(t)
	r["AgentTaskGet"] = `{"data":{"agentTask":` + row + `}}`
	r["AgentTaskListFiltered"] = `{"data":{"agentTasks":[` + row + `]}}`
	_, out, err := runTeamTask(t, r, "team", "task", "get", "task1", "--json")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{`"matchFailure"`, `"roles"`, `"teamWorkerId"`} {
		if !strings.Contains(out, s) {
			t.Fatal(out)
		}
	}
	for _, v := range []string{"true", "false"} {
		calls, out, err := runTeamTask(t, r, "team", "task", "list", "--match-failed="+v, "--json")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(out, "[") {
			t.Fatal(out)
		}
		found := false
		for _, c := range *calls {
			if c.Op == "AgentTaskListFiltered" {
				found = true
				if c.Vars["matchFailed"] != (v == "true") {
					t.Fatal(c.Vars)
				}
			}
		}
		if !found {
			t.Fatal("no filtered read")
		}
	}
	for _, args := range [][]string{{"get", "task1"}, {"create", "example"}, {"assign", "task1", "--assignee", "worker:Rufus"}, {"cancel", "task1"}} {
		_, out, err := runTeamTask(t, taskResponses(), append(append([]string{"team", "task"}, args...), "--json")...)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range []string{`"matchFailure"`, `"roles"`, `"teamWorkerId"`} {
			if strings.Contains(out, s) {
				t.Fatalf("absent metadata serialized: %s", out)
			}
		}
	}
}

func TestTaskMatching1549WriterFirst(t *testing.T) {
	for _, jsonMode := range []bool{false, true} {
		writeTeamBinding(t)
		r := taskResponses()
		r["AgentTaskCreate"] = `{"data":{"createAgentTask":` + matchingFields1549(t) + `}}`
		srv, calls := attnServer(t, r)
		setTeamBindingServer(t, srv.URL)
		f, _ := testFactory(t)
		f.IOStreams.Out = failingWriter{}
		root := NewRootCmd(f)
		args := []string{"--server", srv.URL, "team", "task", "create", "example", "--match"}
		if jsonMode {
			args = append(args, "--json")
		}
		root.SetArgs(args)
		err := root.Execute()
		if err == nil || exitCodeFor(err) == 5 || errors.Is(err, exitcode.ErrSilent) {
			t.Fatalf("writer error must win: %v", err)
		}
		n := 0
		for _, c := range *calls {
			if c.Op == "AgentTaskCreate" {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("creates=%d", n)
		}
	}
}

func TestTaskMatching1549SuccessAndRefusals(t *testing.T) {
	r := taskResponses()
	r["AgentTaskCreate"] = `{"data":{"createAgentTask":` + startedTaskFieldsJSON + `}}`
	_, out, err := runTeamTask(t, r, "team", "task", "create", "example", "--match")
	if err != nil || !strings.Contains(out, "matched: Rufus") || !strings.Contains(out, "created task") || !strings.Contains(out, "wkr2") || !strings.Contains(out, "hrn:worker:acme.com:eng:rufus") {
		t.Fatalf("err=%v out=%s", err, out)
	}
	for _, args := range [][]string{
		{"--match", "--assignee", "worker:Rufus"}, {"--match", "--status", "backlog"}, {"--match", "--blocked-by", "task2"},
		{"--team", "missing"}, {"--team", "retired"}, {"--team", "other-app"},
	} {
		r := taskResponses()
		r["AgentTaskCreate"] = `{"data":{"createAgentTask":null},"errors":[{"message":"matching input refused","extensions":{"code":"BAD_USER_INPUT"}}]}`
		calls, out, err := runTeamTask(t, r, append([]string{"team", "task", "create", "example", "--json"}, args...)...)
		if exitCodeFor(err) != 2 || out != "" {
			t.Fatalf("%v err=%v out=%s", args, err, out)
		}
		n := 0
		for _, c := range *calls {
			if c.Op == "AgentTaskCreate" {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("create count=%d", n)
		}
	}
	r = taskResponses()
	r["AgentTaskListFiltered"] = `{"data":{"agentTasks":[]}}`
	_, out, err = runTeamTask(t, r, "team", "task", "list", "--match-failed", "--json")
	if err != nil || strings.TrimSpace(out) != "[]" {
		t.Fatalf("err=%v out=%s", err, out)
	}
}

func TestTaskMatching1549HumanSafety(t *testing.T) {
	var row map[string]any
	_ = json.Unmarshal([]byte(matchingFields1549(t)), &row)
	hostile := "FUTURE\n\x1b]52;c;payload\a"
	row["matchFailure"].(map[string]any)["reason"] = hostile
	row["matchFailure"].(map[string]any)["failedAt"] = hostile
	row["requirements"].(map[string]any)["roles"] = []string{hostile}
	row["requirements"].(map[string]any)["teamWorkerId"] = hostile
	b, _ := json.Marshal(row)
	r := taskResponses()
	r["AgentTaskCreate"] = `{"data":{"createAgentTask":` + string(b) + `}}`
	r["AgentTaskGet"] = `{"data":{"agentTask":` + string(b) + `}}`
	r["AgentTaskListFiltered"] = `{"data":{"agentTasks":[` + string(b) + `]}}`
	for _, args := range [][]string{{"create", "example", "--match"}, {"get", "task1"}, {"list", "--match-failed"}} {
		_, out, _ := runTeamTask(t, r, append([]string{"team", "task"}, args...)...)
		if strings.ContainsAny(out, "\x1b\a") || !strings.Contains(out, "FUTURE") {
			t.Fatal(out)
		}
	}
	_, out, err := runTeamTask(t, r, "team", "task", "get", "task1", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var dto map[string]any
	_ = json.Unmarshal([]byte(out), &dto)
	if dto["matchFailure"].(map[string]any)["reason"] != hostile {
		t.Fatal("JSON must preserve unknown reason verbatim")
	}
}

func TestTaskMatching1549AbsentFilter(t *testing.T) {
	r := taskResponses()
	r["AgentTaskListFiltered"] = `{"data":{"agentTasks":[]}}`
	calls, _, err := runTeamTask(t, r, "team", "task", "list", "--external-id", "x", "--json")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range *calls {
		if c.Op == "AgentTaskListFiltered" {
			found = true
			if _, ok := c.Vars["matchFailed"]; ok {
				t.Fatal("absent match-failed must omit variable")
			}
		}
	}
	if !found {
		t.Fatal("missing filtered request")
	}
}

// Old flags without any matching additions keep their legacy role:null input.
// Omitting role for a role set must not drop that legacy key everywhere else.
func TestTaskMatching1549LegacyRequirements(t *testing.T) {
	calls, _, err := runTeamTask(t, taskResponses(), "team", "task", "create", "example", "--family-in", "claude", "--json")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range *calls {
		if c.Op == "AgentTaskCreate" {
			found = true
			r := c.Vars["input"].(map[string]any)["requirements"].(map[string]any)
			v, ok := r["role"]
			if !ok || v != nil {
				t.Fatal("legacy role:null changed", r)
			}
			for _, key := range []string{"roles", "team"} {
				if _, ok := r[key]; ok {
					t.Fatal("new key in legacy input", r)
				}
			}
		}
	}
	if !found {
		t.Fatal("missing create")
	}
}
