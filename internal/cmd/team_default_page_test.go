package cmd

import (
	"strings"
	"testing"
)

// cli#967: no list command carries a hidden page default above 20. cli#2734
// replaced the full-page guess with the server's own page end: a continuation
// is said only when agentTasksPage answers hasMore, whatever the limit.

func taskListN(n int, hasMore bool) map[string]string {
	r := taskResponses()
	r["AgentTaskList"] = taskPage2734(taskRows2734(n), n, hasMore)
	return r
}

func runTeamTaskStderr(t *testing.T, responses map[string]string, argv ...string) (*[]attnCall, string, error) {
	t.Helper()
	writeTeamBinding(t)
	srv, calls := attnServer(t, responses)
	setTeamBindingServer(t, srv.URL)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"--server", srv.URL}, argv...))
	err := root.Execute()
	return calls, f.IOStreams.ErrOut.(*strings.Builder).String(), err
}

func TestTeamTaskListAndMineDefaultTo20AndCueOnlyOnServerHasMore(t *testing.T) {
	for _, argv := range [][]string{{"team", "task", "list"}, {"team", "task", "mine"}} {
		name := strings.Join(argv, " ")
		calls, _, err := runTeamTask(t, taskListN(20, true), append(argv, "--json")...)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		c := taskListCall(t, calls)
		if c.Vars["limit"] != float64(20) {
			t.Errorf("%s: default limit sent as %v, want 20", name, c.Vars["limit"])
		}
		_, out, _, err := runTeamTaskStaged2734(t, taskListN(20, true), nil, argv...)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.Contains(out, "--offset 20") {
			t.Errorf("%s: hasMore is the server's page end and is said: %q", name, out)
		}
		_, out, _, err = runTeamTaskStaged2734(t, taskListN(20, false), nil, argv...)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if strings.Contains(out, "--offset") {
			t.Errorf("%s: a full page with hasMore false is the end: %q", name, out)
		}
	}
}

func TestTeamTaskListShortOrExplicitPageHasNoInventedCue(t *testing.T) {
	_, errOut, err := runTeamTaskStderr(t, taskListN(19, false), "team", "task", "list")
	if err != nil || errOut != "" {
		t.Errorf("19 of 20 is a complete list: err=%v cue=%q", err, errOut)
	}
	// An explicit --limit is the user's own page — and the server's page end
	// is still the truth, so hasMore alone decides whether more is said.
	_, out, _, err := runTeamTaskStaged2734(t, taskListN(20, false), nil, "team", "task", "list", "--limit", "20")
	if err != nil || strings.Contains(out, "--offset") {
		t.Errorf("an explicit full page the server says is the end says no more: err=%v out=%q", err, out)
	}
	_, out, _, err = runTeamTaskStaged2734(t, taskListN(20, true), nil, "team", "task", "list", "--limit", "20")
	if err != nil || !strings.Contains(out, "--offset 20") {
		t.Errorf("hasMore is said even at an explicit limit: err=%v out=%q", err, out)
	}
}

func TestTeamEventsAndHostOpsDefaultTo20(t *testing.T) {
	calls, _, err := runTeamTask(t, taskResponses(), "team", "events")
	if err != nil {
		t.Fatal(err)
	}
	if c := findCall(t, calls, "AgentTeamEventsPage"); c.Vars["limit"] != float64(20) {
		t.Errorf("team events default limit %v, want 20", c.Vars["limit"])
	}
	calls, _, err = runTeamTask(t, hostResponses(), "team", "host", "ops")
	if err != nil {
		t.Fatal(err)
	}
	if c := findCall(t, calls, "HostOperationsWithReason"); c.Vars["first"] != float64(20) {
		t.Errorf("team host operations default first %v, want 20", c.Vars["first"])
	}
}
