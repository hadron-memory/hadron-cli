package team

// `team host run|status` — the host poller (hadron-cli#831, cor:agt:070):
// the host's own side of host operations. It authenticates with an App key
// carrying host:operate for ONE host (cor:agt:070:04), read from the host
// configuration's key_file — never from HADRON_TOKEN or the token store,
// which the agents it launches use as their holder's own credential, and
// never from an environment variable. The poller never puts the key in a
// launched agent's environment, argv or files: worker.start hands it the
// single-use launch grant and nothing else. Keeping the key from the agent's
// OS user is the deployment's (see ReadHostKey).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode/utf16"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/delivery"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// hostServer is the poller's Server over GraphQL, with the host key.
type hostServer struct {
	serverURL string
	client    graphql.Client
	app       string
	// noRefusedAt is when this server rejected the document that asks for
	// refusedWorkers (schema skew) while the plain one worked (unix nanos; 0 =
	// not pinned): it lacked hadron-server#4241, so polls skip the extended
	// document, and look again every refusedReprobeEvery, so a server upgraded
	// while the host runs is noticed (cli#2091, cli#2100).
	noRefusedAt atomic.Int64
	// now dates a batch read, for its proof's expiry; time.Now when nil.
	now func() time.Time
}

// registerHost makes the one registration. It asks for refusedWorkers in a
// SEPARATE document and falls back to the plain one when the server rejects it
// as schema skew (a server without hadron-server#4241): the plain registration
// is unchanged, so no poller breaks against an older server (cli#2091). The
// fallback is remembered, so later polls go straight to the plain document,
// except for one fresh try every refusedReprobeEvery. known is false when the server cannot report refusals.
// refusedReprobeEvery is how often a host pinned to the plain registration asks
// the extended document again: one extra request per interval against an old
// server, so an upgrade is noticed without a restart (cli#2100).
const refusedReprobeEvery = 30 * time.Minute

func (s *hostServer) registerHost(ctx context.Context, in *gen.RegisterHostInput) (reg *gen.HostRegistrationFields, refused []*gen.RegisterHostWithRefusedRegisterHostHostRegistrationRefusedWorkersHostRegistrationRefusal, known bool, err error) {
	now := time.Now
	if s.now != nil {
		now = s.now
	}
	at := s.noRefusedAt.Load()
	if at == 0 || now().Sub(time.Unix(0, at)) >= refusedReprobeEvery {
		resp, err := gen.RegisterHostWithRefused(ctx, s.client, in)
		switch {
		case err == nil:
			if resp.RegisterHost == nil {
				return nil, nil, false, exitcode.Newf(exitcode.Error, "the server returned no registration")
			}
			r := resp.RegisterHost
			s.noRefusedAt.Store(0)
			return &r.HostRegistrationFields, r.RefusedWorkers, true, nil
		case !api.IsSchemaSkew(err):
			return nil, nil, false, api.MapError(err)
		}
		// Schema skew: the server may lack refusedWorkers. Ask the plain
		// document; only if it succeeds is the field proved missing.
		plain, perr := gen.RegisterHost(ctx, s.client, in)
		if perr != nil {
			return nil, nil, false, api.MapError(perr)
		}
		if plain.RegisterHost == nil {
			return nil, nil, false, exitcode.Newf(exitcode.Error, "the server returned no registration")
		}
		s.noRefusedAt.Store(now().UnixNano())
		return &plain.RegisterHost.HostRegistrationFields, nil, false, nil
	}
	plain, err := gen.RegisterHost(ctx, s.client, in)
	if err != nil {
		return nil, nil, false, api.MapError(err)
	}
	if plain.RegisterHost == nil {
		return nil, nil, false, exitcode.Newf(exitcode.Error, "the server returned no registration")
	}
	return &plain.RegisterHost.HostRegistrationFields, nil, false, nil
}

func (s *hostServer) Register(ctx context.Context, r hostpoller.Registration) (hostpoller.RegisteredHost, error) {
	in := &gen.RegisterHostInput{
		Capabilities: append([]string{}, r.Capabilities...),
		AppRef:       s.app, Label: r.Label, PollIntervalSeconds: r.PollIntervalSeconds,
		Workers: []*gen.RegisterHostWorkerInput{},
	}
	for _, w := range r.Workers {
		in.Workers = append(in.Workers, &gen.RegisterHostWorkerInput{
			Worker: w.Worker, Family: w.Family, Worktrees: append([]string{}, w.Worktrees...),
		})
	}
	reg, refused, known, err := s.registerHost(ctx, in)
	if err != nil {
		return hostpoller.RegisteredHost{}, err
	}
	out := hostpoller.RegisteredHost{HostID: reg.HostId, RefusedKnown: known}
	for _, w := range reg.Workers {
		if w == nil {
			continue
		}
		rw := hostpoller.RegisteredHostWorker{WorkerID: w.WorkerId}
		if w.Worker != nil {
			rw.Name = w.Worker.Name
			if w.Worker.Urn != nil {
				rw.URN = *w.Worker.Urn
			}
		}
		out.Workers = append(out.Workers, rw)
	}
	// Per-row refusals are text from the server: made safe to print, and the
	// worker is only ever shown, never used to decide anything.
	for _, r := range refused {
		if r == nil {
			continue
		}
		out.Refused = append(out.Refused, hostpoller.RefusedWorker{Worker: output.SafeCell(r.Worker), Code: output.SafeCell(r.Code)})
	}
	return out, nil
}

// hostCollectBatch is the page a host asks collectHostOperations for. It is
// today's EFFECTIVE batch, made explicit (cli#968): hadron-server omits it as
// 50 (cap 200, resolvers.hostOperation.ts), and a default change there must not
// silently shrink a host's page. It is an internal compatibility value, not a
// flag and not a "whole backlog" claim: a Step still collects once, oldest
// first, and a full page is not proof of progress or exhaustion (in-flight
// operations can refill it; that limitation is unchanged and unfixed here).
const hostCollectBatch = 50

func (s *hostServer) Collect(ctx context.Context) ([]hostpoller.Operation, error) {
	first := hostCollectBatch
	resp, err := gen.CollectHostOperations(ctx, s.client, s.app, &first)
	if err != nil {
		return nil, api.MapError(err)
	}
	ops := []hostpoller.Operation{}
	for _, o := range resp.CollectHostOperations {
		if o == nil {
			continue
		}
		op := operationFrom(&o.HostOperationFields)
		if o.LaunchGrant != nil {
			op.LaunchGrant = *o.LaunchGrant
		}
		ops = append(ops, op)
	}
	return ops, nil
}

func (s *hostServer) Report(ctx context.Context, id, outcome, worktree, reason string) (hostpoller.Operation, error) {
	in := &gen.ReportHostOperationInput{AppRef: s.app, Id: id, Outcome: gen.HostOperationOutcome(outcome)}
	if worktree != "" {
		in.Worktree = &worktree
	}
	if reason != "" {
		// The server bounds a reason at 2000 characters, counted as JS
		// string length (UTF-16 units); a git error can run longer, and a
		// refused report would leave the operation in flight.
		reason = truncateUTF16(reason, 2000)
		in.FailureReason = &reason
	}
	resp, err := gen.ReportHostOperation(ctx, s.client, in)
	if err != nil {
		if api.HasErrorCode(err, "HOST_OPERATION_NOT_DONE") {
			return hostpoller.Operation{}, hostpoller.ErrNotDone
		}
		if api.HasErrorCode(err, "HOST_OPERATION_ALREADY_REPORTED") {
			// The refusal carries what the server recorded in
			// extensions.status (server #4062), spelled as the host sends
			// it; the poller acts on that recorded outcome, never on the
			// message text or an inference (#1474).
			recorded, _ := api.ErrorCodeExtension(err, "HOST_OPERATION_ALREADY_REPORTED", "status")
			return hostpoller.Operation{}, hostpoller.ErrFinalWithRecorded(api.MapError(err).Error(), recorded)
		}
		return hostpoller.Operation{}, api.MapError(err)
	}
	if resp.ReportHostOperation == nil {
		return hostpoller.Operation{}, exitcode.Newf(exitcode.Error, "the server returned no operation")
	}
	return operationFrom(&resp.ReportHostOperation.HostOperationFields), nil
}

func operationFrom(o *gen.HostOperationFields) hostpoller.Operation {
	op := hostpoller.Operation{ID: o.Id, Type: o.Type, WorkerID: o.WorkerId, Status: string(o.Status), IssuedBy: string(o.IssuedBy), Args: map[string]any{}}
	if len(o.Args) > 0 {
		_ = json.Unmarshal(o.Args, &op.Args)
	}
	if o.Result != nil && len(*o.Result) > 0 {
		_ = json.Unmarshal(*o.Result, &op.Result)
	}
	if o.FailureReason != nil {
		op.FailureReason = *o.FailureReason
	}
	return op
}

// loadHostPoller reads the configuration and the key, and builds the client
// the poller uses: the host key only, and no session header (a session on a
// host call is refused APP_KEY_SESSION_BOUND).
func loadHostPoller(f *cmdutil.Factory, path string) (*hostpoller.Config, string, *hostServer, error) {
	if strings.TrimSpace(path) == "" {
		return nil, "", nil, exitcode.Newf(exitcode.Usage, "--config is required: the host configuration file")
	}
	cfg, err := hostpoller.LoadConfig(path)
	if err != nil {
		return nil, "", nil, exitcode.New(exitcode.Usage, err)
	}
	key, err := cfg.ReadHostKey()
	if err != nil {
		return nil, "", nil, exitcode.New(exitcode.AuthRequired, err)
	}
	server := cfg.Server
	if server == "" {
		if server, err = f.Server(); err != nil {
			return nil, "", nil, err
		}
	}
	client, err := api.NewClient(server, key, f.HTTPClient, api.WithCredentialSent(f.Credentials(), api.CredentialHostKey))
	if err != nil {
		return nil, "", nil, err
	}
	return cfg, key, &hostServer{client: client, app: cfg.App, serverURL: server}, nil
}

func newCmdTeamHostRun(f *cmdutil.Factory) *cobra.Command {
	var configPath string
	var once bool
	cmd := &cobra.Command{
		Use:   "run --config <host.yaml> [--once]",
		Short: "Run this host's poller: register, collect, carry out, report",
		Long: `Run the host poller (cor:agt:070). Every poll it registers the workers this
host can launch (with their family and existing worktrees), collects the
operations issued to it, carries out only the closed set of five, and
reports every outcome. One poller per host or container.

  worktree.create  git worktree add from a configured repo, as
                   <repo>/<label> under worktrees_root
  worker.start     run the worker's configured launch argv in a registered
                   worktree with HADRON_LAUNCH_GRANT, then report once the
                   agent's session binds (team session start reads the grant);
                   refused before launching when any process is working in the
                   tree (Linux /proc, the pid and a short redacted argv
                   named), and a start that fails stops the process group it
                   launched and leaves no agents entry in the state file.
                   With reset_on_start (default off; TRANSITIONAL like
                   stop_on_end, replaced by the Agent's lifecycle) the
                   worker's EXISTING worktree is first brought back to a
                   clean base (hadron-cli#2057): a dirty or untracked tree
                   refuses the start (WORKTREE_DIRTY), as does a HEAD on no
                   remote branch (WORKTREE_UNPUSHED), reported as the
                   failure so the task waits; commits no remote branch has
                   are kept as refs/hadron/rescue/<utc>/<branch>; then the
                   tree is fetched from origin and checked out detached at
                   origin's default branch. A directory with no Git starts
                   with no reset. Nothing is created or removed.
                   With launch_via: orca (default exec; Linux; TRANSITIONAL)
                   it launches through an Orca terminal instead, from the
                   worker's launcher_profile and fixed per-family templates,
                   always fresh (never --resume), the grant and first prompt
                   passed through a 0600 file beside the state, never typed;
                   the agent runs plainly in Orca's terminal scope; it never
                   falls back to the exec path. Both launcher_profile modes
                   (long, short) start the same interactive agent, so a
                   stalled run shows on screen and can be taken over; short
                   no longer runs headless or exits when its prompt ends.
                   A short run is ended by its own agent's session end, by
                   worker.end's stop, or by the host after its no-agent
                   proof (cli#2201)
  worker.deliver   reported failed: no delivery path yet (hadron-cli#829)
  worker.end       run the worker's configured end prompt, wait end_wait
                   (default 15m), and report FAILED (the server records an
                   ended session as success; the host ends none). With
                   stop_on_end (default never; manager = only when someone
                   who manages the App issued it; always = a short-lived
                   worker) it then stops the worker's agent: after a
                   DELIVERED end prompt it waits stop_wait (default 60s,
                   not end_wait), with none it stops at once; SIGTERM to
                   the process group, SIGKILL after 5s. Only a process
                   found in the worker's registered worktree whose session
                   binding is not another worker's or session's is touched;
                   the report names each pid stopped or says none was
                   touched. Each signal is sent holding the worktree's
                   session-binding lock (the one session start writes
                   under), released at once after it: a bind landing
                   between the check and the signal waits, and a lock
                   not taken within 2s declines the stop (#2068).
                   A plain directory whose binding cache or lock file is
                   missing is declined, not created, when this host runs
                   as a different user from the worktree's owner; the
                   same refusal applies wherever the host takes that
                   lock (the stop, a reset relaunch and its cleanup).
                   An existing lock is opened under ordinary file
                   permissions, so a user who cannot read and write it
                   is refused by the OS, not by this rule.
                   TRANSITIONAL: the field goes with host.yaml
                   (Agent.lifecycle replaces it, hadron-server#4224)
  worktree.remove  only the operation's own worker's registered tree;
                   refused for uncommitted or untracked changes, no
                   upstream, unpushed commits, an agent this host launched
                   (recorded across restarts), any process working in it
                   (Linux /proc), or a tree whose worker.end failed

One worker's operations run one at a time, in the order collected, so a
remove queued behind a start or an end sees what they left. A worktree
belongs to one worker: the config refuses one listed for two.

An unknown type is refused here as well as by the server, and reported
failed. There is no shell passthrough of any kind.

AGENTLESS SESSION END (cli#2201): a short-lived worker that finished its task
without ending its session is closed only on the strict process census (the
one recovery uses). worker.end for a stop_on_end: always worker with no live
agent reports FAILED leading AGENT_ABSENT when the census proves its
worktrees empty; the periodic pass (below: typed launch_via: orca, APPLY)
asks the server to end the session of a worker whose desired state is
STOPPED/NO_OPEN_TASK, after a fresh matching read. An error, an unproven
census, an agent, a leave-alone, paused or long-lived worker never ends one.

PERIODIC RECOVERY (Linux, typed launch_via: orca only) preserves an open
session whose agent vanished. It requires APPLY and current RUNNING desired
state, matching worker/session/App/server/worktree identity, two consecutive
proven-absent process sweeps, exactly one running own-user orca-daemon scope
and a successful terminal list. Missing systemd proof or either readiness
check refuses RELAUNCH_UNPROVEN. Readiness is rechecked before spending each
attempt; the persistent limit is three per worker per rolling hour. Restart
preserves that budget but requires two fresh absence observations. Launch
uses the profile and a fresh recover prompt, never --resume or session start.
Only its own pinned process can pass verification or be stopped on failure.
Plain bindings work without Git. Host up reports this work; it never launches
an open-session agent. Relaunch JSON events name workerId/sessionId/reason
and attempt; noticeFailed plus localCause/noticeCause exposes a local or
undelivered repair. SETUP_FAILED identifies setup/disk failure before the
launcher runs; VERIFY_FAILED identifies an unproven post-launch process check.
An actual expired launch/proof wait remains TIMEOUT. Older servers rejecting
the new reasons as schema skew keep FAILED with its local cause; no borrowed
code or false CAP_REACHED is sent.

After a verified relaunch, optional dead-tab cleanup runs AFTER the relaunch
notice. Set orca_launch.cleanup_dead_tabs: true to enable it (default false;
leave it off until the host manager validates the live close path). Every
pane of an eligible tab must be disconnected, unidentified and in this
worktree; connected shells may hold a human's build, ssh or editor and stay
open. The tracked tab always stays open. Cleanup reacquires admission and
rechecks binding, desired state and strict process proof before closing.
The relaunched PID must be the sole agent match: even a nested agent in its
own tree leaves the old tab open. Disconnected alone is not proof. Local
relaunch-terminal-cleanup events say why a tab was closed or retained;
cleanup delay or panic cannot suppress the preceding relaunch notice.
Non-Linux hosts close no unidentified tab. No old terminal is reused.


THE HOST KEY is never put in a launched agent's environment, argv or files.
It is read only from key_file, which must be mode 0600 or tighter — never
from HADRON_TOKEN, the token store, or any environment variable. The agent
gets the parent environment minus HADRON_TOKEN, HADRON_HOST_KEY,
HADRON_LAUNCH_GRANT and any variable carrying the key, plus its own grant,
and binds on its holder's own stored token. An agent running as the SAME OS
user as the poller can still read key_file itself: run the poller as its own
user, with key_file readable by that user only, to close that.

WHERE AN OCCUPANT IS NAMED (Linux /proc; other platforms name none), its
argv is redacted by keyword (token, secret, password, auth, cookie, key, …),
by the credential flags of curl, sshpass and mysql, by URL userinfo, and by a
fixed list of known token prefixes (hdr_user_, hdr_app_, GitHub, GitLab,
Slack, sk-, AWS access-key ids, the webhook hpt value and path secret). A
credential with neither a keyword nor a listed prefix still renders
verbatim: there is no entropy or general secret scanning.

THE LAUNCH COMMAND must stay in the foreground for the agent's whole life:
its exit is read as the agent's. A launcher that starts a detached session
and returns (a tmux or wrapper script) is reported failed if no session has
bound by then, which revokes the grant. A start reported failed stops what
it launched and forgets its entry in the state file, so a wrapper that keeps
relaunching its agent does not outlive the failure. On Unix that stop is
TERM, a short grace, then KILL to the whole process group — which still
covers a wrapper's children after the launcher itself exited. On Linux the
launch's group id is its own for the agent's whole tracked life: an exited
leader is kept as an unreaped zombie while anything of its group still runs,
and is reaped only once the group is observed empty (or after the stop's
KILL), so another process cannot take the group's number while the launch
may still signal it — the stop can only ever reach this launch's group. A
launch that provably left nothing behind (its group was empty at the exit)
confirms a later stop without signalling anything at all. A Linux kernel
or policy that refuses waitid (an exotic environment) has no hold either:
the stop takes the same shape as the platforms below. Other Unix
platforms (macOS, the BSDs) reap the leader at its exit and have no such
hold, so their stop signals the group id only while the leader is still
live — the number is bound to a live leader and cannot be reused — and
once the leader's exit is observed there, the stop refuses to reach for
the number (an exit frees it at a moment no mutex governs, and a process
that took the reused number would answer a probe or take a signal): the
stop reports it unconfirmed and keeps the records instead. On those
platforms, and on a Linux host that refuses waitid (the same no-hold
path), a stop that signals a live launch is reported unconfirmed even when
the launch did stop, and its entry is kept (an approximation: a probe that
lands between the kernel's reap and the exit being recorded can confirm
soundly). The instant
between a stop's last exit-check and its signal is those platforms'
un-closable window — no syscall there both proves the number is bound and
signals it. Linux is the hardened platform for this stop. On Windows the
process tree is force-killed at once. If the launch's exit is observed after
the binding-authorization wait, taskkill is skipped (its pid may have been
reused). The final exit check and taskkill are not atomic: the launch can
still exit between them. Children it left behind cannot be reached without
a Job Object, which this runner does not yet assign. Binding authority stays
held until taskkill returns. Its cancellation is requested after 10 seconds;
a timeout leaves the stop unconfirmed and its records kept even if taskkill
then reports success. The tree may already be dead or partly stopped.
Available runner error/output is retained as bounded, control-safe single-line
diagnostic context. If a successful helper exits but inherited output pipes
stay open past the 1-second WaitDelay, output collection is incomplete: the
stop remains unconfirmed and records are kept. Cancellation takes precedence
if both conditions occur. These are not real-time OS latency guarantees.
The stopper waits up to 2 seconds to acquire binding authority. Session writers
block on flock/LockFileEx without that deadline; a session start can wait until
the synchronous Windows taskkill and cancellation/drain return releases it.
An entry survives a stop that could not be confirmed. A refusal
saying the operation was already reported stops the launch only when it
names the recorded outcome as failed; one naming success, or naming nothing,
leaves what was launched running.

A worker.start waiting for its bind is re-delivered by each collect and
skipped as in flight. The server must not re-mint its grant meanwhile (a
re-collect omits a start whose grant is still live): run this against a
server with that fix.

Capabilities are an explicit full declaration on every registration refresh:
Omitted or [] means none. On Linux, capabilities: [worker.reset-context]
wires the reset protocol and requires an absolute reset_context.orca_bin
(reserved for the C3 reader). This driver cannot yet bind an Orca terminal
and its controlling tty to the process it launched. It therefore remains
built and off: even a matching binding/process refuses NOT_IDLE:
TARGET_UNBOUND, before a new claim or stop. An idle-looking screen at the
same worktree proves nothing about that process. C3 (hadron-cli#2058) owns
the terminal-aware success path; enabling a config cannot bypass this refusal.
Existing Git/identity refusals and no-signal recovery of recorded resets stay.
Leave the capability unset until that path is built and separately authorized.
Installing this CLI grants no live-host activation. Unknown declarations
still warn on stderr before registration and add no handler.

The configuration (YAML):

  app: hrn:app:<root>:<slug>
  label: orca1                      # default: hostname
  poll_interval_seconds: 30
  key_file: /etc/hadron/host.key    # an App key with host:operate, mode 0600
  worktrees_root: /srv/worktrees
  state_file: /var/lib/hadron/host-state.json   # default: beside the config
  # Reserved C3 reset configuration; this build stays fail-closed, no activation:
  # capabilities: [worker.reset-context]
  # reset_context:
  #   orca_bin: /absolute/path/to/orca
  repos:
    hadron-memory/hadron-cli: /srv/repos/hadron-cli
  workers:
    jonas:                          # name in the App, URN or id
      family: claude
      launch: [claude-worker, --model, opus]
      end: [orca-send, jonas, "please end your session with a handoff"]
      end_wait: 15m
      stop_on_end: never            # never (default) | manager | always
      stop_wait: 60s                # the wait before a stopping end acts
      reset_on_start: false         # true: reset the existing worktree at worker.start
      # recover: [recover-worker]   # required for this worker to reset
      worktrees: [hadron-cli/jonas-2]
    # Through Orca (hadron-cli#2058; launch is not needed then):
    # iris:
    #   family: claude
    #   launch_via: orca
    #   # mode: long|short. Both start the same interactive agent; short is for a
    #   # worker that ends its own session when its task is done.
    #   launcher_profile: {family: claude, mode: short, model: opus, account: holger}
    #   disk_tmp: /home/orca/scratch/tmp/iris   # TMPDIR, CLAUDE_CODE_TMPDIR, node cache
    #   worktrees: [hadron-cli/iris]
  # account_profiles:                # account name -> launcher (and Codex home)
  #   holger: {bin: claude-holger}
  #   codex1: {bin: codex, codex_home: /home/orca/.config/orca/codex-accounts/<id>/home}
  # orca_launch:
  #   orca_bin: /absolute/path/to/orca
  #   cleanup_dead_tabs: false      # opt in only after live host validation

Moving a host from the Orca attention router: see
docs/how-to/move-from-the-attention-router-to-the-host-poller.md in the
hadron-cli repository. Preparing a config authorizes no cutover.

With delivery.enabled (and orca_bin), each poll also reads this host's
ready chat batches (the key needs attention:deliver too) and types each
into its worker's agent terminal, only into an empty composer, then
acknowledges it. A key without attention:deliver turns delivery off for the
run (a delivery-off event); operations carry on. It also reads this host's
due tick and task wakes (dueWakes) and types each as one line through the
same path, then hands its proof back (hadron-cli#1178). A wake that names
channels or a mention is typed once and, by default, held from acknowledgment.
Set delivery.ack_mixed: true only when the server carries A-prime (#2072,
ad7f1c2): its mixed acknowledgment records ticks/tasks without moving chat
marks. Mention text still arrives through its own batch. Status reports the
configured ack-mixed on/off setting; it does not detect server capability.
A held delivery is not reported to the
server yet (hadron-cli#940): it raises no server alarm, and team host
status shows it as UNREPORTED.

A single message longer than 131,071 bytes of UTF-8 cannot be typed (orca
terminal send carries text as one argument). It fails as too-long-to-type,
is never truncated or acknowledged, and stays due. The server delivers the
oldest message first, so it BLOCKS all later chat delivery to that worker
until hadron-server#2121 lands. Delivery for real workers stays off until
then.

--once runs a single poll and waits for what it collected. Events print one
per line; --json prints each as a JSON object.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, key, srv, err := loadHostPoller(f, configPath)
			if err != nil {
				return err
			}
			// Only implemented declarations avoid the unsupported warning.
			for _, capability := range cfg.Capabilities {
				if capability == hostpoller.CapabilityResetContext {
					continue
				}
				if _, err := fmt.Fprintf(f.IOStreams.ErrOut, "warning: declared capability %q has no handler in this poller; collected operations of this type will be reported FAILED. Remove it from capabilities when using this runner.\n", capability); err != nil {
					return err
				}
			}
			state, err := hostpoller.LoadState(cfg.StateFile)
			if err != nil {
				return exitcode.Newf(exitcode.Error, "reading host state %s: %v", cfg.StateFile, err)
			}
			if err := cfg.CheckWorktreeAliases(state); err != nil {
				return exitcode.Newf(exitcode.Usage, "%s: %v", configPath, err)
			}
			emit := hostEventPrinter(f)
			p := hostpoller.New(cfg, srv, hostpoller.ExecHost{OnDrain: emit}, state, key, emit)
			if err := wireResets(p, cfg, srv); err != nil {
				return err
			}
			if cfg.Delivery.Enabled {
				wireDelivery(p, cfg, srv, emit)
			}
			if err := wireOrcaLaunch(p, cfg); err != nil {
				return err
			}
			if err := wireRelaunch(p, cfg, srv); err != nil {
				return err
			}
			// A refused or revoked key, or a registration the server calls
			// malformed, does not heal by retrying: exit with its code.
			p.Fatal = func(err error) bool {
				switch exitcode.FromError(err) {
				case exitcode.AuthRequired, exitcode.Forbidden, exitcode.Usage:
					return true
				}
				return false
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			if once {
				err := p.Step(ctx)
				p.Wait()
				if err != nil {
					return hostStepError(err)
				}
				if n := p.ReportFailures(); n > 0 {
					return exitcode.Newf(exitcode.Unavailable, "%d report(s) did not reach the server; those operations are re-delivered at the next collect", n)
				}
				return nil
			}
			if err := p.Run(ctx); err != nil {
				return hostStepError(err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "", "host configuration file (YAML)")
	cmd.Flags().BoolVar(&once, "once", false, "poll once, wait for what it collected, and exit")
	return cmd
}

// hostStepError keeps a mapped exit code from a failed check-in: the
// register/collect errors wrap api.MapError's coded error.
func hostStepError(err error) error {
	return exitcode.New(exitcode.FromError(err), err)
}

// hostEventDTO is one --json event line. Core event keys are always present;
// fields tagged omitempty are optional when their value is zero or empty.
type hostEventDTO struct {
	ForeignUIDSkipped int    `json:"foreignUidSkipped,omitempty"`
	LocalCause        string `json:"localCause,omitempty"`
	NoticeCause       string `json:"noticeCause,omitempty"`
	FailureReason     string `json:"failureReason,omitempty"`
	WorkerID          string `json:"workerId,omitempty"`
	SessionID         string `json:"sessionId,omitempty"`
	TerminalHandle    string `json:"terminalHandle,omitempty"`
	Reason            string `json:"reason,omitempty"`
	Attempt           int    `json:"attempt,omitempty"`
	CheckedAt         string `json:"checkedAt,omitempty"`
	NoticeFailed      bool   `json:"noticeFailed,omitempty"`
	Time              string `json:"time"`
	Kind              string `json:"kind"`
	Operation         string `json:"operation"`
	Type              string `json:"type"`
	Outcome           string `json:"outcome"`
	Detail            string `json:"detail"`
}

// hostEventPrinter writes events one per line. Operations run concurrently,
// so the writes are serialized.
func hostEventPrinter(f *cmdutil.Factory) func(hostpoller.Event) {
	var mu sync.Mutex
	return func(e hostpoller.Event) {
		mu.Lock()
		defer mu.Unlock()
		if f.JSON {
			checkedAt := ""
			if !e.CheckedAt.IsZero() {
				checkedAt = e.CheckedAt.Format(time.RFC3339Nano)
			}
			b, _ := json.Marshal(hostEventDTO{
				ForeignUIDSkipped: e.ForeignUIDSkipped, LocalCause: e.LocalCause, NoticeCause: e.NoticeCause, FailureReason: e.FailureReason, WorkerID: e.WorkerID, SessionID: e.SessionID, TerminalHandle: e.TerminalHandle, Reason: e.Reason, Attempt: e.Attempt, CheckedAt: checkedAt, NoticeFailed: e.NoticeFailed,
				Time: e.Time.Format(time.RFC3339), Kind: e.Kind, Operation: e.Operation,
				Type: e.Type, Outcome: e.Outcome, Detail: e.Detail,
			})
			fmt.Fprintln(f.IOStreams.Out, string(b))
			return
		}
		parts := []string{e.Time.Format("15:04:05"), e.Kind}
		for _, s := range []string{e.Type, e.Operation, e.WorkerID, e.Outcome} {
			if s != "" {
				parts = append(parts, s)
			}
		}
		line := strings.Join(parts, " ")
		if e.Detail != "" {
			line += ": " + e.Detail
		}
		if e.ForeignUIDSkipped > 0 {
			line += fmt.Sprintf(" (foreignUidSkipped=%d)", e.ForeignUIDSkipped)
		}
		fmt.Fprintln(f.IOStreams.Out, line)
	}
}

type hostStatusWorkerDTO struct {
	Worker    string   `json:"worker"`
	Family    string   `json:"family"`
	Launch    []string `json:"launch"`
	End       []string `json:"end"`
	Worktrees []string `json:"worktrees"`
}

type hostStatusDTO struct {
	App                 string                `json:"app"`
	Label               string                `json:"label"`
	PollIntervalSeconds int                   `json:"pollIntervalSeconds"`
	StateFile           string                `json:"stateFile"`
	Workers             []hostStatusWorkerDTO `json:"workers"`
	Kept                map[string]string     `json:"kept"`
	// Registration is this host's registration as the server holds it; null
	// when the read was refused or this host has not registered yet.
	Registration *hostRegistrationDTO `json:"registration"`
	ReadError    *string              `json:"readError"`
	// Delivery is the poller's local delivery snapshot (cli#906): what this
	// host's poller last saw, read from local state only. It is not
	// authoritative delivery health (S5 is), and it carries codes only.
	Delivery hostDeliveryDTO `json:"delivery"`
}

type hostDeliveryDTO struct {
	Enabled  bool `json:"enabled"`
	AckMixed bool `json:"ackMixed"`
	// Mode is disabled, enabled, or off-by-scope (the host key lacked
	// attention:deliver at the poller's last step).
	Mode       string  `json:"mode"`
	OrcaBin    string  `json:"orcaBin"`
	LastStepAt *string `json:"lastStepAt"`
	LastRead   *string `json:"lastRead"`
	// Stale: delivery is on and the snapshot is missing, unreadable, or
	// older than three poll intervals. Error then names which, as a code.
	Stale bool    `json:"stale"`
	Error *string `json:"error"`
	// WakeRead is the newest tick/task wake read's code (read, read-failed,
	// budget-exhausted), null before one. BudgetExhaustedAt is set only
	// while that read says the App's daily wake budget is spent. Dormant
	// and Withheld are dueWakes' counts.
	WakeRead          *string                 `json:"wakeRead"`
	BudgetExhaustedAt *string                 `json:"budgetExhaustedAt"`
	Dormant           int                     `json:"dormant"`
	Withheld          int                     `json:"withheld"`
	Workers           []hostDeliveryWorkerDTO `json:"workers"`
}

type hostDeliveryWorkerDTO struct {
	Worker     string  `json:"worker"`
	WorkerID   string  `json:"workerId"`
	LastKind   string  `json:"lastKind"`
	LastReason string  `json:"lastReason"`
	LastAt     *string `json:"lastAt"`
	// ConfirmedUnacked counts this host's confirmed-delivery records whose
	// ack has not landed: they reconcile without typing.
	ConfirmedUnacked int    `json:"confirmedUnacked"`
	ResolvedBy       string `json:"resolvedBy"`
	// LastFor says whether the last event was a batch's or a wake's.
	LastFor string `json:"lastFor"`
	// TicksUnacked counts tick and task reasons typed and confirmed whose
	// ack has not landed: never retyped, acked when the proof allows.
	TicksUnacked int `json:"ticksUnacked"`
	// Unreported: the last event was a hold the server was not told about
	// (cli#940): no server hold, alarm or escalation exists for it.
	Unreported bool `json:"unreported"`
	// LastAgeSeconds is the last event's age at this read (cli#1178).
	LastAgeSeconds int64 `json:"lastAgeSeconds"`
	// HeldSince is when the worker's current run of holds began, null when
	// none is outstanding; only a delivery or reconciliation ends one.
	// HeldOver30m flags a run older than heldTooLong.
	HeldSince   *string `json:"heldSince"`
	HeldOver30m bool    `json:"heldOver30m"`
	// NotRevisited: the worker is in a run of holds but its newest event is
	// older than three poll intervals (cli#1322). A held worker is normally
	// re-evaluated every cycle, so no newer local event means only that the
	// hold shown may be stale. The record cannot say why: the server may list
	// no wake for the worker, or the poller may have stopped or its reads
	// failed (see the STALE and wakes lines).
	NotRevisited bool `json:"notRevisited"`
}

// heldTooLong is when `team host status` flags a worker's run of holds
// (cli#1178): the #2746 class held workers for two days with no readable
// reason.
const heldTooLong = 30 * time.Minute

// hostDeliveryStatus builds the delivery block from local state only.
func hostDeliveryStatus(cfg *hostpoller.Config, now time.Time) hostDeliveryDTO {
	d := hostDeliveryDTO{Enabled: cfg.Delivery.Enabled, AckMixed: cfg.Delivery.AckMixed, Mode: "disabled", OrcaBin: cfg.Delivery.OrcaBin, Workers: []hostDeliveryWorkerDTO{}}
	if !d.Enabled {
		return d
	}
	d.Mode = "enabled"
	stateDir := cfg.DeliveryStateDir()
	st, err := delivery.ReadStatus(stateDir)
	if err != nil {
		code := "status-unreadable"
		if errors.Is(err, fs.ErrNotExist) {
			code = "status-missing"
		}
		d.Stale, d.Error = true, &code
		return d
	}
	at, read := st.LastStepAt.UTC().Format(time.RFC3339), st.LastRead
	d.LastStepAt, d.LastRead = &at, &read
	if read == delivery.StepOffByScope {
		d.Mode = "off-by-scope"
	}
	interval := time.Duration(cfg.PollIntervalSeconds) * time.Second
	d.Stale = now.Sub(st.LastStepAt) > 3*interval
	if st.WakeRead != "" {
		wr := st.WakeRead
		d.WakeRead = &wr
	}
	if st.BudgetExhaustedAt != nil {
		b := st.BudgetExhaustedAt.UTC().Format(time.RFC3339)
		d.BudgetExhaustedAt = &b
	}
	d.Dormant, d.Withheld = st.Dormant, st.Withheld
	recs := &delivery.Records{Dir: filepath.Join(stateDir, "delivery", "records")}
	wakes := &delivery.WakeRecords{Dir: filepath.Join(stateDir, "delivery", "wakes")}
	for _, id := range sortedKeys(st.Workers) {
		w := st.Workers[id]
		last := w.At.UTC().Format(time.RFC3339)
		row := hostDeliveryWorkerDTO{Worker: w.Name, WorkerID: id, LastKind: string(w.Kind), LastReason: w.Reason, LastAt: &last, ResolvedBy: w.ResolvedBy, Unreported: w.Unreported,
			LastAgeSeconds: max(int64(now.Sub(w.At)/time.Second), 0)}
		if w.HeldSince != nil {
			since := w.HeldSince.UTC().Format(time.RFC3339)
			row.HeldSince, row.HeldOver30m = &since, now.Sub(*w.HeldSince) > heldTooLong
			row.NotRevisited = now.Sub(w.At) > 3*interval
		}
		if list, err := recs.List(id); err == nil {
			row.ConfirmedUnacked = len(list)
		}
		row.LastFor = "batch"
		if w.Wake {
			row.LastFor = "wake"
		}
		if list, err := wakes.List(id); err == nil {
			for _, r := range list {
				if r.State == delivery.WakeTyped {
					row.TicksUnacked++
				}
			}
		}
		d.Workers = append(d.Workers, row)
	}
	return d
}

func writeHostDelivery(w io.Writer, d hostDeliveryDTO) {
	ack := "off"
	if d.AckMixed {
		ack = "on"
	}
	if !d.Enabled {
		fmt.Fprintln(w, "delivery: disabled, ack-mixed "+ack)
		return
	}
	line := "delivery: " + d.Mode + ", ack-mixed " + ack + ", orca " + d.OrcaBin
	if d.LastStepAt != nil {
		line += ", last step " + *d.LastStepAt + " (" + *d.LastRead + ")"
	}
	if d.Stale {
		line += ", STALE"
	}
	if d.Error != nil {
		line += " (" + *d.Error + ")"
	}
	fmt.Fprintln(w, line)
	if d.WakeRead != nil {
		wl := fmt.Sprintf("wakes: %s, dormant %d, withheld %d", *d.WakeRead, d.Dormant, d.Withheld)
		if d.BudgetExhaustedAt != nil {
			wl += ", budget exhausted at " + *d.BudgetExhaustedAt
		}
		fmt.Fprintln(w, wl)
	}
	for _, wk := range d.Workers {
		unreported := ""
		if wk.Unreported {
			unreported = ", UNREPORTED: no server hold, alarm or escalation"
		}
		held := ""
		switch {
		case wk.HeldOver30m:
			held = ", HELD OVER 30m: no delivery since " + *wk.HeldSince
		case wk.HeldSince != nil:
			held = ", held since " + *wk.HeldSince
		}
		if wk.NotRevisited {
			held += ", NOT RE-EVALUATED recently: no newer local delivery event, so the hold shown may be stale (its cause is not known from this record)"
		}
		fmt.Fprintf(w, "  %s: %s %s (%s)%s at %s (%s ago)%s, confirmed-unacked %d, ticks typed-unacked %d, resolved by %s\n",
			wk.Worker, wk.LastKind, wk.LastReason, wk.LastFor, unreported, *wk.LastAt, time.Duration(wk.LastAgeSeconds)*time.Second, held, wk.ConfirmedUnacked, wk.TicksUnacked, orDash(wk.ResolvedBy))
	}
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func newCmdTeamHostStatus(f *cmdutil.Factory) *cobra.Command {
	var configPath string
	cmd := &cobra.Command{
		Use:   "status --config <host.yaml>",
		Short: "Check a host configuration and show its registration",
		Long: `Check a host poller's configuration and key, and show what it would
register (each worker's family, launch and end argv, and worktrees, created
ones included), the worktrees it keeps because a worker.end on them failed,
and the host's registration as the server holds it, found by the host id
saved at the poller's last registration (a label is not unique). It changes
nothing.

Displayed host/app labels, worker rows, registration metadata and read errors
replace controls with spaces and cap each value at 256 runes. Use --json for
original values. This does not describe kept-worktree diagnostics.

With delivery enabled it also shows the poller's local delivery snapshot:
each worker's newest event (outcome, reason code, time and age), when its
current run of holds began, and HELD OVER 30m once that run is older than
30 minutes without a delivery. A held worker is re-evaluated every cycle; a
held record whose newest event is older than three poll intervals is marked
NOT RE-EVALUATED (notRevisited in --json): no newer local event, so the
hold shown may be stale; the cause is not recorded (the server may list no
wake for that worker, or the poller stopped or its reads failed). Codes only: no text, draft or
proof.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, srv, err := loadHostPoller(f, configPath)
			if err != nil {
				return err
			}
			state, err := hostpoller.LoadState(cfg.StateFile)
			if err != nil {
				return exitcode.Newf(exitcode.Error, "reading host state %s: %v", cfg.StateFile, err)
			}
			created, kept := state.Snapshot()
			dto := hostStatusDTO{
				App: cfg.App, Label: cfg.Label, PollIntervalSeconds: cfg.PollIntervalSeconds,
				StateFile: cfg.StateFile, Workers: []hostStatusWorkerDTO{}, Kept: kept,
			}
			for _, ref := range cfg.WorkerRefs() {
				w := cfg.Workers[ref]
				trees := append(append([]string{}, w.Worktrees...), created[ref]...)
				sort.Strings(trees)
				dto.Workers = append(dto.Workers, hostStatusWorkerDTO{
					Worker: ref, Family: w.Family, Launch: append([]string{}, w.Launch...),
					End: append([]string{}, w.End...), Worktrees: dedupe(trees),
				})
			}
			resp, err := gen.HostRegistrations(cmd.Context(), srv.client, cfg.App)
			if err != nil {
				msg := api.MapError(err).Error()
				dto.ReadError = &msg
			} else {
				hostID := state.RegisteredHostID()
				for _, r := range resp.HostRegistrations {
					// The host id saved at this poller's last registration —
					// never the label, which is not unique.
					if hostID == "" || r == nil || r.HostId != hostID {
						continue
					}
					reg := hostRegistrationDTO{
						HostID: r.HostId, Label: r.Label, PollIntervalSeconds: r.PollIntervalSeconds,
						RegisteredAt: r.RegisteredAt, RefreshedAt: r.RefreshedAt, Live: r.Live, Workers: []hostWorkerDTO{},
					}
					for _, w := range r.Workers {
						if w == nil {
							continue
						}
						hw := hostWorkerDTO{WorkerID: w.WorkerId, Family: w.Family, Worktrees: append([]string{}, w.Worktrees...)}
						if w.Worker != nil {
							hw.WorkerName = &w.Worker.Name
						}
						reg.Workers = append(reg.Workers, hw)
					}
					dto.Registration = &reg
				}
			}
			dto.Delivery = hostDeliveryStatus(cfg, time.Now())
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				fmt.Fprintf(w, "host %s for %s, checking in every %ds\n", output.SafeCell(dto.Label), output.SafeCell(dto.App), dto.PollIntervalSeconds)
				t := output.NewTable(w, "WORKER", "FAMILY", "LAUNCH", "WORKTREES")
				for _, wk := range dto.Workers {
					t.Row(output.SafeCell(wk.Worker), output.SafeCell(wk.Family), output.SafeCell(strings.Join(wk.Launch, " ")), output.SafeCell(strings.Join(wk.Worktrees, ",")))
				}
				if err := t.Flush(); err != nil {
					return err
				}
				for _, tok := range sortedKeys(dto.Kept) {
					fmt.Fprintf(w, "kept: %s (%s)\n", tok, dto.Kept[tok])
				}
				switch {
				case dto.ReadError != nil:
					fmt.Fprintf(w, "registration: not readable (%s)\n", output.SafeCell(*dto.ReadError))
				case dto.Registration == nil:
					fmt.Fprintln(w, "registration: none found for this poller's host id (it has not registered yet, or the registration is gone)")
				default:
					live := "not live"
					if dto.Registration.Live {
						live = "live"
					}
					fmt.Fprintf(w, "registration: host %s, %s, refreshed %s\n", output.SafeCell(dto.Registration.HostID), live, output.SafeCell(dto.Registration.RefreshedAt))
				}
				writeHostDelivery(w, dto.Delivery)
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "", "host configuration file (YAML)")
	return cmd
}

func dedupe(s []string) []string {
	out := []string{}
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// truncateUTF16 cuts s to at most max UTF-16 units, marking the cut.
func truncateUTF16(s string, max int) string {
	if len(utf16.Encode([]rune(s))) <= max {
		return s
	}
	out, n := []rune{}, 0
	for _, r := range s {
		w := len(utf16.Encode([]rune{r}))
		if n+w > max-1 {
			break
		}
		out = append(out, r)
		n += w
	}
	return string(out) + "…"
}
