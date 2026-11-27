package team

// hadron-cli#3301 fix head (BLIND on C3 at 3263edd0): two stored hosts with one host id never share a state
// directory (P2-1); a brief Orca outage keeps the worktrees Orca last answered and is said once per outage
// (P2-4, P3-3); delivery is wired even when the row starts with it off (P2-3); every stored key is scrubbed,
// including those of hosts --app leaves out (P3-1).

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
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
	if err := os.WriteFile(filepath.Join(dir, hostIdentityFile), []byte(`{"server":"other.example","appId":"app-9"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := len(calls())
	_, errOut, err := run3237(t, false, "--once")
	if exitcode.FromError(err) != exitcode.Conflict || !strings.Contains(err.Error(), "app-9") || !strings.Contains(err.Error(), "team host leave") {
		t.Fatalf("another host's claim is refused, exit 5, naming it and the fix: %v\n%s", err, errOut)
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
