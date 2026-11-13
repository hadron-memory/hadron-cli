package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// runTeamTaskTTY is runTeamTask with an answerable terminal: answers is what a
// person would type, one line per prompt.
func runTeamTaskTTY(t *testing.T, responses map[string]string, answers string, argv ...string) (*[]attnCall, string, string, error) {
	t.Helper()
	writeTeamBinding(t)
	srv, calls := attnServer(t, responses)
	setTeamBindingServer(t, srv.URL)
	f, out, errOut := testFactoryTTY(t, answers)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"--server", srv.URL}, argv...))
	err := root.Execute()
	return calls, out.String(), errOut.String(), err
}

func opCalls1336(calls *[]attnCall, op string) int {
	n := 0
	for _, c := range *calls {
		if c.Op == op {
			n++
		}
	}
	return n
}

// BLIND P2 (cli#1980): an update that names nothing is refused rather than
// sent, as `team role update` already does. A bare `ticker settings set` used to
// send a manager-attributed write with every field omitted.
func TestTeamControls1336FieldlessTickerSetRefusedBeforeRequest(t *testing.T) {
	for _, argv := range [][]string{
		{"team", "ticker", "settings", "set"},
		{"team", "ticker", "settings", "set", "--json"},
	} {
		calls, _, err := runTeamTask(t, taskResponses(), argv...)
		if exitCodeFor(err) != exitcode.Usage {
			t.Fatalf("%v: err %v, want a usage refusal", argv, err)
		}
		if len(*calls) != 0 {
			t.Fatalf("%v: requests %v", argv, *calls)
		}
	}
}

// LOCAL P3-3 / §10 P3-1: the interactive revoke prompt was unpinned; deleting it
// left every 1336 test green.
func TestTeamControls1336GrantRmPromptDeclineAndAccept(t *testing.T) {
	rm := `{"data":{"revokeAppGrant":{"id":"g1","name":"WORKERS_STAFF","grantedAt":"2026-11-13","revokedAt":"2026-11-14","app":{"id":"app1"},"worker":{"id":"w1"}}}}`
	r := taskResponses()
	r["TeamAppGrantRm"] = rm
	calls, _, errOut, err := runTeamTaskTTY(t, r, "n\n", "team", "grant", "rm", "worker-id", "WORKERS_STAFF")
	if exitCodeFor(err) != exitcode.Cancelled {
		t.Fatalf("declined revoke must exit Cancelled, got %v", err)
	}
	if opCalls1336(calls, "TeamAppGrantRm") != 0 {
		t.Fatal("the revoke ran after the prompt was declined")
	}
	if !strings.Contains(errOut, "WORKERS_STAFF") || !strings.Contains(errOut, "worker-id") {
		t.Fatalf("prompt must name the grant and the worker: %q", errOut)
	}
	r = taskResponses()
	r["TeamAppGrantRm"] = rm
	calls, out, _, err := runTeamTaskTTY(t, r, "y\n", "team", "grant", "rm", "worker-id", "WORKERS_STAFF")
	if err != nil {
		t.Fatal(err)
	}
	if opCalls1336(calls, "TeamAppGrantRm") != 1 {
		t.Fatal("an accepted revoke must send exactly one mutation")
	}
	if !strings.Contains(out, "WORKERS_STAFF") || !strings.Contains(out, "revoked 2026-11-14") {
		t.Fatalf("text output: %q", out)
	}
}

// §10 P3-2: a blank worker reference is refused before any request.
func TestTeamControls1336BlankWorkerRefRefused(t *testing.T) {
	for _, argv := range [][]string{
		{"team", "grant", "add", "  ", "WORKERS_STAFF"},
		{"team", "grant", "rm", "  ", "WORKERS_STAFF", "--yes"},
	} {
		calls, _, err := runTeamTask(t, taskResponses(), argv...)
		if exitCodeFor(err) != exitcode.Usage || len(*calls) != 0 {
			t.Fatalf("%v: err %v, calls %v", argv, err, *calls)
		}
	}
}

// BLIND P3-2: the grant name is validated against the generated closed list.
func TestTeamControls1336GrantNameFromGeneratedList(t *testing.T) {
	for _, name := range []string{"APP_MEMBERS", "WORKERS_STAFF", "TICKER_SUBSCRIBE_WORKERS"} {
		r := taskResponses()
		r["TeamAppGrantAdd"] = `{"data":{"grantApp":{"id":"g1","name":"` + name + `","grantedAt":"2026-11-13","revokedAt":null,"app":{"id":"app1"},"worker":{"id":"w1"}}}}`
		if _, _, err := runTeamTask(t, r, "team", "grant", "add", "w", name); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if _, _, err := runTeamTask(t, taskResponses(), "team", "grant", "add", "w", "workers_staff"); exitCodeFor(err) != exitcode.Usage {
		t.Fatalf("a name differing only in case is not on the closed list: %v", err)
	}
}

// BLIND P3-3: only a literal null gets the null explanation.
func TestTeamControls1336TickerNumberMessage(t *testing.T) {
	_, _, err := runTeamTask(t, taskResponses(), "team", "ticker", "settings", "set", "--live-cap", "abc")
	if exitCodeFor(err) != exitcode.Usage || err == nil || strings.Contains(err.Error(), "not null") {
		t.Fatalf("a non-number is not a null: %v", err)
	}
	_, _, err = runTeamTask(t, taskResponses(), "team", "ticker", "settings", "set", "--live-cap", "null")
	if exitCodeFor(err) != exitcode.Usage || err == nil || !strings.Contains(err.Error(), "null") {
		t.Fatalf("null keeps its own refusal: %v", err)
	}
}

// BLIND P3-4 / LOCAL P3-1: --clear-repos=false means "do not clear".
func TestTeamControls1336ClearReposFalse(t *testing.T) {
	r := taskResponses()
	r["TeamRoles"] = teamRolesRmJSON
	r["UpdateTeamRoleRepos"] = `{"data":{"updateTeamRole":{"role":"frontend-engineer","loc":"roles:frontend-engineer","nodeId":"role1","repos":["a/b"],"roleAgent":null}}}`
	calls, _, err := runTeamTask(t, r, "team", "role", "update", "frontend-engineer", "--repo", "a/b", "--clear-repos=false", "--json")
	if err != nil {
		t.Fatalf("--clear-repos=false next to --repo is not a conflict: %v", err)
	}
	for _, c := range *calls {
		if c.Op == "UpdateTeamRoleRepos" && !strings.Contains(strings.ReplaceAll(toJSON1336(c.Vars["repos"]), " ", ""), `"a/b"`) {
			t.Fatalf("repos %#v", c.Vars["repos"])
		}
	}
	calls, _, err = runTeamTask(t, taskResponses(), "team", "role", "update", "frontend-engineer", "--clear-repos=false")
	if exitCodeFor(err) != exitcode.Usage || err == nil || !strings.Contains(err.Error(), "nothing to update") || len(*calls) != 0 {
		t.Fatalf("false alone names nothing: %v", err)
	}
}

// BLIND P3-6: the held-workers table names the App, not its opaque id.
func TestTeamControls1336HeldTableShowsAppName(t *testing.T) {
	r := taskResponses()
	r["TeamHeldWorkers"] = `{"data":{"heldWorkers":{"total":1,"items":[{"id":"w1","urn":"hrn:worker:acme.com:eng:iris","name":"Iris","appId":"app-opaque-id","appUrn":"hrn:app:acme.com:eng","appName":"Engineering","role":"engineer","heldAt":"2026-11-13","retiredAt":null,"hasLiveSession":false}]}}}`
	_, out, err := runTeamTask(t, r, "team", "worker", "held", "--user", "u")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Engineering") || strings.Contains(out, "app-opaque-id") {
		t.Fatalf("APP column: %q", out)
	}
}

// BLIND P3-8/9 and LOCAL P3-4: --include-retired is sent only when given, and
// the int32 bound on limit/offset is pinned.
func TestTeamControls1336HeldIncludeRetiredAndBounds(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want any
		sent bool
	}{
		{nil, nil, false},
		{[]string{"--include-retired"}, true, true},
		{[]string{"--include-retired=false"}, false, true},
	} {
		r := taskResponses()
		r["TeamHeldWorkers"] = `{"data":{"heldWorkers":{"items":[],"total":0}}}`
		calls, _, err := runTeamTask(t, r, append([]string{"team", "worker", "held", "--user", "u", "--json"}, tc.args...)...)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range *calls {
			if c.Op != "TeamHeldWorkers" {
				continue
			}
			v, present := c.Vars["includeRetired"]
			if present != tc.sent || (tc.sent && v != tc.want) {
				t.Fatalf("%v: includeRetired present=%t value=%v", tc.args, present, v)
			}
		}
	}
	for _, args := range [][]string{{"--limit", "2147483648"}, {"--offset", "2147483648"}, {"--limit", "-1"}} {
		calls, _, err := runTeamTask(t, taskResponses(), append([]string{"team", "worker", "held", "--user", "u"}, args...)...)
		if exitCodeFor(err) != exitcode.Usage || len(*calls) != 0 {
			t.Fatalf("%v: err %v calls %v", args, err, *calls)
		}
	}
}

// BLIND P3-8: text-mode output of the new write commands.
func TestTeamControls1336TextOutputOfWrites(t *testing.T) {
	r := taskResponses()
	r["TeamAppGrantAdd"] = `{"data":{"grantApp":{"id":"g1","name":"APP_MEMBERS","grantedAt":"2026-11-13","revokedAt":null,"app":{"id":"app1"},"worker":{"id":"w1"}}}}`
	r["TeamTickerSettingsSet"] = `{"data":{"setAppTickerSettings":{"minCadenceSeconds":60,"liveCap":10,"selfSubscribeCadenceSeconds":120,"dailyWakeBudget":100,"dailyWakeBudgetDerived":true}}}`
	_, out, err := runTeamTask(t, r, "team", "grant", "add", "w1", "APP_MEMBERS")
	if err != nil || !strings.Contains(out, "App grant g1: APP_MEMBERS for worker w1 in App app1; granted 2026-11-13; revoked —") {
		t.Fatalf("grant add text: %q err %v", out, err)
	}
	_, out, err = runTeamTask(t, r, "team", "ticker", "settings", "set", "--live-cap", "10")
	if err != nil || !strings.Contains(out, "ticker settings: minimum cadence 60s; live cap 10; self-subscribe cadence 120s; daily wake budget 100 (derived: true)") {
		t.Fatalf("ticker set text: %q err %v", out, err)
	}
}

func toJSON1336(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
