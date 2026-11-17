package hostpoller

import (
	"github.com/hadron-memory/hadron-cli/internal/hostrelaunch"
	"testing"
)

func TestLogRelaunchPropagatesForeignCount2197(t *testing.T) {
	p, _, _ := testPoller(t)
	var events []Event
	p.emit = func(e Event) { events = append(events, e) }
	p.logRelaunch(hostrelaunch.Target{WorkerID: "worker", SessionID: "session"}, hostrelaunch.Result{ForeignUIDSkipped: 13, Status: "skipped", Reason: "RELAUNCH_AGENT_PRESENT"})
	if len(events) != 1 {
		t.Fatalf("events=%d", len(events))
	}
	if events[0].ForeignUIDSkipped != 13 {
		t.Fatalf("count lost from receipt: %+v", events[0])
	}
	if events[0].Kind != "relaunch" || events[0].WorkerID != "worker" {
		t.Fatalf("wrong receipt: %+v", events[0])
	}
}
