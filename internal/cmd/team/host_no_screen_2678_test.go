package team

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/api"
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

// A report the server refuses is retried after the backoff, and its failure line
// is stamped now, is a documented kind, carries a code and no upstream text.
func TestNoScreenReportFailureLineIsTimedCodedAndTextFree2678(t *testing.T) {
	d, events, calls := noScreenWiring(t, map[string][]string{
		"HealthSweepSetTerminals": {
			`{"errors":[{"message":"UPSTREAM-SECRET-TEXT","extensions":{"code":"INTERNAL"}}]}`,
			`{"data":{"setHostWorkerTerminals":{"stored":[{"id":"r1","urn":"u"}],"refused":[]}}}`,
		},
	})
	t0 := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	for m := 0; m <= 25; m++ {
		d.Emit(heldUnknown(t0.Add(time.Duration(m) * time.Minute)))
	}
	n := 0
	for _, c := range calls() {
		if c.op == "HealthSweepSetTerminals" {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("report attempts = %d, want 2 (failed, then stored after the backoff)", n)
	}
	var failure *hostpoller.Event
	for i, e := range *events {
		if strings.Contains(e.Detail, "UPSTREAM-SECRET-TEXT") {
			t.Fatalf("an event line carries upstream text: %q", e.Detail)
		}
		if e.Outcome == "no-screen-report" {
			failure = &(*events)[i]
		}
	}
	if failure == nil {
		t.Fatal("a failed report left no event line")
	}
	if failure.Kind != "delivery-error" || !failure.Time.Equal(deliveryNow) || !strings.Contains(failure.Detail, "(ERROR)") {
		t.Fatalf("failure line = %+v, want kind delivery-error, stamped %v, code ERROR", *failure, deliveryNow)
	}
}

// The report runs inside the delivery event path: a server that never answers
// must not hold it past the timeout.
func TestNoScreenReportIsBoundedByATimeout2678(t *testing.T) {
	old := noScreenReportTimeout
	noScreenReportTimeout = 100 * time.Millisecond
	t.Cleanup(func() { noScreenReportTimeout = old })
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-block }))
	t.Cleanup(func() { close(block); srv.Close() })
	client, err := api.NewClient(srv.URL, "hk_host_key", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	s := &hostServer{client: client, app: "hrn:app:hadronmemory.com:team", now: func() time.Time { return deliveryNow }}
	cfg := &hostpoller.Config{StateFile: filepath.Join(t.TempDir(), "state.json"), Delivery: hostpoller.DeliveryConfig{Enabled: true, OrcaBin: "/bin/true"}}
	var events []hostpoller.Event
	p := hostpoller.New(cfg, nil, nil, &hostpoller.State{}, "", func(hostpoller.Event) {})
	d := wireDelivery(p, cfg, s, func(e hostpoller.Event) { events = append(events, e) })
	t0 := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	for m := 0; m < 15; m++ {
		d.Emit(heldUnknown(t0.Add(time.Duration(m) * time.Minute)))
	}
	start := time.Now()
	d.Emit(heldUnknown(t0.Add(delivery.NoScreenAfter)))
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("the report held the delivery event path for %v", took)
	}
	if last := events[len(events)-1]; !strings.Contains(last.Detail, "(TIMEOUT)") {
		t.Fatalf("last line = %+v, want a TIMEOUT failure", last)
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

// A hold older than the silent gap is no longer shown: the wake that began the
// episode may be gone, so the status would otherwise name a stale terminal forever.
func TestHostStatusHidesAStaleNoScreenEpisode2678(t *testing.T) {
	dir := t.TempDir()
	cfg := &hostpoller.Config{StateFile: filepath.Join(dir, "state.json"), PollIntervalSeconds: 30, Delivery: hostpoller.DeliveryConfig{Enabled: true, OrcaBin: "/bin/true"}}
	now := time.Date(2026, 11, 21, 13, 0, 0, 0, time.UTC)
	book := delivery.NewStatusBook(cfg.DeliveryStateDir())
	last := now.Add(-delivery.NoScreenGap - time.Minute)
	book.Observe(delivery.Event{Time: last, Worker: "Wolfgang", WorkerID: "wkr1", Span: "ticks:tk1", Kind: delivery.Held, Reason: delivery.HoldScreenUnknown, Terminal: "term_wolf", Wake: true})
	book.NoScreen(delivery.NoScreenEpisode{Worker: "Wolfgang", WorkerID: "wkr1", Terminal: "term_wolf", HeldSince: last.Add(-20 * time.Minute), Pending: 1})
	if err := book.Step(now, delivery.StepRead); err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	writeHostDelivery(&buf, hostDeliveryStatus(cfg, now))
	if strings.Contains(buf.String(), "NO SCREEN") {
		t.Errorf("a stale episode is still shown: %s", buf.String())
	}
}

// A report the server skips as stale was superseded by a newer stored one (the
// next health sweep's): it is recorded, not retried. Any other refusal retries.
func TestNoScreenStaleRefusalIsSupersededOtherRefusalRetries2678(t *testing.T) {
	for _, tc := range []struct {
		code      string
		wantCalls int
	}{{"TERMINAL_REPORT_STALE", 1}, {"WORKER_NOT_FOUND", 2}} {
		t.Run(tc.code, func(t *testing.T) {
			refused := `{"data":{"setHostWorkerTerminals":{"stored":[],"refused":[{"worker":"wkr1","code":"` + tc.code + `"}]}}}`
			d, _, calls := noScreenWiring(t, map[string][]string{"HealthSweepSetTerminals": {refused, refused}})
			t0 := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
			for m := 0; m <= 21; m++ {
				d.Emit(heldUnknown(t0.Add(time.Duration(m) * time.Minute)))
			}
			n := 0
			for _, c := range calls() {
				if c.op == "HealthSweepSetTerminals" {
					n++
				}
			}
			if n != tc.wantCalls {
				t.Fatalf("%s: report attempts = %d, want %d", tc.code, n, tc.wantCalls)
			}
		})
	}
}

// Bianca's P3 on #2878: a terminal handle that is not a plain machine token is
// withheld on both surfaces that print it, never echoed.
func TestNoScreenHostileTerminalHandleIsWithheld2678(t *testing.T) {
	hostile := "term\x1b[31m BAD\nhandle"
	ev := noScreenEvent(delivery.NoScreenEpisode{Worker: "Wolfgang", WorkerID: "wkr1", Terminal: hostile, HeldSince: time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC), Pending: 1}, deliveryNow)
	if strings.Contains(ev.Detail, "BAD") || !strings.Contains(ev.Detail, "terminal withheld") {
		t.Errorf("event line echoes a hostile handle: %q", ev.Detail)
	}
	dir := t.TempDir()
	cfg := &hostpoller.Config{StateFile: filepath.Join(dir, "state.json"), PollIntervalSeconds: 30, Delivery: hostpoller.DeliveryConfig{Enabled: true, OrcaBin: "/bin/true"}}
	now := time.Date(2026, 11, 21, 13, 0, 0, 0, time.UTC)
	book := delivery.NewStatusBook(cfg.DeliveryStateDir())
	book.Observe(delivery.Event{Time: now.Add(-time.Minute), Worker: "Wolfgang", WorkerID: "wkr1", Span: "ticks:tk1", Kind: delivery.Held, Reason: delivery.HoldScreenUnknown, Terminal: hostile, Wake: true})
	book.NoScreen(delivery.NoScreenEpisode{Worker: "Wolfgang", WorkerID: "wkr1", Terminal: hostile, HeldSince: now.Add(-20 * time.Minute), Pending: 1})
	if err := book.Step(now, delivery.StepRead); err != nil {
		t.Fatal(err)
	}
	d := hostDeliveryStatus(cfg, now)
	raw, _ := json.Marshal(d)
	var text strings.Builder
	writeHostDelivery(&text, d)
	for _, out := range []string{string(raw), text.String()} {
		if strings.Contains(out, "BAD") || strings.Contains(out, "\x1b") {
			t.Errorf("status echoes a hostile handle: %q", out)
		}
	}
	if !strings.Contains(string(raw), `"terminal":"withheld"`) {
		t.Errorf("--json does not withhold the handle: %s", raw)
	}
}
