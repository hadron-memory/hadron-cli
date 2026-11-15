package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// hadron-cli#2143 (S8 of #4229, server#4257): the platform starts a long-lived
// worker with no task. The operation says why (issuedReason) and a start that
// keeps failing raises a task-less alarm.start-failing (wake handle START).

func hostOpWithReason2143(reason string) string {
	return strings.Replace(issuedHostOpJSON, `"issuedBy":"USER"`, `"issuedBy":"PLATFORM","issuedReason":`+reason, 1)
}

func TestHostOpsShowsTheIssuedReason2143(t *testing.T) {
	r := hostResponses()
	r["HostOperationsWithReason"] = `{"data":{"hostOperations":{"items":[` + hostOpWithReason2143(`"DESIRED_RUNNING"`) + `,` + hostOpJSON + `],"hasNextPage":false,"endCursor":null}}}`
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

// A server older than #4257 rejects the field it has never heard of. host ops
// then asks again without it (Julius, local P2 on #2145): the list still reads.
func TestHostOpsFallsBackOnAServerWithoutIssuedReason2143(t *testing.T) {
	r := hostResponses()
	r["HostOperationsWithReason"] = `{"errors":[{"message":"Cannot query field \"issuedReason\" on type \"HostOperation\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`
	calls, out, err := runTeamTask(t, r, "team", "host", "ops")
	if err != nil {
		t.Fatalf("an older server must still list: %v", err)
	}
	if !strings.Contains(out, "hop1") || strings.Contains(out, "(") && strings.Contains(out, "WORKER (") {
		t.Fatalf("plain list expected:\n%s", out)
	}
	if len(callsFor(calls, "HostOperationsWithReason")) != 1 || len(callsFor(calls, "HostOperations")) != 1 {
		t.Fatalf("one try with the reason, one without: %+v", *calls)
	}
	_, out, err = runTeamTask(t, r, "team", "host", "ops", "--json")
	if err != nil || strings.Contains(out, "issuedReason") {
		t.Fatalf("no reason key from an older server: %v\n%s", err, out)
	}
}

// Only a rejection of that one field falls back: any other refusal is the answer.
func TestHostOpsDoesNotRetryOtherRefusals2143(t *testing.T) {
	r := hostResponses()
	r["HostOperationsWithReason"] = `{"errors":[{"message":"no.","extensions":{"code":"FORBIDDEN"}}]}`
	calls, _, err := runTeamTask(t, r, "team", "host", "ops")
	if err == nil {
		t.Fatal("a refusal must surface")
	}
	if len(callsFor(calls, "HostOperations")) != 0 {
		t.Fatalf("a refusal other than the unknown field must not be retried: %+v", *calls)
	}
}

// The poller's own operations (collect, report) and issue never select a field
// an older server lacks: a host must keep polling across a server upgrade.
func TestHostPollerOperationsDoNotSelectIssuedReason2143(t *testing.T) {
	for name, q := range map[string]string{
		"HostOperations":        gen.HostOperations_Operation,
		"CollectHostOperations": gen.CollectHostOperations_Operation,
		"ReportHostOperation":   gen.ReportHostOperation_Operation,
		"IssueHostOperation":    gen.IssueHostOperation_Operation,
	} {
		if strings.Contains(q, "issuedReason") {
			t.Errorf("%s selects issuedReason; an older server would reject it", name)
		}
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
