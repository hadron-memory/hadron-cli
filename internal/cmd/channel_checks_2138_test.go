package cmd

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

func Test2138TickerLiveCheckReasonsWire(t *testing.T) {
	for _, verb := range []string{"create", "update"} {
		for _, reason := range []string{"live-check", "LIVE_CHECK,read-team-channel,live-check"} {
			t.Run(verb+"/"+reason, func(t *testing.T) {
				op, field := "CreateTicker", "createTicker"
				args := []string{"ticker", "create", "--app", "acme.com:ops", "--name", "checks", "--cadence-seconds", "600"}
				if verb == "update" {
					op, field = "UpdateTicker", "updateTicker"
					args = []string{"ticker", "update", tickerRef}
				}
				gql, captured := captureGraphQL(t, map[string]string{op: `{"data":{"` + field + `":` + tickerFixture + `}}`, "TeamAppIdentity": teamAppIdentityJSON})
				f, _ := testFactory(t)
				root := NewRootCmd(f)
				root.SetArgs(append(args, "--reasons", reason, "--server", gql.URL))
				if err := root.Execute(); err != nil {
					t.Fatal(err)
				}
				var vars struct {
					Input struct {
						Reasons []string `json:"reasons"`
					} `json:"input"`
				}
				if err := json.Unmarshal(captured[op], &vars); err != nil {
					t.Fatal(err)
				}
				want := []string{"LIVE_CHECK"}
				if reason != "live-check" {
					want = []string{"READ_TEAM_CHANNEL", "LIVE_CHECK"}
				}
				if !reflect.DeepEqual(vars.Input.Reasons, want) {
					t.Fatalf("wire reasons %v want %v", vars.Input.Reasons, want)
				}
			})
		}
	}
}

func Test2138CheckSettingsPresence(t *testing.T) {
	r := deliveryResponses()
	r["TeamDeliverySettingsSet"] = `{"data":{"setAppDeliverySettings":{"batchWindowSeconds":120,"busyDelaySeconds":600,"stuckWindowSeconds":900,"standbyEnabled":false,"standbyEffective":false,"checkPeriodSeconds":600,"failedChecksToOverdue":2}}}`
	calls, out, err := runTeamTask(t, r, "team", "delivery-settings", "set", "--check-period-seconds", "600", "--failed-checks-to-overdue", "2", "--json")
	if err != nil {
		t.Fatal(err)
	}
	writes := callsFor(calls, "TeamDeliverySettingsSet")
	if len(writes) != 1 {
		t.Fatalf("writes=%d", len(writes))
	}
	var vars struct {
		Input map[string]any `json:"input"`
	}
	if err := json.Unmarshal(writes[0].RawVars, &vars); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"checkPeriodSeconds": float64(600), "failedChecksToOverdue": float64(2)}
	if !reflect.DeepEqual(vars.Input, want) {
		t.Fatalf("settings patch %v want %v", vars.Input, want)
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatal(err)
	}
	for k, v := range want {
		if dto[k] != v {
			t.Fatalf("readback %s=%v want %v", k, dto[k], v)
		}
	}
	if writes[0].Session != "" {
		t.Fatal("settings write borrowed worker session")
	}
}

func Test2138AgentUsesTasksPresence(t *testing.T) {
	for _, flag := range []string{"--uses-tasks=false", "--uses-tasks=true"} {
		t.Run(flag, func(t *testing.T) {
			gql, captured := captureGraphQL(t, map[string]string{"UpdateAgent": `{"data":{"updateAgent":` + agentJSON + `}}`})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"agent", "update", "agt1", flag, "--server", gql.URL})
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			var vars map[string]any
			if err := json.Unmarshal(captured["UpdateAgent"], &vars); err != nil {
				t.Fatal(err)
			}
			want := flag == "--uses-tasks=true"
			v, ok := vars["usesTasks"]
			if !ok || v != want {
				t.Fatalf("usesTasks presence=%v value=%v want=%v", ok, v, want)
			}
			if len(vars) != 2 {
				t.Fatalf("uses-tasks-only update sent unrelated fields: %v", vars)
			}
		})
	}
}

func Test2138AgentOrdinaryUpdateOmitsUsesTasks(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{"UpdateAgent": `{"data":{"updateAgent":` + agentJSON + `}}`})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "update", "agt1", "--name", "Renamed", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var vars map[string]any
	if err := json.Unmarshal(captured["UpdateAgent"], &vars); err != nil {
		t.Fatal(err)
	}
	if _, ok := vars["usesTasks"]; ok {
		t.Fatalf("ordinary update reset usesTasks: %s", captured["UpdateAgent"])
	}
}

func Test2138WorkerAndRosterPreserveServerCheckStatus(t *testing.T) {
	states := []string{"NO_SESSION", "OK", "OVERDUE", "NO_TASK", "STANDBY", "ISOLATED", "PAUSED", "TEAM_PAUSED", "WITHHELD"}
	for _, state := range states {
		t.Run(state, func(t *testing.T) {
			var check any
			if state != "WITHHELD" {
				status := state
				var pending any
				if state != "NO_SESSION" && state != "OK" && state != "OVERDUE" {
					status = "PENDING"
					pending = state
				}
				check = map[string]any{"status": status, "pendingReason": pending, "lastCheckedAt": nil}
			}
			for _, roster := range []bool{false, true} {
				r := taskResponses()
				args := []string{"team", "worker", "get", "hrn:worker:acme.com:eng-team:iris", "--json"}
				if roster {
					var body map[string]any
					_ = json.Unmarshal([]byte(rosterJSON), &body)
					for _, row := range body["workers"].([]any) {
						row.(map[string]any)["checkStatus"] = check
					}
					raw, _ := json.Marshal(body)
					r["TeamRoster"] = `{"data":{"teamRoster":` + string(raw) + `}}`
					args = []string{"team", "roster", "--json"}
				} else {
					var body map[string]any
					_ = json.Unmarshal([]byte(irisWorkerJSON), &body)
					body["checkStatus"] = check
					raw, _ := json.Marshal(body)
					r["GetWorker"] = `{"data":{"worker":` + string(raw) + `}}`
				}
				_, out, err := runTeamTask(t, r, args...)
				if err != nil {
					t.Fatal(err)
				}
				var got map[string]any
				if err := json.Unmarshal([]byte(out), &got); err != nil {
					t.Fatal(err)
				}
				if roster {
					got = got["workers"].([]any)[0].(map[string]any)
				}
				actual, exists := got["checkStatus"]
				if !exists || !reflect.DeepEqual(actual, check) {
					t.Fatalf("roster=%v check=%v present=%v want=%v", roster, actual, exists, check)
				}
			}
		})
	}
}

func Test2138WorkerCheckTextDistinguishesUnavailable(t *testing.T) {
	for _, tc := range []struct {
		check any
		want  string
		never bool
	}{
		{map[string]any{"status": "PENDING", "pendingReason": "PAUSED", "lastCheckedAt": nil}, "check status: PENDING (PAUSED)", true},
		{map[string]any{"status": "OVERDUE", "pendingReason": nil, "lastCheckedAt": "2026-10-20T00:00:00Z"}, "check status: OVERDUE", false},
		{nil, "check status: —", false},
	} {
		var worker map[string]any
		_ = json.Unmarshal([]byte(irisWorkerJSON), &worker)
		worker["checkStatus"] = tc.check
		raw, _ := json.Marshal(worker)
		responses := taskResponses()
		responses["GetWorker"] = `{"data":{"worker":` + string(raw) + `}}`
		_, out, err := runTeamTask(t, responses, "team", "worker", "get", "hrn:worker:acme.com:eng-team:iris")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, tc.want) {
			t.Fatalf("missing %q: %s", tc.want, out)
		}
		if strings.Contains(out, "last team-channel check: never") != tc.never {
			t.Fatalf("never-checked disclosure: %s", out)
		}
	}
}

func Test2138CheckSettingsNumericRefusalAndServerLimits(t *testing.T) {
	for _, flag := range []string{"--check-period-seconds", "--failed-checks-to-overdue"} {
		for _, value := range []string{"", "1.5", "null", "default", "2147483648"} {
			calls, _, err := runTeamTask(t, deliveryResponses(), "team", "delivery-settings", "set", flag, value)
			if exitCodeFor(err) != 2 || len(*calls) != 0 {
				t.Fatalf("%s %q: err=%v calls=%d", flag, value, err, len(*calls))
			}
		}
		responses := deliveryResponses()
		responses["TeamDeliverySettingsSet"] = `{"errors":[{"message":"The server's current lower bound refuses zero.","extensions":{"code":"DELIVERY_SETTINGS_OUT_OF_RANGE"}}]}`
		calls, _, err := runTeamTask(t, responses, "team", "delivery-settings", "set", flag, "0")
		if exitCodeFor(err) != 2 || !strings.Contains(err.Error(), "current lower bound") || len(callsFor(calls, "TeamDeliverySettingsSet")) != 1 {
			t.Fatalf("server limit not preserved: %v", err)
		}
	}
}

// The reason is sent as an enum VARIABLE, so operation validation alone does
// not catch a missing provider enum member (server#2144 / Elena25935).
func Test2138LiveCheckExistsInProviderSchema(t *testing.T) {
	for _, reason := range gen.AllTickerReason {
		if reason == gen.TickerReason("LIVE_CHECK") {
			return
		}
	}
	t.Fatal("selected server schema lacks TickerReason.LIVE_CHECK; the provider rejects create/update and ticker read-back")
}
