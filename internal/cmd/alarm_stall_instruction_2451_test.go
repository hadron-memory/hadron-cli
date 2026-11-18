package cmd

import (
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"reflect"
	"strings"
	"testing"
)

func alarmStallCases2451() []taskInstructionCase2422 {
	return []taskInstructionCase2422{
		{"ack", []string{"team", "alarm", "ack", "alarm1"}, "AlarmAcknowledge", "AlarmAcknowledgeInstructed", `{"data":{"acknowledgeAlarm":` + ackedAlarmEventFieldsJSON + `}}`},
		{"set", []string{"team", "stall-schedule", "set", "--assignment-minutes", "30"}, "TeamStallScheduleSet", "TeamStallScheduleSetInstructed", `{"data":{"setAgentTaskStallSchedule":` + stallScheduleJSON + `}}`},
	}
}
func TestAlarmStallInstruction2451(t *testing.T) {
	for _, c := range alarmStallCases2451() {
		t.Run(c.name, func(t *testing.T) {
			r := stallResponses()
			r[c.plainOp] = c.response
			r[c.instrOp] = c.response
			calls, _, err := runTeamTask(t, r, c.args...)
			if err != nil {
				t.Fatal(err)
			}
			plain := oneCall2422(t, calls, c.plainOp)
			wantPlainSession := ""
			if c.name == "ack" {
				wantPlainSession = "s-new"
			}
			if plain.Session != wantPlainSession || strings.Contains(plain.Query, "instruction") {
				t.Fatalf("plain changed: %+v", plain)
			}
			args := append(append([]string{}, c.args...), "--instruction", "team-chat-message:"+taskInstructionRef2422)
			calls, _, err = runTeamTask(t, r, args...)
			if err != nil {
				t.Fatal(err)
			}
			got := oneCall2422(t, calls, c.instrOp)
			if got.Session != "s-new" || !strings.Contains(got.Query, "instruction: $instruction") {
				t.Fatalf("instruction/session missing: %+v", got)
			}
			want := map[string]any{"kind": "TEAM_CHAT_MESSAGE", "ref": taskInstructionRef2422}
			if !reflect.DeepEqual(got.Vars["instruction"], want) {
				t.Fatalf("claim=%v", got.Vars)
			}
			delete(got.Vars, "instruction")
			if !reflect.DeepEqual(got.Vars, plain.Vars) {
				t.Fatalf("other variables changed: %v vs %v", got.Vars, plain.Vars)
			}
			for _, call := range *calls {
				if call.Op != c.instrOp {
					if call.Session != "" || call.Vars["instruction"] != nil {
						t.Fatalf("lookup attributed: %+v", call)
					}
				}
			}
		})
	}
}
func TestAlarmStallInstructionBadFlag2451(t *testing.T) {
	for _, c := range alarmStallCases2451() {
		for _, raw := range []string{"", "wrong:ref", "task: "} {
			args := append(append([]string{}, c.args...), "--instruction", raw)
			calls, _, err := runTeamTask(t, stallResponses(), args...)
			if exitCodeFor(err) != exitcode.Usage || len(*calls) != 0 {
				t.Fatalf("%s %q: %v calls=%v", c.name, raw, err, *calls)
			}
		}
	}
}
func TestAlarmStallInstructionRefusal2451(t *testing.T) {
	for _, c := range alarmStallCases2451() {
		t.Run(c.name, func(t *testing.T) {
			r := stallResponses()
			r[c.instrOp] = `{"errors":[{"message":"refused once","extensions":{"code":"FORBIDDEN"}}]}`
			calls, out, err := runTeamTask(t, r, append(append([]string{}, c.args...), "--instruction", "live-chat:opaque")...)
			if err == nil || !strings.Contains(err.Error(), "refused once") || out != "" {
				t.Fatalf("err=%v out=%q", err, out)
			}
			oneCall2422(t, calls, c.instrOp)
			for _, call := range *calls {
				if call.Op == c.plainOp {
					t.Fatal("retried plain")
				}
			}
		})
	}
}
