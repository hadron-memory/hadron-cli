package cmd

import (
	"encoding/json"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"reflect"
	"strings"
	"testing"
)

func TestTeamControls1336ReachEveryGap(t *testing.T) {
	cases := []struct {
		name          string
		args          []string
		op, body, key string
		want          any
	}{
		{"grant add", []string{"team", "grant", "add", "worker-id", "WORKERS_STAFF"}, "TeamAppGrantAdd", `{"grantApp":{"id":"g1","name":"WORKERS_STAFF","grantedAt":"2026-11-13","revokedAt":null,"app":{"id":"app1"},"worker":{"id":"w1"}}}`, "workerRef", "worker-id"},
		{"grant rm", []string{"team", "grant", "rm", "worker-id", "WORKERS_STAFF", "--yes"}, "TeamAppGrantRm", `{"revokeAppGrant":{"id":"g1","name":"WORKERS_STAFF","grantedAt":"2026-11-13","revokedAt":"2026-11-14","app":{"id":"app1"},"worker":{"id":"w1"}}}`, "workerRef", "worker-id"},
		{"ticker", []string{"team", "ticker", "settings", "set", "--live-cap", "101", "--clear-wake-daily-budget=false"}, "TeamTickerSettingsSet", `{"setAppTickerSettings":{"minCadenceSeconds":60,"liveCap":10,"selfSubscribeCadenceSeconds":120,"dailyWakeBudget":100,"dailyWakeBudgetDerived":true}}`, "liveCap", float64(101)},
		{"held", []string{"team", "worker", "held", "--user", "missing-user"}, "TeamHeldWorkers", `{"heldWorkers":{"items":[],"total":0}}`, "userRef", "missing-user"},
		{"pick", []string{"team", "register", "pick", "task.finish"}, "TeamRegisterPick", `{"registerRolePick":{"appId":"app1","code":"task.finish","entries":[]}}`, "code", "task.finish"},
		{"candidates", []string{"team", "register", "candidates", "task.finish"}, "TeamRegisterCandidates", `{"registerRoleCandidates":{"appId":"app1","code":"task.finish","depth":null,"taskLoc":null,"candidates":[],"total":0}}`, "code", "task.finish"},
		{"repos", []string{"team", "role", "update", "frontend-engineer", "--repo", "owner/repo"}, "UpdateTeamRoleRepos", `{"updateTeamRole":{"role":"frontend-engineer","loc":"roles:frontend-engineer","nodeId":"role1","description":null,"roleAgent":null}}`, "repos", []any{"owner/repo"}},
		{"creator", []string{"team", "task", "list", "--creator", "worker:Jane"}, "AgentTaskListCreator", `{"agentTasks":[]}`, "creator", map[string]any{"worker": "Jane", "user": nil}},
		{"attendee", []string{"team", "register", "attendee", "--agent", "agent-id"}, "TeamAttendeeRegister", `{"attendeeRegister":[]}`, "agentRef", "agent-id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := taskResponses()
			r[tc.op] = `{"data":` + tc.body + `}`
			r["TeamRoles"] = teamRolesRmJSON
			calls, out, err := runTeamTask(t, r, append(tc.args, "--json")...)
			if err != nil {
				t.Fatal(err)
			}
			var dto any
			if err = json.Unmarshal([]byte(out), &dto); err != nil {
				t.Fatal(err, out)
			}
			seen := 0
			for _, c := range *calls {
				if c.Op != tc.op {
					continue
				}
				seen++
				vars := c.Vars
				if tc.name == "ticker" {
					vars = c.Vars
					if _, ok := vars["minCadenceSeconds"]; ok {
						t.Fatal("omitted ticker field sent")
					}
					if vars["clearWakeDailyBudget"] != false {
						t.Fatal(vars)
					}
				}
				if !reflect.DeepEqual(vars[tc.key], tc.want) {
					t.Fatalf("%s=%#v want %#v", tc.key, vars[tc.key], tc.want)
				}
				if c.Session != "" && (tc.name == "grant add" || tc.name == "grant rm" || tc.name == "ticker") {
					t.Fatal("manager mutation attributed as worker")
				}
			}
			if seen != 1 {
				t.Fatalf("%s calls=%d", tc.op, seen)
			}
		})
	}
}

func TestTeamControls1336RefuseBeforeRequests(t *testing.T) {
	for _, args := range [][]string{
		{"team", "grant", "rm", "worker-id", "APP_MEMBERS"},
		{"team", "grant", "add", "worker-id", "UNKNOWN"},
		{"team", "ticker", "settings", "set", "--live-cap", "null"},
		{"team", "role", "update", "frontend-engineer", "--repo", "a/b", "--clear-repos"},
		{"team", "task", "list", "--creator", "nobody"},
		{"team", "task", "list", "--creator", "worker:Jane", "--team"},
		{"team", "register", "attendee", "--agent", "a", "--worker", "w"},
		{"team", "worker", "held", "--user", ""},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			calls, _, err := runTeamTask(t, taskResponses(), args...)
			if exitCodeFor(err) != exitcode.Usage {
				t.Fatalf("err %v", err)
			}
			if len(*calls) != 0 {
				t.Fatalf("requests %v", *calls)
			}
		})
	}
}

func TestTeamControls1336TickerPatch(t *testing.T) {
	const body = `{"data":{"setAppTickerSettings":{"minCadenceSeconds":60,"liveCap":10,"selfSubscribeCadenceSeconds":120,"dailyWakeBudget":0,"dailyWakeBudgetDerived":false}}}`
	for _, tc := range []struct {
		args []string
		want map[string]any
	}{
		{[]string{"--wake-daily-budget", "0"}, map[string]any{"wakeDailyBudget": float64(0)}},
		{[]string{"--clear-wake-daily-budget=false"}, map[string]any{"clearWakeDailyBudget": false}},
		{[]string{"--clear-wake-daily-budget"}, map[string]any{"clearWakeDailyBudget": true}},
		{[]string{"--min-cadence-seconds", "1", "--live-cap", "101"}, map[string]any{"minCadenceSeconds": float64(1), "liveCap": float64(101)}},
	} {
		r := taskResponses()
		r["TeamTickerSettingsSet"] = body
		calls, _, e := runTeamTask(t, r, append([]string{"team", "ticker", "settings", "set", "--json"}, tc.args...)...)
		if e != nil {
			t.Fatal(e)
		}
		for _, c := range *calls {
			if c.Op != "TeamTickerSettingsSet" {
				continue
			}
			delete(c.Vars, "appRef")
			if !reflect.DeepEqual(c.Vars, tc.want) {
				t.Fatalf("vars %#v want %#v", c.Vars, tc.want)
			}
		}
	}
}

func TestTeamControls1336RefusalMapping(t *testing.T) {
	for _, tc := range []struct {
		args     []string
		op, code string
		exit     int
	}{
		{[]string{"team", "grant", "add", "w", "APP_MEMBERS"}, "TeamAppGrantAdd", "FORBIDDEN", 8},
		{[]string{"team", "grant", "rm", "w", "APP_MEMBERS", "--yes"}, "TeamAppGrantRm", "WORKER_NOT_FOUND", 4},
		{[]string{"team", "ticker", "settings", "set", "--live-cap", "101"}, "TeamTickerSettingsSet", "TICKER_SETTINGS_OUT_OF_RANGE", 2},
		{[]string{"team", "ticker", "settings", "set", "--min-cadence-seconds", "120"}, "TeamTickerSettingsSet", "TICKER_SETTINGS_CONFLICT", 5},
		{[]string{"team", "ticker", "settings", "set", "--min-cadence-seconds", "120"}, "TeamTickerSettingsSet", "DELIVERY_SETTINGS_CONFLICT", 2},
	} {
		r := taskResponses()
		r[tc.op] = `{"errors":[{"message":"refused","extensions":{"code":"` + tc.code + `"}}]}`
		calls, out, e := runTeamTask(t, r, tc.args...)
		if exitCodeFor(e) != tc.exit {
			t.Fatalf("%s: %v", tc.code, e)
		}
		n := 0
		for _, c := range *calls {
			if c.Op == tc.op {
				n++
			}
		}
		if n != 1 || strings.Contains(out, "App grant ") || strings.Contains(out, "ticker settings:") {
			t.Fatalf("calls %d out %q", n, out)
		}
	}
}

func TestTeamControls1336ClearReposAndCreatorCombined(t *testing.T) {
	r := taskResponses()
	r["TeamRoles"] = teamRolesRmJSON
	r["UpdateTeamRoleRepos"] = `{"data":{"updateTeamRole":{"role":"frontend-engineer","loc":"roles:frontend-engineer","nodeId":"role1","repos":[],"roleAgent":null}}}`
	calls, _, e := runTeamTask(t, r, "team", "role", "update", "frontend-engineer", "--clear-repos", "--json")
	if e != nil {
		t.Fatal(e)
	}
	for _, c := range *calls {
		if c.Op == "UpdateTeamRoleRepos" {
			if !reflect.DeepEqual(c.Vars["repos"], []any{}) {
				t.Fatal(c.Vars)
			}
			if _, ok := c.Vars["description"]; ok {
				t.Fatal("omitted description sent")
			}
		}
	}
	r = taskResponses()
	r["AgentTaskListCreator"] = `{"data":{"agentTasks":[]}}`
	calls, _, e = runTeamTask(t, r, "team", "task", "list", "--creator", "user:alice", "--text", "fix", "--unresolved=false", "--status", "STARTED", "--json")
	if e != nil {
		t.Fatal(e)
	}
	for _, c := range *calls {
		if c.Op == "AgentTaskListCreator" {
			if c.Vars["text"] != "fix" || c.Vars["unresolvedFindings"] != false {
				t.Fatal(c.Vars)
			}
		}
	}
}

func TestTeamControls1336ServerOrderAndNeutralEmpty(t *testing.T) {
	r := taskResponses()
	r["TeamRegisterPick"] = `{"data":{"registerRolePick":{"appId":"a","code":"c","entries":[{"id":"z","kind":"WORKER"},{"id":"a","kind":"USER"}]}}}`
	_, out, e := runTeamTask(t, r, "team", "register", "pick", "c", "--json")
	if e != nil {
		t.Fatal(e)
	}
	var d struct {
		Entries []struct {
			ID string `json:"id"`
		}
	}
	if e = json.Unmarshal([]byte(out), &d); e != nil {
		t.Fatal(e)
	}
	if len(d.Entries) != 2 || d.Entries[0].ID != "z" || d.Entries[1].ID != "a" {
		t.Fatal(out)
	}
	r = taskResponses()
	r["TeamHeldWorkers"] = `{"data":{"heldWorkers":{"items":[],"total":0}}}`
	_, out, e = runTeamTask(t, r, "team", "worker", "held", "--user", "missing")
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(strings.ToLower(out), "not found") || !strings.Contains(out, "No held workers returned") {
		t.Fatal(out)
	}
}

func TestTeamControls1336EmptyDTOArraysAndNoDecoration(t *testing.T) {
	for _, tc := range []struct {
		args               []string
		op, body, arrayKey string
	}{
		{[]string{"team", "worker", "held", "--user", "u"}, "TeamHeldWorkers", `{"heldWorkers":{"items":[],"total":0}}`, "items"},
		{[]string{"team", "register", "pick", "c"}, "TeamRegisterPick", `{"registerRolePick":{"appId":"a","code":"c","entries":[]}}`, "entries"},
		{[]string{"team", "register", "candidates", "c"}, "TeamRegisterCandidates", `{"registerRoleCandidates":{"appId":"a","code":"c","depth":null,"taskLoc":null,"total":0,"candidates":[]}}`, "candidates"},
		{[]string{"team", "register", "attendee", "--agent", "a"}, "TeamAttendeeRegister", `{"attendeeRegister":[]}`, ""},
	} {
		r := taskResponses()
		r[tc.op] = `{"data":` + tc.body + `}`
		calls, out, e := runTeamTask(t, r, append(tc.args, "--json")...)
		if e != nil {
			t.Fatal(e)
		}
		if tc.arrayKey == "" {
			if strings.TrimSpace(out) != "[]" {
				t.Fatal(out)
			}
		} else {
			var v map[string]json.RawMessage
			if e = json.Unmarshal([]byte(out), &v); e != nil {
				t.Fatal(e)
			}
			if string(v[tc.arrayKey]) != "[]" {
				t.Fatalf("empty shape %s", out)
			}
		}
		for _, c := range *calls {
			if c.Op == "TeamAppIdentity" {
				t.Fatal("JSON decorated with App lookup")
			}
		}
	}
}

func TestTeamControls1336HeldCountOnlyHuman(t *testing.T) {
	r := taskResponses()
	r["TeamHeldWorkers"] = `{"data":{"heldWorkers":{"items":[],"total":37}}}`
	_, out, e := runTeamTask(t, r, "team", "worker", "held", "--user", "u", "--limit", "0")
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(out, "total: 37") {
		t.Fatalf("count missing: %s", out)
	}
}
