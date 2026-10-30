package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

func darkEvent1208(acked bool) string {
	raw := alarmEventFieldsJSON
	if acked {
		raw = ackedAlarmEventFieldsJSON
	}
	var e map[string]any
	if err := json.Unmarshal([]byte(raw), &e); err != nil {
		panic(err)
	}
	e["taskId"] = nil
	e["type"] = "worker.dark"
	e["payload"] = map[string]any{"reason": "overdue", "lastCheckedAt": "PRIVATE-EVIDENCE"}
	b, _ := json.Marshal(e)
	return string(b)
}
func TestDarkAlarmHuman1208(t *testing.T) {
	for _, ack := range []bool{false, true} {
		r := taskResponses()
		args := []string{"team", "events", "--alarms"}
		r["AgentTeamEvents"] = `{"data":{"agentTeamEvents":[` + darkEvent1208(false) + `]}}`
		if ack {
			args = []string{"team", "alarm", "ack", "evt2"}
			r["AlarmAcknowledge"] = `{"data":{"acknowledgeAlarm":` + darkEvent1208(true) + `}}`
		}
		_, out, err := runTeamTask(t, r, args...)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "worker Rufus") || !strings.Contains(out, "overdue") {
			t.Errorf("worker/reason missing: %q", out)
		}
		if strings.Contains(out, "PRIVATE-EVIDENCE") || strings.Contains(out, "on task") {
			t.Errorf("wrong human output: %q", out)
		}
	}
}
func TestDarkSettings1208(t *testing.T) {
	r := deliveryResponses()
	r["TeamDeliverySettingsSet"] = strings.Replace(r["TeamDeliverySettingsSet"], `"batchWindowSeconds":120`, `"darkAlarmWindowSeconds":1800,"batchWindowSeconds":120`, 1)
	calls, out, err := runTeamTask(t, r, "team", "delivery-settings", "set", "--dark-alarm-window-seconds", "1800", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"darkAlarmWindowSeconds": 1800`) {
		t.Fatalf("missing field %s", out)
	}
	found := false
	for _, c := range *calls {
		if c.Op == "TeamDeliverySettingsSet" {
			found = true
			input := c.Vars["input"].(map[string]any)
			if input["darkAlarmWindowSeconds"] != float64(1800) {
				t.Fatal(c.Vars)
			}
		}
	}
	if !found {
		t.Fatal("no settings mutation")
	}
}
func TestTaskAlarmBaseline1208(t *testing.T) {
	r := taskResponses()
	r["AgentTeamEvents"] = `{"data":{"agentTeamEvents":[` + alarmEventFieldsJSON + `]}}`
	_, out, err := runTeamTask(t, r, "team", "events", "--alarms")
	if err != nil {
		t.Fatal(err)
	}
	if want := "app: hrn:app:acme.com:eng-team — Eng Team (from the worktree binding)\nID    AT                    TYPE               ACTOR  TASK   ACK\nevt2  2026-10-12T03:00:00Z  stall.no-progress  —      task1  open\n"; out != want {
		t.Fatalf("task event changed: %q", out)
	}
	_, out, err = runTeamTask(t, r, "team", "alarm", "ack", "evt2")
	if err != nil {
		t.Fatal(err)
	}
	if want := "✓ acknowledged alarm evt2 (stall.no-progress on task task1, at 2026-10-12T03:05:00Z)\n"; out != want {
		t.Fatalf("task ack changed: %q", out)
	}
}
