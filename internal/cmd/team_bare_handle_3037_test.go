package cmd

// cli#3037: a bare task handle resolves from the worktree binding for the two READS that did not
// (`team task get`, `team pr readiness`), and `--app` reaches every `team pr` verb's task ref (it was
// ignored). The server's count rule is untouched: with no binding, no --app and several Apps the refusal
// (TASK_HANDLE_QUALIFY) still comes through. Qualifying from the binding adds no reach: a qualified handle
// never reaches past the caller's access to the App it names (cor:agt:050:01).

import (
	"strings"
	"testing"
)

const (
	bound3037AppID = "capp100000000000000000000" // the bindingFixture's appId
	bound3037URN   = "hrn:app:acme.com:dev-team"
	bound3037Hnd   = "hrn:agenttask:acme.com:dev-team:AT0.7"
)

func identity3037(id, urn string) string {
	return `{"data":{"app":{"id":"` + id + `","urn":"` + urn + `","name":"Dev"}}}`
}

// runBound3037 runs a command in a worktree whose binding is written (bound) or absent, against the fake of
// server1831 (every operation without a canned answer is a GraphQL error), recording what was sent.
func runBound3037(t *testing.T, bound bool, overrides map[string]string, argv ...string) (*[]call1831, string, error) {
	t.Helper()
	if bound {
		writeTeamBinding(t)
	} else {
		teamGitDir(t)
	}
	srv, calls := server1831(t, overrides)
	if bound {
		setTeamBindingServer(t, srv.URL)
	}
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"--server", srv.URL}, argv...))
	err := root.Execute()
	return calls, out.String() + f.IOStreams.ErrOut.(*strings.Builder).String(), err
}

func opVars3037(calls *[]call1831, op string) (map[string]any, int) {
	var last map[string]any
	n := 0
	for _, c := range *calls {
		if c.Op == op {
			last, n = c.Vars, n+1
		}
	}
	return last, n
}

const sha3037 = "0123456789abcdef0123456789abcdef01234567"

func readiness3037() string {
	return `{"data":{"pullRequestReadiness":{"ready":false,"head":"` + sha3037 + `","slots":[]}}}`
}

// The two reads, in a bound worktree: the bare handle is qualified from the binding's App (one identity
// lookup, by the binding's App id) and the request carries the qualified handle.
func TestBareHandleReadsQualifyFromTheBinding3037(t *testing.T) {
	for _, tc := range []struct {
		name, op, key string
		argv          []string
		overrides     map[string]string
	}{
		{"task get", "AgentTaskGet", "id", []string{"team", "task", "get", "AT0.7", "--json"}, nil},
		{"pr readiness", "PRReadiness", "taskRef", []string{"team", "pr", "readiness", "AT0.7", "--head", sha3037, "--json"}, map[string]string{"PRReadiness": readiness3037()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ov := map[string]string{"TeamAppIdentity": identity3037(bound3037AppID, bound3037URN)}
			for k, v := range tc.overrides {
				ov[k] = v
			}
			calls, out, err := runBound3037(t, true, ov, tc.argv...)
			vars, n := opVars3037(calls, tc.op)
			if n != 1 || vars[tc.key] != bound3037Hnd {
				t.Fatalf("want one %s carrying %q, got %d with %v (err %v)\n%s", tc.op, bound3037Hnd, n, vars, err, out)
			}
			id, lookups := opVars3037(calls, "TeamAppIdentity")
			if lookups != 1 || id["appRef"] != bound3037AppID {
				t.Errorf("one identity lookup by the binding's App id: %d %v", lookups, id)
			}
		})
	}
}

// Where the binding must NOT qualify: no binding (the server's count rule answers, untouched), a task id, an
// already qualified handle, a binding for another server, and an identity answer that is not the binding's App.
func TestBareHandleBindingDoesNotQualifyElsewhere3037(t *testing.T) {
	refusal := `{"errors":[{"message":"You belong to more than one App.","extensions":{"code":"TASK_HANDLE_QUALIFY"}}]}`
	id := map[string]string{"TeamAppIdentity": identity3037(bound3037AppID, bound3037URN)}
	for _, tc := range []struct {
		name  string
		bound bool
		ref   string
		ov    map[string]string
		want  string
	}{
		{"no binding: bare reaches the server", false, "AT0.7", merge3037(id, map[string]string{"AgentTaskGet": refusal}), "AT0.7"},
		{"a task id is unchanged", true, "01a1c78a26947a15a374469a1ec1eea2", id, "01a1c78a26947a15a374469a1ec1eea2"},
		{"a qualified handle is unchanged", true, "hrn:agenttask:other.com:team:AT0.7", id, "hrn:agenttask:other.com:team:AT0.7"},
		{"the lookup names another App than the binding's", true, "AT0.7", map[string]string{"TeamAppIdentity": identity3037("capp199999999999999999999", "hrn:app:evil.example:other")}, "AT0.7"},
		{"the lookup finds no App", true, "AT0.7", map[string]string{"TeamAppIdentity": `{"data":{"app":null}}`}, "AT0.7"},
		{"the lookup fails", true, "AT0.7", map[string]string{"TeamAppIdentity": `{"errors":[{"message":"boom","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`}, "AT0.7"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls, out, _ := runBound3037(t, tc.bound, tc.ov, "team", "task", "get", tc.ref, "--json")
			vars, n := opVars3037(calls, "AgentTaskGet")
			if n != 1 || vars["id"] != tc.want {
				t.Fatalf("want the ref %q sent unchanged, got %d with %v\n%s", tc.want, n, vars, out)
			}
			if !tc.bound {
				if _, lookups := opVars3037(calls, "TeamAppIdentity"); lookups != 0 {
					t.Errorf("no binding: no App lookup at all, got %d", lookups)
				}
			}
		})
	}
	// the server's refusal still reaches the user when nothing could qualify
	_, out, err := runBound3037(t, false, map[string]string{"AgentTaskGet": refusal}, "team", "task", "get", "AT0.7")
	if err == nil || !strings.Contains(out+err.Error(), "You belong to more than one App.") {
		t.Errorf("the server's TASK_HANDLE_QUALIFY answer is passed through: %v %s", err, out)
	}
}

func merge3037(a, b map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

// A binding for another server is not this server's App: no qualification from it.
func TestBareHandleBindingOfAnotherServerIsIgnored3037(t *testing.T) {
	writeTeamBinding(t)
	srv, calls := server1831(t, map[string]string{"TeamAppIdentity": identity3037(bound3037AppID, bound3037URN)})
	setTeamBindingServer(t, "http://other.invalid")
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"--server", srv.URL, "team", "task", "get", "AT0.7", "--json"})
	_ = root.Execute()
	if vars, n := opVars3037(calls, "AgentTaskGet"); n != 1 || vars["id"] != "AT0.7" {
		t.Fatalf("a binding of another server must not qualify the handle: %d %v", n, vars)
	}
}

// --app reaches every pr verb's task ref (it was ignored), by the same composer as `team task`; --app wins
// over the binding (one source, no lookup); a task naming another App than --app is refused before any request.
func TestPRVerbsHonorAppFlag3037(t *testing.T) {
	const want = "hrn:agenttask:acme.com:dev-team:AT0.7"
	app := []string{"--app", bound3037URN}
	for _, tc := range []struct {
		name, op string
		argv     []string
		reply    string
	}{
		{"schedule", "PRSchedule", []string{"team", "pr", "schedule", "AT0.7", "--slot", "local"}, ""},
		{"freeze", "PRFreeze", []string{"team", "pr", "freeze", "AT0.7", sha3037}, ""},
		{"seat", "PRSeat", []string{"team", "pr", "seat", "AT0.7.1", "--slot", "local", "--head", sha3037}, ""},
		{"verdict", "PRVerdict", []string{"team", "pr", "verdict", "AT0.7.1", "--head", sha3037, "--verdict", "APPROVE"}, ""},
		{"readiness", "PRReadiness", []string{"team", "pr", "readiness", "AT0.7", "--head", sha3037}, readiness3037()},
		{"ready", "PRReady", []string{"team", "pr", "ready", "AT0.7", "--head", sha3037}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ov := map[string]string{}
			if tc.reply != "" {
				ov[tc.op] = tc.reply
			}
			calls, out, _ := runBound3037(t, true, ov, append(tc.argv, app...)...)
			vars, n := opVars3037(calls, tc.op)
			got, _ := vars["taskRef"].(string)
			wantRef := want
			if strings.HasSuffix(tc.argv[3], ".1") {
				wantRef = want + ".1"
			}
			if n != 1 || got != wantRef {
				t.Fatalf("--app must qualify the task ref: want %q, got %d with %v\n%s", wantRef, n, vars, out)
			}
			// the read needs no binding lookup when --app names the App (the writes make their own binding
			// check for the worker session they send, which is not ours to count)
			if _, lookups := opVars3037(calls, "TeamAppIdentity"); tc.op == "PRReadiness" && lookups != 0 {
				t.Errorf("--app is the one source for a read: no binding lookup, got %d", lookups)
			}
		})
	}
	t.Run("a task in another App than --app is refused before any request", func(t *testing.T) {
		calls, out, err := runBound3037(t, true, nil, "team", "pr", "readiness", "hrn:agenttask:other.com:team:AT0.7", "--head", sha3037, "--app", bound3037URN)
		if err == nil || len(*calls) != 0 || !strings.Contains(out+err.Error(), "names a task in another App") || strings.Contains(err.Error(), "other.com") {
			t.Fatalf("want a usage refusal and no request: %v %d %s", err, len(*calls), out)
		}
	})
}

// The pr WRITES keep resolving through the bound worker session: from a bound worktree with no --app the bare
// handle is sent as given and no extra App lookup is made (the lookup is for the two reads only).
func TestPRWritesWithABindingAreUnchanged3037(t *testing.T) {
	calls, out, _ := runBound3037(t, true, map[string]string{"TeamAppIdentity": identity3037(bound3037AppID, bound3037URN)}, "team", "pr", "freeze", "AT0.7", sha3037)
	vars, n := opVars3037(calls, "PRFreeze")
	if n != 1 || vars["taskRef"] != "AT0.7" {
		t.Fatalf("a write sends the bare handle as given: %d %v\n%s", n, vars, out)
	}
	if _, lookups := opVars3037(calls, "TeamAppIdentity"); lookups != 0 {
		t.Errorf("a write makes no App lookup, got %d", lookups)
	}
}
