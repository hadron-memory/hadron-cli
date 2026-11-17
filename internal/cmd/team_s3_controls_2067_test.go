package cmd

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func TestAgentLifecycle2067(t *testing.T) {
	for _, value := range []string{"LONG_LIVED", "SHORT_LIVED"} {
		t.Run(value, func(t *testing.T) {
			body := strings.Replace(agentJSON, `"usesTasks":true,`, `"usesTasks":true,"lifecycle":"`+value+`",`, 1)
			calls, out, err := runTeamTask(t, map[string]string{"UpdateAgentWithLifecycle": `{"data":{"updateAgent":` + body + `}}`}, "agent", "update", "agt1", "--lifecycle", value, "--json")
			if err != nil {
				t.Fatal(err)
			}
			updates := callsFor(calls, "UpdateAgentWithLifecycle")
			if len(updates) != 1 || updates[0].Vars["lifecycle"] != value || len(updates[0].Vars) != 2 {
				t.Fatalf("updates: %+v", updates)
			}
			if !strings.Contains(out, `"lifecycle": "`+value+`"`) {
				t.Fatal(out)
			}
			calls, out, err = runTeamTask(t, map[string]string{"GetAgent": `{"data":{"agent":` + body + `}}`, "GetAgentLifecycle": `{"data":{"agent":` + body + `}}`}, "agent", "get", "agt1", "--json")
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
				if _, ok := writes[0].Vars["instruction"]; ok {
					t.Fatalf("unclaimed write sent instruction: %v", writes[0].Vars)
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
			if _, ok := ops[0].Vars["instruction"]; ok {
				t.Fatal(ops[0].Vars)
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
			op = "UpdateAgentWithLifecycle"
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
		{"team", "worker", "leave-alone", "Iris", "--reason", "why"},
		{"team", "worker", "leave-alone", "Iris", "--instruction", "task:x"},
		{"team", "worker", "leave-alone", " ", "--on"}, {"team", "worker", "leave-alone", "Iris", "--on", "--off"},
		{"team", "worker", "leave-alone", "Iris", "--on=false"}, {"team", "worker", "leave-alone", "Iris", "--off=false"},
		{"team", "worker", "leave-alone", "Iris", "--on=false", "--off"},
		{"team", "worker", "leave-alone", "Iris", "--off=false", "--on"},
		{"team", "app", "sweep-mode", "OFF", "--instruction", "bogus:x"},
		{"team", "worker", "leave-alone", "Iris", "--off", "--reason", "why"}, {"team", "worker", "leave-alone", "Iris", "--on", "--instruction", "bogus:x"},
		{"team", "app", "sweep-mode", "INVALID"}, {"team", "app", "sweep-mode", "--instruction", "task:x"},
	} {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			calls, _, err := runTeamTask(t, map[string]string{}, argv...)
			if err == nil || exitCodeFor(err) != exitcode.Usage || len(*calls) != 0 {
				t.Fatalf("%v calls=%+v", err, *calls)
			}
		})
	}
}

func TestS3Instruction2067(t *testing.T) {
	for _, tc := range []struct {
		op, body string
		argv     []string
	}{
		{"SetWorkerLeaveAlone", `{"data":{"setWorkerLeaveAlone":{"workerId":"wkr1","leaveAlone":true,"reason":null,"by":"u1","at":"2026-11-15T00:00:00Z"}}}`, []string{"team", "worker", "leave-alone", irisURN1592, "--on"}},
		{"SetAppSweepMode", `{"data":{"setAppSweepMode":{"sweepMode":"APPLY"}}}`, []string{"team", "app", "sweep-mode", "APPLY", "--app", app1594}},
	} {
		for _, bound := range []bool{true, false} {
			t.Run(tc.op+fmt.Sprint(bound), func(t *testing.T) {
				argv := append(append([]string{}, tc.argv...), "--instruction", "live-chat:client:conversation:42", "--json")
				calls, r := (run1592{bound: bound, response: map[string]string{tc.op: tc.body}}).exec(t, argv...)
				if r.err != nil {
					t.Fatalf("%v %s", r.err, r.errOut)
				}
				ops := callsFor(calls, tc.op)
				if len(ops) != 1 {
					t.Fatal(*calls)
				}
				wantSession := ""
				if bound {
					wantSession = "s-new"
				}
				if ops[0].Session != wantSession {
					t.Fatalf("session %q want %q", ops[0].Session, wantSession)
				}
				want := map[string]any{"kind": "LIVE_CHAT", "ref": "client:conversation:42"}
				if !reflect.DeepEqual(ops[0].Vars["instruction"], want) {
					t.Fatal(ops[0].Vars)
				}
				for _, c := range *calls {
					if c.Op != tc.op && c.Session != "" {
						t.Fatalf("attributed lookup %+v", c)
					}
				}
				// A refused claim is one attempt, never retried without the instruction.
				calls, r = (run1592{bound: bound, response: map[string]string{tc.op: gqlErrorJSON("FORBIDDEN")}}).exec(t, argv...)
				if exitCodeFor(r.err) != exitcode.Forbidden || len(callsFor(calls, tc.op)) != 1 {
					t.Fatalf("%v %+v", r.err, *calls)
				}
			})
		}
	}
}

func TestS3WorkerMarkerReadBack2067(t *testing.T) {
	for _, marked := range []bool{true, false} {
		var reason, by, at any
		if marked {
			reason = "maintenance"
			by = "u1"
			at = "today"
		}
		raw, err := json.Marshal(map[string]any{"data": map[string]any{"worker": map[string]any{"id": "wkr1", "leaveAlone": marked, "leaveAloneReason": reason, "leaveAloneBy": by, "leaveAloneAt": at}}})
		if err != nil {
			t.Fatal(err)
		}
		calls, out, err := runTeamTask(t, map[string]string{"GetWorkerLeaveAlone": string(raw)}, "team", "worker", "leave-alone", "Iris", "--json")
		if err != nil {
			t.Fatal(err)
		}
		var dto map[string]any
		if err := json.Unmarshal([]byte(out), &dto); err != nil {
			t.Fatal(err)
		}
		if dto["leaveAlone"] != marked || dto["workerId"] != "wkr1" || len(dto) != 5 {
			t.Fatal(out)
		}
		for _, key := range []string{"reason", "by", "at"} {
			v, ok := dto[key]
			if !ok || (marked && v == nil) || (!marked && v != nil) {
				t.Fatalf("%s: %s", key, out)
			}
		}
		reads := callsFor(calls, "GetWorkerLeaveAlone")
		if len(reads) != 1 || reads[0].Session != "" {
			t.Fatalf("reads %+v", *calls)
		}
	}
}

func TestS3HumanReceipts2067(t *testing.T) {
	for _, tc := range []struct {
		body, op, want string
		argv           []string
	}{
		{`{"data":{"setWorkerLeaveAlone":{"workerId":"w1","leaveAlone":true,"reason":"maintenance","by":"u1","at":"today"}}}`, "SetWorkerLeaveAlone", "leave alone: true", []string{"team", "worker", "leave-alone", "Iris", "--on"}},
		{`{"data":{"app":{"sweepMode":"OBSERVE"}}}`, "GetAppSweepMode", "sweep mode: OBSERVE", []string{"team", "app", "sweep-mode"}},
		{`{"data":{"agent":` + strings.Replace(agentJSON, `"usesTasks":true,`, `"usesTasks":true,"lifecycle":"SHORT_LIVED",`, 1) + `}}`, "GetAgent", "lifecycle: SHORT_LIVED", []string{"agent", "get", "agt1"}},
	} {
		_, out, err := runTeamTask(t, map[string]string{tc.op: tc.body, "GetAgentLifecycle": tc.body, "TeamAppIdentity": teamAppIdentityJSON}, tc.argv...)
		if err != nil || !strings.Contains(out, tc.want) {
			t.Fatalf("%v %s", err, out)
		}
	}
}

func TestS3BindingAndDirectRef2067(t *testing.T) {
	for _, door := range []struct {
		op, reply string
		argv      []string
	}{
		{"SetWorkerLeaveAlone", `{"data":{"setWorkerLeaveAlone":{"workerId":"wkr1","leaveAlone":true,"reason":null,"by":null,"at":null}}}`, []string{"team", "worker", "leave-alone", irisURN1592, "--on", "--app", otherApp1594}},
		{"SetAppSweepMode", `{"data":{"setAppSweepMode":{"sweepMode":"OFF"}}}`, []string{"team", "app", "sweep-mode", "OFF", "--app", app1594}},
	} {
		for _, foreign := range []bool{true, false} {
			t.Run(door.op+fmt.Sprint(foreign), func(t *testing.T) {
				o := run1592{bound: true, response: map[string]string{door.op: door.reply}}
				if foreign {
					o.mutate = func(b map[string]any) { b["server"] = "https://foreign.invalid" }
				}
				calls, r := o.exec(t, append(door.argv, "--instruction", "task:one", "--json")...)
				if r.err != nil {
					t.Fatalf("%v %s", r.err, r.errOut)
				}
				ops := callsFor(calls, door.op)
				if len(ops) != 1 {
					t.Fatal(*calls)
				}
				want := "s-new"
				if foreign {
					want = ""
				}
				if ops[0].Session != want {
					t.Fatalf("session=%q want=%q", ops[0].Session, want)
				}
				if door.op == "SetWorkerLeaveAlone" {
					input := ops[0].Vars["input"].(map[string]any)
					if _, ok := input["appRef"]; ok {
						t.Fatalf("direct reference inherited App: %v", input)
					}
				}
			})
		}
	}
}

func TestS3MissingResults2067(t *testing.T) {
	for _, door := range []struct {
		op, field string
		argv      []string
	}{
		{"SetWorkerLeaveAlone", "setWorkerLeaveAlone", []string{"team", "worker", "leave-alone", "Iris", "--on"}},
		{"SetAppSweepMode", "setAppSweepMode", []string{"team", "app", "sweep-mode", "OFF"}},
		{"GetAppSweepMode", "app", []string{"team", "app", "sweep-mode"}},
	} {
		_, out, err := runTeamTask(t, map[string]string{door.op: `{"data":{"` + door.field + `":null}}`}, door.argv...)
		if err == nil || out != "" {
			t.Fatalf("%v %s", err, out)
		}
	}
}

func TestS3Selections2067(t *testing.T) {
	hasField := func(op, field string) bool {
		for _, line := range strings.Split(op, "\n") {
			if strings.TrimSpace(line) == field {
				return true
			}
		}
		return false
	}
	for _, op := range []string{gen.GetAgentLifecycle_Operation, gen.CreateAgentWithLifecycle_Operation, gen.UpdateAgentWithLifecycle_Operation} {
		if !hasField(op, "lifecycle") {
			t.Fatal("agent operation drops lifecycle")
		}
	}
	for _, op := range []string{gen.GetWorkerLeaveAlone_Operation} {
		for _, field := range []string{"leaveAlone", "leaveAloneReason", "leaveAloneBy", "leaveAloneAt"} {
			if !hasField(op, field) {
				t.Fatalf("worker operation drops %s", field)
			}
		}
	}
}

func TestS3MarkerSanitization2067(t *testing.T) {
	attack := "maintenance\nForged: yes\x1b[31m"
	result := map[string]any{"workerId": "w1", "leaveAlone": true, "reason": attack, "by": attack, "at": attack}
	raw, _ := json.Marshal(map[string]any{"data": map[string]any{"setWorkerLeaveAlone": result}})
	for _, asJSON := range []bool{true, false} {
		argv := []string{"team", "worker", "leave-alone", irisURN1592, "--on"}
		if asJSON {
			argv = append(argv, "--json")
		}
		_, out, err := runTeamTask(t, map[string]string{"SetWorkerLeaveAlone": string(raw)}, argv...)
		if err != nil {
			t.Fatal(err)
		}
		if asJSON {
			var dto map[string]any
			if json.Unmarshal([]byte(out), &dto) != nil || dto["reason"] != attack || dto["by"] != attack || dto["at"] != attack {
				t.Fatal(out)
			}
		} else {
			if strings.Contains(out, "\nForged:") || strings.Contains(out, "\x1b") {
				t.Fatal(out)
			}
			assertDetailLineCount(t, out, 5)
		}
	}
}

func TestS3AgentCreateLifecycle2067(t *testing.T) {
	for _, value := range []string{"LONG_LIVED", "SHORT_LIVED", ""} {
		body := strings.Replace(agentJSON, `"usesTasks":true,`, `"usesTasks":true,"lifecycle":"SHORT_LIVED",`, 1)
		argv := []string{"agent", "create", "--name", "Engineer", "--json"}
		if value != "" {
			argv = append(argv, "--lifecycle", value)
		}
		op := "CreateAgent"
		if value != "" {
			op = "CreateAgentWithLifecycle"
		}
		calls, out, err := runTeamTask(t, map[string]string{op: `{"data":{"createAgent":` + body + `}}`}, argv...)
		if err != nil {
			t.Fatal(err)
		}
		ops := callsFor(calls, op)
		if len(ops) != 1 || len(*calls) != 1 {
			t.Fatal(*calls)
		}
		got, present := ops[0].Vars["lifecycle"]
		if present != (value != "") || present && got != value {
			t.Fatalf("lifecycle %v %v", got, present)
		}
		if value != "" && !strings.Contains(out, `"lifecycle": "SHORT_LIVED"`) {
			t.Fatal(out)
		}
	}
	for _, value := range []string{"", "short_lived", "OTHER"} {
		calls, _, err := runTeamTask(t, map[string]string{}, "agent", "create", "--name", "Engineer", "--lifecycle", value)
		if exitCodeFor(err) != exitcode.Usage || len(*calls) != 0 {
			t.Fatalf("%v %+v", err, *calls)
		}
	}
	calls, out, err := runTeamTask(t, map[string]string{"CreateAgentWithLifecycle": gqlErrorJSON("FORBIDDEN")}, "agent", "create", "--name", "Engineer", "--lifecycle", "SHORT_LIVED")
	if exitCodeFor(err) != exitcode.Forbidden || len(*calls) != 1 || out != "" {
		t.Fatalf("%v %s %+v", err, out, *calls)
	}
}

// A profile-only reader receives null working-state fields, not an unset marker.
func TestS3WorkerMarkerHidden2067(t *testing.T) {
	body := `{"data":{"worker":{"id":"wkr1","leaveAlone":null,"leaveAloneReason":null,"leaveAloneBy":null,"leaveAloneAt":null}}}`
	for _, jsonOutput := range []bool{false, true} {
		argv := []string{"team", "worker", "leave-alone", irisURN1592}
		if jsonOutput {
			argv = append(argv, "--json")
		}
		calls, out, err := runTeamTask(t, map[string]string{"GetWorkerLeaveAlone": body}, argv...)
		if err != nil {
			t.Fatal(err)
		}
		if jsonOutput {
			var dto map[string]any
			if err := json.Unmarshal([]byte(out), &dto); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"leaveAlone", "reason", "by", "at"} {
				v, present := dto[key]
				if !present || v != nil {
					t.Fatalf("hidden field %s must remain null: %s", key, out)
				}
			}
		} else if !strings.Contains(out, "leave alone: unknown (not visible to you)") {
			t.Fatalf("hidden marker misrepresented: %s", out)
		}
		if len(callsFor(calls, "GetWorkerLeaveAlone")) != 1 || len(callsFor(calls, "SetWorkerLeaveAlone")) != 0 {
			t.Fatalf("read must not write: %+v", *calls)
		}
	}
}
