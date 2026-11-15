package hostpoller

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/delivery"
	"github.com/hadron-memory/hadron-cli/internal/hostrelaunch"
)

// RelaunchDependencies are configured once before Run. The source and notices
// use the host key; only the poller may invoke this path. Ready proves the
// daemon is reachable before any launch budget is spent.
type RelaunchDependencies struct {
	Source      RelaunchSource
	Ready       func(context.Context) error
	OpenJournal func() (hostrelaunch.Journal, uint64, func(), error)
	Binding     func(hostrelaunch.Target) (hostrelaunch.Binding, error)
	Probe       func(context.Context, hostrelaunch.Target) (hostrelaunch.Observation, error)
	Verify      func(context.Context, Agent, hostrelaunch.Target, hostrelaunch.Observation) error
	Notice      func(context.Context, hostrelaunch.Target, hostrelaunch.Result) error
}

func (p *Poller) EnableRelaunch(d RelaunchDependencies) error {
	if runtime.GOOS != "linux" {
		return errors.New("periodic relaunch requires Linux process proofs")
	}
	if d.Source == nil || d.Ready == nil || p.orcaLaunch == nil || p.profiles == nil {
		return errors.New("periodic relaunch needs the host source, daemon readiness and typed Orca launcher")
	}
	if d.OpenJournal == nil {
		d.OpenJournal = func() (hostrelaunch.Journal, uint64, func(), error) {
			return openRelaunchJournal(filepath.Join(filepath.Dir(p.cfg.StateFile), "relaunch"))
		}
	}
	if d.Binding == nil {
		d.Binding = func(t hostrelaunch.Target) (hostrelaunch.Binding, error) {
			b := delivery.WorktreeBindingIdentity(t.Worktree)
			return hostrelaunch.Binding{AppID: b.AppID, WorkerID: b.WorkerID, SessionID: b.SessionID, Server: strings.TrimRight(b.Server, "/"), Usable: b.Usable}, nil
		}
	}
	if d.Probe == nil {
		d.Probe = probeRelaunchWorktree
	}
	if d.Verify == nil {
		d.Verify = verifyRelaunchProcess
	}
	epoch, err := newRelaunchEpoch()
	if err != nil {
		return err
	}
	p.relaunchEpoch = epoch
	p.relaunch = &d
	return nil
}

func (p *Poller) relaunchStep(ctx context.Context) {
	if p.relaunch == nil {
		return
	}
	p.relaunchMu.Lock()
	defer p.relaunchMu.Unlock()
	d := p.relaunch
	hostID := p.state.RegisteredHostID()
	initial, err := d.Source.RelaunchSnapshot(ctx, hostID)
	if err != nil || initial.HostID != hostID || initial.AppID == "" || initial.Server == "" {
		p.relaunchEpoch = ""
		p.log("relaunch-unproven", "", "worker.relaunch", "skipped", hostrelaunch.Unproven)
		return
	}
	journal, generation, closeJournal, err := d.OpenJournal()
	if err != nil || journal == nil || generation == 0 || closeJournal == nil {
		p.relaunchEpoch = ""
		p.log("relaunch-unproven", "", "worker.relaunch", "skipped", hostrelaunch.Unproven)
		return
	}
	defer closeJournal()
	// Even an unavailable-daemon sweep consumes a generation, so it cannot
	// bridge absence observations across an outage. It consumes no attempt.
	if err := d.Ready(ctx); err != nil {
		p.log("relaunch-unproven", "", "worker.relaunch", "skipped", hostrelaunch.Unproven)
		return
	}
	if p.relaunchEpoch == "" {
		epoch, e := newRelaunchEpoch()
		if e != nil {
			p.log("relaunch-unproven", "", "worker.relaunch", "skipped", hostrelaunch.Unproven)
			return
		}
		p.relaunchEpoch = epoch
	}
	for _, row := range initial.Workers {
		if ctx.Err() != nil {
			return
		}
		target, ref, w, token, ok := p.relaunchTarget(initial, row)
		if !ok {
			p.log("relaunch-unproven", "", "worker.relaunch", "skipped", hostrelaunch.Unproven)
			continue
		}
		if target.SessionID == "" {
			continue
		} // normal worker.start owns a new session
		var launched Agent
		runner := hostrelaunch.Runner{Sweep: generation, Now: p.now, Journal: journal,
			Current: func(ctx context.Context, t hostrelaunch.Target) (hostrelaunch.Desired, error) {
				snap, e := d.Source.RelaunchSnapshot(ctx, t.HostID)
				if e != nil {
					return hostrelaunch.Desired{}, e
				}
				for _, fresh := range snap.Workers {
					if fresh.WorkerID == t.WorkerID {
						next, _, _, _, valid := p.relaunchTarget(snap, fresh)
						if !valid {
							return hostrelaunch.Desired{}, errors.New("unproven target")
						}
						return hostrelaunch.Desired{Target: next, SweepMode: snap.SweepMode, State: fresh.Desired, Reason: fresh.Reason}, nil
					}
				}
				return hostrelaunch.Desired{}, errors.New("worker is no longer in desired state")
			}, Binding: func(_ context.Context, t hostrelaunch.Target) (hostrelaunch.Binding, error) { return d.Binding(t) },
			Probe: func(ctx context.Context, t hostrelaunch.Target) (hostrelaunch.Observation, error) {
				p.mu.Lock()
				old := p.agents[t.WorkerID]
				p.mu.Unlock()
				if old != nil {
					select {
					case <-old.Done():
					default:
						if old.PID() <= 0 {
							return hostrelaunch.Observation{}, nil
						}
						return hostrelaunch.Observation{Proven: true, PIDs: []int{old.PID()}}, nil
					}
				}
				return d.Probe(ctx, t)
			},
			Prepare: func(_ context.Context, t hostrelaunch.Target) (hostrelaunch.Prepared, error) {
				if !w.launchesViaOrca() {
					return hostrelaunch.Prepared{}, errors.New("no typed Orca launcher profile")
				}
				profile, account, found, e := p.profiles.Profile(ref)
				if e != nil || !found || !SameFamily(profile.Family, w.Family) {
					return hostrelaunch.Prepared{}, errors.New("no matching launcher profile")
				}
				if _, e = LaunchArgv(profile, account); e != nil {
					return hostrelaunch.Prepared{}, e
				}
				fingerprint, e := json.Marshal(struct {
					Profile LauncherProfile
					Account AccountProfile
					Disk    string
				}{profile, account, w.DiskTmp})
				if e != nil {
					return hostrelaunch.Prepared{}, e
				}
				return hostrelaunch.Prepared{Fingerprint: p.relaunchEpoch + ":" + string(fingerprint), Launch: func(ctx context.Context) (hostrelaunch.Agent, error) {
					spec, e := orcaProfileLaunchSpec(ref, w, t.Worktree, "", "", profile, account)
					if e != nil {
						return nil, e
					}
					launched, e = p.orcaLaunch(ctx, spec)
					if launched == nil {
						return nil, e
					}
					return &relaunchOwnedAgent{agent: launched, target: t, verify: d.Verify}, e
				}}, nil
			},
			Lock: func(_ context.Context, t hostrelaunch.Target) (func(), error) {
				release, e := p.reserveWorktreeLaunch(token)
				if e != nil {
					return nil, e
				}
				bindingRelease, e := delivery.LockBinding(t.Worktree, time.Second)
				if e != nil {
					release()
					return nil, e
				}
				return func() {
					// Track before releasing launch admission, including a failed launch
					// whose own terminal could not be confirmed stopped.
					if launched != nil {
						select {
						case <-launched.Done():
						default:
							p.trackAgent(t.WorkerID, token, launched)
							p.mu.Lock()
							p.agentSessions[t.WorkerID] = t.SessionID
							p.mu.Unlock()
						}
					}
					bindingRelease()
					release()
				}, nil
			}, Notice: d.Notice,
			Log: func(t hostrelaunch.Target, result hostrelaunch.Result) { p.logRelaunch(t, result) },
		}
		_, _ = runner.Execute(ctx, target) // closed result/reason is emitted by Log
	}
}

func (p *Poller) relaunchTarget(s RelaunchSnapshot, row RelaunchWorker) (hostrelaunch.Target, string, WorkerConfig, string, bool) {
	p.mu.Lock()
	ref := p.workerRefs[row.WorkerID]
	p.mu.Unlock()
	w, ok := p.cfg.Workers[ref]
	if !ok || s.AppID == "" || s.HostID == "" || s.Server == "" || !SameFamily(w.Family, row.Family) || len(row.Worktrees) != 1 {
		return hostrelaunch.Target{}, "", WorkerConfig{}, "", false
	}
	token := row.Worktrees[0]
	if !ValidNameToken(token) || !contains(p.state.worktreesFor(ref, w.Worktrees), token) {
		return hostrelaunch.Target{}, "", WorkerConfig{}, "", false
	}
	root, err := filepath.EvalSymlinks(p.cfg.WorktreesRoot)
	if err != nil {
		return hostrelaunch.Target{}, "", WorkerConfig{}, "", false
	}
	dir, err := filepath.EvalSymlinks(p.cfg.WorktreePath(token))
	if err != nil || !within(dir, root) {
		return hostrelaunch.Target{}, "", WorkerConfig{}, "", false
	}
	return hostrelaunch.Target{AppID: s.AppID, HostID: s.HostID, WorkerID: row.WorkerID, SessionID: row.SessionID, Server: strings.TrimRight(s.Server, "/"), Worktree: dir}, ref, w, token, true
}

type relaunchOwnedAgent struct {
	agent  Agent
	target hostrelaunch.Target
	verify func(context.Context, Agent, hostrelaunch.Target, hostrelaunch.Observation) error
}

func (a *relaunchOwnedAgent) Reference() string {
	if t, ok := a.agent.(interface{ Terminal() (string, int, string) }); ok {
		handle, _, _ := t.Terminal()
		return handle
	}
	return ""
}
func (a *relaunchOwnedAgent) Verify(ctx context.Context, t hostrelaunch.Target, o hostrelaunch.Observation) error {
	if t != a.target {
		return errors.New("launch target changed")
	}
	return a.verify(ctx, a.agent, t, o)
}
func (a *relaunchOwnedAgent) Rollback(context.Context) error {
	if done, _ := a.agent.Terminate(); !done {
		return errors.New("own launch cleanup is unconfirmed")
	}
	return nil
}
func (p *Poller) logRelaunch(t hostrelaunch.Target, r hostrelaunch.Result) {
	if p.emit == nil {
		return
	}
	p.emit(Event{Time: p.now().UTC(), Kind: "relaunch", Type: "worker.relaunch", Outcome: r.Status, Detail: r.Reason, WorkerID: t.WorkerID, SessionID: t.SessionID, TerminalHandle: r.Reference, Reason: r.Reason, Attempt: r.Attempt, CheckedAt: r.AbsentObservedAt, NoticeFailed: r.NoticeFailed})
}

func newRelaunchEpoch() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes[:]), nil
}
