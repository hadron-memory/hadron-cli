package cmd

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func cadenceEvent1607(t *testing.T, ack bool, reason string, full, subject bool) string {
	t.Helper()
	raw := alarmEventFieldsJSON
	if ack {
		raw = ackedAlarmEventFieldsJSON
	}
	var e map[string]any
	if err := json.Unmarshal([]byte(raw), &e); err != nil {
		t.Fatal(err)
	}
	e["type"] = "alarm.coordinator-cadence"
	e["taskId"] = nil
	payload := map[string]any{"reason": reason, "windowMinutes": 30}
	if full {
		payload["lastAssignedAt"] = "PRIVATE-LAST-ASSIGNED"
		payload["freeWorkerCount"] = 17
	}
	e["payload"] = payload
	if !subject {
		e["subjectWorker"] = nil
	}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCoordinatorCadenceAlarm1607(t *testing.T) {
	for _, tc := range []struct {
		name, reason  string
		full, subject bool
	}{
		{"full", "coordinator-cadence", true, true},
		{"redacted", "coordinator-cadence", false, true},
		{"unknown_reason", "UNTRUSTED-CADENCE-REASON", true, true},
		{"dark_reason_is_not_cadence_reason", "overdue", false, true},
		{"no_subject", "coordinator-cadence", false, false},
	} {
		for _, ack := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "_list", true: "_ack"}[ack], func(t *testing.T) {
				raw := cadenceEvent1607(t, ack, tc.reason, tc.full, tc.subject)
				r := taskResponses()
				r["AgentTeamEvents"] = `{"data":{"agentTeamEvents":[` + raw + `]}}`
				r["AlarmAcknowledge"] = `{"data":{"acknowledgeAlarm":` + raw + `}}`
				args := []string{"team", "events", "--alarms"}
				op := "AgentTeamEvents"
				if ack {
					args = []string{"team", "alarm", "ack", "evt2"}
					op = "AlarmAcknowledge"
				}
				calls, out, err := runTeamTask(t, r, args...)
				if err != nil {
					t.Fatal(err)
				}
				if len(callsFor(calls, op)) != 1 {
					t.Fatalf("one %s expected: %+v", op, *calls)
				}
				if tc.subject && !strings.Contains(out, "coordinator Rufus") {
					t.Fatalf("coordinator missing: %q", out)
				}
				if tc.subject {
					if ack {
						want := "✓ acknowledged alarm evt2 (alarm.coordinator-cadence on coordinator Rufus, at 2026-10-12T03:05:00Z)\n"
						if out != want {
							t.Fatalf("ack must name only the coordinator: %q", out)
						}
					} else {
						lines := strings.Split(strings.TrimSpace(out), "\n")
						fields := strings.Fields(lines[len(lines)-1])
						if len(fields) != 7 || fields[4] != "coordinator" || fields[5] != "Rufus" {
							t.Fatalf("TASK cell must name only the coordinator: %q", out)
						}
					}
				}
				if !tc.subject {
					if ack && strings.Contains(out, " on ") {
						t.Fatalf("invented subject: %q", out)
					}
					if !ack {
						lines := strings.Split(strings.TrimSpace(out), "\n")
						fields := strings.Fields(lines[len(lines)-1])
						if len(fields) != 6 || fields[4] != "—" {
							t.Fatalf("missing subject must stay dash: %q", out)
						}
					}
				}
				for _, bad := range []string{"PRIVATE-LAST-ASSIGNED", "freeWorkerCount", "UNTRUSTED-CADENCE-REASON", "(overdue)", "(coordinator-cadence)", "on task"} {
					if strings.Contains(out, bad) {
						t.Fatalf("payload/task leaked %q: %s", bad, out)
					}
				}
				_, out, err = runTeamTask(t, r, append(args, "--json")...)
				if err != nil {
					t.Fatal(err)
				}
				var got map[string]any
				if ack {
					if err = json.Unmarshal([]byte(out), &got); err != nil {
						t.Fatal(err)
					}
				} else {
					var rows []map[string]any
					if err = json.Unmarshal([]byte(out), &rows); err != nil || len(rows) != 1 {
						t.Fatalf("rows=%v err=%v", rows, err)
					}
					got = rows[0]
				}
				var want map[string]any
				if err = json.Unmarshal([]byte(raw), &want); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got["payload"], want["payload"]) || !reflect.DeepEqual(got["subjectWorker"], want["subjectWorker"]) || got["taskId"] != nil {
					t.Fatalf("JSON metadata changed: %s", out)
				}
				keys := make([]string, 0, len(got))
				for k := range got {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				if strings.Join(keys, ",") != "acknowledgedAt,acknowledgedBy,actor,actorRunId,appId,chain,createdAt,episodeStart,id,payload,severity,subjectWorker,taskId,type,wakes" {
					t.Fatalf("JSON shape changed: %v", keys)
				}
			})
		}
	}
}

func TestCoordinatorCadenceHelp1607(t *testing.T) {
	for _, path := range [][]string{{"team", "events"}, {"team", "alarm"}, {"team", "alarm", "ack"}} {
		f, _ := testFactory(t)
		c, _, err := NewRootCmd(f).Find(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(c.Long, "coordinator-cadence") || !strings.Contains(c.Long, "dark-worker") {
			t.Fatalf("help %v omits alarm kinds: %s", path, c.Long)
		}
	}
}
