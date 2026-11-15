package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func TestAgentLifecycle2067(t *testing.T) {
	for _, value := range []string{"LONG_LIVED", "SHORT_LIVED"} {
		t.Run(value, func(t *testing.T) {
			body := strings.Replace(agentJSON, `"usesTasks":true,`, `"usesTasks":true,"lifecycle":"`+value+`",`, 1)
			calls, out, err := runTeamTask(t, map[string]string{"UpdateAgent": `{"data":{"updateAgent":` + body + `}}`}, "agent", "update", "agt1", "--lifecycle", value, "--json")
			if err != nil {
				t.Fatal(err)
			}
			updates := callsFor(calls, "UpdateAgent")
			if len(updates) != 1 || updates[0].Vars["lifecycle"] != value || len(updates[0].Vars) != 2 {
				t.Fatalf("updates: %+v", updates)
			}
			if !strings.Contains(out, `"lifecycle": "`+value+`"`) {
				t.Fatal(out)
			}
			calls, out, err = runTeamTask(t, map[string]string{"GetAgent": `{"data":{"agent":` + body + `}}`}, "agent", "get", "agt1", "--json")
			if err != nil || !strings.Contains(out, `"lifecycle": "`+value+`"`) {
				t.Fatalf("%v %s", err, out)
			}
			for _, c := range *calls {
				if c.Session != "" {
					t.Fatalf("read attributed: %+v", c)
				}
			}
		})
	}
	vars := runAgentUpdateCapture(t, "--name", "Renamed")
	if _, ok := vars["lifecycle"]; ok {
		t.Fatalf("omitted lifecycle sent: %v", vars)
	}
}

func TestS3ControlsWire2067(t *testing.T) {
	for _, mark := range []string{"on", "off"} {
		t.Run(mark, func(t *testing.T) {
			on := mark == "on"
			result := `{"workerId":"w1","leaveAlone":false,"reason":null,"by":null,"at":null}`
			if on {
				result = `{"workerId":"w1","leaveAlone":true,"reason":"maintenance","by":"u1","at":"2026-11-15T00:00:00Z"}`
			}
			argv := []string{"team", "worker", "leave-alone", "Iris", "--" + mark, "--json"}
			if on {
				argv = append(argv, "--reason", "maintenance")
			}
			for range 2 {
				calls, out, err := runTeamTask(t, map[string]string{"SetWorkerLeaveAlone": `{"data":{"setWorkerLeaveAlone":` + result + `}}`}, argv...)
				if err != nil {
					t.Fatal(err)
				}
				writes := callsFor(calls, "SetWorkerLeaveAlone")
				if len(writes) != 1 {
					t.Fatalf("writes: %+v", *calls)
				}
				input := writes[0].Vars["input"].(map[string]any)
				if input["worker"] != "Iris" || input["leaveAlone"] != on || input["appRef"] != "capp100000000000000000000" {
					t.Fatalf("input %v", input)
				}
				if on && input["reason"] != "maintenance" {
					t.Fatal(input)
				}
				if !on {
					if _, ok := input["reason"]; ok {
						t.Fatal(input)
					}
				}
				if writes[0].Session != "" {
					t.Fatal("unclaimed write attributed")
				}
				var dto map[string]any
				if json.Unmarshal([]byte(out), &dto) != nil || dto["leaveAlone"] != on || len(dto) != 5 {
					t.Fatal(out)
				}
			}
		})
	}
	for _, mode := range []string{"", "OFF", "OBSERVE", "APPLY"} {
		t.Run("mode="+mode, func(t *testing.T) {
			op := "GetAppSweepMode"
			reply := `{"data":{"app":{"sweepMode":"OFF"}}}`
			argv := []string{"team", "app", "sweep-mode", "--json"}
			if mode != "" {
				op = "SetAppSweepMode"
				reply = `{"data":{"setAppSweepMode":{"sweepMode":"` + mode + `"}}}`
				argv = append(argv, mode)
			}
			calls, out, err := runTeamTask(t, map[string]string{op: reply}, argv...)
			if err != nil {
				t.Fatal(err)
			}
			ops := callsFor(calls, op)
			if len(ops) != 1 {
				t.Fatal(*calls)
			}
			if ops[0].Vars["appRef"] != "capp100000000000000000000" || ops[0].Session != "" {
				t.Fatal(ops)
			}
			if mode != "" && ops[0].Vars["mode"] != mode {
				t.Fatal(ops)
			}
			var dto map[string]any
			if json.Unmarshal([]byte(out), &dto) != nil || len(dto) != 2 || dto["appRef"] == nil || dto["sweepMode"] == nil {
				t.Fatal(out)
			}
		})
	}
}

func TestS3Refusals2067(t *testing.T) {
	for code, want := range map[string]int{"WORKER_LEAVE_ALONE_NOT_PERMITTED": exitcode.Forbidden, "WORKER_NOT_FOUND": exitcode.NotFound, "WORKER_RETIRED": exitcode.Conflict, "BAD_USER_INPUT": exitcode.Usage} {
		t.Run(code, func(t *testing.T) {
			calls, out, err := runTeamTask(t, map[string]string{"SetWorkerLeaveAlone": `{"errors":[{"message":"refused","extensions":{"code":"` + code + `"}}]}`}, "team", "worker", "leave-alone", "Iris", "--on", "--json")
			if exitCodeFor(err) != want || out != "" || len(callsFor(calls, "SetWorkerLeaveAlone")) != 1 {
				t.Fatalf("%v %q %+v", err, out, *calls)
			}
		})
	}
	for _, argv := range [][]string{{"team", "app", "sweep-mode", "APPLY"}, {"agent", "update", "agt1", "--lifecycle", "SHORT_LIVED"}} {
		op := "SetAppSweepMode"
		if argv[0] == "agent" {
			op = "UpdateAgent"
		}
		calls, out, err := runTeamTask(t, map[string]string{op: `{"errors":[{"message":"refused","extensions":{"code":"FORBIDDEN"}}]}`}, argv...)
		if exitCodeFor(err) != exitcode.Forbidden || out != "" || len(callsFor(calls, op)) != 1 {
			t.Fatalf("%v %q %+v", err, out, *calls)
		}
	}
}

func TestS3UsageGuards2067(t *testing.T) {
	for _, argv := range [][]string{
		{"agent", "update", "agt1", "--lifecycle", ""}, {"agent", "update", "agt1", "--lifecycle", "short_lived"},
		{"team", "worker", "leave-alone", "Iris"}, {"team", "worker", "leave-alone", "Iris", "--on", "--off"},
		{"team", "worker", "leave-alone", "Iris", "--on=false"}, {"team", "worker", "leave-alone", "Iris", "--off=false"},
		{"team", "worker", "leave-alone", "Iris", "--off", "--reason", "why"}, {"team", "worker", "leave-alone", "Iris", "--on", "--instruction", "bogus:x"},
		{"team", "app", "sweep-mode", "INVALID"}, {"team", "app", "sweep-mode", "--instruction", "task:x"},
	} {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			calls, _, err := runTeamTask(t, map[string]string{}, argv...)
			if err == nil || (!isUsageError(err) && exitCodeFor(err) != exitcode.Usage) || len(*calls) != 0 {
				t.Fatalf("%v calls=%+v", err, *calls)
			}
		})
	}
}
