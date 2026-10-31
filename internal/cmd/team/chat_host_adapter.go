package team

import (
	"context"
	"net/url"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/delivery"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
)

func postDeliveryServerURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return ""
	}
	u.Host = strings.ToLower(u.Host)
	if u.Scheme == "https" {
		u.Host = strings.TrimSuffix(u.Host, ":443")
	} else {
		u.Host = strings.TrimSuffix(u.Host, ":80")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return u.String()
}

type postHostPass struct {
	cfg     *hostpoller.Config
	state   *hostpoller.State
	server  *hostServer
	deliver hostpoller.Deliverer
	// names is the registered display name per worker id, from the last
	// recipients read; Push matches stored mention tokens against it.
	names map[string]string
}

func preparePostHostDelivery(f *cmdutil.Factory) postHostDeliveryPreparer {
	return func(ctx context.Context, path string, target postHostTarget) (postHostDeliveryPass, error) {
		// Loading is conditional on the explicit flag, never on ambient configuration.
		cfg, _, srv, err := loadHostPoller(f, path)
		if err != nil {
			return nil, exitcode.Newf(exitcode.FromError(err), "cannot prepare host delivery configuration or key; nothing posted")
		}
		if !cfg.Delivery.Enabled {
			return nil, exitcode.Newf(exitcode.Usage, "host configuration must enable delivery; nothing posted")
		}
		hostURL := cfg.Server
		if hostURL == "" {
			hostURL = target.Server
		}
		canonical := postDeliveryServerURL(target.Server)
		if canonical == "" || canonical != postDeliveryServerURL(hostURL) {
			return nil, exitcode.Newf(exitcode.Conflict, "host delivery server differs from posting server; nothing posted")
		}
		// Canonicalize both App refs with the posting credential. No host key is
		// borrowed for the post, and neither identity read registers a host.
		c, err := f.GraphQLClient()
		if err != nil {
			return nil, postHostPreflightFailure(err, "posting caller setup")
		}
		a, err := gen.TeamAppIdentity(ctx, c, target.AppRef)
		if err != nil {
			return nil, postHostPreflightFailure(err, "posting App identity read")
		}
		if a == nil || a.App == nil {
			return nil, exitcode.Newf(exitcode.NotFound, "posting App was not found; nothing posted")
		}
		h, err := gen.TeamAppIdentity(ctx, c, cfg.App)
		if err != nil {
			return nil, postHostPreflightFailure(err, "host App identity read")
		}
		if h == nil || h.App == nil || h.App.Id != a.App.Id {
			return nil, exitcode.Newf(exitcode.Conflict, "host delivery App differs from posting App; nothing posted")
		}
		srv.app = a.App.Id
		state, err := hostpoller.LoadState(cfg.StateFile)
		if err != nil {
			return nil, exitcode.Newf(exitcode.Usage, "cannot read host state; nothing posted")
		}
		if err := cfg.CheckWorktreeAliases(state); err != nil {
			return nil, exitcode.Newf(exitcode.Usage, "host delivery worktrees overlap; nothing posted")
		}
		p := &postHostPass{cfg: cfg, state: state, server: srv}
		if _, err := p.recipients(ctx); err != nil {
			return nil, postHostPreflightFailure(err, "host registration read")
		}
		// Read-only authority check before the irreversible post: the server checks
		// live host:operate/attention:deliver and registration. Discard these proofs;
		// Run obtains fresh pages after the post, not a cached pre-post snapshot.
		if _, err := srv.Batches(ctx); err != nil {
			return nil, postHostPreflightFailure(err, "host delivery read")
		}
		p.deliver = delivery.New(srv, orcaterm.NewClient(orcaterm.Exec(cfg.Delivery.OrcaBin, orcaterm.CallTimeout)), cfg.DeliveryStateDir())
		return p, nil
	}
}

// The state/config identify this poller's registration and permitted local
// worktrees; the session-free Batches call independently restricts work to the
// host of the currently authorized key. No App-wide worker enumeration.
func (p *postHostPass) recipients(ctx context.Context) (map[string]delivery.Recipient, error) {
	hostID := p.state.RegisteredHostID()
	if hostID == "" {
		return nil, postNotRegistered
	}
	r, err := gen.HostRegistrations(ctx, p.server.client, p.server.app)
	if err != nil {
		return nil, deliveryError("read", err)
	}
	if r == nil {
		return nil, callError("read", "no-page")
	}
	out := map[string]delivery.Recipient{}
	p.names = map[string]string{}
	for _, reg := range r.HostRegistrations {
		if reg == nil || reg.HostId != hostID {
			continue
		}
		if !reg.Live {
			return nil, postNotLive
		}
		for _, w := range reg.Workers {
			if w == nil || w.Worker == nil || w.Worker.Id != w.WorkerId {
				continue
			}
			urn := ""
			if w.Worker.Urn != nil {
				urn = *w.Worker.Urn
			}
			ref := p.cfg.RefFor(w.WorkerId, urn, w.Worker.Name)
			if ref == "" {
				continue
			}
			candidate := p.cfg.Recipient(p.state, ref, w.WorkerId)
			permitted := map[string]bool{}
			for _, token := range w.Worktrees {
				if hostpoller.ValidNameToken(token) {
					permitted[p.cfg.WorktreePath(token)] = true
				}
			}
			trees := []string{}
			for _, tree := range candidate.Worktrees {
				if permitted[tree] {
					trees = append(trees, tree)
				}
			}
			if len(trees) == 0 {
				continue
			}
			candidate.Worktrees = trees
			out[w.WorkerId] = candidate
			p.names[w.WorkerId] = w.Worker.Name
		}
		return out, nil
	}
	return nil, postNotRegistered
}
func (p *postHostPass) Run(ctx context.Context) (postHostDeliverySummary, error) {
	summary, _, err := p.pass(ctx, nil)
	return summary, err
}

// pass re-checks the host state, reads the due batches and delivers them,
// all of them or (only != nil) just those of the named worker ids. The
// per-worker result is the worst outcome seen for that worker.
func (p *postHostPass) pass(ctx context.Context, only func(map[string]delivery.Recipient) map[string]bool) (postHostDeliverySummary, map[string]*pushOutcome, error) {
	summary := postHostDeliverySummary{}
	per := map[string]*pushOutcome{}
	fresh, err := hostpoller.LoadState(p.cfg.StateFile)
	if err != nil || fresh.RegisteredHostID() != p.state.RegisteredHostID() {
		return summary, per, exitcode.Newf(exitcode.Conflict, "host state changed")
	}
	if err := p.cfg.CheckWorktreeAliases(fresh); err != nil {
		return summary, per, exitcode.Newf(exitcode.Conflict, "host worktrees overlap")
	}
	p.state = fresh
	recipients, err := p.recipients(ctx)
	if err != nil {
		return summary, per, err
	}
	var want map[string]bool
	if only != nil {
		want = only(recipients)
	}
	batches, err := p.server.Batches(ctx)
	if err != nil {
		return summary, per, err
	}
	blocked := map[string]bool{}
	for _, b := range batches {
		if ctx.Err() != nil {
			return summary, per, ctx.Err()
		}
		if want != nil && !want[b.WorkerID] {
			continue
		}
		r, ok := recipients[b.WorkerID]
		if !ok {
			summary.Excluded++
			continue
		}
		if blocked[b.WorkerID] {
			summary.Pending++
			// A later batch left due behind an earlier typed one does not
			// undo that delivery (its ack-pending qualifier stays).
			if per[b.WorkerID].state != pushStatePushed {
				per[b.WorkerID].note(pushStateNotPushed, "pending")
			}
			continue
		}
		if per[b.WorkerID] == nil {
			per[b.WorkerID] = &pushOutcome{}
		}
		o := p.deliver.Deliver(ctx, b, r)
		switch o.Kind {
		case delivery.Delivered, delivery.Reconciled:
			summary.Delivered++
			reason := ""
			if o.AckPending {
				// Typed and recorded, but the ack did not land: pushed is
				// true, "acknowledged" is not (cor:agt:080:16). The record
				// reconciles it without retyping; never claim not typed.
				reason = "ack-pending"
			}
			per[b.WorkerID].note(pushStatePushed, reason)
			if o.AckPending {
				summary.Pending++
				blocked[b.WorkerID] = true
			}
		case delivery.Held:
			summary.Held++
			blocked[b.WorkerID] = true
			per[b.WorkerID].note(pushStateHeld, o.Reason)
		case delivery.Failed:
			summary.Failed++
			blocked[b.WorkerID] = true
			per[b.WorkerID].note(pushStateNotPushed, o.Reason)
		default:
			summary.Pending++
			blocked[b.WorkerID] = true
			per[b.WorkerID].note(pushStateNotPushed, o.Reason)
		}
	}
	return summary, per, nil
}

// pushOutcome folds a worker's batch outcomes: held beats not-pushed beats
// pushed, because the worse state is the one the sender must act on.
type pushOutcome struct{ state, reason string }

func pushRank(s string) int {
	switch s {
	case pushStateHeld:
		return 3
	case pushStateNotPushed:
		return 2
	case pushStatePushed:
		return 1
	}
	return 0
}

func (o *pushOutcome) note(state, reason string) {
	if o == nil {
		return
	}
	if pushRank(state) > pushRank(o.state) || (state == o.state && o.reason == "" && reason != "") {
		o.state, o.reason = state, reason
	}
}

// mentionTokenOf is hadron-server's canonical mention token of a display
// name (src/lib/mentionToken.ts, cor:agt:020:04), ported exactly: lowercase,
// runs outside [a-z0-9._-] become "-", trimmed to alphanumeric ends. It keeps
// ".", "_" and "-" as atom characters; "" means unaddressable. The map is
// lossy and carries no uniqueness guarantee: two names may share a token.
func mentionTokenOf(name string) string {
	// JS toLowerCase maps U+0130 to "i" + U+0307 (the dot then becomes "-");
	// Go's rune mapping gives "i". Match the server.
	l := strings.ToLower(strings.ReplaceAll(name, "\u0130", "i\u0307"))
	var b strings.Builder
	dash := false
	for _, r := range l {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			if dash {
				b.WriteByte('-')
				dash = false
			}
			b.WriteRune(r)
		} else {
			dash = true
		}
	}
	if dash {
		b.WriteByte('-')
	}
	t := b.String()
	alnum := func(r byte) bool { return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') }
	for len(t) > 0 && !alnum(t[0]) {
		t = t[1:]
	}
	for len(t) > 0 && !alnum(t[len(t)-1]) {
		t = t[:len(t)-1]
	}
	return t
}

// Push delivers the whole due batch of each named recipient (stored mention
// tokens). A token addresses EVERY worker registered on this host whose
// server token (mentionTokenOf) equals it, as the server's own mention
// matching does; two names may share one. The token's report folds those
// workers' outcomes, worst state winning (held beats not-pushed beats pushed).
// A token matching no worker here, or with no batch, is not-pushed: the one
// state for "nothing came back". On a pass-level error the outcomes already
// confirmed are kept and every recipient without one is not-pushed
// "push-failed"; the error is returned beside the results.
func (p *postHostPass) Push(ctx context.Context, tokens []string) ([]postPushResult, error) {
	ids := map[string][]string{} // token -> worker ids
	_, per, err := p.pass(ctx, func(recipients map[string]delivery.Recipient) map[string]bool {
		want := map[string]bool{}
		for id := range recipients {
			tok := mentionTokenOf(p.names[id])
			for _, t := range tokens {
				if tok != "" && tok == t {
					ids[t] = append(ids[t], id)
					want[id] = true
				}
			}
		}
		return want
	})
	out := []postPushResult{}
	for _, t := range tokens {
		res := postPushResult{Recipient: t, State: pushStateNotPushed, Reason: "no-batch"}
		if err != nil {
			res.Reason = "push-failed"
		}
		folded := &pushOutcome{}
		for _, id := range ids[t] {
			if o := per[id]; o != nil && o.state != "" {
				folded.note(o.state, o.reason)
			}
		}
		if folded.state != "" {
			res = postPushResult{Recipient: t, State: folded.state, Reason: folded.reason}
		}
		out = append(out, res)
	}
	return out, err
}
