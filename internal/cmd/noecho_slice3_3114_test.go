package cmd

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#3114 slice 3 (team): flag values, and values reaching a refusal through a helper or the flag
// parser, which the positional flow guard cannot see. Text and --json, whole output.
func TestSlice3RefusalsNeverShowTheTypedValue3114(t *testing.T) {
	p := pastedUpper3029()
	low := strings.ToLower(p)
	app := []string{"--app", "hrn:app:acme.com:ops"}
	noWorker := map[string]string{
		"GetWorkerInApp":   `{"data":{"worker":null}}`,
		"GetWorkerIdInApp": `{"data":{"worker":null}}`,
		"GetWorker":        `{"data":{"worker":null}}`,
	}
	for _, tc := range []struct {
		name      string
		responses map[string]string
		argv      []string
		says      string
		exit      int
	}{
		{"worker get", noWorker, append([]string{"team", "worker", "get", p}, app...), "no worker", exitcode.NotFound},
		{"worker get urn", noWorker, []string{"team", "worker", "get", "hrn:worker:acme.com:ops:" + low}, "no worker", exitcode.NotFound},
		{"task get", map[string]string{"AgentTaskGet": `{"data":{"agentTask":null}}`}, []string{"team", "task", "get", p}, "task not found", exitcode.NotFound},
		{"task tree", map[string]string{"AgentTaskTree": `{"data":{"agentTaskTree":[]}}`}, []string{"team", "task", "tree", p}, "task not found", exitcode.NotFound},
		{"task handle with app id", nil, []string{"team", "task", "get", "AT1.2", "--app", "01a2112fa4677921b26bd792c3cd0e6e"}, "neither value is shown", exitcode.Usage},
		{"task party", nil, append([]string{"team", "task", "ls", "--assignee", p}, app...), "names a party", exitcode.Usage},
		{"task ref", nil, append([]string{"team", "task", "create", "x", "--ref", p}, app...), "--ref takes", exitcode.Usage},
		{"task ref target", nil, append([]string{"team", "task", "create", "x", "--ref", "issue=|" + p}, app...), "no target", exitcode.Usage},
		{"task ref label", nil, append([]string{"team", "task", "create", "x", "--ref", "issue=" + p + "|"}, app...), "empty label", exitcode.Usage},
		{"task model", nil, append([]string{"team", "task", "create", "x", "--model", p + " x"}, app...), "one word", exitcode.Usage},
		{"host op arg", nil, append([]string{"team", "host", "issue", "worker.start", "--host", "h1", "--worker", "w1", "--arg", p}, app...), "--arg takes key=value", exitcode.Usage},
		{"shed positional", nil, []string{"team", "session", "shed", p}, "takes no arguments", exitcode.Usage},
		{"shed flag", nil, []string{"team", "session", "shed", "--" + p}, "unknown flag", exitcode.Usage},
		{"shed flag value", nil, []string{"team", "session", "shed", "--cooldown", p}, "--cooldown", exitcode.Usage},
		{"outcome flag", nil, []string{"team", "session", "outcome", "s1", "--" + p}, "unknown flag", exitcode.Usage},
		{"outcome negative", nil, []string{"team", "session", "outcome", "s1", "-0.5"}, "reads as a flag", exitcode.Usage},
		{"session ls pr", nil, append([]string{"team", "session", "ls", "--pr", p}, app...), "pr ref", exitcode.Usage},
		{"session ls commit", nil, append([]string{"team", "session", "ls", "--commit", p}, app...), "commit ref", exitcode.Usage},
		{"session ls branch", nil, append([]string{"team", "session", "ls", "--branch", p + ":x:y"}, app...), "branch ref", exitcode.Usage},
		{"wake worker", nil, []string{"team", "wake-settings", "get", "--worker", p}, "worker id", exitcode.Usage},
		{"host arg twice", nil, append([]string{"team", "host", "issue", "worker.start", "--host", "h1", "--worker", "w1", "--arg", p + "=1", "--arg", p + "=2"}, app...), "given twice", exitcode.Usage},
		{"task other app", nil, []string{"team", "task", "get", "hrn:agenttask:acme.com:" + low + ":AT1", "--app", "hrn:app:acme.com:ops"}, "task in another App than --app (neither value is shown)", exitcode.Usage},
		{"host setup workdir", nil, append([]string{"team", "host", "assign", "w1", "--host", "h1", "--family", "claude", "--workdir", "/" + p}, app...), "--workdir is", exitcode.Usage},
		{"role rm", map[string]string{"TeamRoles": teamRolesJSON, "TeamAppIdentity": teamAppIdentityJSON}, append([]string{"team", "role", "rm", p, "--yes"}, app...), "no such role", exitcode.NotFound},
		{"role list name", nil, append([]string{"team", "role-lists", "set", "defect-link", p + ":x"}, app...), "':' or whitespace", exitcode.Usage},
		{"availability name", nil, []string{"team", "availability", "set", "--worker", p, "--state", "available"}, "needs the App", exitcode.Usage},
		{"alarms party", nil, append([]string{"team", "alarms", "--team=user:" + p}, app...), "--team names a worker", exitcode.Usage},
		{"init memory", map[string]string{"GetMemory": `{"data":{"memory":null}}`}, []string{"team", "init", "-m", "acme.com::" + low}, "memory", exitcode.NotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seen, code := renderedNoEcho3029(t, tc.responses, tc.argv...)
			if code != tc.exit {
				t.Errorf("exit = %d, want %d: %s", code, tc.exit, seen)
			}
			if strings.Contains(strings.ToLower(seen), low) {
				t.Errorf("shows the value: %s", seen)
			}
			if !strings.Contains(seen, tc.says) || (!strings.Contains(seen, "not shown") && !strings.Contains(seen, "neither value is shown")) {
				t.Errorf("want %q and that the value is not shown: %s", tc.says, seen)
			}
		})
	}
}

// A typed enum, time or number drops the echo and names the accepted form.
func TestSlice3EnumRefusalsNameTheFormNotTheValue3114(t *testing.T) {
	low := strings.ToLower(pastedUpper3029())
	app := []string{"--app", "hrn:app:acme.com:ops"}
	for _, tc := range []struct {
		argv []string
		form string
	}{
		{[]string{"team", "alarms", "--all", "--status"}, "--status takes"},
		{[]string{"team", "alarms", "--all", "--since"}, "RFC 3339"},
		{[]string{"team", "events", "--since"}, "RFC 3339"},
		{[]string{"team", "metrics", "--since"}, "RFC 3339"},
		{[]string{"team", "metrics", "--until"}, "RFC 3339"},
		{[]string{"team", "review-stats", "--month"}, "YYYY-MM"},
		{[]string{"team", "utilization", "--family", "claude", "--tier"}, "high, mid or low"},
		{[]string{"team", "utilization", "--all", "--window"}, "1h to 168h"},
		{[]string{"team", "availability", "set", "--family", "claude", "--state"}, "available or unavailable"},
		{[]string{"team", "availability", "set", "--family", "claude", "--state", "unavailable", "--reason"}, "HOST_DOWN"},
		{[]string{"team", "stop", "set", "--scope", "team", "--worker", "w1", "--level"}, "pause-dispatch, pause or halt"},
		{[]string{"team", "stop", "set", "--level", "pause", "--scope"}, "worker, team or host"},
		{[]string{"team", "task", "list", "--status"}, "--status takes"},
		{[]string{"team", "task", "list", "--ref", "x", "--ref-kind"}, "use one of"},
		{[]string{"team", "task", "create", "x", "--status"}, "backlog or pending"},
		{[]string{"team", "task", "create", "x", "--start-by"}, "RFC 3339"},
		{[]string{"team", "task", "create", "x", "--finish-by"}, "RFC 3339"},
		{[]string{"team", "task", "create", "x", "--tier"}, "high, mid or low"},
		{[]string{"team", "task", "create", "x", "--effort"}, "LOW, MEDIUM"},
		{[]string{"team", "task", "create", "x", "--match", "--min-tier"}, "low, mid or high"},
		{[]string{"team", "wake-settings", "set", "--worker", "hrn:worker:acme.com:ops:iris", "--mentions"}, "immediate or off"},
		{[]string{"team", "health", "install", "--config", "c", "--interval"}, "duration like"},
		{[]string{"team", "chat", "post", "x", "--reply-to"}, "--reply-to takes"},
		{[]string{"team", "host", "ops", "--status"}, "issued, collected"},
	} {
		t.Run(strings.Join(tc.argv, " "), func(t *testing.T) {
			argv := append(append([]string{}, tc.argv...), low)
			if tc.argv[1] != "wake-settings" && tc.argv[1] != "health" {
				argv = append(argv, app...)
			}
			seen, code := renderedNoEcho3029(t, nil, argv...)
			if code != exitcode.Usage || !strings.Contains(seen, tc.form) {
				t.Errorf("exit %d, want the accepted form %q: %s", code, tc.form, seen)
			}
			if strings.Contains(strings.ToLower(seen), low) {
				t.Errorf("shows the value: %s", seen)
			}
		})
	}
}

// #3173 F4: the unconfirmed-marker message says which way each edge runs, for the router's outgoing edge
// and for the mirrored back-edge (the leaf edge is pinned by #2736's own test).
func TestSlice3PreflightRouteMarkerNamesTheEdgeDirection3114(t *testing.T) {
	const null = `{"data":{"updateEdge":null}}`
	_, err := runExisting2736(t, `{"note":"x"}`, null)
	if err == nil || !strings.Contains(err.Error(), "the route from the router to the target ") {
		t.Errorf("outgoing edge: %v", err)
	}
	gql, _ := queueGraphQL(t, map[string][]string{
		"GetNodeRaw": {existingRouter2736(`{"note":"x"}`), existingRouter2736(`{"note":"x"}`)},
		"GetNode":    {existingTarget2736(`{"note":"x"}`)},
		"ResolveUrn": {`{"data":{"resolveUrn":{"id":"n_target","kind":"node","memoryId":"mem2"}}}`},
		"UpdateEdge": {updated2736(crossMemoryMarker2736), null},
		"UpdateNode": {`{"data":{"updateNode":` + newRouteNodeJSON + `}}`},
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(routeCrossArgs2736(gql.URL, "--cross-memory"))
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "the route from the target to the router ") {
		t.Errorf("back-edge: %v", err)
	}
}

// #3173 F5: a scaffold that fails part-way gives the ids the server returned, top-down; an edge target
// that cannot be resolved is named by its position.
func TestSlice3SpecNewPartialFailures3114(t *testing.T) {
	node := `{"data":{"createSpecNode":{"id":"new1","memoryId":"mem1","loc":"msg","name":"x","nodeType":"info","tags":["spec"],"updatedAt":"2026-06-14T00:00:00Z"}}}`
	gql, _ := queueGraphQL(t, map[string][]string{
		"FindNodes":      {`{"data":{"nodes":[]}}`},
		"CreateSpecNode": {node, `{"errors":[{"message":"write refused"}]}`},
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "new", "msg:010:01", "--new-path", "--title", "Send", "--no-edges", "-m", specMem, "--server", gql.URL})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "after creating its first 1 of ") || !strings.Contains(err.Error(), "ids new1)") || strings.Contains(err.Error(), "msg:010") {
		t.Errorf("partial scaffold: %v", err)
	}

	gql2, captured := queueGraphQL(t, map[string][]string{
		"FindNodes":  {`{"data":{"nodes":[` + specNodeList("msg", `["spec"]`) + `]}}`, `{"data":{"nodes":[]}}`},
		"ResolveUrn": {`{"data":{"resolveUrn":null}}`},
	})
	f2, _ := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"spec", "new", "msg:010:01", "--new-path", "--title", "Send", "-m", specMem, "--server", gql2.URL})
	err := root2.Execute()
	if err == nil || !strings.Contains(err.Error(), "cannot wire edge 1 of node 1 of ") || len(captured["CreateSpecNode"]) != 0 {
		t.Errorf("unresolvable edge: %v", err)
	}
}

// Sites no flag pin reaches: the live and held refusals of a bind, the lost defect link, and the server URL.
func TestSlice3HelperOnlyRefusals3114(t *testing.T) {
	teamGitDir(t)
	sess := `{"data":{"sessionsPage":{"total":1,"hasMore":false,"items":[` + activeSessionJSON + `]}}}`
	live := map[string]string{"GetWorkerInApp": `{"data":{"worker":` + withLiveness(heldBy("u-dara"), "true") + `}}`, "TeamSessionsPage": sess, "AuthContext": authContextHolgerJSON, "GetUser": heldHolderUserJSON("u-dara", "Dara", "dara")}
	run := func(responses map[string]string, argv ...string) string {
		gql, _ := captureGraphQL(t, responses)
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append(argv, "--server", gql.URL))
		err := root.Execute()
		if err == nil {
			t.Fatalf("%v: want a refusal", argv)
		}
		return err.Error()
	}
	if got := run(live, "team", "session", "start", "--as", "Iris", "--app", "hrn:app:acme.com:eng-team"); strings.Contains(got, "name Iris") || !strings.Contains(got, "worker release wkr1") {
		t.Errorf("held: %s", got)
	}
	if got := run(map[string]string{"TaskLinkDefect": `{"data":{"linkDefectToPullRequest":null}}`}, "team", "task", "link-defect", "AT1.2", "--defect", defect2189); strings.Contains(got, "AT1.2") {
		t.Errorf("link-defect: %s", got)
	}
}

// A --server URL can hold userinfo, so a binding on another server never prints it.
func TestSlice3ServerMismatchHidesTheServerGiven3114(t *testing.T) {
	writeTeamBinding(t)
	setTeamBindingServer(t, "https://bound.example.com")
	gql, _ := captureGraphQL(t, nil)
	host := strings.TrimPrefix(gql.URL, "http://")
	for _, argv := range [][]string{
		{"team", "session", "end"},
		{"team", "task", "mine"},
		{"team", "worker", "update", "Iris", "--title", "x", "--as-worker"},
	} {
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append(argv, "--server", "http://u:"+pastedUpper3029()+"@"+host))
		err := root.Execute()
		if err == nil || strings.Contains(err.Error(), "AbCdEf") || strings.Contains(err.Error(), host) {
			t.Errorf("%v: %v", argv, err)
		}
	}
}

// host up names neither the host nor the worker typed.
func TestSlice3HostUpHidesTheHostAndWorker3114(t *testing.T) {
	fx := newHostUpFixture1389(t)
	fx.registry = []string{"wkr-iris"}
	fx.irisRunningJonasMissing()
	if _, _, err := fx.run("--apply", "--worker", "Nobody"); err == nil || strings.Contains(err.Error(), "Nobody") || strings.Contains(err.Error(), "orca1") || !strings.Contains(err.Error(), "neither is shown") {
		t.Errorf("--worker: %v", err)
	}
}
