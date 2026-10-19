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
	u.Scheme = strings.ToLower(u.Scheme)
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
			return nil, err
		}
		a, err := gen.TeamAppIdentity(ctx, c, target.AppRef)
		if err != nil || a == nil || a.App == nil {
			return nil, exitcode.Newf(exitcode.Forbidden, "cannot resolve posting App for host delivery; nothing posted")
		}
		h, err := gen.TeamAppIdentity(ctx, c, cfg.App)
		if err != nil || h == nil || h.App == nil || h.App.Id != a.App.Id {
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
			return nil, exitcode.Newf(exitcode.Forbidden, "host registration unavailable; nothing posted")
		}
		// Read-only authority check before the irreversible post: the server checks
		// live host:operate/attention:deliver and registration. Discard these proofs;
		// Run obtains fresh pages after the post, not a cached pre-post snapshot.
		if _, err := srv.Batches(ctx); err != nil {
			return nil, exitcode.Newf(exitcode.Forbidden, "host delivery not authorized; nothing posted")
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
		return nil, callError("read", "no-page")
	}
	r, err := gen.HostRegistrations(ctx, p.server.client, p.server.app)
	if err != nil {
		return nil, deliveryError("read", err)
	}
	if r == nil {
		return nil, callError("read", "no-page")
	}
	out := map[string]delivery.Recipient{}
	for _, reg := range r.HostRegistrations {
		if reg == nil || reg.HostId != hostID || !reg.Live {
			continue
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
		}
		return out, nil
	}
	return nil, callError("read", "no-page")
}
func (p *postHostPass) Run(ctx context.Context) (postHostDeliverySummary, error) {
	summary := postHostDeliverySummary{}
	fresh, err := hostpoller.LoadState(p.cfg.StateFile)
	if err != nil || fresh.RegisteredHostID() != p.state.RegisteredHostID() {
		return summary, exitcode.Newf(exitcode.Conflict, "host state changed")
	}
	if err := p.cfg.CheckWorktreeAliases(fresh); err != nil {
		return summary, exitcode.Newf(exitcode.Conflict, "host worktrees overlap")
	}
	p.state = fresh
	recipients, err := p.recipients(ctx)
	if err != nil {
		return summary, err
	}
	batches, err := p.server.Batches(ctx)
	if err != nil {
		return summary, err
	}
	blocked := map[string]bool{}
	for _, b := range batches {
		if ctx.Err() != nil {
			return summary, ctx.Err()
		}
		r, ok := recipients[b.WorkerID]
		if !ok || blocked[b.WorkerID] {
			summary.Pending++
			continue
		}
		o := p.deliver.Deliver(ctx, b, r)
		switch o.Kind {
		case delivery.Delivered, delivery.Reconciled:
			summary.Delivered++
			if o.AckPending {
				summary.Pending++
				blocked[b.WorkerID] = true
			}
		case delivery.Held:
			summary.Held++
			blocked[b.WorkerID] = true
		case delivery.Failed:
			summary.Failed++
			blocked[b.WorkerID] = true
		default:
			summary.Pending++
			blocked[b.WorkerID] = true
		}
	}
	return summary, nil
}
