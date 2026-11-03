package team

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/delivery"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
)

// #1192: one poller step end to end, with a non-empty due wake. The wake
// read, the delivery decision and the ack are each pinned in their own
// package; this drives them wired together as `team host run` wires them
// (wireDelivery), so a regression between the layers — a typed wake never
// acked, or an ack for a send that was held — fails here.

// stepRegistrar is the poller's registration and operations server: it
// registers one worker and hands out no operations.
type stepRegistrar struct{ workerID string }

func (r stepRegistrar) Register(context.Context, hostpoller.Registration) (hostpoller.RegisteredHost, error) {
	return hostpoller.RegisteredHost{Workers: []hostpoller.RegisteredHostWorker{{WorkerID: r.workerID, Name: "Iris"}}}, nil
}
func (stepRegistrar) Collect(context.Context) ([]hostpoller.Operation, error) { return nil, nil }
func (stepRegistrar) Report(context.Context, string, string, string, string) (hostpoller.Operation, error) {
	return hostpoller.Operation{}, nil
}

// fakeOrcaRunner answers the `orca terminal` calls delivery makes: one
// terminal in the worker's worktree showing screen, and every send accepted
// with a started turn. It records the text of each send.
type fakeOrcaRunner struct {
	mu     sync.Mutex
	tree   string
	screen []string
	sent   []string
}

func (f *fakeOrcaRunner) run(_ context.Context, argv []string) orcaterm.Result {
	f.mu.Lock()
	defer f.mu.Unlock()
	reply := func(result any) orcaterm.Result {
		b, _ := json.Marshal(map[string]any{"ok": true, "result": result})
		return orcaterm.Result{Stdout: string(b)}
	}
	switch strings.Join(argv[:2], " ") {
	case "terminal list":
		return reply(map[string]any{"terminals": []map[string]any{{"handle": "term_iris", "worktreePath": f.tree, "agentIdentity": "claude"}}})
	case "terminal read":
		return reply(map[string]any{"terminal": map[string]any{"source": "screen", "tail": f.screen}})
	case "terminal send":
		if i := slices.Index(argv, "--text"); i >= 0 && i+1 < len(argv) {
			f.sent = append(f.sent, argv[i+1])
		}
		return reply(map[string]any{"send": map[string]any{"accepted": true, "prompt": map[string]any{"stages": []string{"input_accepted", "turn_started"}}}})
	}
	return orcaterm.Result{Code: 1}
}

func (f *fakeOrcaRunner) sends() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.sent...)
}

const stepRule = "────────────────────────────────────────────────────────────"
const stepHint = "  ⏵⏵ bypass permissions on (shift+tab to cycle) · ← for agents"

var (
	stepIdle  = []string{"⏺ Done.", "✻ Cogitated for 1m 3s", stepRule, "❯ ", stepRule, stepHint}
	stepBusy  = []string{"  (ctrl+b to run in background)", "✽ Bunning… (5m 55s · ↓ 15.6k tokens)", stepRule, "❯ ", stepRule, stepHint}
	stepDraft = []string{"⏺ Done.", "✻ Cogitated for 1m 3s", stepRule, "❯ a half-typed thought", stepRule, stepHint}
)

// stepWakePage is one due TICK wake for wkr1, due a minute ago, its busy
// deadline ten minutes out (so a busy agent defers it).
func stepWakePage(now time.Time) string {
	return fmt.Sprintf(`{"data":{"dueWakes":{"status":"OK","withheld":0,"dormant":[],
 "wakes":[{"proof":"WAKE-PROOF-STEP-SECRET","worker":{"id":"wkr1"},
  "deliverAfter":%q,"busyDeadline":%q,"channels":[],
  "reasons":[{"kind":"TICK","ticker":{"id":"tk1","name":"Base tick","lastTickAt":%q},"task":null}]}],
 "pageInfo":{"hasNextPage":false,"endCursor":"w1"}}}}`,
		now.Add(-time.Minute).Format(time.RFC3339), now.Add(10*time.Minute).Format(time.RFC3339), now.Add(-2*time.Minute).Format(time.RFC3339))
}

const (
	stepNoBatches = `{"data":{"deliveryBatches":{"batches":[],"pageInfo":{"hasNextPage":false,"endCursor":null}}}}`
	stepNoWakes   = `{"data":{"dueWakes":{"status":"OK","withheld":0,"dormant":[],"wakes":[],"pageInfo":{"hasNextPage":false,"endCursor":null}}}}`
	stepAcked     = `{"data":{"acknowledgeWakes":{"acknowledged":1,"unchanged":0}}}`
)

// runWakeStep wires a poller as `team host run` does, with the Orca runner
// replaced, and runs one step (plus a quiet second one, so the status
// snapshot written at a step's end includes the first step's outcome).
func runWakeStep(t *testing.T, screen []string, extra map[string][]string) (*fakeOrcaRunner, []deliveryCall, delivery.Status) {
	t.Helper()
	root := t.TempDir()
	tree := filepath.Join(root, "iris")
	if err := os.MkdirAll(filepath.Join(tree, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, ".git", "hadron-team-session.json"), []byte(`{"workerId":"wkr1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	orca := &fakeOrcaRunner{tree: tree, screen: screen}
	prev := orcaRunner
	orcaRunner = func(string) orcaterm.Runner { return orca.run }
	t.Cleanup(func() { orcaRunner = prev })

	bodies := map[string][]string{
		"DeliveryBatches": {stepNoBatches, stepNoBatches},
		"DueWakes":        {stepWakePage(time.Now().UTC()), stepNoWakes},
	}
	for op, b := range extra {
		bodies[op] = b
	}
	srv, calls := deliveryFake(t, bodies)
	stateDir := t.TempDir()
	cfg := &hostpoller.Config{StateFile: filepath.Join(stateDir, "state.json"), WorktreesRoot: root,
		Workers:  map[string]hostpoller.WorkerConfig{"Iris": {Family: "claude", Worktrees: []string{"iris"}}},
		Delivery: hostpoller.DeliveryConfig{Enabled: true, OrcaBin: "/nonexistent/orca"}}
	p := hostpoller.New(cfg, stepRegistrar{workerID: "wkr1"}, nil, &hostpoller.State{}, "", func(hostpoller.Event) {})
	wireDelivery(p, cfg, srv, func(hostpoller.Event) {})

	for range 2 {
		if err := p.Step(context.Background()); err != nil {
			t.Fatal(err)
		}
		p.Wait()
	}
	st, err := delivery.ReadStatus(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	return orca, calls(), st
}

func acksIn(calls []deliveryCall) [][]string {
	out := [][]string{}
	for _, c := range calls {
		if c.op != "AcknowledgeWakes" {
			continue
		}
		var proofs []string
		_ = json.Unmarshal(c.vars["proofs"], &proofs)
		out = append(out, proofs)
	}
	return out
}

func TestPollerStepTypesADueWakeAndAcksItsProofOnce(t *testing.T) {
	orca, calls, st := runWakeStep(t, stepIdle, map[string][]string{"AcknowledgeWakes": {stepAcked}})
	sent := orca.sends()
	if len(sent) != 1 || sent[0] != "[hadron tick] Base tick: scheduled check-in. Read your team chat and tasks." {
		t.Fatalf("terminal got %q, want the one tick line", sent)
	}
	if acks := acksIn(calls); len(acks) != 1 || len(acks[0]) != 1 || acks[0][0] != "WAKE-PROOF-STEP-SECRET" {
		t.Fatalf("acks %v, want exactly one, with the wake's proof", acks)
	}
	if w := st.Workers["wkr1"]; w.Kind != delivery.Delivered || !w.Wake {
		t.Fatalf("status %+v, want a delivered wake", w)
	}
}

func TestPollerStepNeverAcksAWakeItDidNotType(t *testing.T) {
	for name, c := range map[string]struct {
		screen []string
		kind   delivery.Kind
		reason string
	}{
		"a busy agent defers it": {stepBusy, delivery.Deferred, "busy"},
		"a draft holds it":       {stepDraft, delivery.Held, delivery.HoldDraft},
	} {
		t.Run(name, func(t *testing.T) {
			orca, calls, st := runWakeStep(t, c.screen, nil)
			if sent := orca.sends(); len(sent) != 0 {
				t.Fatalf("terminal got %q, want nothing typed", sent)
			}
			if acks := acksIn(calls); len(acks) != 0 {
				t.Fatalf("acks %v, want none for an untyped wake", acks)
			}
			if w := st.Workers["wkr1"]; w.Kind != c.kind || w.Reason != c.reason || !w.Wake {
				t.Fatalf("status %+v, want %s/%s on the wake", w, c.kind, c.reason)
			}
		})
	}
}
