// Package hostrelaunch implements the safety/state machine for C5 (#2060).
// It never binds or ends a session and knows no launcher argv. Its adapter must
// serialize with normal profile launches, supply a strict process proof, and
// launch only through C3's typed Orca profile machinery.
package hostrelaunch

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"path/filepath"
	"time"
)

const (
	Planned          = "planned"
	Applied          = "applied"
	Skipped          = "skipped"
	Failed           = "failed"
	Pending          = "RELAUNCH_PENDING_CONTINUITY"
	RateLimited      = "RELAUNCH_RATE_LIMITED"
	IdentityChanged  = "RELAUNCH_IDENTITY_CHANGED"
	Unproven         = "RELAUNCH_UNPROVEN"
	AgentPresent     = "RELAUNCH_AGENT_PRESENT"
	DuplicateAgents  = "DUPLICATE_AGENTS"
	NoProfile        = "NO_LAUNCHER_PROFILE"
	LaunchFailed     = "RELAUNCH_FAILED"
	VerifyFailed     = "RELAUNCH_UNVERIFIED"
	RollbackFailed   = "RELAUNCH_CLEANUP_UNCONFIRMED"
	StateUnavailable = "RELAUNCH_STATE_UNAVAILABLE"
	// NoExitProof: an exit pass (cli#2161) found no proof the agent ended for
	// a context reset of this session; the sweep's two-absence rule decides.
	NoExitProof = "RELAUNCH_NO_EXIT_PROOF"
)

// Target is an exact, already-bound identity, not a request to bind one.
type Target struct{ AppID, HostID, WorkerID, SessionID, Server, Worktree string }

// Desired is a fresh server answer. The adapter canonicalizes its worktree.
type Desired struct {
	Target
	SweepMode, State, Reason string
}

// Binding is the identity read from the target worktree, never the caller's.
type Binding struct {
	AppID, WorkerID, SessionID, Server string
	Usable                             bool
}

// Observation is strict evidence. A default/partial result cannot prove zero.
type Observation struct {
	PIDs   []int
	Proven bool
}

// Agent is only the launch this attempt created. Rollback must never select
// other terminals by worktree or family. Verify links its pinned identity to
// the one process observed after the launch.
type Agent interface {
	Reference() string
	Verify(context.Context, Target, Observation) error
	Rollback(context.Context) error
}

// Prepared contains a snapshot of a typed profile; preparing it must not write
// files or start anything. All launch preparation belongs inside Launch.
type Prepared struct {
	Fingerprint string
	Launch      func(context.Context) (Agent, error)
}

// Record persists across processes. Attempts are per worker, across sessions.
type Record struct {
	AbsentFor   string      `json:"absentFor"`
	AbsentSweep uint64      `json:"absentSweep"`
	Attempts    []time.Time `json:"attempts"`
	Notice      string      `json:"notice"`
}

// Journal is already locked by the run-level adapter; reads never create it.
type Journal interface {
	Load(string) (Record, error)
	Save(string, Record) error
}

// Result has closed status codes and optional diagnostic causes. Adapters must
// bound and redact causes before exposing them; causes may include launcher errors.
type Result struct {
	LocalCause, NoticeCause   string
	Status, Reason, Reference string
	FailureReason             string
	Attempt                   int
	NoticeFailed              bool
	AbsentObservedAt          time.Time
}

// Runner is one sweep. Sweep must be a persisted increasing generation for an
// applying run (dry runs never increment it). A skipped intervening sweep then
// breaks continuity even when that worker never reaches Execute.
type Runner struct {
	// BeforeAttempt rechecks transient launch prerequisites before charging budget.
	BeforeAttempt func(context.Context) error
	Sweep         uint64
	DryRun        bool
	Now           func() time.Time
	Journal       Journal
	Current       func(context.Context, Target) (Desired, error)
	Binding       func(context.Context, Target) (Binding, error)
	Probe         func(context.Context, Target) (Observation, error)
	Prepare       func(context.Context, Target) (Prepared, error)
	// Lock must serialize this worktree with C3 starts, not just other C5 calls.
	Lock func(context.Context, Target) (func(), error)
	// Notice is nil until the server's honest outcome surface is available.
	// Log is the caller's structured event/session logger; dry runs call neither.
	Notice func(context.Context, Target, Result) error
	Log    func(Target, Result)
	// ExitProof, when set, makes this an exit pass (cli#2161, C12): it is
	// asked under the lock, after every guard and the cap, in place of the
	// two-consecutive-sweep absence rule and nothing else. True means the
	// agent this host launched has exited for a context reset of this exact
	// session. An exit pass never records an absence observation: it saw one
	// worker, not a sweep.
	ExitProof func(context.Context, Target) (bool, error)
}

func (t Target) valid() bool {
	return t.AppID != "" && t.HostID != "" && t.WorkerID != "" && t.SessionID != "" && t.Server != "" && filepath.IsAbs(t.Worktree)
}
func fingerprint(t Target, profile string) string {
	var b []byte
	for _, field := range []string{t.AppID, t.HostID, t.WorkerID, t.SessionID, t.Server, t.Worktree, profile} {
		b = binary.AppendUvarint(b, uint64(len(field)))
		b = append(b, field...)
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func (r Runner) guard(ctx context.Context, t Target) (string, error) {
	d, err := r.Current(ctx, t)
	if err != nil {
		return "RELAUNCH_SOURCE_UNAVAILABLE", err
	}
	if d.Target != t {
		return IdentityChanged, nil
	}
	switch d.Reason {
	case "LEAVE_ALONE", "HALTED", "PAUSED", "TEAM_PAUSED", "RESETTING", "RETIRED", "SWEEP_OFF":
		return d.Reason, nil
	}
	if d.SweepMode != "APPLY" {
		return "SWEEP_NOT_APPLY", nil
	}
	if d.State != "RUNNING" {
		return "NOT_SHOULD_RUN", nil
	}
	if d.Reason != "LONG_LIVED" && d.Reason != "HOLDS_TASK" {
		return "UNKNOWN_REASON", nil
	}
	b, err := r.Binding(ctx, t)
	if err != nil {
		return "RELAUNCH_BINDING_UNAVAILABLE", err
	}
	if !b.Usable {
		return Unproven, nil
	}
	if b.AppID != t.AppID || b.WorkerID != t.WorkerID || b.SessionID != t.SessionID || b.Server != t.Server {
		return IdentityChanged, nil
	}
	o, err := r.Probe(ctx, t)
	if err != nil {
		return Unproven, err
	}
	if !o.Proven {
		return Unproven, nil
	}
	for _, pid := range o.PIDs {
		if pid <= 0 {
			return Unproven, nil
		}
	}
	if len(o.PIDs) > 1 {
		return DuplicateAgents, nil
	}
	if len(o.PIDs) == 1 {
		return AgentPresent, nil
	}
	return "", nil
}

// Execute proves a current target, requires two consecutive absent observations,
// persists its attempt before launching, then verifies exactly the process this
// launch made. A failure can roll back only the returned Agent.
func (r Runner) Execute(ctx context.Context, t Target) (out Result, err error) {
	out = Result{Status: Skipped, Reason: StateUnavailable}
	if !t.valid() || r.Current == nil || r.Binding == nil || r.Probe == nil || r.Prepare == nil || r.Journal == nil || (!r.DryRun && (r.Lock == nil || r.Sweep == 0)) {
		return out, errors.New("relaunch safety dependencies or target are incomplete")
	}
	var release func()
	unlock := func() {
		if release != nil {
			fn := release
			release = nil
			fn()
		}
	}
	if notice := r.Notice; notice != nil {
		r.Notice = func(ctx context.Context, t Target, result Result) error {
			// Tracking and admission release happen before repair network I/O.
			// Reporting is metadata only and revalidates its own target.
			unlock()
			return notice(ctx, t, result)
		}
	}
	// Nothing logged or notified by a dry run, including failures.
	defer func() {
		if err != nil {
			out.LocalCause = err.Error()
		}
		if err != nil && out.Reason == StateUnavailable {
			out.Status = Failed
		}
		if !r.DryRun && r.Log != nil {
			r.Log(t, out)
		}
	}()
	if e := ctx.Err(); e != nil {
		out.Reason = "RELAUNCH_CANCELLED"
		return out, e
	}
	if reason, e := r.guard(ctx, t); reason != "" {
		out.Reason = reason
		if reason == AgentPresent && e == nil && !r.DryRun {
			e = r.endObservedAbsence(t)
		}
		return out, e
	}
	out.AbsentObservedAt = r.clock()
	prepared, e := r.Prepare(ctx, t)
	if e != nil || prepared.Fingerprint == "" || prepared.Launch == nil {
		out.Reason = NoProfile
		if !r.DryRun {
			r.earlyProfileNotice(ctx, t, &out)
		}
		return out, e
	}
	if r.DryRun {
		out.Status, out.Reason = Planned, "TERMINAL_MISSING"
		return out, nil
	}
	releaseFn, e := r.Lock(ctx, t)
	if e != nil {
		return out, e
	}
	if releaseFn == nil {
		return out, errors.New("relaunch lock supplied no release")
	}
	release = releaseFn
	defer unlock()
	if reason, e := r.guard(ctx, t); reason != "" {
		out.Reason = reason
		if reason == AgentPresent && e == nil && !r.DryRun {
			e = r.endObservedAbsence(t)
		}
		return out, e
	}
	out.AbsentObservedAt = r.clock()
	// Resolve again after the lock wait; the actual launch uses this snapshot.
	prepared, e = r.Prepare(ctx, t)
	if e != nil || prepared.Fingerprint == "" || prepared.Launch == nil {
		out.Reason = NoProfile
		if !r.DryRun {
			r.earlyProfileNotice(ctx, t, &out)
		}
		return out, e
	}
	rec, e := r.Journal.Load(t.WorkerID)
	if e != nil {
		return out, e
	}
	now := r.clock()
	active := make([]time.Time, 0, len(rec.Attempts))
	for _, at := range rec.Attempts {
		if at.IsZero() {
			return out, errors.New("invalid relaunch attempt timestamp")
		}
		if now.Sub(at) < time.Hour {
			active = append(active, at)
		}
	}
	rec.Attempts = active
	notify := func(code string) { r.notifyResult(ctx, t, &out, &rec, code) }

	if len(rec.Attempts) >= 3 {
		out.Reason = RateLimited
		notify(RateLimited + ":" + rec.Attempts[0].Format(time.RFC3339Nano))
		return out, nil
	}
	fp := fingerprint(t, prepared.Fingerprint)
	if r.ExitProof != nil {
		proven, e := r.ExitProof(ctx, t)
		if e != nil || !proven {
			out.Reason = NoExitProof
			return out, e
		}
	} else if rec.AbsentFor != fp || rec.AbsentSweep+1 != r.Sweep {
		rec.AbsentFor, rec.AbsentSweep = fp, r.Sweep
		if e := r.Journal.Save(t.WorkerID, rec); e != nil {
			return out, e
		}
		out.Reason = Pending
		return out, nil
	}
	if r.BeforeAttempt != nil {
		if e := r.BeforeAttempt(ctx); e != nil {
			out.Reason = Unproven
			return out, e
		}
	}
	// Claim before even the last guard. A refused/failed attempt is not free.
	rec.AbsentFor, rec.AbsentSweep = "", 0
	rec.Attempts = append(rec.Attempts, now)
	if e := r.Journal.Save(t.WorkerID, rec); e != nil {
		return out, e
	}
	out.Attempt = len(rec.Attempts)
	if reason, e := r.guard(ctx, t); reason != "" {
		out.Reason = reason
		notify(reason)
		return out, e
	}
	if e := ctx.Err(); e != nil {
		out.Reason = "RELAUNCH_CANCELLED"
		return out, e
	}
	out.AbsentObservedAt = r.clock()
	a, e := prepared.Launch(ctx)
	if e != nil || a == nil {
		out.Status, out.Reason = Failed, LaunchFailed
		out.FailureReason = observedFailureReason(e)
		if a != nil && out.FailureReason == FailureSetupFailed {
			out.FailureReason = "" // A returned agent cannot prove pre-launch setup failure.
		}

		if a != nil {
			out.Reference = a.Reference()
			if stopErr := a.Rollback(ctx); stopErr != nil {
				out.Reason = RollbackFailed
			}
		}
		if e == nil {
			e = errors.New("profile launcher returned no agent")
		}
		notify(LaunchFailed)
		return out, e
	}
	out.Reference = a.Reference()
	if out.Reference == "" {
		e = errors.New("profile launcher supplied no launch reference")
	} else {
		e = ctx.Err()
	}
	var verified Observation
	proofFailed := false
	if e == nil {
		verified, e = r.Probe(ctx, t)
		proofFailed = e != nil
	}
	if e == nil && (!verified.Proven || len(verified.PIDs) != 1 || verified.PIDs[0] <= 0) {
		e = errors.New("no unique process proven after relaunch")
		proofFailed = true
	}
	if e == nil {
		e = a.Verify(ctx, t, verified)
		proofFailed = e != nil
	}
	if e == nil {
		// The agent can never change a binding on this path; server/binding changes
		// during launch still invalidate it even when its process proof passes.
		d, de := r.Current(ctx, t)
		b, be := r.Binding(ctx, t)
		if de != nil {
			e = de
		} else if be != nil {
			e = be
		} else if d.Target != t || d.SweepMode != "APPLY" || d.State != "RUNNING" || (d.Reason != "LONG_LIVED" && d.Reason != "HOLDS_TASK") || !b.Usable || b.AppID != t.AppID || b.WorkerID != t.WorkerID || b.SessionID != t.SessionID || b.Server != t.Server {
			e = errors.New("relaunch target changed while launching")
		}
	}
	if e != nil {
		out.Status, out.Reason = Failed, VerifyFailed
		out.FailureReason = observedFailureReason(e)
		if proofFailed {
			if errors.Is(e, context.Canceled) {
				out.FailureReason = ""
			} else if out.FailureReason == "" || out.FailureReason == FailureSetupFailed {
				out.FailureReason = FailureVerifyFailed
			}
		} else if out.FailureReason == FailureSetupFailed || out.FailureReason == FailureVerifyFailed {
			// A fresh server/binding refusal is not a process-proof failure.
			out.FailureReason = ""
		}
		if stopErr := a.Rollback(ctx); stopErr != nil {
			out.Reason = RollbackFailed
		}
		notify(out.Reason)
		return out, e
	}
	out.Status, out.Reason = Applied, "TERMINAL_MISSING"
	notify("RELAUNCHED:" + now.Format(time.RFC3339Nano))
	return out, nil
}

func (r Runner) clock() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}
func (r Runner) notifyResult(ctx context.Context, t Target, out *Result, rec *Record, code string) {
	// Keep distinct typed reasons and target identities distinct locally too.
	key := fingerprint(t, code+":"+out.FailureReason)
	if r.Notice == nil || rec.Notice == key || out.AbsentObservedAt.IsZero() {
		return
	}
	if err := r.Notice(ctx, t, *out); err != nil {
		out.NoticeCause = err.Error()
		out.NoticeFailed = true
		return
	}
	// The server owns once-per-episode idempotence. A failed response/save is
	// retried later; a failed transport is never marked as delivered locally.
	rec.Notice = key
	if err := r.Journal.Save(t.WorkerID, *rec); err != nil {
		out.NoticeCause = err.Error()
		out.NoticeFailed = true
	}
}
func (r Runner) earlyProfileNotice(ctx context.Context, t Target, out *Result) {
	if r.Notice == nil || out.AbsentObservedAt.IsZero() {
		return
	}
	rec, err := r.Journal.Load(t.WorkerID)
	if err != nil {
		out.NoticeCause = err.Error()
		out.NoticeFailed = true
		return
	}
	// A refusal identifies the candidate attempt, but consumes no launch.
	out.Attempt = 1
	now := r.clock()
	for _, at := range rec.Attempts {
		if at.IsZero() {
			out.NoticeFailed = true
			return
		}
		if now.Sub(at) < time.Hour {
			out.Attempt++
		}
	}
	r.notifyResult(ctx, t, out, &rec, "REFUSED:"+NoProfile+":"+t.SessionID)
}

func observedFailureReason(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return FailureTimeout
	}
	var classified interface{ RelaunchFailureReason() string }
	if errors.As(err, &classified) {
		switch reason := classified.RelaunchFailureReason(); reason {
		case FailureTimeout, FailureTerminalCreateRefused, FailureLaunchExitNonzero, FailureSetupFailed, FailureVerifyFailed:
			return reason
		}
	}
	return ""
}

// A proven live process ends this observer's absence episode. Preserve the
// rolling attempt budget: seeing life must not make past launches free.
func (r Runner) endObservedAbsence(t Target) error {
	rec, err := r.Journal.Load(t.WorkerID)
	if err != nil {
		return err
	}
	if rec.Notice == "" && rec.AbsentFor == "" && rec.AbsentSweep == 0 {
		return nil
	}
	rec.Notice, rec.AbsentFor, rec.AbsentSweep = "", "", 0
	return r.Journal.Save(t.WorkerID, rec)
}
