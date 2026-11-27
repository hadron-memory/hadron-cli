package team

// `team host run` with no --config (hadron-cli#3237, slice C3 of hadron-server#5209): the host takes its workers
// from the platform. It loads the host credential(s) `team host join` stored (internal/hostcred), runs one poller
// per credential (R6's recommended answer), and each poll reads that host's platform row and placements with the
// host key only and builds the workers from them and what hostfacts finds on this machine. With --config, today's
// path runs unchanged until X2 (the epic's cutover).

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/auth"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/config"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/hostcred"
	"github.com/hadron-memory/hadron-cli/internal/hostfacts"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
)

// platformDeps are the seams of the platform path; productionPlatformDeps fills them. A test swaps them for a
// fake Orca, a fake launch and a fixed credential list.
type platformDeps struct {
	goos        string
	credentials func() ([]hostcred.Entry, error)
	facts       func() *hostfacts.Facts
	// wire attaches the Orca launch and relaunch to a poller (production: wirePlatformOrca).
	wire func(p *hostpoller.Poller, cfg *hostpoller.Config, srv *hostServer, profiles hostpoller.ProfileSource) error
	// delivery attaches chat delivery once (production: wireDelivery).
	delivery func(p *hostpoller.Poller, cfg *hostpoller.Config, srv *hostServer, emit func(hostpoller.Event))
	// worktreeRun is the runner a worktree.create's `orca worktree create` goes through; the poller closes it
	// over that one call (orcaterm.WorktreeCreateGuard).
	worktreeRun func(orcaBin string) orcaterm.Runner
}

// worktreeCreateTimeout bounds one `orca worktree create`: a checkout of a large repository, not a terminal call.
const worktreeCreateTimeout = 5 * time.Minute

var platformDepsFor = func() platformDeps {
	return platformDeps{
		goos:        runtime.GOOS,
		credentials: hostcred.Load,
		facts: func() *hostfacts.Facts {
			f := hostfacts.New(nil)
			if bin, miss := f.OrcaBin(); miss == nil {
				f.Run = hostfacts.ProductionRunner(bin)
			}
			return f
		},
		wire: wirePlatformOrca,
		delivery: func(p *hostpoller.Poller, cfg *hostpoller.Config, srv *hostServer, emit func(hostpoller.Event)) {
			wireDelivery(p, cfg, srv, emit)
		},
		worktreeRun: func(orcaBin string) orcaterm.Runner {
			return orcaterm.ExecEnv(orcaBin, worktreeCreateTimeout, hostfacts.ScrubbedEnv(os.Environ()))
		},
	}
}

const (
	platformJoinFirst = "this machine holds no host credential: make it a host first with `hadron team host join`, or run with --config <host.yaml>"
	platformNeedsV1   = "this server cannot serve a host with no --config (`team host run` reads the host's platform row, which needs hadron-server 0.47.0 or later, hadron-server#5259); upgrade the server, or run with --config <host.yaml>"
)

// platformHost is one stored credential and the client that carries its key, and only its key.
type platformHost struct {
	entry  hostcred.Entry
	client graphql.Client
	srv    *hostServer
}

// who names a host in a message: its id and its App's id. Never the key.
func (h platformHost) who() string {
	return fmt.Sprintf("host %s (App %s)", h.entry.HostID, h.entry.AppID)
}

func runPlatformHosts(cmd *cobra.Command, f *cmdutil.Factory, once bool) error {
	deps := platformDepsFor()
	if err := OrcaLaunchPlatformError(deps.goos); err != nil {
		return err
	}
	entries, err := deps.credentials()
	if err != nil {
		// The store's own refusal names the file and the fix (a loose mode, a symlink); it never holds a key.
		return exitcode.New(exitcode.AuthRequired, err)
	}
	entries, err = narrowEntries(f, entries)
	if err != nil {
		return err
	}
	facts := deps.facts()
	orcaBin, miss := facts.OrcaBin()
	if miss != nil {
		return exitcode.Newf(exitcode.OrcaUnreachable, "%s", miss.Error())
	}
	keys := hostcred.Keys(entries)
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	emit := hostEventPrinter(f)
	var pollers []*hostpoller.Poller
	var hosts []platformHost
	for _, e := range entries {
		hostEmit := hostEmitter(emit, e.HostID)
		h, p, err := startPlatformHost(ctx, f, deps, facts, orcaBin, e, keys, hostEmit)
		if err != nil {
			if len(entries) == 1 {
				return err
			}
			// Ruling Q4 (a): one host that cannot start is reported; the others run.
			hostEmit(hostpoller.Event{Kind: "host-not-started", Outcome: "FAILED", Detail: err.Error()})
			continue
		}
		hosts, pollers = append(hosts, h), append(pollers, p)
	}
	if len(pollers) == 0 {
		return exitcode.Newf(exitcode.Error, "no host could start")
	}
	return runPollers(ctx, hosts, pollers, once)
}

// hostEmitter stamps each event with its host, so the events of several hosts in one stream can be told
// apart, and with the time when the emitter left it unset.
func hostEmitter(emit func(hostpoller.Event), hostID string) func(hostpoller.Event) {
	return func(e hostpoller.Event) {
		if e.Time.IsZero() {
			e.Time = time.Now()
		}
		e.HostID = hostID
		emit(e)
	}
}

// narrowEntries keeps the credentials this run serves: every one, or --server's and --app's (an App id, as
// `team host show` prints it). None left is exit 3 naming `team host join`.
func narrowEntries(f *cmdutil.Factory, entries []hostcred.Entry) ([]hostcred.Entry, error) {
	if len(entries) == 0 {
		return nil, exitcode.Newf(exitcode.AuthRequired, "%s", platformJoinFirst)
	}
	app := strings.TrimSpace(f.AppFlag)
	if strings.Contains(app, ":") {
		// The store keeps an App's id only, so a URN or <root>:<slug> cannot be matched here.
		return nil, exitcode.Newf(exitcode.Usage, "--app takes the App id that `hadron team host show` prints here, not %q (the store keeps App ids only)", app)
	}
	server := strings.TrimSpace(f.ServerFlag)
	out := []hostcred.Entry{}
	for _, e := range entries {
		if app != "" && e.AppID != app {
			continue
		}
		// As the store matches it (hostcred.Entry.Matches): by host (hostnames ignore case), so a scheme, a path or a
		// trailing slash does not decide it.
		if server != "" && !strings.EqualFold(auth.Host(e.Server), auth.Host(server)) {
			continue
		}
		out = append(out, e)
	}
	if len(out) == 0 {
		ids := []string{}
		for _, e := range entries {
			ids = append(ids, e.AppID+" on "+e.Server)
		}
		sort.Strings(ids)
		return nil, exitcode.Newf(exitcode.AuthRequired, "no stored host credential matches --app/--server (this machine holds hosts of: %s); give --app an App id from that list, or join that App with `hadron team host join`", strings.Join(ids, "; "))
	}
	return out, nil
}

// startPlatformHost checks one credential against the platform and builds its poller. Every refusal is fixed
// text naming the host and the fix; none quotes the server's own message or carries the key.
func startPlatformHost(ctx context.Context, f *cmdutil.Factory, deps platformDeps, facts *hostfacts.Facts, orcaBin string, e hostcred.Entry, keys []string, emit func(hostpoller.Event)) (platformHost, *hostpoller.Poller, error) {
	client, err := api.NewClient(e.Server, e.Key, f.HTTPClient, api.WithCredentialSent(f.Credentials(), api.CredentialHostKey))
	if err != nil {
		return platformHost{}, nil, err
	}
	h := platformHost{entry: e, client: client, srv: &hostServer{client: client, app: e.AppID, serverURL: e.Server}}
	resp, err := readPlatform(ctx, h)
	if err != nil {
		return h, nil, err
	}
	if resp.MyApp == nil || resp.MyApp.Id != e.AppID {
		return h, nil, exitcode.Newf(exitcode.AuthRequired, "%s: the stored key does not answer for this App; join again with `hadron team host join --replace` (as whoever manages the App)", h.who())
	}
	if resp.HostSetup.Source != gen.HostSetupSourceServer {
		// Ruling Q1 (a): App state, not a flag, so exit 5; nothing is registered or launched.
		return h, nil, exitcode.Newf(exitcode.Conflict, "%s: the App's host setup source is %s, so the platform places no workers on a host and there is nothing to run from; a manager switches it with `hadron team host setup --source SERVER`, or run with --config <host.yaml>", h.who(), resp.HostSetup.Source)
	}
	dir, err := config.EnsureHostStateDir(e.HostID)
	if err != nil {
		return h, nil, exitcode.Newf(exitcode.Error, "%s: the host's state directory: %v", h.who(), err)
	}
	build := func(r *gen.HostRunPlatformResponse) *hostpoller.Config {
		return platformConfig(ctx, r, e, facts, orcaBin, dir, emit)
	}
	cfg := build(resp)
	state, err := hostpoller.LoadState(cfg.StateFile)
	if err != nil {
		return h, nil, exitcode.Newf(exitcode.Error, "reading host state %s: %v", cfg.StateFile, err)
	}
	p := hostpoller.New(cfg, h.srv, hostpoller.ExecHost{}, state, e.Key, emit)
	p.SetScrubKeys(keys)
	p.SetPlatformRefresh(func(ctx context.Context) (*hostpoller.Config, error) {
		r, err := readPlatform(ctx, h)
		if err != nil {
			return nil, err
		}
		if r.HostSetup.Source != gen.HostSetupSourceServer {
			return nil, hostpoller.ErrSetupSourceHost
		}
		return build(r), nil
	})
	profiles := hostpoller.AssignmentProfiles{Get: p.Current, Lookup: hostpoller.PlatformAccountLookup(facts)}
	if err := deps.wire(p, cfg, h.srv, profiles); err != nil {
		return h, nil, err
	}
	p.SetOrcaWorktrees(facts, deps.worktreeRun(orcaBin))
	// Wired once; each step skips it while the platform row's deliveryEnabled is false.
	deps.delivery(p, cfg, h.srv, emit)
	p.Fatal = func(err error) bool {
		switch exitcode.FromError(err) {
		case exitcode.AuthRequired, exitcode.Forbidden, exitcode.Usage, exitcode.NotFound:
			return true
		}
		return false
	}
	return h, p, nil
}

// readPlatform is the one platform read, with the host key and no session. A server without V1 rejects the
// whole document as schema skew.
func readPlatform(ctx context.Context, h platformHost) (*gen.HostRunPlatformResponse, error) {
	resp, err := gen.HostRunPlatform(api.WithoutSession(ctx), h.client, h.entry.AppID, h.entry.HostID)
	if err != nil {
		if api.IsSchemaSkew(err) || api.IsFrameworkValidationOnlyFor(err, "agentTeamHost") {
			return nil, exitcode.Newf(exitcode.Error, "%s", platformNeedsV1)
		}
		code := exitcode.FromError(api.MapError(err))
		return nil, exitcode.Newf(code, "%s: reading the host's platform settings was refused: %s", h.who(), failureClassPlatform(code))
	}
	if resp == nil || resp.HostSetup == nil {
		return nil, exitcode.Newf(exitcode.Error, "%s: the server returned no host setup", h.who())
	}
	return resp, nil
}

// failureClassPlatform is the fixed text of a refused platform read: a host key that is revoked or the host
// removed reads as not found or not permitted; the fix is to join again.
func failureClassPlatform(code int) string {
	switch code {
	case exitcode.AuthRequired, exitcode.Forbidden, exitcode.NotFound:
		return failureClassHost(code) + "; if the host was removed or its key revoked, join again with `hadron team host join --replace`"
	}
	return failureClassHost(code)
}

// platformConfig builds one poll's Config from the platform's answer and this machine's facts. Orca not
// answering is not fatal: the workers register with no worktree (so nothing starts) and the host keeps
// checking in, which a lapsed host would not.
func platformConfig(ctx context.Context, r *gen.HostRunPlatformResponse, e hostcred.Entry, facts *hostfacts.Facts, orcaBin, dir string, emit func(hostpoller.Event)) *hostpoller.Config {
	set := hostpoller.PlatformSettings{HostID: e.HostID, Label: e.HostID}
	if r.AgentTeamHost != nil {
		set.Label = r.AgentTeamHost.Label
		set.PollIntervalSeconds = r.AgentTeamHost.PollIntervalSeconds
		set.DeliveryEnabled = r.AgentTeamHost.DeliveryEnabled
	}
	var as []hostpoller.Assignment
	for _, a := range r.HostSetup.Assignments {
		if a == nil || (a.HostId != "" && a.HostId != e.HostID) {
			continue
		}
		x := hostpoller.Assignment{WorkerID: a.WorkerId, Workdir: a.Workdir, Family: a.Family}
		if a.Worker != nil {
			x.WorkerName = a.Worker.Name
		}
		if a.Model != nil {
			x.Model = *a.Model
		}
		if a.Effort != nil {
			x.Effort = *a.Effort
		}
		if a.AccountProfile != nil {
			x.Account = *a.AccountProfile
		}
		as = append(as, x)
	}
	pf := hostpoller.PlatformFacts{OrcaBin: orcaBin, HostDir: dir}
	if len(as) > 0 {
		repos, miss := facts.Repos(ctx)
		var trees []hostfacts.Worktree
		if miss == nil {
			trees, miss = facts.Worktrees(ctx)
		}
		if miss != nil {
			emit(hostpoller.Event{Kind: "facts-unavailable", Detail: miss.Error() + "; the assigned workers register with no worktree until Orca answers"})
		} else {
			pf.Repos, pf.Worktrees = repos, trees
		}
	}
	return hostpoller.BuildPlatformConfig(e.AppID, set, as, pf)
}

// runPollers runs every host's poller. Ruling Q4 (a): a fatal stops only that poller; the process ends when none
// remain, with the code of the first host (in credential order) that ended in error (--once: the worst code of
// the pass).
func runPollers(ctx context.Context, hosts []platformHost, pollers []*hostpoller.Poller, once bool) error {
	errs := make([]error, len(pollers))
	var wg sync.WaitGroup
	for i, p := range pollers {
		wg.Add(1)
		go func(i int, p *hostpoller.Poller) {
			defer wg.Done()
			if once {
				err := p.Step(ctx)
				p.Wait()
				if err != nil {
					errs[i] = hostStepError(fmt.Errorf("%s: %w", hosts[i].who(), err))
				} else if n := p.ReportFailures(); n > 0 {
					errs[i] = exitcode.Newf(exitcode.Unavailable, "%s: %d report(s) did not reach the server; those operations are re-delivered at the next collect", hosts[i].who(), n)
				}
				return
			}
			if err := p.Run(ctx); err != nil {
				errs[i] = hostStepError(fmt.Errorf("%s: %w", hosts[i].who(), err))
			}
		}(i, p)
	}
	wg.Wait()
	var worst error
	for _, err := range errs {
		if err == nil {
			continue
		}
		if worst == nil || (once && exitcode.FromError(err) > exitcode.FromError(worst)) {
			worst = err
		}
	}
	return worst
}
