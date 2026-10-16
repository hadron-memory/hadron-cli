package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// hadron-cli#831: an agent the host poller launched binds with the single-use
// grant it was handed (cor:agt:070:08). The grant names the worker, so --as is
// optional with it, and the grant is the only launch credential on the wire.

func launchGrantStubs() map[string]string {
	return map[string]string{
		"GetWorker":        `{"data":{"worker":` + irisWorkerJSON + `}}`,
		"StartTeamSession": `{"data":{"startSession":` + startedSessionJSON + `}}`,
	}
}

func startInput(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var vars struct {
		Input map[string]any `json:"input"`
	}
	if err := json.Unmarshal(raw, &vars); err != nil {
		t.Fatalf("StartTeamSession variables: %v (%s)", err, raw)
	}
	return vars.Input
}

// The environment path is the one the poller uses: no flag, no --as.
func TestSessionStartRedeemsLaunchGrantFromEnvironment(t *testing.T) {
	teamGitDir(t)
	t.Setenv("HADRON_LAUNCH_GRANT", "hlg_abc")
	gql, captured := captureGraphQL(t, launchGrantStubs())
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--tool", "claude-code", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("a launched start must bind: %v", err)
	}
	in := startInput(t, captured["StartTeamSession"])
	if in["launchGrant"] != "hlg_abc" {
		t.Errorf("launchGrant = %v, want hlg_abc", in["launchGrant"])
	}
	if _, sent := in["workerRef"]; sent {
		t.Errorf("without --as the grant names the worker; workerRef must be omitted, got %v", in["workerRef"])
	}
	if _, sent := in["force"]; sent {
		t.Error("force must never ride with a grant")
	}
	// The worker is read AFTER the bind, by the session's workerId.
	if _, read := captured["GetWorker"]; !read {
		t.Error("the launched worker must be read for the binding")
	}
	if !strings.Contains(out.String(), "as Iris") {
		t.Errorf("the receipt must name the launched worker:\n%s", out.String())
	}
}

// The flag wins over the environment.
func TestSessionStartLaunchGrantFlagOverridesEnvironment(t *testing.T) {
	teamGitDir(t)
	t.Setenv("HADRON_LAUNCH_GRANT", "hlg_env")
	gql, captured := captureGraphQL(t, launchGrantStubs())
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--launch-grant", "hlg_flag", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if got := startInput(t, captured["StartTeamSession"])["launchGrant"]; got != "hlg_flag" {
		t.Errorf("launchGrant = %v, want hlg_flag", got)
	}
}

// An ordinary start never sends the field at all (the #139 trap: a null on the
// wire is not an absent field).
func TestSessionStartWithoutGrantOmitsLaunchGrant(t *testing.T) {
	teamGitDir(t)
	t.Setenv("HADRON_LAUNCH_GRANT", "")
	gql, captured := captureGraphQL(t, startWithAffinityStubs(irisWorkerJSON, nil))
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if v, sent := startInput(t, captured["StartTeamSession"])["launchGrant"]; sent {
		t.Errorf("an ordinary start must omit launchGrant, sent %v", v)
	}
}

func TestSessionStartLaunchGrantRefusals(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"empty grant flag", []string{"--launch-grant", " "}, "--launch-grant must not be empty"},
		{"force with a grant", []string{"--launch-grant", "hlg_x", "--force"}, "--force does not apply"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			teamGitDir(t)
			t.Setenv("HADRON_LAUNCH_GRANT", "")
			gql, captured := captureGraphQL(t, launchGrantStubs())
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append([]string{"team", "session", "start", "--server", gql.URL}, c.args...))
			err := root.Execute()
			if code := exitCodeFor(err); code != exitcode.Usage {
				t.Errorf("exit = %d, want %d; err: %v", code, exitcode.Usage, err)
			}
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("error must contain %q: %v", c.want, err)
			}
			if _, called := captured["StartTeamSession"]; called {
				t.Error("a refused start must not reach the server")
			}
		})
	}
}

// The server's two launch refusals keep their classes: a grant that cannot
// work is a permission (8), a bind the state refuses is a conflict (5).
func TestSessionStartLaunchGrantServerRefusals(t *testing.T) {
	cases := []struct {
		code string
		want int
	}{
		{"LAUNCH_GRANT_INVALID", exitcode.Forbidden},
		{"LAUNCH_REFUSED", exitcode.Conflict},
	}
	for _, c := range cases {
		t.Run(c.code, func(t *testing.T) {
			teamGitDir(t)
			stubs := launchGrantStubs()
			stubs["StartTeamSession"] = `{"errors":[{"message":"refused","extensions":{"code":"` + c.code + `"}}],"data":null}`
			gql, _ := captureGraphQL(t, stubs)
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"team", "session", "start", "--launch-grant", "hlg_x", "--server", gql.URL})
			err := root.Execute()
			if code := exitCodeFor(err); code != c.want {
				t.Errorf("exit = %d, want %d; err: %v", code, c.want, err)
			}
		})
	}
}

// A spent grant left in the environment (the poller sets it for the agent's
// whole process tree) never reaches a later ordinary bind: --as wins, and
// --force is not refused on a grant the user never chose.
func TestSessionStartAsIgnoresTheGrantInTheEnvironment(t *testing.T) {
	teamGitDir(t)
	t.Setenv("HADRON_LAUNCH_GRANT", "hlg_spent")
	gql, captured := captureGraphQL(t, startWithAffinityStubs(irisWorkerJSON, nil))
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--force", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("--as with a spent grant in the environment must bind normally: %v", err)
	}
	if v, sent := startInput(t, captured["StartTeamSession"])["launchGrant"]; sent {
		t.Errorf("--as must not send the environment's grant, sent %v", v)
	}
}
