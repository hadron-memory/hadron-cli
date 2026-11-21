package team

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/delivery"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
)

// cli#2678: wireDelivery surfaces a worker held as screen-unknown for 15
// minutes, once per episode: one terminal report (locked, no_screen) and one host
// event naming the terminal, held-since and pending count. Typing is not part of
// it: nothing here holds an Orca handle.

func noScreenWiring(t *testing.T, bodies map[string][]string) (*delivery.Deliverer, *[]hostpoller.Event, func() []deliveryCall) {
	t.Helper()
	s, calls := deliveryFake(t, bodies)
	cfg := &hostpoller.Config{StateFile: filepath.Join(t.TempDir(), "state.json"), Delivery: hostpoller.DeliveryConfig{Enabled: true, OrcaBin: "/bin/true"}}
	var events []hostpoller.Event
	emit := func(e hostpoller.Event) { events = append(events, e) }
	p := hostpoller.New(cfg, nil, nil, &hostpoller.State{}, "", func(hostpoller.Event) {})
	return wireDelivery(p, cfg, s, emit), &events, calls
}

func heldUnknown(at time.Time) delivery.Event {
	return delivery.Event{Time: at, Worker: "Wolfgang", WorkerID: "wkr1", Span: "ticks:tk1", Kind: delivery.Held, Reason: delivery.HoldScreenUnknown, Terminal: "term_wolf", Wake: true}
}

func TestNoScreenEpisodeIsReportedOnceAndNamesTheTerminal2678(t *testing.T) {
	d, events, calls := noScreenWiring(t, map[string][]string{
		"HealthSweepSetTerminals": {`{"data":{"setHostWorkerTerminals":{"stored":[{"id":"r1","urn":"u"}],"refused":[]}}}`},
	})
	t0 := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	for m := 0; m <= 66; m++ {
		d.Emit(heldUnknown(t0.Add(time.Duration(m) * time.Minute)))
	}
	var reports []deliveryCall
	for _, c := range calls() {
		if c.op == "HealthSweepSetTerminals" {
			reports = append(reports, c)
		}
	}
	if len(reports) != 1 {
		t.Fatalf("terminal reports = %d, want exactly one per episode", len(reports))
	}
	in := string(reports[0].vars["input"])
	for _, want := range []string{`"locked"`, `"no_screen"`, `"wkr1"`} {
		if !strings.Contains(in, want) {
			t.Errorf("report %s does not carry %s", in, want)
		}
	}
	var found []hostpoller.Event
	for _, e := range *events {
		if e.Kind == "delivery-no-screen" {
			found = append(found, e)
		}
	}
	if len(found) != 1 {
		t.Fatalf("no-screen host events = %d, want one", len(found))
	}
	for _, want := range []string{"Wolfgang", "term_wolf", "2026-11-21T12:00:00Z", "1 pending"} {
		if !strings.Contains(found[0].Detail, want) {
			t.Errorf("event %q does not name %q", found[0].Detail, want)
		}
	}
}

// A report the server refuses is retried on the next hold, and a failure line
// carries no upstream text.
func TestNoScreenReportFailureRetriesAndLogsNoUpstreamText2678(t *testing.T) {
	d, events, calls := noScreenWiring(t, map[string][]string{
		"HealthSweepSetTerminals": {
			`{"errors":[{"message":"UPSTREAM-SECRET-TEXT","extensions":{"code":"INTERNAL"}}]}`,
			`{"data":{"setHostWorkerTerminals":{"stored":[{"id":"r1","urn":"u"}],"refused":[]}}}`,
		},
	})
	t0 := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	for m := 0; m <= 20; m++ {
		d.Emit(heldUnknown(t0.Add(time.Duration(m) * time.Minute)))
	}
	n := 0
	for _, c := range calls() {
		if c.op == "HealthSweepSetTerminals" {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("report attempts = %d, want 2 (failed, then stored)", n)
	}
	for _, e := range *events {
		if strings.Contains(e.Detail, "UPSTREAM-SECRET-TEXT") {
			t.Fatalf("an event line carries upstream text: %q", e.Detail)
		}
	}
}

// The host status carries the three facts a person needs to act on an episode:
// the terminal to open, since when, and how many wakes wait. --json carries them
// too (null when none), and a screen that returns clears them.
func TestHostStatusShowsTheNoScreenEpisode2678(t *testing.T) {
	dir := t.TempDir()
	cfg := &hostpoller.Config{StateFile: filepath.Join(dir, "state.json"), PollIntervalSeconds: 30, Delivery: hostpoller.DeliveryConfig{Enabled: true, OrcaBin: "/bin/true"}}
	now := time.Date(2026, 11, 21, 13, 0, 0, 0, time.UTC)
	book := delivery.NewStatusBook(cfg.DeliveryStateDir())
	since := now.Add(-20 * time.Minute)
	book.Observe(delivery.Event{Time: since, Worker: "Wolfgang", WorkerID: "wkr1", Span: "ticks:tk1", Kind: delivery.Held, Reason: delivery.HoldScreenUnknown, Terminal: "term_wolf", Wake: true})
	book.Observe(delivery.Event{Time: now.Add(-10 * time.Second), Worker: "Wolfgang", WorkerID: "wkr1", Span: "ticks:tk1", Kind: delivery.Held, Reason: delivery.HoldScreenUnknown, Terminal: "term_wolf", Wake: true})
	book.Observe(delivery.Event{Time: now.Add(-10 * time.Second), Worker: "Iris", WorkerID: "wkr2", Kind: delivery.Held, Reason: delivery.HoldDraft, Wake: true})
	book.NoScreen(delivery.NoScreenEpisode{Worker: "Wolfgang", WorkerID: "wkr1", Terminal: "term_wolf", HeldSince: since, Pending: 2})
	if err := book.Step(now, delivery.StepRead); err != nil {
		t.Fatal(err)
	}
	d := hostDeliveryStatus(cfg, now)
	var buf strings.Builder
	writeHostDelivery(&buf, d)
	var wolf, iris string
	for _, line := range strings.Split(buf.String(), "\n") {
		switch {
		case strings.Contains(line, "Wolfgang:"):
			wolf = line
		case strings.Contains(line, "Iris:"):
			iris = line
		}
	}
	for _, want := range []string{"NO SCREEN since 2026-11-21T12:40:00Z", "2 pending", "open terminal term_wolf in Orca", "nothing is typed"} {
		if !strings.Contains(wolf, want) {
			t.Errorf("status line lacks %q: %q", want, wolf)
		}
	}
	if strings.Contains(iris, "NO SCREEN") {
		t.Errorf("a draft hold carries the no-screen line: %q", iris)
	}
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"noScreen":{"terminal":"term_wolf","since":"2026-11-21T12:40:00Z","pending":2}`, `"noScreen":null`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("--json lacks %s: %s", want, raw)
		}
	}
	// The screen comes back (a delivery): the episode is over on the status too.
	book.Observe(delivery.Event{Time: now, Worker: "Wolfgang", WorkerID: "wkr1", Kind: delivery.Delivered, Wake: true})
	_ = book.Step(now, delivery.StepRead)
	var after strings.Builder
	writeHostDelivery(&after, hostDeliveryStatus(cfg, now))
	if strings.Contains(after.String(), "NO SCREEN") {
		t.Errorf("a delivered wake leaves the no-screen line: %s", after.String())
	}
}
