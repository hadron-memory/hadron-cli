package team

// hadron-cli#3301 fix head (BLIND on C3 at 3263edd0): two stored hosts with one host id never share a state
// directory (P2-1); a brief Orca outage keeps the worktrees Orca last answered and is said once per outage
// (P2-4, P3-3); delivery is wired even when the row starts with it off (P2-3); every stored key is scrubbed,
// including those of hosts --app leaves out (P3-1).

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/auth"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/config"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/hostcred"
	"github.com/hadron-memory/hadron-cli/internal/hostfacts"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func TestHostRunPlatformRefusesASecondHostWithTheSameHostID3301(t *testing.T) {
	srv1, _ := goodServer3237(t, "app-1", "host-1")
	srv2, calls2 := goodServer3237(t, "app-2", "host-1")
	w := withDeps3237(t, []hostcred.Entry{
		{Server: srv1.URL, AppID: "app-1", HostID: "host-1", Key: key1},
		{Server: srv2.URL, AppID: "app-2", HostID: "host-1", Key: key2},
	})
	out, errOut, err := run3237(t, false, "--once")
	if exitcode.FromError(err) != exitcode.Conflict {
		t.Fatalf("the first host runs and the second's refusal is the --once exit (5): %v\n%s\n%s", err, out, errOut)
	}
	if len(w.pollers) != 1 {
		t.Fatalf("one poller per host id; got %d", len(w.pollers))
	}
	if !strings.Contains(out+errOut, "host-not-started") || !strings.Contains(out+errOut, "same host id") {
		t.Errorf("the second host is reported as not started, saying why:\n%s\n%s", out, errOut)
	}
	if c := calls2(); len(c) != 0 {
		t.Errorf("the second host is refused before any request; it sent %d", len(c))
	}
	if strings.Contains(out+errOut, key1) || strings.Contains(out+errOut, key2) {
		t.Error("a key reached a stream")
	}
}

func TestHostRunPlatformScrubsTheKeysOfHostsItDoesNotServe3301(t *testing.T) {
	srv1, _ := goodServer3237(t, "app-1", "host-1")
	w := withDeps3237(t, []hostcred.Entry{
		{Server: srv1.URL, AppID: "app-1", HostID: "host-1", Key: key1},
		{Server: "https://other.example", AppID: "app-2", HostID: "host-2", Key: key2},
	})
	t.Setenv("HADRON_TOKEN", "hdr_person_must_not_be_used")
	io, _, _ := output.Test()
	out, errOut := &strings.Builder{}, &strings.Builder{}
	io.Out, io.ErrOut = output.Tracked(out), errOut
	c := newCmdTeamHostRun(&cmdutil.Factory{IOStreams: io, AppFlag: "app-1"})
	c.SetArgs([]string{"--once"})
	c.SetOut(out)
	c.SetErr(errOut)
	if err := c.Execute(); err != nil {
		t.Fatalf("run --once --app app-1: %v\n%s\n%s", err, out, errOut)
	}
	if len(w.pollers) != 1 {
		t.Fatalf("want one poller, got %d", len(w.pollers))
	}
	if got := strings.Join(w.pollers[0].ScrubKeys(), " "); !strings.Contains(got, key2) {
		t.Error("the key of a stored host --app leaves out is still scrubbed from this host's agents")
	}
}

func TestHostRunPlatformWiresDeliveryWhenTheRowStartsOff3301(t *testing.T) {
	srv, _ := fakeServer3237(t, func(op string, _ json.RawMessage) string {
		switch op {
		case "HostRunPlatform":
			return strings.Replace(platformAnswer3237("app-1", "host-1", "SERVER"), `"deliveryEnabled":true`, `"deliveryEnabled":false`, 1)
		case "RegisterHost", "RegisterHostWithRefused":
			return registerAnswer3237("host-1")
		case "CollectHostOperations":
			return `{"data":{"collectHostOperations":[]}}`
		}
		return ""
	})
	withDeps3237(t, []hostcred.Entry{{Server: srv.URL, AppID: "app-1", HostID: "host-1", Key: key1}})
	var wired atomic.Int32
	inner := platformDepsFor
	platformDepsFor = func() platformDeps {
		d := inner()
		d.delivery = func(*hostpoller.Poller, *hostpoller.Config, *hostServer, func(hostpoller.Event)) { wired.Add(1) }
		return d
	}
	t.Cleanup(func() { platformDepsFor = inner })
	if out, errOut, err := run3237(t, false, "--once"); err != nil {
		t.Fatalf("run --once: %v\n%s\n%s", err, out, errOut)
	}
	if wired.Load() != 1 {
		t.Fatalf("delivery is wired once even with the row off, so turning it on needs no restart; wired %d times", wired.Load())
	}
}

// An Orca that stops answering keeps the worktrees it last answered, so a live agent's tree keeps its path, and
// the outage is said once, with its end said once too.
func TestAnOrcaOutageKeepsTheLastWorktreesAndIsSaidOnce3301(t *testing.T) {
	var resp gen.HostRunPlatformResponse
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(platformAnswer3237("app-1", "host-1", "SERVER")), &env); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(env.Data, &resp); err != nil {
		t.Fatal(err)
	}
	up := atomic.Bool{}
	up.Store(true)
	good := facts3237(t)
	facts := hostfacts.New(func(ctx context.Context, argv []string) orcaterm.Result {
		if !up.Load() {
			return orcaterm.Result{Code: 7, Stderr: "orca: not running"}
		}
		return good.Run(ctx, argv)
	})
	facts.LookPath = good.LookPath
	var events []hostpoller.Event
	emit := func(e hostpoller.Event) { events = append(events, e) }
	e := hostcred.Entry{Server: "https://h.example", AppID: "app-1", HostID: "host-1", Key: key1}
	last := &lastFacts{}
	build := func() *hostpoller.Config {
		return platformConfig(context.Background(), &resp, e, facts, "/opt/orca/bin/orca", t.TempDir(), emit, last)
	}
	judy := func(c *hostpoller.Config) string {
		w, ok := c.Worker("wkr-judy")
		if !ok || len(w.Worktrees) != 1 {
			return ""
		}
		return c.WorktreePath(w.Worktrees[0])
	}
	if got := judy(build()); got != "/home/u/orca/workspaces/hadron-cli/judy" {
		t.Fatalf("with Orca up the workdir resolves: %q", got)
	}
	up.Store(false)
	for i := 0; i < 3; i++ {
		if got := judy(build()); got != "/home/u/orca/workspaces/hadron-cli/judy" {
			t.Fatalf("poll %d of the outage: the last answered worktree is kept, got %q", i+1, got)
		}
	}
	up.Store(true)
	build()
	build()
	kinds := []string{}
	for _, ev := range events {
		kinds = append(kinds, ev.Kind)
	}
	if strings.Join(kinds, ",") != "facts-unavailable,facts-available" {
		t.Fatalf("one facts-unavailable per outage and one facts-available at its end; got %v", kinds)
	}
}

// At a start with Orca silent there is nothing to keep: the workers register with no worktree.
func TestAnOrcaSilentAtStartRegistersNoWorktree3301(t *testing.T) {
	var resp gen.HostRunPlatformResponse
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	_ = json.Unmarshal([]byte(platformAnswer3237("app-1", "host-1", "SERVER")), &env)
	if err := json.Unmarshal(env.Data, &resp); err != nil {
		t.Fatal(err)
	}
	facts := hostfacts.New(func(context.Context, []string) orcaterm.Result { return orcaterm.Result{Code: 7} })
	facts.LookPath = func(string) (string, error) { return "/opt/orca/bin/orca", nil }
	var events []hostpoller.Event
	c := platformConfig(context.Background(), &resp, hostcred.Entry{AppID: "app-1", HostID: "host-1"}, facts, "/opt/orca/bin/orca", t.TempDir(), func(e hostpoller.Event) { events = append(events, e) }, &lastFacts{})
	if w, _ := c.Worker("wkr-judy"); len(w.Worktrees) != 0 {
		t.Fatalf("no worktree registers before Orca has answered: %v", w.Worktrees)
	}
	if len(events) != 1 || events[0].Kind != "facts-unavailable" || !strings.Contains(events[0].Detail, "register with no worktree") {
		t.Fatalf("said once, naming the effect: %+v", events)
	}
}

// BLIND delta N1: the state directory is claimed by server and App, so a host of the same id from another App
// is refused in another process too, before anything is registered.
func TestHostRunPlatformRefusesAStateDirectoryAnotherHostClaimed3301(t *testing.T) {
	srv, calls := goodServer3237(t, "app-1", "host-1")
	withDeps3237(t, []hostcred.Entry{{Server: srv.URL, AppID: "app-1", HostID: "host-1", Key: key1}})
	if _, _, err := run3237(t, false, "--once"); err != nil {
		t.Fatalf("the first run claims the directory: %v", err)
	}
	dir, err := config.HostStateDir("host-1")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, hostIdentityFile))
	if err != nil || !strings.Contains(string(raw), `"appId":"app-1"`) {
		t.Fatalf("the claim names the App: %s, %v", raw, err)
	}
	if _, _, err := run3237(t, false, "--once"); err != nil {
		t.Fatalf("the same host runs again on its own claim: %v", err)
	}
	// LOCAL delta P3-2 (c): the claim is by server and App; this App's id on another server is not this host.
	if err := os.WriteFile(filepath.Join(dir, hostIdentityFile), []byte(`{"server":"other.example","appId":"app-1"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run3237(t, false, "--once"); exitcode.FromError(err) != exitcode.Conflict {
		t.Fatalf("a claim of this App's id on another server is refused, exit 5: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, hostIdentityFile), []byte(`{"server":"other.example","appId":"app-9"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := len(calls())
	_, errOut, err := run3237(t, false, "--once")
	// app-9 is not stored here, so `team host leave` cannot clear its claim: the refusal names the directory
	// to remove instead (LOCAL delta D2).
	if exitcode.FromError(err) != exitcode.Conflict || !strings.Contains(err.Error(), "app-9") || !strings.Contains(err.Error(), "no longer holds") || !strings.Contains(err.Error(), dir) || strings.Contains(err.Error(), "team host leave") {
		t.Fatalf("a claim of a host no longer stored is refused, exit 5, naming the directory to remove: %v\n%s", err, errOut)
	}
	for _, c := range calls()[before:] {
		if c.op != "HostRunPlatform" {
			t.Errorf("nothing past the platform read is sent: %s", c.op)
		}
	}
}

// BLIND delta P2-3, through the production wireDelivery: a row that starts with delivery off still has it wired.
func TestHostRunPlatformRealWireDeliveryWiresWithTheRowOff3301(t *testing.T) {
	srv, _ := fakeServer3237(t, func(op string, _ json.RawMessage) string {
		switch op {
		case "HostRunPlatform":
			return strings.Replace(platformAnswer3237("app-1", "host-1", "SERVER"), `"deliveryEnabled":true`, `"deliveryEnabled":false`, 1)
		case "RegisterHost", "RegisterHostWithRefused":
			return registerAnswer3237("host-1")
		case "CollectHostOperations":
			return `{"data":{"collectHostOperations":[]}}`
		}
		return ""
	})
	w := withDeps3237(t, []hostcred.Entry{{Server: srv.URL, AppID: "app-1", HostID: "host-1", Key: key1}})
	inner := platformDepsFor
	platformDepsFor = func() platformDeps {
		d := inner()
		d.delivery = func(p *hostpoller.Poller, cfg *hostpoller.Config, s *hostServer, emit func(hostpoller.Event)) {
			wireDelivery(p, cfg, s, emit)
		}
		return d
	}
	t.Cleanup(func() { platformDepsFor = inner })
	if out, errOut, err := run3237(t, false, "--once"); err != nil {
		t.Fatalf("run --once: %v\n%s\n%s", err, out, errOut)
	}
	if len(w.pollers) != 1 || !w.pollers[0].DeliveryEnabled() {
		t.Fatal("the real wireDelivery wires delivery with the row off, so turning it on needs no restart")
	}
}

// BLIND delta N-A: a first entry refused for good does not hold its host id, so the claim's holder, stored
// after it, still runs.
func TestHostRunPlatformAFirstHostRefusedForGoodLeavesItsIDToTheClaimHolder3301(t *testing.T) {
	srv1, _ := goodServer3237(t, "app-1", "host-1")
	srv2, calls2 := goodServer3237(t, "app-2", "host-1")
	w := withDeps3237(t, []hostcred.Entry{
		{Server: srv1.URL, AppID: "app-1", HostID: "host-1", Key: key1},
		{Server: srv2.URL, AppID: "app-2", HostID: "host-1", Key: key2},
	})
	dir, err := config.EnsureHostStateDir("host-1")
	if err != nil {
		t.Fatal(err)
	}
	claim, _ := json.Marshal(hostIdentity{Server: strings.ToLower(auth.Host(srv2.URL)), AppID: "app-2"})
	if err := os.WriteFile(filepath.Join(dir, hostIdentityFile), append(claim, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	out, errOut, err := run3237(t, false, "--once")
	if exitcode.FromError(err) != exitcode.Conflict {
		t.Fatalf("the first host's claim refusal is the --once exit (5): %v\n%s\n%s", err, out, errOut)
	}
	if !strings.Contains(out+errOut+err.Error(), "team host leave") {
		t.Errorf("a claim held by a stored host names `team host leave`:\n%s\n%s\n%v", out, errOut, err)
	}
	if len(w.pollers) != 1 {
		t.Fatalf("the claim holder runs; got %d pollers\n%s\n%s", len(w.pollers), out, errOut)
	}
	if strings.Contains(out+errOut, "same host id") {
		t.Errorf("the claim holder is not refused as a duplicate of a host that never started:\n%s\n%s", out, errOut)
	}
	if len(calls2()) == 0 {
		t.Error("the claim holder never reached its server")
	}
}

// BLIND delta N-B / SECOND P3: of two claims of one unclaimed directory at once, exactly one wins.
func TestClaimHostStateHasOneWinnerUnderARace3301(t *testing.T) {
	a := hostcred.Entry{Server: "https://a.example", AppID: "app-a", HostID: "host-1"}
	b := hostcred.Entry{Server: "https://b.example", AppID: "app-b", HostID: "host-1"}
	for i := range 200 {
		dir := t.TempDir()
		var wins atomic.Int32
		start := make(chan struct{})
		done := make(chan error, 2)
		for _, e := range []hostcred.Entry{a, b} {
			go func() {
				<-start
				err := claimHostState(dir, e, []hostcred.Entry{a, b})
				if err == nil {
					wins.Add(1)
				}
				done <- err
			}()
		}
		close(start)
		for range 2 {
			if err := <-done; err != nil && exitcode.FromError(err) != exitcode.Conflict {
				t.Fatalf("round %d: the loser reads the winner's claim and is refused with exit 5, not %v", i, err)
			}
		}
		if n := wins.Load(); n != 1 {
			t.Fatalf("round %d: %d claims won; exactly one may", i, n)
		}
		left, _ := filepath.Glob(filepath.Join(dir, ".host-identity-*"))
		if len(left) != 0 {
			t.Fatalf("round %d: temporary stamps left behind: %v", i, left)
		}
	}
}

// BLIND delta N-A at 10bf3a7b: a first entry whose server is down is refused by the other host's claim before
// its platform read, so it never takes the id and the claim holder runs.
func TestHostRunPlatformATransientFirstHostDoesNotBlockTheClaimHolder3301(t *testing.T) {
	fastBackoff3301(t)
	var reads1 atomic.Int32
	down := transientServer3301(t, &reads1)
	srv2, calls2 := goodServer3237(t, "app-2", "host-1")
	w := withDeps3237(t, []hostcred.Entry{
		{Server: down, AppID: "app-1", HostID: "host-1", Key: key1},
		{Server: srv2.URL, AppID: "app-2", HostID: "host-1", Key: key2},
	})
	dir, err := config.EnsureHostStateDir("host-1")
	if err != nil {
		t.Fatal(err)
	}
	claim, _ := json.Marshal(hostIdentity{Server: strings.ToLower(auth.Host(srv2.URL)), AppID: "app-2"})
	if err := os.WriteFile(filepath.Join(dir, hostIdentityFile), append(claim, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	out, errOut, err := run3237(t, false, "--once")
	if exitcode.FromError(err) != exitcode.Conflict {
		t.Fatalf("the first host's claim refusal is the --once exit (5): %v\n%s\n%s", err, out, errOut)
	}
	if reads1.Load() != 0 {
		t.Errorf("the claim is judged before the platform read; the first host read %d time(s)", reads1.Load())
	}
	if len(w.pollers) != 1 || len(calls2()) == 0 || strings.Contains(out+errOut, "same host id") {
		t.Fatalf("the claim holder runs, not refused as a duplicate: %d poller(s)\n%s\n%s", len(w.pollers), out, errOut)
	}
}

// BLIND P2-1 (same host) and D-1: a running host holds its run lock, so a second `host run` of it is refused
// (exit 5) and `team host leave` keeps its directory; the lock is released when the run ends.
func TestHostRunPlatformHoldsARunLockForItsLife3301(t *testing.T) {
	srv, _ := goodServer3237(t, "app-1", "host-1")
	e := hostcred.Entry{Server: srv.URL, AppID: "app-1", HostID: "host-1", Key: key1}
	withDeps3237(t, []hostcred.Entry{e})
	if _, _, err := run3237(t, false, "--once"); err != nil {
		t.Fatalf("first run: %v", err)
	}
	lk, err := lockHostRun(e)
	if err != nil {
		t.Fatalf("the run released its lock when it ended: %v", err)
	}
	_, errOut, err := run3237(t, false, "--once")
	if exitcode.FromError(err) != exitcode.Conflict || !strings.Contains(err.Error(), "already running this host") {
		t.Fatalf("a second run of a running host is refused, exit 5: %v\n%s", err, errOut)
	}
	released, err := releaseHostState(e)
	if released || err == nil || !strings.Contains(err.Error(), "still running") {
		t.Fatalf("leave keeps a running host's directory: released %v, %v", released, err)
	}
	dir, _ := config.HostStateDir("host-1")
	if _, err := os.Stat(filepath.Join(dir, hostIdentityFile)); err != nil {
		t.Fatalf("the directory is kept whole: %v", err)
	}
	_ = lk.Unlock()
	if released, err := releaseHostState(e); !released || err != nil {
		t.Fatalf("with no run, leave releases the directory: %v, %v", released, err)
	}
}

// BLIND delta D-2: on a file system without hard links the claim falls back to an exclusive create, still with
// one winner.
func TestClaimHostStateWithoutHardLinks3301(t *testing.T) {
	old := linkFile
	linkFile = func(string, string) error { return &os.LinkError{Op: "link", Err: errors.ErrUnsupported} }
	t.Cleanup(func() { linkFile = old })
	a := hostcred.Entry{Server: "https://a.example", AppID: "app-a", HostID: "host-1"}
	b := hostcred.Entry{Server: "https://b.example", AppID: "app-b", HostID: "host-1"}
	dir := t.TempDir()
	if err := claimHostState(dir, a, []hostcred.Entry{a, b}); err != nil {
		t.Fatalf("the fallback claims: %v", err)
	}
	if err := claimHostState(dir, a, []hostcred.Entry{a, b}); err != nil {
		t.Fatalf("its own claim again: %v", err)
	}
	if err := claimHostState(dir, b, []hostcred.Entry{a, b}); exitcode.FromError(err) != exitcode.Conflict {
		t.Fatalf("another host is refused: %v", err)
	}
	// Raced, the exclusive create still has exactly one winner.
	for i := range 200 {
		dir := t.TempDir()
		var wins atomic.Int32
		start := make(chan struct{})
		done := make(chan struct{}, 2)
		for _, e := range []hostcred.Entry{a, b} {
			go func() {
				<-start
				if claimHostState(dir, e, []hostcred.Entry{a, b}) == nil {
					wins.Add(1)
				}
				done <- struct{}{}
			}()
		}
		close(start)
		<-done
		<-done
		if n := wins.Load(); n != 1 {
			t.Fatalf("round %d: %d claims won without hard links; exactly one may", i, n)
		}
	}
}

// QA P3 at 10bf3a7b: the claim compares the App as well: another App's claim on this server is not this host's.
func TestClaimHostStateComparesTheApp3301(t *testing.T) {
	a := hostcred.Entry{Server: "https://a.example", AppID: "app-a", HostID: "host-1"}
	b := hostcred.Entry{Server: "https://a.example", AppID: "app-b", HostID: "host-1"}
	dir := t.TempDir()
	if err := claimHostState(dir, a, []hostcred.Entry{a, b}); err != nil {
		t.Fatal(err)
	}
	if err := claimHostState(dir, b, []hostcred.Entry{a, b}); exitcode.FromError(err) != exitcode.Conflict {
		t.Fatalf("another App on the same server is refused: %v", err)
	}
}

// LOCAL delta N1 at 10bf3a7b: a stored host that --app leaves out still holds its claim, so the refusal names
// `team host leave`, not a directory to remove.
func TestHostRunPlatformAClaimOfAHostAppLeavesOutIsLive3301(t *testing.T) {
	srv, _ := goodServer3237(t, "app-1", "host-1")
	other := "https://other.example"
	withDeps3237(t, []hostcred.Entry{
		{Server: srv.URL, AppID: "app-1", HostID: "host-1", Key: key1},
		{Server: other, AppID: "app-2", HostID: "host-1", Key: key2},
	})
	dir, err := config.EnsureHostStateDir("host-1")
	if err != nil {
		t.Fatal(err)
	}
	claim, _ := json.Marshal(identityOf(hostcred.Entry{Server: other, AppID: "app-2"}))
	if err := os.WriteFile(filepath.Join(dir, hostIdentityFile), append(claim, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	io, _, _ := output.Test()
	out, errOut := &strings.Builder{}, &strings.Builder{}
	io.Out, io.ErrOut = output.Tracked(out), errOut
	c := newCmdTeamHostRun(&cmdutil.Factory{IOStreams: io, AppFlag: "app-1"})
	c.SetArgs([]string{"--once"})
	c.SetOut(out)
	c.SetErr(errOut)
	err = c.Execute()
	if exitcode.FromError(err) != exitcode.Conflict || !strings.Contains(err.Error(), "team host leave") || strings.Contains(err.Error(), "no longer holds") {
		t.Fatalf("the claim of a stored host --app leaves out is live, naming `team host leave`: %v", err)
	}
}

// LOCAL delta N1 at 10bf3a7b: while a first entry retries a transient start it holds its id (the directory is
// unclaimed), so a second entry of that id is refused as a duplicate, never run beside it.
func TestHostRunPlatformATransientStartHoldsItsID3301(t *testing.T) {
	fastBackoff3301(t)
	var reads atomic.Int32
	down := transientServer3301(t, &reads)
	srv2, calls2 := goodServer3237(t, "app-2", "host-1")
	w := withDeps3237(t, []hostcred.Entry{
		{Server: down, AppID: "app-1", HostID: "host-1", Key: key1},
		{Server: srv2.URL, AppID: "app-2", HostID: "host-1", Key: key2},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	out, _ := runCtx3301(t, ctx)
	if len(w.pollers) != 0 || len(calls2()) != 0 || !strings.Contains(out, "same host id") {
		t.Fatalf("the second host of the id is refused while the first retries: %d poller(s), %d call(s)\n%s", len(w.pollers), len(calls2()), out)
	}
}

// LOCAL P3-2 (d): an identity file that names no host is refused as unreadable (exit 1), never adopted.
func TestClaimHostStateRefusesAnUnreadableClaim3301(t *testing.T) {
	a := hostcred.Entry{Server: "https://a.example", AppID: "app-a", HostID: "host-1"}
	for _, body := range []string{"not json", `{"server":"","appId":"app-a"}`, `{"server":"a.example"}`} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, hostIdentityFile), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		err := claimHostState(dir, a, []hostcred.Entry{a})
		if exitcode.FromError(err) != exitcode.Error || !strings.Contains(err.Error(), "does not say which host") {
			t.Fatalf("%q is refused as unreadable: %v", body, err)
		}
	}
}

// LOCAL delta N1 at 10bf3a7b: leave releases only a directory claimed by this server as well as this App.
func TestReleaseHostStateComparesTheServer3301(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	e := hostcred.Entry{Server: "https://a.example", AppID: "app-a", HostID: "host-1"}
	dir, err := config.EnsureHostStateDir("host-1")
	if err != nil {
		t.Fatal(err)
	}
	claim, _ := json.Marshal(hostIdentity{Server: "b.example", AppID: "app-a"})
	if err := os.WriteFile(filepath.Join(dir, hostIdentityFile), claim, 0o600); err != nil {
		t.Fatal(err)
	}
	if released, err := releaseHostState(e); released || err != nil {
		t.Fatalf("this App's claim on another server is kept: %v, %v", released, err)
	}
	// SEC10 D at 10bf3a7b: a directory with no claim is kept too.
	if err := os.Remove(filepath.Join(dir, hostIdentityFile)); err != nil {
		t.Fatal(err)
	}
	if released, err := releaseHostState(e); released || err != nil {
		t.Fatalf("an unclaimed directory is kept: %v, %v", released, err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("the unclaimed directory is still there: %v", err)
	}
}

// BLIND delta R-1 at d210fc01: the run lock is taken before the platform read, so a second run of a running host
// is refused without reading the platform, claiming or building a poller.
func TestHostRunPlatformLocksBeforeThePlatformRead3301(t *testing.T) {
	srv, calls := goodServer3237(t, "app-1", "host-1")
	e := hostcred.Entry{Server: srv.URL, AppID: "app-1", HostID: "host-1", Key: key1}
	w := withDeps3237(t, []hostcred.Entry{e})
	lk, err := lockHostRun(e)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lk.Unlock() }()
	_, errOut, err := run3237(t, false, "--once")
	if exitcode.FromError(err) != exitcode.Conflict || !strings.Contains(err.Error(), "already running this host") {
		t.Fatalf("a run of a running host is refused, exit 5: %v\n%s", err, errOut)
	}
	if n := len(calls()); n != 0 {
		t.Errorf("refused before the platform read; the server saw %d call(s)", n)
	}
	if len(w.pollers) != 0 {
		t.Errorf("no poller is built for a refused run: %d", len(w.pollers))
	}
	dir, _ := config.HostStateDir("host-1")
	if _, err := os.Stat(filepath.Join(dir, hostIdentityFile)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a refused run claims nothing: %v", err)
	}
}

// A start that fails after the lock releases it, so the retried start (or another run) can take it.
func TestHostRunPlatformAFailedStartReleasesTheLock3301(t *testing.T) {
	var reads atomic.Int32
	down := transientServer3301(t, &reads)
	e := hostcred.Entry{Server: down, AppID: "app-1", HostID: "host-1", Key: key1}
	withDeps3237(t, []hostcred.Entry{e})
	if _, _, err := run3237(t, false, "--once"); err == nil {
		t.Fatal("a host whose server is down does not start under --once")
	}
	if reads.Load() == 0 {
		t.Fatal("the start reached the platform read")
	}
	lk, err := lockHostRun(e)
	if err != nil {
		t.Fatalf("the failed start released its lock: %v", err)
	}
	_ = lk.Unlock()
}

// BLIND delta R-3 at d210fc01: only a file system without hard links falls back; another link failure fails the
// claim and creates nothing.
func TestClaimHostStateFallsBackOnlyWithoutHardLinks3301(t *testing.T) {
	old := linkFile
	t.Cleanup(func() { linkFile = old })
	a := hostcred.Entry{Server: "https://a.example", AppID: "app-a", HostID: "host-1"}
	for _, c := range []struct {
		err      error
		fallback bool
	}{
		{syscall.EIO, false},
		{syscall.ENOSPC, false},
		{errors.ErrUnsupported, true},
		{syscall.EPERM, true},
		{syscall.EMLINK, true},
	} {
		linkFile = func(string, string) error { return &os.LinkError{Op: "link", Err: c.err} }
		dir := t.TempDir()
		err := claimHostState(dir, a, []hostcred.Entry{a})
		_, statErr := os.Stat(filepath.Join(dir, hostIdentityFile))
		if c.fallback {
			if err != nil || statErr != nil {
				t.Errorf("%v: falls back and claims: %v, %v", c.err, err, statErr)
			}
			continue
		}
		if err == nil || !errors.Is(statErr, os.ErrNotExist) {
			t.Errorf("%v: no fallback, the claim fails and nothing is written: %v, %v", c.err, err, statErr)
		}
	}
}

// BLIND delta R-2 at d210fc01: a failed fallback write removes its short claim, so a later run is not refused for
// good by an unreadable stamp.
func TestClaimHostStateFallbackFailedWriteLeavesNoClaim3301(t *testing.T) {
	oldLink, oldWrite := linkFile, writeClaim
	t.Cleanup(func() { linkFile, writeClaim = oldLink, oldWrite })
	linkFile = func(string, string) error { return &os.LinkError{Op: "link", Err: errors.ErrUnsupported} }
	writeClaim = func(f *os.File, b []byte) error {
		_, _ = f.Write(b[:len(b)/2])
		return syscall.ENOSPC
	}
	a := hostcred.Entry{Server: "https://a.example", AppID: "app-a", HostID: "host-1"}
	dir := t.TempDir()
	if err := claimHostState(dir, a, []hostcred.Entry{a}); err == nil {
		t.Fatal("a failed write fails the claim")
	}
	if _, err := os.Stat(filepath.Join(dir, hostIdentityFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the short claim is removed: %v", err)
	}
	writeClaim = oldWrite
	if err := claimHostState(dir, a, []hostcred.Entry{a}); err != nil {
		t.Fatalf("a later claim succeeds: %v", err)
	}
}
