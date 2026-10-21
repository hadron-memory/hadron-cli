package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// #910 (C4 of hadron-server#2016): `team task mine` is `task list` with its
// filters fixed to the bound worker's open work — assignee worker:<bound id>,
// status PENDING and STARTED — and refuses without a usable binding rather
// than falling back to any wider list.

func taskListCall(t *testing.T, calls *[]attnCall) attnCall {
	t.Helper()
	var found []attnCall
	for _, c := range *calls {
		if c.Op == "AgentTaskList" {
			found = append(found, c)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want exactly one AgentTaskList call, got %d: %+v", len(found), *calls)
	}
	return found[0]
}

func noTaskListCall(t *testing.T, calls *[]attnCall) {
	t.Helper()
	if calls == nil {
		return
	}
	for _, c := range *calls {
		if c.Op == "AgentTaskList" {
			t.Fatalf("a refused `task mine` still listed tasks: %+v", c.Vars)
		}
	}
}

func TestTeamTaskMineSendsTheBoundWorkersOpenFilter(t *testing.T) {
	calls, _, err := runTeamTask(t, taskResponses(), "team", "task", "mine")
	if err != nil {
		t.Fatal(err)
	}
	c := taskListCall(t, calls)
	if c.Vars["appRef"] != "capp100000000000000000000" {
		t.Fatalf("appRef %v, want the bound App", c.Vars["appRef"])
	}
	a, _ := c.Vars["assignee"].(map[string]any)
	if a == nil || a["worker"] != "wkr1" || a["user"] != nil {
		t.Fatalf("assignee %v, want {worker: wkr1} (the bound worker's id)", c.Vars["assignee"])
	}
	st, _ := c.Vars["status"].([]any)
	if len(st) != 2 || st[0] != "PENDING" || st[1] != "STARTED" {
		t.Fatalf("status %v, want exactly [PENDING STARTED]", c.Vars["status"])
	}
	for _, k := range []string{"owner", "parentTaskId", "queued", "rootsOnly", "tag"} {
		if v, ok := c.Vars[k]; ok && v != nil {
			t.Fatalf("%s = %v: `task mine` sends no filter beyond assignee and status", k, v)
		}
	}
	if c.Vars["limit"] != float64(20) || c.Vars["offset"] != float64(0) {
		t.Fatalf("paging %v/%v, want the list defaults 20/0", c.Vars["limit"], c.Vars["offset"])
	}
	for _, c := range *calls {
		if strings.HasPrefix(strings.TrimSpace(c.Query), "mutation") {
			t.Fatalf("`task mine` is read-only, but sent %s", c.Op)
		}
	}
}

// Human and JSON output are the list's own: the same request through
// `task list` with the filters spelled out renders byte-identically.
func TestTeamTaskMineMatchesTheExplicitList(t *testing.T) {
	for _, mode := range [][]string{nil, {"--json"}} {
		_, mine, err := runTeamTask(t, taskResponses(), append([]string{"team", "task", "mine"}, mode...)...)
		if err != nil {
			t.Fatal(err)
		}
		_, list, err := runTeamTask(t, taskResponses(), append([]string{"team", "task", "list",
			"--assignee", "worker:wkr1", "--status", "PENDING", "--status", "STARTED"}, mode...)...)
		if err != nil {
			t.Fatal(err)
		}
		if mine != list {
			t.Fatalf("%v: `task mine` and the explicit list differ:\nmine:\n%s\nlist:\n%s", mode, mine, list)
		}
	}
}

func TestTeamTaskMineEmptyIsAnEmptyList(t *testing.T) {
	r := taskResponses()
	r["AgentTaskList"] = `{"data":{"agentTasks":[]}}`
	_, out, err := runTeamTask(t, r, "team", "task", "mine", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "[]" {
		t.Fatalf("empty result renders %q, want []", out)
	}
}

func TestTeamTaskMinePaging(t *testing.T) {
	calls, _, err := runTeamTask(t, taskResponses(), "team", "task", "mine", "--limit", "10", "--offset", "5")
	if err != nil {
		t.Fatal(err)
	}
	c := taskListCall(t, calls)
	if c.Vars["limit"] != float64(10) || c.Vars["offset"] != float64(5) {
		t.Fatalf("paging %v/%v, want 10/5", c.Vars["limit"], c.Vars["offset"])
	}
	for _, bad := range [][]string{{"--limit", "0"}, {"--limit", "201"}, {"--offset", "-1"}} {
		calls, _, err := runTeamTask(t, taskResponses(), append([]string{"team", "task", "mine"}, bad...)...)
		if err == nil || exitCodeFor(err) != exitcode.Usage {
			t.Fatalf("%v: want a usage refusal, got %v", bad, err)
		}
		noTaskListCall(t, calls)
	}
}

// The fixed meaning has no override: the list's filter flags do not exist
// on `task mine`.
func TestTeamTaskMineOffersNoFilterOverrides(t *testing.T) {
	for _, flag := range [][]string{
		{"--assignee", "worker:Rufus"}, {"--status", "FINISHED"}, {"--owner", "worker:Rufus"},
		{"--queued"}, {"--roots-only"}, {"--parent", "task1"}, {"--tag", "ci"},
	} {
		calls, _, err := runTeamTask(t, taskResponses(), append([]string{"team", "task", "mine"}, flag...)...)
		if err == nil || !strings.Contains(err.Error(), "unknown flag") {
			t.Fatalf("%v: want an unknown-flag refusal, got %v", flag, err)
		}
		noTaskListCall(t, calls)
	}
}

func TestTeamTaskMineRefusesWithoutABinding(t *testing.T) {
	// A worktree with no binding, and an App named: still no list, because
	// no worker is bound to say whose tasks are "mine".
	teamGitDir(t)
	srv, calls := attnServer(t, taskResponses())
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"--server", srv.URL, "--app", "capp100000000000000000000", "team", "task", "mine"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "hadron team session start") {
		t.Fatalf("want a refusal pointing at `hadron team session start`, got %v", err)
	}
	if exitCodeFor(err) != exitcode.Usage {
		t.Fatalf("exit %d, want Usage", exitCodeFor(err))
	}
	noTaskListCall(t, calls)
}

func TestTeamTaskMineRefusesOutsideAWorktree(t *testing.T) {
	t.Setenv(team.GitDirEnv, "")
	t.Chdir(t.TempDir())
	srv, calls := attnServer(t, taskResponses())
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"--server", srv.URL, "--app", "capp100000000000000000000", "team", "task", "mine"})
	if err := root.Execute(); err == nil {
		t.Fatal("`task mine` outside a worktree listed something instead of refusing")
	}
	noTaskListCall(t, calls)
}

func TestTeamTaskMineRefusesAnUnusableBinding(t *testing.T) {
	for name, fixture := range map[string]string{
		"corrupt":   `{"sessionId":`,
		"no worker": strings.Replace(bindingFixture, `"workerId":"wkr1"`, `"workerId":""`, 1),
		"no App":    strings.Replace(bindingFixture, `"appId":"capp100000000000000000000",`, ``, 1),
		// Joost's P2-2 on #922: a worker and App without a session is not a
		// worker-session binding.
		"empty session":   strings.Replace(bindingFixture, `"sessionId":"s-new"`, `"sessionId":""`, 1),
		"missing session": strings.Replace(bindingFixture, `"sessionId":"s-new",`, ``, 1),
	} {
		t.Run(name, func(t *testing.T) {
			dir := teamGitDir(t)
			if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(fixture), 0o600); err != nil {
				t.Fatal(err)
			}
			srv, calls := attnServer(t, taskResponses())
			if name != "corrupt" {
				setTeamBindingServer(t, srv.URL)
			}
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"--server", srv.URL, "team", "task", "mine"})
			if err := root.Execute(); err == nil {
				t.Fatal("an unusable binding listed tasks instead of refusing")
			}
			noTaskListCall(t, calls)
		})
	}
}

// --app may name the bound App in any accepted spelling — its id, its URN or
// the short <root>:<slug> form — but never redirect "mine" to another one:
// the refs are compared by identity, and the query stays on the bound id
// (Joost's P2-1 on #922).
func TestTeamTaskMineAcceptsTheBoundAppInAnySpelling(t *testing.T) {
	for _, ref := range []string{"capp100000000000000000000", "hrn:app:acme.com:eng-team", "acme.com:eng-team"} {
		t.Run(ref, func(t *testing.T) {
			calls, _, err := runTeamTask(t, taskResponses(), "--app", ref, "team", "task", "mine")
			if err != nil {
				t.Fatalf("--app %s names the bound App and must be accepted: %v", ref, err)
			}
			if c := taskListCall(t, calls); c.Vars["appRef"] != "capp100000000000000000000" {
				t.Fatalf("appRef %v, want the bound id", c.Vars["appRef"])
			}
		})
	}
}

func TestTeamTaskMineRefusesAnotherApp(t *testing.T) {
	r := taskResponses()
	r["TeamAppIdentity"] = strings.ReplaceAll(teamAppIdentityJSON, "capp100000000000000000000", "capp200000000000000000000")
	calls, _, err := runTeamTask(t, r, "--app", "hrn:app:acme.com:other-team", "team", "task", "mine")
	if err == nil || exitCodeFor(err) != exitcode.Usage || !strings.Contains(err.Error(), "capp100000000000000000000") {
		t.Fatalf("want a usage refusal naming the bound App, got %v", err)
	}
	noTaskListCall(t, calls)
}

// An --app the server cannot resolve is not the bound App; a failed lookup
// keeps its own error rather than becoming either answer.
func TestTeamTaskMineAppIdentityUnknownOrFailed(t *testing.T) {
	r := taskResponses()
	r["TeamAppIdentity"] = `{"data":{"app":null}}`
	calls, _, err := runTeamTask(t, r, "--app", "hrn:app:acme.com:nowhere", "team", "task", "mine")
	if err == nil || exitCodeFor(err) != exitcode.Usage {
		t.Fatalf("unresolvable --app: want a usage refusal, got %v", err)
	}
	noTaskListCall(t, calls)

	r = taskResponses()
	r["TeamAppIdentity"] = gqlErrorJSON("UNAUTHENTICATED")
	calls, _, err = runTeamTask(t, r, "--app", "hrn:app:acme.com:eng-team", "team", "task", "mine")
	if err == nil || exitCodeFor(err) != exitcode.AuthRequired {
		t.Fatalf("failed identity lookup: want its AuthRequired error, got %v (exit %d)", err, exitCodeFor(err))
	}
	noTaskListCall(t, calls)
}

func TestTeamTaskMineRefusesAnotherServer(t *testing.T) {
	writeTeamBinding(t)
	setTeamBindingServer(t, "https://elsewhere.example")
	srv, calls := attnServer(t, taskResponses())
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"--server", srv.URL, "team", "task", "mine"})
	err := root.Execute()
	if err == nil || exitCodeFor(err) != exitcode.Usage || !strings.Contains(err.Error(), "elsewhere.example") {
		t.Fatalf("want a usage refusal naming the binding's server, got %v", err)
	}
	noTaskListCall(t, calls)
}

// The server's task-read gate stays the only gate, and its refusal maps
// exactly as the list's does.
func TestTeamTaskMinePreservesTheServerRefusal(t *testing.T) {
	r := taskResponses()
	r["AgentTaskList"] = `{"data":null,"errors":[{"message":"not a participant","extensions":{"code":"FORBIDDEN"}}]}`
	_, _, mineErr := runTeamTask(t, r, "team", "task", "mine")
	_, _, listErr := runTeamTask(t, r, "team", "task", "list", "--assignee", "worker:wkr1")
	if mineErr == nil || exitCodeFor(mineErr) != exitcode.Forbidden || exitCodeFor(mineErr) != exitCodeFor(listErr) {
		t.Fatalf("mine %v (exit %d), list %v (exit %d): want the same Forbidden", mineErr, exitCodeFor(mineErr), listErr, exitCodeFor(listErr))
	}
}

func TestTeamTaskMineHelpStatesTheFixedFilters(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "task", "mine", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	help := strings.Join(strings.Fields(out.String()), " ")
	for _, want := range []string{"PENDING", "STARTED", "bound worker", "hadron team session start", "cap 200"} {
		if !strings.Contains(help, want) {
			t.Errorf("help should say %q:\n%s", want, out.String())
		}
	}
}
