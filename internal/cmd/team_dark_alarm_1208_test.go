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

func TestDarkSettingsGetAndPresence1208(t *testing.T) {
	for _, jsonMode := range []bool{false, true} {
		args := []string{"team", "delivery-settings", "get"}
		if jsonMode {
			args = append(args, "--json")
		}
		_, out, err := runTeamTask(t, deliveryResponses(), args...)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "1800") {
			t.Fatal(out)
		}
	}
	calls, _, err := runTeamTask(t, deliveryResponses(), "team", "delivery-settings", "set", "--busy-delay-seconds", "700")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range *calls {
		if c.Op == "TeamDeliverySettingsSet" {
			found = true
			input := c.Vars["input"].(map[string]any)
			if _, ok := input["darkAlarmWindowSeconds"]; ok {
				t.Fatalf("omitted field sent %v", input)
			}
		}
	}
	if !found {
		t.Fatal("no mutation")
	}
	for _, value := range []string{"", "1.5", "null", "2147483648"} {
		calls, _, err := runTeamTask(t, deliveryResponses(), "team", "delivery-settings", "set", "--dark-alarm-window-seconds", value)
		if err == nil || exitCodeFor(err) != 2 {
			t.Fatalf("%q: %v", value, err)
		}
		for _, c := range *calls {
			if c.Op == "TeamDeliverySettingsSet" {
				t.Fatal("invalid integer sent")
			}
		}
	}
	r := deliveryResponses()
	r["TeamDeliverySettingsSet"] = `{"errors":[{"message":"window outside server bounds","extensions":{"code":"BAD_USER_INPUT"}}]}`
	calls, _, err = runTeamTask(t, r, "team", "delivery-settings", "set", "--dark-alarm-window-seconds", "299")
	if err == nil || exitCodeFor(err) != 2 || len(callsFor(calls, "TeamDeliverySettingsSet")) != 1 {
		t.Fatalf("server refusal: %v %v", err, calls)
	}
}
func TestDarkAlarmJSONAndReasonVocabulary1208(t *testing.T) {
	for _, reason := range []string{"wake-unacked", "UNTRUSTED-REASON"} {
		r := taskResponses()
		r["AgentTeamEvents"] = `{"data":{"agentTeamEvents":[` + strings.Replace(darkEvent1208(false), "overdue", reason, 1) + `]}}`
		_, out, err := runTeamTask(t, r, "team", "events", "--alarms")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "worker Rufus") || strings.Contains(out, "UNTRUSTED-REASON") || strings.Contains(out, "PRIVATE-EVIDENCE") {
			t.Fatal(out)
		}
		if reason == "wake-unacked" && !strings.Contains(out, reason) {
			t.Fatal(out)
		}
		_, out, err = runTeamTask(t, r, "team", "events", "--alarms", "--json")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "PRIVATE-EVIDENCE") || !strings.Contains(out, reason) {
			t.Fatalf("JSON changed: %s", out)
		}
	}
}
