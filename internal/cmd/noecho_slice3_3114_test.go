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
		{"task handle with app id", nil, []string{"team", "task", "get", "AT1.2", "--app", "01a2112fa4677921b26bd792c3cd0e6e"}, "needs the App's URN", exitcode.Usage},
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
		{"task other app", nil, []string{"team", "task", "get", "hrn:agenttask:acme.com:" + low + ":AT1", "--app", "hrn:app:acme.com:ops"}, "names a task in another App", exitcode.Usage},
		{"host setup workdir", nil, append([]string{"team", "host", "assign", "w1", "--host", "h1", "--family", "claude", "--workdir", "/" + p}, app...), "--workdir is", exitcode.Usage},
		{"role rm", map[string]string{"TeamRoles": teamRolesJSON, "TeamAppIdentity": teamAppIdentityJSON}, append([]string{"team", "role", "rm", p, "--yes"}, app...), "no role", exitcode.NotFound},
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
			if !strings.Contains(seen, tc.says) || !strings.Contains(seen, "not shown") {
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
