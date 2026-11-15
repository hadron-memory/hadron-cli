package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// hadron-cli#2143 (S8 of #4229, server#4257): the platform starts a long-lived
// worker with no task. The operation says why (issuedReason) and a start that
// keeps failing raises a task-less alarm.start-failing (wake handle START).

func hostOpWithReason2143(reason string) string {
	return strings.Replace(issuedHostOpJSON, `"issuedBy":"USER"`, `"issuedBy":"PLATFORM","issuedReason":`+reason, 1)
}

func TestHostOpsShowsTheIssuedReason2143(t *testing.T) {
	r := hostResponses()
	r["HostOperations"] = `{"data":{"hostOperations":{"items":[` + hostOpWithReason2143(`"DESIRED_RUNNING"`) + `,` + hostOpJSON + `],"hasNextPage":false,"endCursor":null}}}`
	_, out, err := runTeamTask(t, r, "team", "host", "ops")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "PLATFORM (DESIRED_RUNNING)") {
		t.Fatalf("the reason must show beside PLATFORM:\n%s", out)
	}
	var plain string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "hop1") {
			plain = l
		}
	}
	if plain == "" || strings.Contains(plain, "(") && strings.Contains(plain, "WORKER (") {
		t.Fatalf("an operation without a reason is unchanged:\n%s", out)
	}

	_, out, err = runTeamTask(t, r, "team", "host", "ops", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &page); err != nil || len(page.Items) != 2 {
		t.Fatalf("%v %s", err, out)
	}
	if page.Items[0]["issuedReason"] != "DESIRED_RUNNING" {
		t.Fatalf("issuedReason missing from --json: %v", page.Items[0])
	}
	if _, has := page.Items[1]["issuedReason"]; has {
		t.Fatalf("an operation without a reason keeps its old shape: %v", page.Items[1])
	}
}

// An older server has no issuedReason field: the response simply lacks it.
func TestHostOpsToleratesAServerWithoutIssuedReason2143(t *testing.T) {
	_, out, err := runTeamTask(t, hostResponses(), "team", "host", "ops")
	if err != nil || strings.Contains(out, "(") && strings.Contains(out, "WORKER (") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func startFailingEvent2143(t *testing.T, ack bool, subject bool) string {
	t.Helper()
	raw := alarmEventFieldsJSON
	if ack {
		raw = ackedAlarmEventFieldsJSON
	}
	var e map[string]any
	if err := json.Unmarshal([]byte(raw), &e); err != nil {
		t.Fatal(err)
	}
	e["type"] = "alarm.start-failing"
	e["taskId"] = nil
	e["payload"] = map[string]any{"failures": 3, "windowMinutes": 60}
	if !subject {
		e["subjectWorker"] = nil
	}
	b, _ := json.Marshal(e)
	return string(b)
}

func TestStartFailingAlarmNamesTheWorkerWithNoTask2143(t *testing.T) {
	for _, ack := range []bool{false, true} {
		raw := startFailingEvent2143(t, ack, true)
		r := taskResponses()
		r["AgentTeamEventsPage"] = `{"data":{"agentTeamEventsPage":{"events":[` + raw + `],"hasMore":false,"nextCursor":null}}}`
		r["AlarmAcknowledge"] = `{"data":{"acknowledgeAlarm":` + raw + `}}`
		args := []string{"team", "events", "--alarms"}
		if ack {
			args = []string{"team", "alarm", "ack", "evt2"}
		}
		_, out, err := runTeamTask(t, r, args...)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "worker Rufus") || strings.Contains(out, "on task") || strings.Contains(out, "(3)") {
			t.Fatalf("ack=%v: want the worker and no task:\n%s", ack, out)
		}
		if ack && !strings.Contains(out, "(alarm.start-failing on worker Rufus, at ") {
			t.Fatalf("ack receipt: %q", out)
		}
		_, out, err = runTeamTask(t, r, append(args, "--json")...)
		if err != nil || !strings.Contains(out, `"alarm.start-failing"`) || !strings.Contains(out, `"failures": 3`) {
			t.Fatalf("--json must keep the payload as returned: %v\n%s", err, out)
		}
	}
}

func TestStartFailingAlarmWithNoSubjectStaysADash2143(t *testing.T) {
	raw := startFailingEvent2143(t, false, false)
	r := taskResponses()
	r["AgentTeamEventsPage"] = `{"data":{"agentTeamEventsPage":{"events":[` + raw + `],"hasMore":false,"nextCursor":null}}}`
	_, out, err := runTeamTask(t, r, "team", "events", "--alarms")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if f := strings.Fields(lines[len(lines)-1]); len(f) != 6 || f[4] != "—" {
		t.Fatalf("a missing subject stays a dash: %q", out)
	}
}
