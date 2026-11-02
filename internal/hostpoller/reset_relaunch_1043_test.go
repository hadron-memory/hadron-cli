package hostpoller

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

func relaunchFixture(t *testing.T) (*resetFixture, *fakeHost) {
	t.Helper()
	x := newResetFixture(t)
	host := x.p.host.(*fakeHost)
	host.agent = &fakeAgent{done: make(chan struct{}), pid: 5151}
	return x, host
}

func irisCfg(x *resetFixture) WorkerConfig { return x.p.cfg.Workers["iris"] }

var oldIdentity = ProcessIdentity{PID: 4242, StartTime: "1"}

func TestRelaunchStartsTheConfiguredAgentWithoutAGrant1043(t *testing.T) {
	x, host := relaunchFixture(t)
	got, code, detail := x.p.relaunchAgent(context.Background(), resetOp(), irisCfg(x), "repo/iris", oldIdentity, "hrn:node:acme.com:iris:sheds:auto-1")
	if code != "" {
		t.Fatalf("%s: %s", code, detail)
	}
	if got.PID != 5151 {
		t.Fatalf("identity %+v", got)
	}
	if len(host.launches) != 1 {
		t.Fatalf("launches %d", len(host.launches))
	}
	l := host.launches[0]
	if !slices.Equal(l.Argv, irisCfg(x).Launch) || l.Dir != x.p.cfg.WorktreePath("repo/iris") {
		t.Fatalf("launch %+v", l)
	}
	for _, e := range l.Env {
		if strings.HasPrefix(e, launchGrantEnvVar+"=") || strings.Contains(e, hostKey) {
			t.Fatalf("relaunch env carries %q: a reset keeps the binding and needs no grant", e)
		}
	}
	x.p.mu.Lock()
	tracked := x.p.agents["id-iris"]
	x.p.mu.Unlock()
	if tracked == nil || tracked.PID() != 5151 {
		t.Fatal("the relaunched agent is not tracked: worktree.remove and the next reset would not see it")
	}
	if len(host.prompts) != 1 || !slices.Equal(host.prompts[0], irisCfg(x).Recover) {
		t.Fatalf("recover prompt %v", host.prompts)
	}
}

func TestRelaunchFailureCodes1043(t *testing.T) {
	cases := map[string]struct {
		setup func(*resetFixture, *fakeHost)
		code  string
	}{
		"launch fails":  {func(_ *resetFixture, h *fakeHost) { h.launchErr = errors.New("no such file") }, "RELAUNCH_BLOCKED"},
		"same process":  {func(_ *resetFixture, h *fakeHost) { h.agent.pid = 4242 }, "RELAUNCH_UNVERIFIED"},
		"unobservable":  {func(x *resetFixture, _ *fakeHost) { x.proc.err = errors.New("gone") }, "RELAUNCH_UNVERIFIED"},
		"prompt fails":  {func(_ *resetFixture, h *fakeHost) { h.promptErr = errors.New("exit 1") }, "RELAUNCH_UNVERIFIED"},
		"exits at once": {func(_ *resetFixture, h *fakeHost) { close(h.agent.done) }, "RELAUNCH_UNVERIFIED"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			x, host := relaunchFixture(t)
			c.setup(x, host)
			_, code, _ := x.p.relaunchAgent(context.Background(), resetOp(), irisCfg(x), "repo/iris", oldIdentity, "hrn:node:acme.com:iris:sheds:auto-1")
			if code != c.code {
				t.Fatalf("code %q, want %q", code, c.code)
			}
		})
	}
}

func TestRecoverPromptGetsIdsOnly1043(t *testing.T) {
	x, host := relaunchFixture(t)
	x.p.relaunchAgent(context.Background(), resetOp(), irisCfg(x), "repo/iris", oldIdentity, "hrn:node:acme.com:iris:sheds:auto-1")
	env := host.promptEnvs[0]
	for _, want := range []string{"HADRON_SESSION_ID=s1", "HADRON_WORKER_ID=id-iris", "HADRON_RECOVERY_NODE=hrn:node:acme.com:iris:sheds:auto-1"} {
		if !slices.Contains(env, want) {
			t.Fatalf("env lacks %s", want)
		}
	}
	for _, e := range env {
		if strings.Contains(e, hostKey) {
			t.Fatal("the recover prompt's env carries the host key")
		}
	}
}

// Before any process control: a worker this host could not hand a recovery
// prompt to is refused, so a stop is never made that cannot be recovered.
func TestResetRefusesAWorkerWithoutARecoverPrompt1043(t *testing.T) {
	x := newResetFixture(t)
	w := x.p.cfg.Workers["iris"]
	w.Recover = nil
	x.p.cfg.Workers["iris"] = w
	wantReason(t, x.run(t, resetOp()), "HOST_UNSUPPORTED")
	if x.proc.observed != 0 {
		t.Fatal("observed a process for a worker with no recover prompt")
	}
}
