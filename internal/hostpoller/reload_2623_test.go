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
// A new content is applied on the second poll that sees it (so a half-written
// file never is), hence the two steps after each edit.

const reloadBase = `app: hrn:app:acme.com:team
label: box1
poll_interval_seconds: 30
key_file: %KEY%
worktrees_root: %ROOT%
state_file: %ROOT%/state.json
repos:
  acme/repo: %ROOT%/clone
workers:
  iris:
    family: claude
    launch: [claude-worker]
`

const billie = "  billie:\n    family: claude\n    launch: [claude-worker]\n"

type reloadFixture struct {
	t      *testing.T
	path   string
	dir    string
	p      *Poller
	srv    *fakeServer
	mu     sync.Mutex
	events []Event
}

func newReloadFixture(t *testing.T) *reloadFixture { return newReloadFixtureFrom(t, reloadBase) }

func newReloadFixtureFrom(t *testing.T, text string) *reloadFixture {
	t.Helper()
	dir := t.TempDir()
	key := filepath.Join(dir, "host.key")
	if err := os.WriteFile(key, []byte(hostKey), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "clone"), 0o755); err != nil {
		t.Fatal(err)
	}
	fx := &reloadFixture{t: t, dir: dir, path: filepath.Join(dir, "host.yaml")}
	fx.write(text)
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

// edit writes text and polls twice: the first sight of new content, then the
// poll that applies it.
func (fx *reloadFixture) edit(text string) {
	fx.t.Helper()
	fx.write(text)
	fx.step()
	fx.step()
}

// registered is the worker names of the newest registration.
func (fx *reloadFixture) registered() string {
	fx.srv.mu.Lock()
	defer fx.srv.mu.Unlock()
	var out []string
	for _, w := range fx.srv.regs[len(fx.srv.regs)-1].Workers {
		out = append(out, w.Worker)
	}
	return strings.Join(out, ",")
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

func (fx *reloadFixture) quiet() bool {
	return len(fx.kinds("config-reloaded"))+len(fx.kinds("config-reload-refused"))+len(fx.kinds("config-restart-needed")) == 0
}

func TestRunningPollerPicksUpAWorkerAddedToItsConfig2623(t *testing.T) {
	fx := newReloadFixture(t)
	fx.step()
	if got := fx.registered(); got != "iris" {
		t.Fatalf("first registration = %q, want iris", got)
	}
	fx.edit(reloadBase + billie)
	if got := fx.registered(); got != "billie,iris" {
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

func TestAHalfWrittenFileIsNeverApplied2623(t *testing.T) {
	fx := newReloadFixture(t)
	fx.step()
	// A truncating write that has not finished: the file is empty or partial
	// when the poller looks first, and whole when it looks next.
	fx.write("app: hrn:app:acme.com:team\n")
	fx.step()
	fx.write(reloadBase + billie)
	fx.step()
	if got := fx.registered(); got != "iris" {
		t.Fatalf("content seen once was applied: %q", got)
	}
	fx.step()
	if got := fx.registered(); got != "billie,iris" {
		t.Fatalf("content seen on two polls was not applied: %q", got)
	}
	if n := len(fx.kinds("config-reload-refused")); n != 0 {
		t.Fatalf("the partial content must not have been refused either: %+v", fx.kinds("config-reload-refused"))
	}
}

func TestRunningPollerDropsARemovedWorker2623(t *testing.T) {
	fx := newReloadFixtureFrom(t, reloadBase+billie)
	fx.step()
	if got := fx.registered(); got != "billie,iris" {
		t.Fatalf("registration = %q", got)
	}
	fx.edit(reloadBase)
	if got := fx.registered(); got != "iris" {
		t.Fatalf("registration after removing billie = %q", got)
	}
	if _, ok := fx.p.recipient("id-billie"); ok {
		t.Fatal("a removed worker still resolves as a recipient")
	}
	if ev := fx.kinds("config-reloaded"); len(ev) != 1 || !strings.Contains(ev[0].Detail, "workers removed: billie") {
		t.Fatalf("want one config-reloaded event naming billie, got %+v", ev)
	}
}

// An operation issued for a worker the file no longer names is reported failed
// with the reason, never run with a zero-value worker.
func TestAnOperationForARemovedWorkerFailsWithAReason2623(t *testing.T) {
	fx := newReloadFixtureFrom(t, reloadBase+billie)
	fx.step()
	fx.edit(reloadBase)
	fx.srv.batches = [][]Operation{{{ID: "o1", Type: OpWorktreeCreate, WorkerID: "id-billie",
		Args: map[string]any{"repo": "acme/repo", "baseBranch": "main", "branchName": "b", "label": "x"}}}}
	fx.step()
	fx.p.Wait()
	fx.srv.mu.Lock()
	defer fx.srv.mu.Unlock()
	if len(fx.srv.reports) != 1 || fx.srv.reports[0].outcome != Failed || !strings.Contains(fx.srv.reports[0].reason, "not configured") {
		t.Fatalf("reports = %+v, want one FAILED naming the worker as not configured", fx.srv.reports)
	}
}

func TestAWorkerChangedInPlaceIsApplied2623(t *testing.T) {
	fx := newReloadFixture(t)
	fx.step()
	fx.edit(strings.Replace(reloadBase, "launch: [claude-worker]", "launch: [claude-worker, --model, opus]", 1))
	if w, _ := fx.p.config().Worker("iris"); strings.Join(w.Launch, " ") != "claude-worker --model opus" {
		t.Fatalf("launch = %v", w.Launch)
	}
	if ev := fx.kinds("config-reloaded"); len(ev) != 1 || !strings.Contains(ev[0].Detail, "workers changed: iris") {
		t.Fatalf("got %+v", ev)
	}
}

func TestChangedReposAndAccountsAreNamedInTheEvent2623(t *testing.T) {
	fx := newReloadFixture(t)
	fx.step()
	other := filepath.Join(fx.dir, "other")
	if err := os.Mkdir(other, 0o755); err != nil {
		t.Fatal(err)
	}
	fx.edit(strings.Replace(reloadBase, "acme/repo: %ROOT%/clone", "acme/repo: "+other, 1))
	if got, _ := fx.p.config().Repo("acme/repo"); got != other {
		t.Fatalf("repo = %q", got)
	}
	if ev := fx.kinds("config-reloaded"); len(ev) != 1 || !strings.Contains(ev[0].Detail, "also changed: repos") {
		t.Fatalf("got %+v", ev)
	}
}

func TestAnInvalidEditChangesNothingAndIsSaidOnce2623(t *testing.T) {
	fx := newReloadFixture(t)
	fx.step()
	bad := reloadBase + "  billie:\n    family: claude\n" // no launch command: refused at start too
	fx.edit(bad)
	fx.step()
	if got := fx.registered(); got != "iris" {
		t.Fatalf("an invalid edit changed the registration to %q", got)
	}
	ev := fx.kinds("config-reload-refused")
	if len(ev) != 1 || !strings.Contains(ev[0].Detail, "launch must name a command") || !strings.Contains(ev[0].Detail, "keeps the one it has") {
		t.Fatalf("want one refusal naming the cause, got %+v", ev)
	}
	// Mending the file applies it.
	fx.edit(reloadBase + billie)
	if got := fx.registered(); got != "billie,iris" {
		t.Fatalf("registration after mending = %q", got)
	}
	// The same bad content returning later is reported again.
	fx.edit(reloadBase)
	fx.edit(bad)
	if n := len(fx.kinds("config-reload-refused")); n != 2 {
		t.Fatalf("the same bad content must be reported again after the file was good: %d refusals", n)
	}
}

func TestAnUnreadableConfigFileKeepsTheRunningOneAndIsSaidOnce2623(t *testing.T) {
	fx := newReloadFixture(t)
	fx.step()
	if err := os.Remove(fx.path); err != nil {
		t.Fatal(err)
	}
	fx.step()
	fx.step()
	if got := fx.registered(); got != "iris" {
		t.Fatalf("registration = %q", got)
	}
	if ev := fx.kinds("config-reload-refused"); len(ev) != 1 || !strings.Contains(ev[0].Detail, "cannot be read") {
		t.Fatalf("want one refusal, got %+v", ev)
	}
	fx.write(reloadBase) // the content the poller holds: nothing to do, nothing to say
	fx.step()
	fx.step()
	if n := len(fx.kinds("config-reload-refused")); n != 1 {
		t.Fatalf("restoring the held content logged %d refusals", n)
	}
}

func TestADuplicatedWorktreeIsRefusedLikeAtStart2623(t *testing.T) {
	fx := newReloadFixture(t)
	fx.step()
	fx.edit(reloadBase + "    worktrees: [repo/x]\n" + billie + "    worktrees: [repo/x]\n")
	ev := fx.kinds("config-reload-refused")
	if len(ev) != 1 || !strings.Contains(ev[0].Detail, "belongs to one worker") {
		t.Fatalf("want a refusal naming the clash, got %+v", ev)
	}
}

func TestASettingReadOnlyAtStartIsNamedNotApplied2623(t *testing.T) {
	fx := newReloadFixture(t)
	fx.step()
	fx.edit(strings.Replace(reloadBase, "poll_interval_seconds: 30", "poll_interval_seconds: 60", 1) + billie)
	if got := fx.registered(); got != "billie,iris" {
		t.Fatalf("the worker change must still apply: %q", got)
	}
	if fx.p.config().PollIntervalSeconds != 30 {
		t.Fatalf("poll_interval_seconds was applied live: %d", fx.p.config().PollIntervalSeconds)
	}
	ev := fx.kinds("config-restart-needed")
	if len(ev) != 1 || !strings.Contains(ev[0].Detail, "poll_interval_seconds") || !strings.Contains(ev[0].Detail, "restart the poller") {
		t.Fatalf("want one restart-needed event naming the setting, got %+v", ev)
	}
	// What was applied is not also said to need a restart.
	for _, applied := range []string{"workers", "repos", "account_profiles"} {
		if strings.Contains(strings.SplitN(ev[0].Detail, ":", 2)[1], applied) {
			t.Fatalf("restart-needed names %q, which was applied: %q", applied, ev[0].Detail)
		}
	}
}

const reloadOrcaWorker = `  oscar:
    family: claude
    launch_via: orca
    launcher_profile: {family: claude, mode: short, model: opus, account: holger}
    disk_tmp: /tmp/oscar
account_profiles:
  holger: {bin: claude-holger}
orca_launch:
  orca_bin: /usr/bin/orca
`

func TestAWorkerThatNeedsOrcaLaunchWiringIsRefused2623(t *testing.T) {
	fx := newReloadFixture(t)
	fx.step()
	fx.edit(reloadBase + reloadOrcaWorker)
	if got := fx.registered(); got != "iris" {
		t.Fatalf("registration = %q: an Orca-launched worker needs wiring made at start", got)
	}
	ev := fx.kinds("config-reload-refused")
	if len(ev) != 1 || !strings.Contains(ev[0].Detail, "restart") {
		t.Fatalf("want one refusal that says to restart, got %+v", ev)
	}
}

// The wiring is judged by what the poller started with, not by the file now in
// force: an Orca worker removed and later re-added is not refused.
func TestAnOrcaWorkerRemovedAndReAddedIsNotRefused2623(t *testing.T) {
	fx := newReloadFixtureFrom(t, reloadBase+reloadOrcaWorker)
	fx.step()
	fx.edit(reloadBase + "orca_launch:\n  orca_bin: /usr/bin/orca\n")
	if strings.Contains(fx.registered(), "oscar") {
		t.Fatalf("oscar still registered: %q", fx.registered())
	}
	fx.edit(reloadBase + reloadOrcaWorker)
	if got := fx.registered(); got != "iris,oscar" {
		t.Fatalf("registration after re-adding = %q", got)
	}
	if n := len(fx.kinds("config-reload-refused")); n != 0 {
		t.Fatalf("re-adding a worker for wiring that exists was refused: %+v", fx.kinds("config-reload-refused"))
	}
}

func TestACommentOnlyEditReloadsNothing2623(t *testing.T) {
	fx := newReloadFixture(t)
	fx.step()
	fx.edit("# touched by an operator\n" + reloadBase)
	if !fx.quiet() {
		t.Fatalf("a comment-only edit logged events: %+v", fx.events)
	}
	fx.edit("# touched\n" + reloadBase + billie)
	if got := fx.registered(); got != "billie,iris" {
		t.Fatalf("the next real edit must still apply: %q", got)
	}
}

// Hot reload must be no weaker than a restart: a file anyone but the poller's
// own user can write is not applied.
func TestAGroupWritableConfigFileIsRefused2623(t *testing.T) {
	fx := newReloadFixture(t)
	fx.step()
	fx.write(reloadBase + billie)
	if err := os.Chmod(fx.path, 0o664); err != nil {
		t.Fatal(err)
	}
	fx.step()
	fx.step()
	fx.step()
	if got := fx.registered(); got != "iris" {
		t.Fatalf("a group-writable config was applied: %q", got)
	}
	ev := fx.kinds("config-reload-refused")
	if len(ev) != 1 || !strings.Contains(ev[0].Detail, "writable by group or others") || !strings.Contains(ev[0].Detail, "mode 664") {
		t.Fatalf("want one loud refusal naming the mode, got %+v", ev)
	}
	// Tightening the mode, with the same content, applies it.
	if err := os.Chmod(fx.path, 0o600); err != nil {
		t.Fatal(err)
	}
	fx.step()
	if got := fx.registered(); got != "billie,iris" {
		t.Fatalf("after chmod 600 the edit must apply: %q", got)
	}
}

func TestAChangedLaunchProgramThatOthersCanWriteIsRefused2623(t *testing.T) {
	fx := newReloadFixture(t)
	fx.step()
	prog := filepath.Join(fx.dir, "launcher")
	if err := os.WriteFile(prog, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(prog, 0o777); err != nil { // beyond the process umask
		t.Fatal(err)
	}
	edited := reloadBase + "  billie:\n    family: claude\n    launch: [" + prog + "]\n"
	fx.edit(edited)
	fx.step()
	if got := fx.registered(); got != "iris" {
		t.Fatalf("a world-writable launch program was applied: %q", got)
	}
	ev := fx.kinds("config-reload-refused")
	if len(ev) != 1 || !strings.Contains(ev[0].Detail, "launcher") || !strings.Contains(ev[0].Detail, "writable by group or others") {
		t.Fatalf("want a refusal naming the program, got %+v", ev)
	}
	if err := os.Chmod(prog, 0o755); err != nil {
		t.Fatal(err)
	}
	fx.step()
	if got := fx.registered(); got != "billie,iris" {
		t.Fatalf("after chmod 755 the edit must apply: %q", got)
	}
}

// An unchanged entry is not re-judged: a reload that only adds a worker does
// not refuse because of a program the poller already runs.
func TestAnUnchangedWorkersProgramIsNotRejudged2623(t *testing.T) {
	fx := newReloadFixtureFrom(t, strings.Replace(reloadBase, "[claude-worker]", "["+"%ROOT%"+"/old-launcher]", 1))
	prog := filepath.Join(fx.dir, "old-launcher")
	if err := os.WriteFile(prog, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(prog, 0o777); err != nil {
		t.Fatal(err)
	}
	fx.step()
	fx.edit(strings.Replace(reloadBase, "[claude-worker]", "["+"%ROOT%"+"/old-launcher]", 1) + billie)
	if got := fx.registered(); got != "billie,iris" {
		t.Fatalf("registration = %q: iris's unchanged program must not block the change", got)
	}
}

func TestEveryReloadableFieldIsARealYAMLField2623(t *testing.T) {
	for _, f := range reloadable {
		if yamlName(f) == f && f != "Workers" {
			t.Errorf("reloadable field %q has no yaml key: the classification names a field that does not exist", f)
		}
	}
	if len(fixedChanges(&Config{}, &Config{App: "x", PollIntervalSeconds: 1})) != 2 {
		t.Fatal("a field that is not classified reloadable must count as read-at-start")
	}
}

func TestPrintableDropsControlsAndCutsOnRunes2623(t *testing.T) {
	got := printable("bad\x1b[31m key\n\tvalue "+strings.Repeat("é", 500), 20)
	if strings.ContainsAny(got, "\x1b\n\t") || !strings.HasSuffix(got, "…") || len([]rune(got)) != 21 {
		t.Fatalf("printable = %q", got)
	}
}

// Readers on other goroutines (a carried-out operation, delivery) and a reload
// share only an atomic pointer and snapshots that never change: run with
// -race to see it hold.
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
					c := fx.p.config()
					for _, ref := range c.WorkerRefs() {
						_, _ = c.Worker(ref)
					}
					_, _ = c.Repo("acme/repo")
					_, _ = fx.p.recipient("id-iris")
				}
			}
		}()
	}
	for i := 0; i < 20; i++ {
		extra := ""
		if i%2 == 0 {
			extra = billie
		}
		fx.edit(reloadBase + extra)
	}
	close(done)
	wg.Wait()
}

// The file's owner and mode are judged on the file actually read: a symlink is
// followed once, at the open, and the target's mode is what counts.
func TestASymlinkedConfigIsJudgedByItsTarget2623(t *testing.T) {
	fx := newReloadFixture(t)
	fx.step()
	real := filepath.Join(fx.dir, "real.yaml")
	text := strings.NewReplacer("%KEY%", filepath.Join(fx.dir, "host.key"), "%ROOT%", fx.dir).Replace(reloadBase + billie)
	if err := os.WriteFile(real, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(real, 0o666); err != nil { // beyond the process umask
		t.Fatal(err)
	}
	if err := os.Remove(fx.path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, fx.path); err != nil {
		t.Fatal(err)
	}
	fx.step()
	fx.step()
	fx.step()
	if got := fx.registered(); got != "iris" {
		t.Fatalf("a symlink to a world-writable file was applied: %q", got)
	}
	ev := fx.kinds("config-reload-refused")
	if len(ev) != 1 || !strings.Contains(ev[0].Detail, "mode 666") {
		t.Fatalf("want one refusal naming the target's mode, got %+v", ev)
	}
}

func TestAnOversizedConfigFileIsRefused2623(t *testing.T) {
	fx := newReloadFixture(t)
	fx.step()
	fx.write(reloadBase + "# " + strings.Repeat("x", maxConfigBytes) + "\n")
	fx.step()
	fx.step()
	if got := fx.registered(); got != "iris" {
		t.Fatalf("registration = %q", got)
	}
	if ev := fx.kinds("config-reload-refused"); len(ev) != 1 || !strings.Contains(ev[0].Detail, "larger than") {
		t.Fatalf("got %+v", ev)
	}
}
