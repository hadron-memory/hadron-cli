package hostpoller

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
	PostLaunchWait time.Duration
	Source         RelaunchSource
	Ready          func(context.Context) error
	OpenJournal    func() (hostrelaunch.Journal, uint64, func(), error)
	Binding        func(hostrelaunch.Target) (hostrelaunch.Binding, error)
	Probe          func(context.Context, hostrelaunch.Target) (hostrelaunch.Observation, error)
	Verify         func(context.Context, Agent, hostrelaunch.Target, hostrelaunch.Observation) error
	Notice         func(context.Context, hostrelaunch.Target, hostrelaunch.Result) error
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
		if d.PostLaunchWait == 0 {
			d.PostLaunchWait = 30 * time.Second
		}
	}
	if d.PostLaunchWait < 0 || d.PostLaunchWait > time.Minute {
		return errors.New("post-launch proof wait is outside its bound")
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
	if p.relaunch == nil || !p.relaunchMu.TryLock() {
		return
	}
	// Collection and delivery must not await a recovery launch. There is one
	// owned pass at most, with no backlog of ticks. Wait (including --once)
	// joins it; normal cancellation and a fatal check-in stop it.
	jobCtx, cancel := context.WithCancel(ctx)
	p.mu.Lock()
	p.relaunchCancel = cancel
	p.mu.Unlock()
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		defer p.relaunchMu.Unlock()
		defer func() {
			cancel()
			p.mu.Lock()
			p.relaunchCancel = nil
			p.mu.Unlock()
		}()
		p.relaunchPass(jobCtx)
	}()
}

func (p *Poller) cancelRelaunch() {
	p.mu.Lock()
	cancel := p.relaunchCancel
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (p *Poller) relaunchPass(ctx context.Context) {
	defer func() {
		if ctx.Err() != nil {
			// A partially observed cancelled pass cannot join two absence
			// observations if this poller is subsequently used again.
			p.relaunchEpoch = ""
		}
	}()
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
	if initial.SweepMode != "APPLY" || len(initial.Workers) == 0 {
		return
	}
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
		firstSnapshot := true
		runner := hostrelaunch.Runner{Sweep: generation, Now: p.now, Journal: journal, BeforeAttempt: d.Ready,
			Current: func(ctx context.Context, t hostrelaunch.Target) (hostrelaunch.Desired, error) {
				// The discovery snapshot is already a current read for the first
				// guard. Healthy workers need no repeated host-wide fetch. Every
				// guard after admission and before/after launch still reads fresh.
				snap := initial
				var e error
				if firstSnapshot {
					firstSnapshot = false
				} else {
					snap, e = d.Source.RelaunchSnapshot(ctx, t.HostID)
				}
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
				return postLaunchObservation(ctx, d, launched, t)
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
	p.emit(Event{Time: p.now().UTC(), Kind: "relaunch", Type: "worker.relaunch", Outcome: r.Status, Detail: r.Reason, WorkerID: t.WorkerID, SessionID: t.SessionID, TerminalHandle: r.Reference, Reason: r.Reason, Attempt: r.Attempt, CheckedAt: r.AbsentObservedAt, NoticeFailed: r.NoticeFailed, FailureReason: r.FailureReason, LocalCause: p.relaunchCause(r.LocalCause), NoticeCause: p.relaunchCause(r.NoticeCause)})
}

func newRelaunchEpoch() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes[:]), nil
}

// Only a new, already-counted launch may wait for its exec transition. Errors,
// unknown observations, extra/foreign agents and cancellation never become
// absence, and there is no second launch inside this window.
// Retry the complete strict census, never individual unreadable PIDs. The child
// deadline bounds cooperative probes; parent cancellation always wins. An error
// invalidates the entire observation, including any partial positive proof.
func retryRelaunchCensus(ctx context.Context, d *RelaunchDependencies, t hostrelaunch.Target) (hostrelaunch.Observation, error) {
	const maxProbes = 3
	const pause = 20 * time.Millisecond
	const budget = 250 * time.Millisecond
	bounded, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	var last error
	for attempt := 0; attempt < maxProbes; attempt++ {
		if err := ctx.Err(); err != nil {
			return hostrelaunch.Observation{}, err
		}
		if err := bounded.Err(); err != nil {
			last = err
			break
		}
		observation, err := d.Probe(bounded, t)
		if parentErr := ctx.Err(); parentErr != nil {
			return hostrelaunch.Observation{}, parentErr
		}
		if boundErr := bounded.Err(); boundErr != nil {
			last = boundErr
			break
		}
		if err == nil {
			return observation, nil
		}
		last = err
		if attempt+1 < maxProbes {
			if err := sleepCtx(bounded, pause); err != nil {
				last = err
				break
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return hostrelaunch.Observation{}, err
	}
	// Do not classify this proof budget as a launcher TIMEOUT.
	return hostrelaunch.Observation{}, fmt.Errorf("%w: %v", hostrelaunch.ErrCensusUnavailable, last)
}

func postLaunchObservation(ctx context.Context, d *RelaunchDependencies, a Agent, t hostrelaunch.Target) (hostrelaunch.Observation, error) {
	observation, err := retryRelaunchCensus(ctx, d, t)
	if a == nil || d.PostLaunchWait <= 0 {
		return observation, err
	}
	deadline := time.NewTimer(d.PostLaunchWait)
	defer deadline.Stop()
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	for err == nil && observation.Proven && len(observation.PIDs) == 0 {
		select {
		case <-ctx.Done():
			return hostrelaunch.Observation{}, ctx.Err()
		case <-a.Done():
			return observation, nil
		case <-deadline.C:
			return hostrelaunch.Observation{}, &hostrelaunch.LaunchError{Code: hostrelaunch.FailureTimeout, Err: errors.New("new agent did not become observable within its proof window")}
		case <-tick.C:
			observation, err = retryRelaunchCensus(ctx, d, t)
		}
	}
	return observation, err
}

func (p *Poller) relaunchCause(raw string) string {
	if p.key != "" {
		raw = strings.ReplaceAll(raw, p.key, "[redacted]")
	}
	raw = strings.Map(func(r rune) rune {
		if r < ' ' || r == 127 {
			return ' '
		}
		return r
	}, raw)
	runes := []rune(raw)
	if len(runes) > 512 {
		raw = string(runes[:512]) + "…"
	}
	return raw
}
