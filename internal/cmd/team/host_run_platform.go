package team

// `team host run` with no --config (hadron-cli#3237, slice C3 of hadron-server#5209): the host takes its workers
// from the platform. It loads the host credential(s) `team host join` stored (internal/hostcred), runs one poller
// per credential (R6's recommended answer), and each poll reads that host's platform row and placements with the
// host key only and builds the workers from them and what hostfacts finds on this machine. With --config, today's
// path runs unchanged until X2 (the epic's cutover).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/gofrs/flock"
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
	// Every stored key is scrubbed from every agent, including the keys of hosts this run does not serve.
	keys := hostcred.Keys(entries)
	// Every stored host, not only those this run serves: a state-directory claim held by one of them is live.
	stored := entries
	entries, err = narrowEntries(f, entries)
	if err != nil {
		return err
	}
	facts := deps.facts()
	orcaBin, miss := facts.OrcaBin()
	if miss != nil {
		return exitcode.Newf(exitcode.OrcaUnreachable, "%s", miss.Error())
	}
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	emit := hostEventPrinter(f)
	slots := make([]*hostSlot, 0, len(entries))
	started := map[string]hostcred.Entry{}
	// Each started host holds its state directory's run lock for the life of this process (BLIND P2-1 and D-1 on
	// #3301): a second `host run` of the same host is refused, and `team host leave` keeps a running host's
	// directory.
	var lockMu sync.Mutex
	var held []*flock.Flock
	defer func() {
		lockMu.Lock()
		defer lockMu.Unlock()
		for _, l := range held {
			_ = l.Unlock()
		}
	}()
	start := func(ctx context.Context, s *hostSlot) (platformHost, *hostpoller.Poller, error) {
		h, p, err := startPlatformHost(ctx, f, deps, s.facts, orcaBin, s.entry, stored, keys, s.emit)
		if err != nil {
			return h, p, err
		}
		lk, err := lockHostRun(s.entry)
		if err != nil {
			return h, nil, err
		}
		lockMu.Lock()
		held = append(held, lk)
		lockMu.Unlock()
		return h, p, nil
	}
	for _, e := range entries {
		// Each host reads Orca through its own copy of the facts, so no poller shares another's (BLIND P2-5).
		hf := *facts
		s := &hostSlot{entry: e, facts: &hf, emit: hostEmitter(emit, e.HostID)}
		if first, dup := started[e.HostID]; dup {
			// A host's state lives under <state>/hosts/<hostId>, so two pollers of one id would share it.
			s.err = exitcode.Newf(exitcode.Conflict, "host %s (App %s on %s) has the same host id as host %s (App %s) on %s, and this machine keeps a host's state by its id, so only one of them can be a host here; leave the other with `hadron team host leave`", e.HostID, e.AppID, auth.Host(e.Server), first.HostID, first.AppID, auth.Host(first.Server))
		} else {
			s.host, s.poller, s.err = start(ctx, s)
			s.host.entry = e
			// Only a host that started, or that will retry its start, holds the id: one refused for good (another
			// host's claim on the state directory, a revoked key) leaves it to a later entry of that id, which may
			// be the claim's holder (BLIND delta N-A on #3301).
			if s.err == nil || transientStart(s.err) {
				started[e.HostID] = e
			}
		}
		if s.err != nil && len(entries) == 1 && (once || !transientStart(s.err)) {
			return s.err
		}
		slots = append(slots, s)
	}
	return runPollers(ctx, slots, once, start)
}

// hostSlot is one stored credential of this run: the host and its poller once started, or the error its start
// ended with (a transient one is retried in the slot's own goroutine, without --once: #3301 LOCAL F4).
type hostSlot struct {
	entry  hostcred.Entry
	facts  *hostfacts.Facts
	emit   func(hostpoller.Event)
	host   platformHost
	poller *hostpoller.Poller
	err    error
}

// errTransientStart marks a start that ended on a platform read the server did not refuse (a transport failure,
// a gateway 5xx, an internal fault): the same failure mid-run is retried by Run, so at start it is too. Every
// other start failure is fixed (a refusal naming its fix, a local refusal) and ends that host.
type errTransientStart struct{ err error }

func (e errTransientStart) Error() string { return e.err.Error() }
func (e errTransientStart) Unwrap() error { return e.err }

func transientStart(err error) bool {
	var t errTransientStart
	return errors.As(err, &t)
}

// platformStartBackoff is the wait before retry n (1-based) of a host whose start read failed transiently:
// 5s doubling to 5m. A test shortens it.
var platformStartBackoff = func(n int) time.Duration {
	d := 5 * time.Second
	for i := 1; i < n && d < 5*time.Minute; i++ {
		d *= 2
	}
	return min(d, 5*time.Minute)
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
func startPlatformHost(ctx context.Context, f *cmdutil.Factory, deps platformDeps, facts *hostfacts.Facts, orcaBin string, e hostcred.Entry, stored []hostcred.Entry, keys []string, emit func(hostpoller.Event)) (platformHost, *hostpoller.Poller, error) {
	client, err := api.NewClient(e.Server, e.Key, f.HTTPClient, api.WithCredentialSent(f.Credentials(), api.CredentialHostKey))
	if err != nil {
		return platformHost{}, nil, err
	}
	h := platformHost{entry: e, client: client, srv: &hostServer{client: client, app: e.AppID, serverURL: e.Server}}
	// The claim on the state directory is local: judged before the platform read, so a host whose server is
	// down is refused for good by another host's claim rather than taking the id and retrying (BLIND delta N-A
	// at 10bf3a7b). An unclaimed directory is claimed after the read, once the key answers for the App.
	if err := peekHostClaim(e, stored); err != nil {
		return h, nil, err
	}
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
	if err := claimHostState(dir, e, stored); err != nil {
		return h, nil, err
	}
	last := &lastFacts{}
	build := func(r *gen.HostRunPlatformResponse) *hostpoller.Config {
		return platformConfig(ctx, r, e, facts, orcaBin, dir, emit, last)
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

// hostIdentityFile names, inside a host's state directory, the host that owns it: a host id is unique only on
// its server, so the directory is claimed by server and App, and another host of the same id is refused rather
// than sharing its state, in this process or another (BLIND delta N1 on #3301).
const hostIdentityFile = "host-identity.json"

type hostIdentity struct {
	Server string `json:"server"`
	AppID  string `json:"appId"`
}

// claimHostState stamps an unclaimed state directory with this host and refuses one another host claimed. The
// stamp is written whole to a temporary file and linked into place, which fails if the file exists, so of two
// processes claiming one directory at once exactly one wins and the other reads its claim (BLIND delta N-B and
// SECOND P3 on #3301); a reader never sees a half-written stamp.
func claimHostState(dir string, e hostcred.Entry, stored []hostcred.Entry) error {
	path := filepath.Join(dir, hostIdentityFile)
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		won, cerr := createHostIdentity(dir, path, identityOf(e))
		if cerr != nil {
			return exitcode.Newf(exitcode.Error, "host %s (App %s): claiming the host's state directory %s: %v", e.HostID, e.AppID, dir, cerr)
		}
		if won {
			return nil
		}
		raw, err = os.ReadFile(path)
	}
	if err != nil {
		return exitcode.Newf(exitcode.Error, "host %s (App %s): reading %s: %v", e.HostID, e.AppID, path, err)
	}
	return judgeHostClaim(raw, dir, path, e, stored)
}

// peekHostClaim judges an existing claim without writing one; no claim, or none readable yet, is left to
// claimHostState after the platform read.
func peekHostClaim(e hostcred.Entry, stored []hostcred.Entry) error {
	dir, err := config.HostStateDir(e.HostID)
	if err != nil {
		return nil
	}
	path := filepath.Join(dir, hostIdentityFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return judgeHostClaim(raw, dir, path, e, stored)
}

// judgeHostClaim refuses a claim that is not this host's: exit 5 naming `team host leave` while the claiming host
// is stored here, else naming the directory to remove.
func judgeHostClaim(raw []byte, dir, path string, e hostcred.Entry, stored []hostcred.Entry) error {
	want := identityOf(e)
	var got hostIdentity
	if err := json.Unmarshal(raw, &got); err != nil || got.Server == "" || got.AppID == "" {
		return exitcode.Newf(exitcode.Error, "host %s (App %s): %s does not say which host owns this state directory; remove it if no other host of this id runs here", e.HostID, e.AppID, path)
	}
	if got == want {
		return nil
	}
	for _, o := range stored {
		if identityOf(o) == got {
			return exitcode.Newf(exitcode.Conflict, "host %s (App %s on %s): this machine's state directory for host id %s belongs to the host of App %s on %s, and a host's state is kept by its id, so only one of them can be a host here; leave the other with `hadron team host leave`", e.HostID, e.AppID, want.Server, e.HostID, got.AppID, got.Server)
		}
	}
	// The claim's host is no longer stored here (left with an older CLI, or its credential removed by hand):
	// its state is not this host's to inherit, so the directory is named for removal (LOCAL delta D2 on #3301).
	return exitcode.Newf(exitcode.Conflict, "host %s (App %s on %s): this machine's state directory %s was claimed by the host of App %s on %s, which this machine's host credential store no longer holds; its state is not this host's, so remove that directory and run again", e.HostID, e.AppID, want.Server, dir, got.AppID, got.Server)
}

// identityOf is the claim a stored host makes on its state directory.
func identityOf(e hostcred.Entry) hostIdentity {
	return hostIdentity{Server: strings.ToLower(auth.Host(e.Server)), AppID: e.AppID}
}

// releaseHostState removes a host's state directory once the host is left, when the directory's claim names
// that host: its registration, assignments and operations ended with it, and a later host of the same id must
// start clean rather than be refused for good (LOCAL delta D2 on #3301). A directory another host claims, or
// one with no claim, is kept. released reports whether it was removed.
func releaseHostState(e hostcred.Entry) (released bool, err error) {
	dir, err := config.HostStateDir(e.HostID)
	if err != nil {
		return false, err
	}
	if fi, err := os.Lstat(dir); err != nil || !fi.IsDir() {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		if err == nil {
			return false, fmt.Errorf("%s is not a directory", dir)
		}
		return false, err
	}
	raw, err := os.ReadFile(filepath.Join(dir, hostIdentityFile))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var got hostIdentity
	if json.Unmarshal(raw, &got) != nil || got != identityOf(e) {
		return false, nil
	}
	// A `host run` still running this host holds its run lock: its directory is kept (BLIND delta D-1).
	lk := flock.New(filepath.Join(dir, hostRunLockFile))
	ok, err := lk.TryLock()
	if err != nil {
		return false, err
	}
	if !ok {
		return false, fmt.Errorf("a `hadron team host run` of this host is still running, so %s is kept; stop it, then remove that directory", dir)
	}
	// Released before the removal: Windows does not delete a locked file.
	_ = lk.Unlock()
	if err := os.RemoveAll(dir); err != nil {
		return false, err
	}
	return true, nil
}

// createHostIdentity writes the stamp only if none exists: false, nil when another process stamped it first.
func createHostIdentity(dir, path string, id hostIdentity) (bool, error) {
	body, err := json.Marshal(id)
	if err != nil {
		return false, err
	}
	tmp, err := os.CreateTemp(dir, ".host-identity-*")
	if err != nil {
		return false, err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(append(body, '\n')); err != nil {
		_ = tmp.Close()
		return false, err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return false, err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return false, err
	}
	err = linkFile(tmp.Name(), path)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, os.ErrExist):
		return false, nil
	}
	// A file system without hard links (BLIND delta D-2): an exclusive create is still one winner, though a
	// reader racing the write may see it short and is refused as unreadable, not as another host's.
	out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err := out.Write(append(body, '\n')); err != nil {
		_ = out.Close()
		return false, err
	}
	return true, out.Close()
}

// linkFile is os.Link; a test makes it fail as a file system without hard links does.
var linkFile = os.Link

// hostRunLockFile, inside a host's state directory, is held by the `host run` that runs that host.
const hostRunLockFile = "host-run.lock"

// lockHostRun takes the host's run lock without waiting: held by another process, it is exit 5.
func lockHostRun(e hostcred.Entry) (*flock.Flock, error) {
	dir, err := config.HostStateDir(e.HostID)
	if err != nil {
		return nil, exitcode.Newf(exitcode.Error, "host %s (App %s): the host's state directory: %v", e.HostID, e.AppID, err)
	}
	lk := flock.New(filepath.Join(dir, hostRunLockFile))
	ok, err := lk.TryLock()
	if err != nil {
		return nil, exitcode.Newf(exitcode.Error, "host %s (App %s): locking %s: %v", e.HostID, e.AppID, lk.Path(), err)
	}
	if !ok {
		return nil, exitcode.Newf(exitcode.Conflict, "host %s (App %s): another `hadron team host run` on this machine is already running this host (it holds %s); stop that one first", e.HostID, e.AppID, lk.Path())
	}
	return lk, nil
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
		switch code {
		case exitcode.AuthRequired, exitcode.Forbidden, exitcode.NotFound, exitcode.Usage, exitcode.Conflict:
			return nil, exitcode.Newf(code, "%s: reading the host's platform settings was refused: %s", h.who(), failureClassPlatform(code))
		}
		out := exitcode.Newf(code, "%s: reading the host's platform settings failed: %s", h.who(), failureClassPlatform(code))
		// Not refused by the server for good (#3301 LOCAL F4): a start retries it, as Run retries it mid-run.
		return nil, errTransientStart{out}
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

// lastFacts is what Orca last answered for one host: an Orca that stops answering keeps these, so a brief
// outage never empties the worktree paths of agents that are still running (BLIND P2-4 on #3301). down marks
// the episode, so facts-unavailable is said once per outage, not every poll.
type lastFacts struct {
	ok    bool
	down  bool
	repos []hostfacts.Repo
	trees []hostfacts.Worktree
}

// platformConfig builds one poll's Config from the platform's answer and this machine's facts. Orca not
// answering is not fatal: the worktrees Orca last answered are kept (none at a start with Orca silent, so the
// workers register with no worktree and nothing starts), and the host keeps checking in, which a lapsed host
// would not.
func platformConfig(ctx context.Context, r *gen.HostRunPlatformResponse, e hostcred.Entry, facts *hostfacts.Facts, orcaBin, dir string, emit func(hostpoller.Event), last *lastFacts) *hostpoller.Config {
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
		switch {
		case miss == nil:
			if last.down {
				emit(hostpoller.Event{Kind: "facts-available", Detail: "Orca answers again; the worktrees are resolved from its answer"})
			}
			*last = lastFacts{ok: true, repos: repos, trees: trees}
		case !last.down:
			last.down = true
			detail := miss.Error() + "; the assigned workers register with no worktree until Orca answers"
			if last.ok {
				detail = miss.Error() + "; the worktrees Orca last answered are kept until it answers again"
			}
			emit(hostpoller.Event{Kind: "facts-unavailable", Detail: detail})
		}
		if last.ok {
			pf.Repos, pf.Worktrees = last.repos, last.trees
		}
	}
	return hostpoller.BuildPlatformConfig(e.AppID, set, as, pf)
}

// namedHostError names the host an error came from, once: a platform read's own error already starts with it.
func namedHostError(h platformHost, err error) error {
	if strings.HasPrefix(err.Error(), h.who()) {
		return err
	}
	return fmt.Errorf("%s: %w", h.who(), err)
}

// runPollers runs every host's poller. Ruling Q4 (a): a fatal stops only that poller, and a host that could not
// start is reported and joins the same selection; the process ends when none remain, with the code of the first
// host (in credential order) that ended in error (--once: the worst code of the pass). Without --once, a host
// whose start failed transiently retries its start with backoff in its own goroutine, then runs.
func runPollers(ctx context.Context, slots []*hostSlot, once bool, restart func(context.Context, *hostSlot) (platformHost, *hostpoller.Poller, error)) error {
	errs := make([]error, len(slots))
	var wg sync.WaitGroup
	for i, s := range slots {
		wg.Add(1)
		go func(i int, s *hostSlot) {
			defer wg.Done()
			if s.err != nil && !once && transientStart(s.err) {
				s.host, s.poller, s.err = retryStart(ctx, s, restart)
				if s.err == nil && s.poller == nil {
					return // cancelled while retrying: a stop, not a failure
				}
			}
			if s.err != nil {
				if len(slots) > 1 {
					s.emit(hostpoller.Event{Kind: "host-not-started", Outcome: "FAILED", Detail: s.err.Error()})
				}
				errs[i] = s.err
				return
			}
			p := s.poller
			if once {
				err := p.Step(ctx)
				p.Wait()
				if err != nil {
					errs[i] = hostStepError(namedHostError(s.host, err))
				} else if n := p.ReportFailures(); n > 0 {
					errs[i] = exitcode.Newf(exitcode.Unavailable, "%s: %d report(s) did not reach the server; those operations are re-delivered at the next collect", s.host.who(), n)
				}
				return
			}
			if err := p.Run(ctx); err != nil {
				errs[i] = hostStepError(namedHostError(s.host, err))
			}
		}(i, s)
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

// retryStart retries a host's start while it fails transiently: one host-start-retrying event for the episode,
// one host-started when it starts. A fixed failure ends it with that failure; a cancelled context ends it with
// neither a poller nor an error.
func retryStart(ctx context.Context, s *hostSlot, restart func(context.Context, *hostSlot) (platformHost, *hostpoller.Poller, error)) (platformHost, *hostpoller.Poller, error) {
	s.emit(hostpoller.Event{Kind: "host-start-retrying", Outcome: "", Detail: s.err.Error() + "; retrying the start with backoff"})
	for n := 1; ; n++ {
		select {
		case <-ctx.Done():
			return s.host, nil, nil
		case <-time.After(platformStartBackoff(n)):
		}
		h, p, err := restart(ctx, s)
		h.entry = s.entry
		switch {
		case err == nil:
			s.emit(hostpoller.Event{Kind: "host-started", Detail: fmt.Sprintf("%s started after %d retry(ies)", h.who(), n)})
			return h, p, nil
		case ctx.Err() != nil:
			return h, nil, nil
		case !transientStart(err):
			return h, nil, err
		}
	}
}
