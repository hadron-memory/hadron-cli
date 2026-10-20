package cmd

import (
	"encoding/json"
	"reflect"
	"testing"
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
				gql, captured := captureGraphQL(t, map[string]string{op: `{"data":{"` + field + `":` + tickerFixture + `}}`})
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
