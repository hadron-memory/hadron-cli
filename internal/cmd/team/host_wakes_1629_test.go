package team

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/delivery"
)

// hadron-cli#1629 (consumer half of hadron-server#2169, cor:agt:090:04): the
// host poller asks for stop notices, and its read keeps the whole-lane shape
// that declares orca1's lane for STOP_CONSUMER_FRESH_SECONDS (no workerRefs,
// no after on the first page, first: 200).

const stopPage = `{"data":{"dueWakes":{"status":"OK","withheld":0,"dormant":[],
 "wakes":[{"proof":"WAKE-PROOF-S-SECRET","worker":{"id":"wkr1"},
  "deliverAfter":"2026-11-04T10:00:00Z","busyDeadline":"2026-11-04T10:00:00Z","channels":[],
  "reasons":[{"kind":"STOP","ticker":null,"task":null,
   "stop":{"eventId":"ev1","signalId":"sig1","level":"HALT","notice":"STOP","interrupt":true}}]}],
 "pageInfo":{"hasNextPage":false,"endCursor":"c1"}}}}`

func TestThePollerAsksForStopNoticesWithTheDeclaringShape(t *testing.T) {
	s, calls := deliveryFake(t, map[string][]string{"DueWakes": {stopPage}})
	if _, err := s.Wakes(context.Background()); err != nil {
		t.Fatal(err)
	}
	cs := calls()
	if len(cs) != 1 {
		t.Fatalf("calls %+v", cs)
	}
	var accepts []string
	if err := json.Unmarshal(cs[0].vars["accepts"], &accepts); err != nil {
		t.Fatalf("accepts %s: %v", cs[0].vars["accepts"], err)
	}
	want := map[string]bool{"MENTION": true, "TICK": true, "TASK": true, "STOP": true}
	if len(accepts) != len(want) {
		t.Fatalf("accepts %v", accepts)
	}
	for _, k := range accepts {
		if !want[k] {
			t.Fatalf("accepts %v", accepts)
		}
	}
	if string(cs[0].vars["first"]) != "200" {
		t.Fatalf("first %s", cs[0].vars["first"])
	}
	for _, absent := range []string{"workerRefs", "after"} {
		if v, ok := cs[0].vars[absent]; ok && string(v) != "null" {
			t.Fatalf("sends %s=%s: the read would not declare the lane", absent, v)
		}
	}
}

func TestAStopReasonIsMappedAsGiven(t *testing.T) {
	s, _ := deliveryFake(t, map[string][]string{"DueWakes": {stopPage}})
	page, err := s.Wakes(context.Background())
	if err != nil || len(page.Wakes) != 1 {
		t.Fatalf("page %+v err %v", page, err)
	}
	w := page.Wakes[0]
	r := w.Reasons[0]
	if r.Kind != delivery.WakeStop || r.Stop == nil ||
		*r.Stop != (delivery.StopReason{EventID: "ev1", SignalID: "sig1", Level: "HALT", Notice: "STOP", Interrupt: true}) {
		t.Fatalf("reason %+v", r)
	}
	if err := delivery.CheckWake(w); err != nil {
		t.Fatalf("a mapped stop wake is well formed: %v", err)
	}
}
