package cmd

import (
	"strings"
	"testing"
)

// cli#967: no list command carries a hidden page default above 20, and a full
// DEFAULT page of a list with no total says more may exist.

func taskListN(n int) map[string]string {
	rows := make([]string, n)
	for i := range rows {
		rows[i] = taskFieldsJSON
	}
	r := taskResponses()
	r["AgentTaskList"] = `{"data":{"agentTasks":[` + strings.Join(rows, ",") + `]}}`
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

func TestTeamTaskListAndMineDefaultTo20AndSayWhenThePageIsFull(t *testing.T) {
	for _, argv := range [][]string{{"team", "task", "list"}, {"team", "task", "mine"}} {
		name := strings.Join(argv, " ")
		calls, errOut, err := runTeamTaskStderr(t, taskListN(20), argv...)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		c := taskListCall(t, calls)
		if c.Vars["limit"] != float64(20) {
			t.Errorf("%s: default limit sent as %v, want 20", name, c.Vars["limit"])
		}
		for _, want := range []string{"full page", "--offset 20"} {
			if !strings.Contains(errOut, want) {
				t.Errorf("%s: stderr missing %q: %q", name, want, errOut)
			}
		}
	}
}

func TestTeamTaskListShortOrExplicitPageHasNoCue(t *testing.T) {
	_, errOut, err := runTeamTaskStderr(t, taskListN(19), "team", "task", "list")
	if err != nil || errOut != "" {
		t.Errorf("19 of 20 is a complete list: err=%v cue=%q", err, errOut)
	}
	_, errOut, err = runTeamTaskStderr(t, taskListN(20), "team", "task", "list", "--limit", "20")
	if err != nil || errOut != "" {
		t.Errorf("an explicit --limit is the user's own page: err=%v cue=%q", err, errOut)
	}
}

func TestTeamEventsAndHostOpsDefaultTo20(t *testing.T) {
	calls, _, err := runTeamTask(t, taskResponses(), "team", "events")
	if err != nil {
		t.Fatal(err)
	}
	if c := findCall(t, calls, "AgentTeamEvents"); c.Vars["limit"] != float64(20) {
		t.Errorf("team events default limit %v, want 20", c.Vars["limit"])
	}
	calls, _, err = runTeamTask(t, hostResponses(), "team", "host", "ops")
	if err != nil {
		t.Fatal(err)
	}
	if c := findCall(t, calls, "HostOperations"); c.Vars["first"] != float64(20) {
		t.Errorf("team host operations default first %v, want 20", c.Vars["first"])
	}
}
