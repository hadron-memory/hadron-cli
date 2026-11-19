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

	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
)

// cli#2623, the whole path: a poller started from a configuration file does
// not know a worker until her entry is added to the file; after the edit the
// very next poll registers her, and a due wake for her is typed into her
// terminal. Everything is a fixture: a temp dir for the config, the state and
// the worktrees, a fake `orca` standing in for the real one (the package's
// orcaRunner seam), and an httptest server for Hadron. Nothing here reaches a
// real host, Orca or poller (findings:host-command-help-harness-must-sandbox).

// reloadRegistrar answers a registration with an id for each worker named in
// it, wkr-<name>, and hands out no operations.
type reloadRegistrar struct{}

func (reloadRegistrar) Register(_ context.Context, r hostpoller.Registration) (hostpoller.RegisteredHost, error) {
	ans := hostpoller.RegisteredHost{}
	for _, w := range r.Workers {
		ans.Workers = append(ans.Workers, hostpoller.RegisteredHostWorker{WorkerID: "wkr-" + strings.ToLower(w.Worker), Name: w.Worker})
	}
	return ans, nil
}
func (reloadRegistrar) Collect(context.Context) ([]hostpoller.Operation, error) { return nil, nil }
func (reloadRegistrar) Report(context.Context, string, string, string, string) (hostpoller.Operation, error) {
	return hostpoller.Operation{}, nil
}

// twoTreeOrca is a fake orca with one idle terminal in each configured tree.
type twoTreeOrca struct {
	mu    sync.Mutex
	trees map[string]string // terminal handle -> worktree path
	sent  map[string][]string
}

func (o *twoTreeOrca) run(_ context.Context, argv []string) orcaterm.Result {
	o.mu.Lock()
	defer o.mu.Unlock()
	reply := func(result any) orcaterm.Result {
		b, _ := json.Marshal(map[string]any{"ok": true, "result": result})
		return orcaterm.Result{Stdout: string(b)}
	}
	handle := func() string {
		if i := slices.Index(argv, "--terminal"); i >= 0 && i+1 < len(argv) {
			return argv[i+1]
		}
		return ""
	}
	if len(argv) < 2 {
		return orcaterm.Result{Code: 1}
	}
	switch strings.Join(argv[:2], " ") {
	case "terminal list":
		var ts []map[string]any
		for h, tree := range o.trees {
			ts = append(ts, map[string]any{"handle": h, "worktreePath": tree, "agentIdentity": "claude"})
		}
		return reply(map[string]any{"terminals": ts})
	case "terminal read":
		return reply(map[string]any{"terminal": map[string]any{"source": "screen", "tail": stepIdle}})
	case "terminal send":
		if i := slices.Index(argv, "--text"); i >= 0 && i+1 < len(argv) {
			o.sent[handle()] = append(o.sent[handle()], argv[i+1])
		}
		return reply(map[string]any{"send": map[string]any{"accepted": true, "prompt": map[string]any{"stages": []string{"input_accepted", "turn_started"}}}})
	}
	return orcaterm.Result{Code: 1}
}

func (o *twoTreeOrca) sends(handle string) []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string{}, o.sent[handle]...)
}

func wakePageFor(workerID string, now time.Time) string {
	return fmt.Sprintf(`{"data":{"dueWakes":{"status":"OK","withheld":0,"dormant":[],
 "wakes":[{"proof":"PROOF-%s","worker":{"id":%q},
  "deliverAfter":%q,"busyDeadline":%q,"channels":[],
  "reasons":[{"kind":"TICK","ticker":{"id":"tk1","name":"Base tick","lastTickAt":%q},"task":null}]}],
 "pageInfo":{"hasNextPage":false,"endCursor":"w1"}}}}`,
		workerID, workerID, now.Add(-time.Minute).Format(time.RFC3339), now.Add(10*time.Minute).Format(time.RFC3339), now.Add(-2*time.Minute).Format(time.RFC3339))
}

func TestAWorkerAddedToARunningPollersConfigGetsItsWake2623(t *testing.T) {
	// Under /tmp, not TMPDIR: the reload trust check judges every directory
	// above the config file, and a developer's TMPDIR may be group-writable.
	root, err := os.MkdirTemp("/tmp", "reloadwake2623-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	for _, w := range []string{"iris", "billie"} {
		tree := filepath.Join(root, w)
		if err := os.MkdirAll(filepath.Join(tree, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tree, ".git", "hadron-team-session.json"), []byte(fmt.Sprintf(`{"workerId":"wkr-%s"}`, w)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	orca := &twoTreeOrca{trees: map[string]string{"term_iris": filepath.Join(root, "iris"), "term_billie": filepath.Join(root, "billie")}, sent: map[string][]string{}}
	prev := orcaRunner
	orcaRunner = func(string) orcaterm.Runner { return orca.run }
	t.Cleanup(func() { orcaRunner = prev })

	keyFile := filepath.Join(root, "host.key")
	if err := os.WriteFile(keyFile, []byte("hk_fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	conf := filepath.Join(root, "host.yaml")
	base := fmt.Sprintf(`app: hrn:app:acme.com:team
key_file: %s
worktrees_root: %s
state_file: %s/state.json
delivery:
  enabled: true
  orca_bin: /nonexistent/orca
workers:
  Iris:
    family: claude
    launch: [claude-worker]
    worktrees: [iris]
`, keyFile, root, root)
	if err := os.WriteFile(conf, []byte(base), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := hostpoller.LoadConfig(conf)
	if err != nil {
		t.Fatal(err)
	}
	state, err := hostpoller.LoadState(cfg.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	// Billie's wake is due on every read until it is acknowledged.
	now := time.Now().UTC()
	bodies := map[string][]string{
		"DeliveryBatches":  {stepNoBatches, stepNoBatches, stepNoBatches, stepNoBatches, stepNoBatches},
		"DueWakes":         {wakePageFor("wkr-billie", now), wakePageFor("wkr-billie", now), wakePageFor("wkr-billie", now), stepNoWakes, stepNoWakes},
		"AcknowledgeWakes": {stepAcked},
	}
	srv, _ := deliveryFake(t, bodies)
	p := hostpoller.New(cfg, reloadRegistrar{}, nil, state, "", func(hostpoller.Event) {})
	wireDelivery(p, cfg, srv, func(hostpoller.Event) {})
	step := func() {
		t.Helper()
		if err := p.Step(context.Background()); err != nil {
			t.Fatal(err)
		}
		p.Wait()
	}

	step() // billie is not in the file: her wake has no recipient on this host
	if got := orca.sends("term_billie"); len(got) != 0 {
		t.Fatalf("typed %q into a worker this poller was never configured for", got)
	}

	// The operator adds her to the file while the poller runs.
	added := base + "  Billie:\n    family: claude\n    launch: [claude-worker]\n    worktrees: [billie]\n"
	if err := os.WriteFile(conf, []byte(added), 0o600); err != nil {
		t.Fatal(err)
	}
	step() // the poller sees the new content for the first time
	if got := orca.sends("term_billie"); len(got) != 0 {
		t.Fatalf("typed %q on the first sight of new content: it is applied on the second poll that sees it", got)
	}
	step() // the second sighting applies it: no restart
	got := orca.sends("term_billie")
	if len(got) != 1 || !strings.Contains(got[0], "Base tick") {
		t.Fatalf("terminal for the added worker got %q, want her tick line: the poller kept the worker set it read at start", got)
	}
	if other := orca.sends("term_iris"); len(other) != 0 {
		t.Fatalf("iris's terminal got %q for billie's wake", other)
	}
}

// The help says the file is re-read and which settings still need a restart,
// and names the three events an operator will see.
func TestHostRunHelpSaysWhatAReloadAppliesAndWhatNeedsARestart2623(t *testing.T) {
	cmd := newCmdTeamHostRun(nil)
	help := strings.Join(strings.Fields(cmd.Long), " ")
	for _, want := range []string{
		"The file is re-read every poll", "take effect on the next poll, with no restart",
		"config-reloaded", "config-restart-needed", "config-reload-refused",
		"read once at start", "keeps the configuration it has",
		"second poll that sees it", "owned by the poller's user", "not writable by group or others",
		"stops other users and loose modes", "removed-worker-end", "one component at a time, including those a chain of symlinks", "each symlink followed must be owned by the poller's user or root", "every directory above them and above the file", "codex_home, claude_config_dir, disk_tmp", "inside the poller's trust boundary, as it is for a restart",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("team host run help lacks %q", want)
		}
	}
}
