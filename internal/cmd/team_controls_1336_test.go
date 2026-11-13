package cmd

import (
	"encoding/json"
	"reflect"
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
		{"repos", []string{"team", "role", "update", "frontend-engineer", "--repo", "owner/repo"}, "UpdateTeamRoleMeta", `{"updateTeamRole":{"role":"frontend-engineer","loc":"roles:frontend-engineer","nodeId":"role1","description":null,"roleAgent":null}}`, "repos", []any{"owner/repo"}},
		{"creator", []string{"team", "task", "list", "--creator", "worker:Jane"}, "AgentTaskListCreator", `{"agentTasks":[]}`, "creator", map[string]any{"worker": "Jane"}},
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
					vars = c.Vars["input"].(map[string]any)
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
