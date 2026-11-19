package hostpoller

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// cli#2623: a running poller picks up a change to its configuration file. Each
// test starts a poller from a real file in a temp dir, steps it, edits the
// file, and steps it again, as an operator editing a live host's config would.

const reloadBase = `app: hrn:app:acme.com:team
label: box1
poll_interval_seconds: 30
key_file: %KEY%
worktrees_root: %ROOT%
state_file: %ROOT%/state.json
repos:
  acme/repo: /src/repo
workers:
  iris:
    family: claude
    launch: [claude-worker]
`

type reloadFixture struct {
	t      *testing.T
	path   string
	dir    string
	p      *Poller
	srv    *fakeServer
	mu     sync.Mutex
	events []Event
}

func newReloadFixture(t *testing.T) *reloadFixture {
	t.Helper()
	dir := t.TempDir()
	key := filepath.Join(dir, "host.key")
	if err := os.WriteFile(key, []byte(hostKey), 0o600); err != nil {
		t.Fatal(err)
	}
	fx := &reloadFixture{t: t, dir: dir, path: filepath.Join(dir, "host.yaml")}
	fx.write(reloadBase)
	cfg, err := LoadConfig(fx.path)
	if err != nil {
		t.Fatal(err)
	}
	st, err := LoadState(cfg.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	fx.srv = &fakeServer{notDone: map[string]int{}, endedOnce: map[string]bool{}}
	fx.p = New(cfg, fx.srv, &fakeHost{trees: map[string]WorktreeState{}}, st, hostKey, func(e Event) {
		fx.mu.Lock()
		fx.events = append(fx.events, e)
		fx.mu.Unlock()
	})
	return fx
}

// write replaces the file with text, filling the two path placeholders.
func (fx *reloadFixture) write(text string) {
	fx.t.Helper()
	text = strings.NewReplacer("%KEY%", filepath.Join(fx.dir, "host.key"), "%ROOT%", fx.dir).Replace(text)
	if err := os.WriteFile(fx.path, []byte(text), 0o600); err != nil {
		fx.t.Fatal(err)
	}
}

func (fx *reloadFixture) step() {
	fx.t.Helper()
	if err := fx.p.Step(context.Background()); err != nil {
		fx.t.Fatal(err)
	}
}

// registered is the worker names of the newest registration.
func (fx *reloadFixture) registered() []string {
	fx.srv.mu.Lock()
	defer fx.srv.mu.Unlock()
	var out []string
	for _, w := range fx.srv.regs[len(fx.srv.regs)-1].Workers {
		out = append(out, w.Worker)
	}
	return out
}

func (fx *reloadFixture) kinds(kind string) []Event {
	fx.mu.Lock()
	defer fx.mu.Unlock()
	var out []Event
	for _, e := range fx.events {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

func TestRunningPollerPicksUpAWorkerAddedToItsConfig2623(t *testing.T) {
	fx := newReloadFixture(t)
	fx.step()
	if got := strings.Join(fx.registered(), ","); got != "iris" {
		t.Fatalf("first registration = %q, want iris", got)
	}
	fx.write(reloadBase + "  billie:\n    family: claude\n    launch: [claude-worker]\n")
	fx.step()
	if got := strings.Join(fx.registered(), ","); got != "billie,iris" {
		t.Fatalf("registration after the edit = %q, want billie,iris: the poller kept the worker set it read at start", got)
	}
	// The new worker's wake path: the registration answer names her id, and a
	// batch for that id resolves to a recipient with her configured interface.
	if r, ok := fx.p.recipient("id-billie"); !ok || r.Name != "billie" {
		t.Fatalf("recipient for the added worker = %+v, %v", r, ok)
	}
	if ev := fx.kinds("config-reloaded"); len(ev) != 1 || !strings.Contains(ev[0].Detail, "workers added: billie") {
		t.Fatalf("want one config-reloaded event naming billie, got %+v", ev)
	}
	fx.step()
	if n := len(fx.kinds("config-reloaded")); n != 1 {
		t.Fatalf("an unchanged file must not reload again: %d events", n)
	}
}

func TestRunningPollerDropsARemovedWorker2623(t *testing.T) {
	fx := newReloadFixture(t)
	fx.write(reloadBase + "  billie:\n    family: claude\n    launch: [claude-worker]\n")
	cfg, err := LoadConfig(fx.path)
	if err != nil {
		t.Fatal(err)
	}
	fx.p.cfg = cfg
	fx.step()
	fx.write(reloadBase)
	fx.step()
	if got := strings.Join(fx.registered(), ","); got != "iris" {
		t.Fatalf("registration after removing billie = %q", got)
	}
	if _, ok := fx.p.recipient("id-billie"); ok {
		t.Fatal("a removed worker still resolves as a recipient")
	}
	if ev := fx.kinds("config-reloaded"); len(ev) != 1 || !strings.Contains(ev[0].Detail, "workers removed: billie") {
		t.Fatalf("want one config-reloaded event naming billie, got %+v", ev)
	}
}

func TestAnInvalidEditChangesNothingAndIsSaidOnce2623(t *testing.T) {
	fx := newReloadFixture(t)
	fx.step()
	fx.write(reloadBase + "  billie:\n    family: claude\n") // no launch command: refused at start too
	fx.step()
	fx.step()
	if got := strings.Join(fx.registered(), ","); got != "iris" {
		t.Fatalf("an invalid edit changed the registration to %q", got)
	}
	ev := fx.kinds("config-reload-refused")
	if len(ev) != 1 || !strings.Contains(ev[0].Detail, "launch must name a command") || !strings.Contains(ev[0].Detail, "keeps the one it has") {
		t.Fatalf("want one refusal naming the cause, got %+v", ev)
	}
	// Mending the file applies it, and the same bad content later is reported again.
	fx.write(reloadBase + "  billie:\n    family: claude\n    launch: [claude-worker]\n")
	fx.step()
	if got := strings.Join(fx.registered(), ","); got != "billie,iris" {
		t.Fatalf("registration after mending = %q", got)
	}
}

func TestASettingReadOnlyAtStartIsNamedNotApplied2623(t *testing.T) {
	fx := newReloadFixture(t)
	fx.step()
	edited := strings.Replace(reloadBase, "poll_interval_seconds: 30", "poll_interval_seconds: 60", 1) +
		"  billie:\n    family: claude\n    launch: [claude-worker]\n"
	fx.write(edited)
	fx.step()
	if got := strings.Join(fx.registered(), ","); got != "billie,iris" {
		t.Fatalf("the worker change must still apply: %q", got)
	}
	if fx.p.cfg.PollIntervalSeconds != 30 {
		t.Fatalf("poll_interval_seconds was applied live: %d", fx.p.cfg.PollIntervalSeconds)
	}
	ev := fx.kinds("config-restart-needed")
	if len(ev) != 1 || !strings.Contains(ev[0].Detail, "poll_interval_seconds") || !strings.Contains(ev[0].Detail, "restart the poller") {
		t.Fatalf("want one restart-needed event naming the setting, got %+v", ev)
	}
}

func TestAWorkerThatNeedsOrcaLaunchWiringIsRefused2623(t *testing.T) {
	fx := newReloadFixture(t)
	fx.step()
	fx.write(reloadBase + `  billie:
    family: claude
    launch_via: orca
    launcher_profile: {family: claude, mode: short, model: opus, account: holger}
    disk_tmp: /tmp/billie
account_profiles:
  holger: {bin: claude-holger}
orca_launch:
  orca_bin: /usr/bin/orca
`)
	fx.step()
	if got := strings.Join(fx.registered(), ","); got != "iris" {
		t.Fatalf("registration = %q: an Orca-launched worker needs wiring made at start", got)
	}
	ev := fx.kinds("config-reload-refused")
	if len(ev) != 1 || !strings.Contains(ev[0].Detail, "restart") {
		t.Fatalf("want one refusal that says to restart, got %+v", ev)
	}
}

func TestACommentOnlyEditReloadsNothing2623(t *testing.T) {
	fx := newReloadFixture(t)
	fx.step()
	fx.write("# touched by an operator\n" + reloadBase)
	fx.step()
	if n := len(fx.kinds("config-reloaded")) + len(fx.kinds("config-reload-refused")) + len(fx.kinds("config-restart-needed")); n != 0 {
		t.Fatalf("a comment-only edit logged %d events", n)
	}
	fx.write("# touched\n" + reloadBase + "  billie:\n    family: claude\n    launch: [claude-worker]\n")
	fx.step()
	if got := strings.Join(fx.registered(), ","); got != "billie,iris" {
		t.Fatalf("the next real edit must still apply: %q", got)
	}
}

// Readers on other goroutines (a carried-out operation, delivery) race a
// reload only through the lock: run with -race to see it enforced.
func TestReloadAndReadersDoNotRace2623(t *testing.T) {
	fx := newReloadFixture(t)
	fx.step()
	done := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
					_ = fx.p.cfg.WorkerRefs()
					_, _ = fx.p.cfg.Worker("iris")
					_, _ = fx.p.cfg.Repo("acme/repo")
					_ = fx.p.cfg.Recipient(fx.p.state, "iris", "id-iris")
				}
			}
		}()
	}
	for i := 0; i < 40; i++ {
		extra := ""
		if i%2 == 0 {
			extra = "  billie:\n    family: claude\n    launch: [claude-worker]\n"
		}
		fx.write(reloadBase + extra)
		fx.step()
	}
	close(done)
	wg.Wait()
}
