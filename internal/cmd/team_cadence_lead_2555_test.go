package cmd

import (
	"strings"
	"testing"
)

// cli#2555 (server#4375 companion): the cadence alarm is emitted as
// alarm.lead-cadence, and events raised before the rename still say
// alarm.coordinator-cadence. Both render the same way: the subject in the TASK
// cell (list) and on the ack receipt, no payload text.
func TestCadenceAlarmBothTypesRender2555(t *testing.T) {
	for _, typ := range []string{"alarm.lead-cadence", "alarm.coordinator-cadence"} {
		raw := strings.Replace(cadenceEvent1607(t, false, "coordinator-cadence", true, true), "alarm.coordinator-cadence", typ, 1)
		r := taskResponses()
		r["AgentTeamEventsPage"] = `{"data":{"agentTeamEventsPage":{"events":[` + raw + `],"hasMore":false,"nextCursor":null}}}`
		r["AlarmAcknowledge"] = `{"data":{"acknowledgeAlarm":` + raw + `}}`
		t.Run(typ+" list", func(t *testing.T) {
			_, out, err := runTeamTask(t, r, "team", "events", "--alarms")
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(out), "\n")
			fields := strings.Fields(lines[len(lines)-1])
			if len(fields) != 7 || fields[4] != "coordinator" || fields[5] != "Rufus" {
				t.Fatalf("TASK cell must name the subject: %q", out)
			}
			for _, bad := range []string{"PRIVATE-LAST-ASSIGNED", "freeWorkerCount"} {
				if strings.Contains(out, bad) {
					t.Fatalf("payload leaked %q: %s", bad, out)
				}
			}
		})
		t.Run(typ+" ack", func(t *testing.T) {
			_, out, err := runTeamTask(t, r, "team", "alarm", "ack", "evt2")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, "("+typ+" on coordinator Rufus") {
				t.Fatalf("ack must name the subject: %q", out)
			}
		})
	}
}

func TestCadenceAlarmHelpNamesBothStrings2555(t *testing.T) {
	for _, path := range [][]string{{"team", "events"}, {"team", "alarm"}, {"team", "alarm", "ack"}} {
		f, _ := testFactory(t)
		c, _, err := NewRootCmd(f).Find(path)
		if err != nil {
			t.Fatal(err)
		}
		long := strings.Join(strings.Fields(c.Long), " ")
		for _, w := range []string{"lead-cadence", "alarm.coordinator-cadence"} {
			if !strings.Contains(long, w) {
				t.Errorf("help %v lacks %q", path, w)
			}
		}
		if !strings.Contains(long, "before the rename") {
			t.Errorf("help %v does not say the legacy string appears on earlier events", path)
		}
	}
}
