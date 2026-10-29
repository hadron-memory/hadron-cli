package team

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/delivery"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
)

// hadron-cli#1178 item 0: the host poller reads this host's due tick and
// task wakes (dueWakes) and hands proofs back (acknowledgeWakes), with the
// host key. Before this, nothing in production read dueWakes, so
// delivery.enabled delivered chat but no wakes, and ticker-wake could not
// retire.

const wakePage1 = `{"data":{"dueWakes":{"status":"OK","withheld":0,
 "dormant":[{"mentions":2,"worker":{"id":"wkr9"}}],
 "wakes":[{"proof":"WAKE-PROOF-1-SECRET","worker":{"id":"wkr1"},
  "deliverAfter":"2026-10-19T11:58:00Z","busyDeadline":"2026-10-19T12:08:00Z",
  "channels":[{"channel":{"id":"ch1"}}],
  "reasons":[
   {"kind":"TICK","ticker":{"id":"tk1","name":"Base tick","lastTickAt":"2026-10-19T11:55:00Z"},"task":null},
   {"kind":"TASK","ticker":null,"task":{"eventId":"ev1","chainIndex":0,"event":"ASSIGNED","id":"t1","title":"Fix it"}},
   {"kind":"MENTION","ticker":null,"task":null}]}],
 "pageInfo":{"hasNextPage":true,"endCursor":"c1"}}}}`

const wakePage2 = `{"data":{"dueWakes":{"status":"OK","withheld":3,"dormant":[],
 "wakes":[{"proof":"WAKE-PROOF-2-SECRET","worker":{"id":"wkr2"},
  "deliverAfter":"2026-10-19T11:59:00Z","busyDeadline":"2026-10-19T12:09:00Z","channels":[],
  "reasons":[{"kind":"TASK","ticker":null,"task":{"eventId":"ev2","chainIndex":3,"event":"CHILD_FINISHED","id":"t2","title":"Child"}}]}],
 "pageInfo":{"hasNextPage":false,"endCursor":"c2"}}}}`

func TestWakesPageToExhaustionWithTheHostKeyAndMapReasons(t *testing.T) {
	s, calls := deliveryFake(t, map[string][]string{"DueWakes": {wakePage1, wakePage2}})
	page, err := s.Wakes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if page.BudgetExhausted || page.Dormant != 1 || page.Withheld != 3 || len(page.Wakes) != 2 {
		t.Fatalf("page %+v", page)
	}
	w := page.Wakes[0]
	if w.Proof != "WAKE-PROOF-1-SECRET" || w.WorkerID != "wkr1" || !w.NamesChannels || len(w.Reasons) != 3 {
		t.Fatalf("wake %+v", w)
	}
	if !w.DeliverAfter.Equal(time.Date(2026, 10, 19, 11, 58, 0, 0, time.UTC)) || !w.BusyDeadline.Equal(time.Date(2026, 10, 19, 12, 8, 0, 0, time.UTC)) {
		t.Fatalf("timing %v %v", w.DeliverAfter, w.BusyDeadline)
	}
	tick, task, mention := w.Reasons[0], w.Reasons[1], w.Reasons[2]
	if tick.Kind != delivery.WakeTick || tick.TickerID != "tk1" || tick.TickerName != "Base tick" || !tick.LastTickAt.Equal(time.Date(2026, 10, 19, 11, 55, 0, 0, time.UTC)) {
		t.Fatalf("tick %+v", tick)
	}
	if task.Kind != delivery.WakeTask || task.Task == nil || *task.Task != (delivery.TaskReason{EventID: "ev1", ChainIndex: 0, Event: "assigned", ID: "t1", Title: "Fix it"}) {
		t.Fatalf("task %+v", task)
	}
	if mention.Kind != delivery.WakeMention {
		t.Fatalf("mention %+v", mention)
	}
	if err := delivery.CheckWake(w); err != nil {
		t.Fatalf("a mapped wake is well formed: %v", err)
	}
	w2 := page.Wakes[1]
	if w2.NamesChannels || w2.Reasons[0].Task.Event != "child finished" || w2.Reasons[0].Task.ChainIndex != 3 {
		t.Fatalf("second wake %+v", w2)
	}
	cs := calls()
	if len(cs) != 2 {
		t.Fatalf("calls %+v", cs)
	}
	for i, c := range cs {
		if c.op != "DueWakes" || c.auth != "Bearer hk_host_key" || c.session != "" {
			t.Fatalf("call %d: %+v", i, c)
		}
		if string(c.vars["appRef"]) != `"hrn:app:hadronmemory.com:team"` || string(c.vars["first"]) != "200" {
			t.Fatalf("call %d vars %s", i, c.vars)
		}
		// No accepts: a STOP notice is never offered to a deliverer that
		// cannot type it. No workerRefs: the host key names the host.
		for _, absent := range []string{"accepts", "workerRefs"} {
			if v, ok := c.vars[absent]; ok && string(v) != "null" {
				t.Fatalf("call %d sends %s=%s", i, absent, v)
			}
		}
	}
	if _, ok := cs[0].vars["after"]; ok {
		t.Fatalf("first page sends after: %s", cs[0].vars["after"])
	}
	if string(cs[1].vars["after"]) != `"c1"` {
		t.Fatalf("second page after %s", cs[1].vars["after"])
	}
}

func TestWakeEventWording(t *testing.T) {
	for ev, want := range map[string]string{
		"ASSIGNED": "assigned", "CHILD_FINISHED": "child finished", "ALARM": "alarm",
		"END_SESSION": "end session", "MATCHED": "matched", "REASSIGNED": "reassigned",
		"SOME_FUTURE_EVENT": "some future event",
	} {
		if got := wakeEventWords(ev); got != want {
			t.Errorf("%s -> %q, want %q", ev, got, want)
		}
	}
}

func TestWakesBudgetExhaustedTypesNothing(t *testing.T) {
	exhausted := `{"data":{"dueWakes":{"status":"WAKE_BUDGET_EXHAUSTED","withheld":4,"dormant":[],"wakes":[],"pageInfo":{"hasNextPage":false,"endCursor":null}}}}`
	s, _ := deliveryFake(t, map[string][]string{"DueWakes": {exhausted}})
	page, err := s.Wakes(context.Background())
	if err != nil || !page.BudgetExhausted || page.Withheld != 4 || len(page.Wakes) != 0 {
		t.Fatalf("page %+v err %v", page, err)
	}
}

func TestWakesScopeRefusalSwitchesDeliveryOff(t *testing.T) {
	s, _ := deliveryFake(t, map[string][]string{"DueWakes": {`{"errors":[{"message":"x","extensions":{"code":"APP_KEY_SCOPE_REQUIRED"}}],"data":null}`}})
	if _, err := s.Wakes(context.Background()); !errors.Is(err, hostpoller.ErrDeliveryScope) {
		t.Fatalf("err %v", err)
	}
}

// A page that breaks the contract refuses the whole read: nothing is typed.
func TestWakesRefuseABrokenPage(t *testing.T) {
	for name, body := range map[string]string{
		"no page":          `{"data":{"dueWakes":null}}`,
		"bad deliverAfter": strings.Replace(wakePage2, `"2026-10-19T11:59:00Z"`, `"soon"`, 1),
		"bad busyDeadline": strings.Replace(wakePage2, `"2026-10-19T12:09:00Z"`, `"later"`, 1),
		"looping cursor":   strings.Replace(strings.Replace(wakePage2, `"hasNextPage":false`, `"hasNextPage":true`, 1), `"c2"`, `null`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			s, _ := deliveryFake(t, map[string][]string{"DueWakes": {body}})
			page, err := s.Wakes(context.Background())
			if err == nil || len(page.Wakes) != 0 {
				t.Fatalf("page %+v err %v", page, err)
			}
			if strings.Contains(err.Error(), "SECRET") {
				t.Fatalf("an error carries a proof: %v", err)
			}
		})
	}
}

func TestAckWakeSendsOneProofAndMapsTheCounts(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		want delivery.AckResult
		ok   bool
	}{
		"acked":     {`{"data":{"acknowledgeWakes":{"acknowledged":1,"unchanged":0}}}`, delivery.Acked, true},
		"unchanged": {`{"data":{"acknowledgeWakes":{"acknowledged":0,"unchanged":1}}}`, delivery.Unchanged, true},
		"bad count": {`{"data":{"acknowledgeWakes":{"acknowledged":0,"unchanged":0}}}`, "", false},
		"stale":     {`{"errors":[{"message":"stale WAKE-PROOF-1-SECRET","extensions":{"code":"WAKE_PROOF_STALE"}}],"data":null}`, "", false},
	} {
		t.Run(name, func(t *testing.T) {
			s, calls := deliveryFake(t, map[string][]string{"AcknowledgeWakes": {tc.body}})
			res, err := s.AckWake(context.Background(), "WAKE-PROOF-1-SECRET")
			if (err == nil) != tc.ok || res != tc.want {
				t.Fatalf("res %q err %v", res, err)
			}
			if err != nil && strings.Contains(err.Error(), "SECRET") {
				t.Fatalf("an ack error carries the proof: %v", err)
			}
			c := calls()[0]
			var proofs []string
			if err := json.Unmarshal(c.vars["proofs"], &proofs); err != nil || len(proofs) != 1 || proofs[0] != "WAKE-PROOF-1-SECRET" || c.auth != "Bearer hk_host_key" || c.session != "" {
				t.Fatalf("call %+v", c)
			}
		})
	}
}

// The wired deliverer hands typed wakes' proofs back through the host
// server: without it a wake is typed and recorded but never acknowledged.
func TestWireDeliveryAcknowledgesWakesThroughTheHostServer(t *testing.T) {
	s, _ := deliveryFake(t, map[string][]string{})
	cfg := &hostpoller.Config{StateFile: filepath.Join(t.TempDir(), "state.json"), Delivery: hostpoller.DeliveryConfig{Enabled: true, OrcaBin: "/bin/true"}}
	p := hostpoller.New(cfg, nil, nil, &hostpoller.State{}, "", func(hostpoller.Event) {})
	d := wireDelivery(p, cfg, s, func(hostpoller.Event) {})
	if ws, ok := d.Wakes.(*hostServer); !ok || ws != s {
		t.Fatalf("deliverer acks wakes through %T %v, want the host server", d.Wakes, d.Wakes)
	}
	if d.AckMixed {
		t.Fatal("AckMixed stays off until A′ is verified deployed")
	}
	if p.WakeStep == nil || !p.DeliveryEnabled() {
		t.Fatal("wake status hook and delivery not wired")
	}
}
