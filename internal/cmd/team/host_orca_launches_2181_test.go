package team

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
)

// #2181 acceptance 1-3, end to end: the host poller as `team host run` wires
// it, with a counting fake `orca`. Every call to the runner is one `orca`
// process in production. A worker holding a draft is held every cycle; its
// cost must not grow with the roster once nothing has changed.

type countingOrca struct {
	mu     sync.Mutex
	trees  []string
	screen []string
	counts map[string]int
	stamp  string
}

func (f *countingOrca) run(_ context.Context, argv []string) orcaterm.Result {
	f.mu.Lock()
	defer f.mu.Unlock()
	verb := strings.Join(argv[:2], " ")
	f.counts[verb]++
	reply := func(result any) orcaterm.Result {
		b, _ := json.Marshal(map[string]any{"ok": true, "result": result})
		return orcaterm.Result{Stdout: string(b)}
	}
	switch verb {
	case "terminal list":
		rows := []map[string]any{}
		for i, tr := range f.trees {
			rows = append(rows, map[string]any{"handle": fmt.Sprintf("term_%d", i), "worktreePath": tr, "agentIdentity": "claude", "lastOutputAt": f.stamp})
		}
		return reply(map[string]any{"terminals": rows, "totalCount": len(rows), "truncated": false})
	case "terminal read":
		return reply(map[string]any{"terminal": map[string]any{"source": "screen", "tail": f.screen}})
	}
	return orcaterm.Result{Code: 1}
}

type manyRegistrar struct{ n int }

func (r manyRegistrar) Register(context.Context, hostpoller.Registration) (hostpoller.RegisteredHost, error) {
	var ws []hostpoller.RegisteredHostWorker
	for i := 0; i < r.n; i++ {
		ws = append(ws, hostpoller.RegisteredHostWorker{WorkerID: fmt.Sprintf("wkr%d", i), Name: fmt.Sprintf("W%d", i)})
	}
	return hostpoller.RegisteredHost{Workers: ws}, nil
}
func (manyRegistrar) Collect(context.Context) ([]hostpoller.Operation, error) { return nil, nil }
func (manyRegistrar) Report(context.Context, string, string, string, string) (hostpoller.Operation, error) {
	return hostpoller.Operation{}, nil
}

// launchesInSteadyState runs three steps for n workers that each hold a due
// wake behind a draft, and returns the orca calls the 2nd and 3rd made.
func launchesInSteadyState(t *testing.T, n int) map[string]int {
	t.Helper()
	root := t.TempDir()
	orca := &countingOrca{screen: stepDraft, counts: map[string]int{}, stamp: "1794735433200"}
	workers := map[string]hostpoller.WorkerConfig{}
	var wakes []string
	now := time.Now().UTC()
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("w%d", i)
		tree := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Join(tree, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tree, ".git", "hadron-team-session.json"), []byte(fmt.Sprintf(`{"workerId":"wkr%d"}`, i)), 0o600); err != nil {
			t.Fatal(err)
		}
		orca.trees = append(orca.trees, tree)
		workers[fmt.Sprintf("W%d", i)] = hostpoller.WorkerConfig{Family: "claude", Worktrees: []string{name}}
		wakes = append(wakes, fmt.Sprintf(`{"proof":"P%d","worker":{"id":"wkr%d"},"deliverAfter":%q,"busyDeadline":%q,"channels":[],
 "reasons":[{"kind":"TICK","ticker":{"id":"tk","name":"Base tick","lastTickAt":%q},"task":null}]}`,
			i, i, now.Add(-time.Minute).Format(time.RFC3339), now.Add(10*time.Minute).Format(time.RFC3339), now.Add(-2*time.Minute).Format(time.RFC3339)))
	}
	page := `{"data":{"dueWakes":{"status":"OK","withheld":0,"dormant":[],"wakes":[` + strings.Join(wakes, ",") + `],"pageInfo":{"hasNextPage":false,"endCursor":"w1"}}}}`
	prevWindow := sharedListWindow
	sharedListWindow = 400 * time.Millisecond // a cycle here is milliseconds
	t.Cleanup(func() { sharedListWindow = prevWindow })
	prev := orcaRunner
	orcaRunner = func(string) orcaterm.Runner { return orca.run }
	t.Cleanup(func() { orcaRunner = prev })
	srv, _ := deliveryFake(t, map[string][]string{
		"DeliveryBatches": {stepNoBatches, stepNoBatches, stepNoBatches},
		"DueWakes":        {page, page, page},
	})
	cfg := &hostpoller.Config{StateFile: filepath.Join(t.TempDir(), "state.json"), WorktreesRoot: root, Workers: workers,
		Delivery: hostpoller.DeliveryConfig{Enabled: true, OrcaBin: "/nonexistent/orca"}}
	p := hostpoller.New(cfg, manyRegistrar{n}, nil, &hostpoller.State{}, "", func(hostpoller.Event) {})
	wireDelivery(p, cfg, srv, func(hostpoller.Event) {})
	step := func() {
		if err := p.Step(context.Background()); err != nil {
			t.Fatal(err)
		}
		p.Wait()
	}
	step()
	orca.mu.Lock()
	before := map[string]int{}
	for k, v := range orca.counts {
		before[k] = v
	}
	orca.mu.Unlock()
	// The window is wall-clock: let the first cycle's list age out.
	time.Sleep(sharedListWindow + 100*time.Millisecond)
	step()
	step()
	orca.mu.Lock()
	defer orca.mu.Unlock()
	out := map[string]int{"first-cycle reads": before["terminal read"], "first-cycle lists": before["terminal list"]}
	for k, v := range orca.counts {
		out[k] = v - before[k]
	}
	return out
}

func TestOrcaLaunchesPerCycleDoNotGrowWithTheRoster2181(t *testing.T) {
	small, large := launchesInSteadyState(t, 5), launchesInSteadyState(t, 40)
	t.Logf("5 workers: %v", small)
	t.Logf("40 workers: %v", large)
	// First cycle: a worker whose decision needs a screen still gets one.
	if large["first-cycle reads"] != 40 {
		t.Fatalf("each held worker is read once on its first cycle: %v", large)
	}
	// Steady state, two cycles, nothing changed: no screen reads at all, and
	// no more lists than cycles, for 5 workers or 40.
	for name, c := range map[string]map[string]int{"5": small, "40": large} {
		if c["terminal read"] != 0 {
			t.Fatalf("%s workers: held and quiet, yet %d screen reads", name, c["terminal read"])
		}
		if c["terminal list"] > 2 || c["terminal list"] < 1 {
			t.Fatalf("%s workers: %d list calls in 2 cycles, want at most 1 per cycle", name, c["terminal list"])
		}
	}
}
