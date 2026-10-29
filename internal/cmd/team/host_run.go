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
	client graphql.Client
	app    string
	// now dates a batch read, for its proof's expiry; time.Now when nil.
	now func() time.Time
}

func (s *hostServer) Register(ctx context.Context, r hostpoller.Registration) (hostpoller.RegisteredHost, error) {
	in := &gen.RegisterHostInput{
		AppRef: s.app, Label: r.Label, PollIntervalSeconds: r.PollIntervalSeconds,
		Workers: []*gen.RegisterHostWorkerInput{},
	}
	for _, w := range r.Workers {
		in.Workers = append(in.Workers, &gen.RegisterHostWorkerInput{
			Worker: w.Worker, Family: w.Family, Worktrees: append([]string{}, w.Worktrees...),
		})
	}
	resp, err := gen.RegisterHost(ctx, s.client, in)
	if err != nil {
		return hostpoller.RegisteredHost{}, api.MapError(err)
	}
	if resp.RegisterHost == nil {
		return hostpoller.RegisteredHost{}, exitcode.Newf(exitcode.Error, "the server returned no registration")
	}
	out := hostpoller.RegisteredHost{HostID: resp.RegisterHost.HostId}
	for _, w := range resp.RegisterHost.Workers {
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
			return hostpoller.Operation{}, fmt.Errorf("%w: %v", hostpoller.ErrFinal, api.MapError(err))
		}
		return hostpoller.Operation{}, api.MapError(err)
	}
	if resp.ReportHostOperation == nil {
		return hostpoller.Operation{}, exitcode.Newf(exitcode.Error, "the server returned no operation")
	}
	return operationFrom(&resp.ReportHostOperation.HostOperationFields), nil
}

func operationFrom(o *gen.HostOperationFields) hostpoller.Operation {
	op := hostpoller.Operation{ID: o.Id, Type: o.Type, WorkerID: o.WorkerId, Status: string(o.Status), Args: map[string]any{}}
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
	client, err := api.NewClient(server, key, f.HTTPClient)
	if err != nil {
		return nil, "", nil, err
	}
	return cfg, key, &hostServer{client: client, app: cfg.App}, nil
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
                   agent's session binds (team session start reads the grant)
  worker.deliver   reported failed: no delivery path yet (hadron-cli#829)
  worker.end       run the worker's configured end prompt, wait end_wait
                   (default 15m), and report; never kills the agent
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

THE HOST KEY is never put in a launched agent's environment, argv or files.
It is read only from key_file, which must be mode 0600 or tighter — never
from HADRON_TOKEN, the token store, or any environment variable. The agent
gets the parent environment minus HADRON_TOKEN, HADRON_HOST_KEY,
HADRON_LAUNCH_GRANT and any variable carrying the key, plus its own grant,
and binds on its holder's own stored token. An agent running as the SAME OS
user as the poller can still read key_file itself: run the poller as its own
user, with key_file readable by that user only, to close that.

THE LAUNCH COMMAND must stay in the foreground for the agent's whole life:
its exit is read as the agent's. A launcher that starts a detached session
and returns (a tmux or wrapper script) is reported failed if no session has
bound by then, which revokes the grant.

A worker.start waiting for its bind is re-delivered by each collect and
skipped as in flight. The server must not re-mint its grant meanwhile (a
re-collect omits a start whose grant is still live): run this against a
server with that fix.

The configuration (YAML):

  app: hrn:app:<root>:<slug>
  label: orca1                      # default: hostname
  poll_interval_seconds: 30
  key_file: /etc/hadron/host.key    # an App key with host:operate, mode 0600
  worktrees_root: /srv/worktrees
  state_file: /var/lib/hadron/host-state.json   # default: beside the config
  repos:
    hadron-memory/hadron-cli: /srv/repos/hadron-cli
  workers:
    jonas:                          # name in the App, URN or id
      family: claude
      launch: [claude-worker, --model, opus]
      end: [orca-send, jonas, "please end your session with a handoff"]
      end_wait: 15m
      worktrees: [hadron-cli/jonas-2]

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
channels or a mention is typed once and acknowledged only once it no longer
does. A held delivery is not reported to the
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
			state, err := hostpoller.LoadState(cfg.StateFile)
			if err != nil {
				return exitcode.Newf(exitcode.Error, "reading host state %s: %v", cfg.StateFile, err)
			}
			if err := cfg.CheckWorktreeAliases(state); err != nil {
				return exitcode.Newf(exitcode.Usage, "%s: %v", configPath, err)
			}
			emit := hostEventPrinter(f)
			p := hostpoller.New(cfg, srv, hostpoller.ExecHost{}, state, key, emit)
			if cfg.Delivery.Enabled {
				wireDelivery(p, cfg, srv, emit)
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

// hostEventDTO is one --json event line: every key always present, "" when
// it does not apply.
type hostEventDTO struct {
	Time      string `json:"time"`
	Kind      string `json:"kind"`
	Operation string `json:"operation"`
	Type      string `json:"type"`
	Outcome   string `json:"outcome"`
	Detail    string `json:"detail"`
}

// hostEventPrinter writes events one per line. Operations run concurrently,
// so the writes are serialized.
func hostEventPrinter(f *cmdutil.Factory) func(hostpoller.Event) {
	var mu sync.Mutex
	return func(e hostpoller.Event) {
		mu.Lock()
		defer mu.Unlock()
		if f.JSON {
			b, _ := json.Marshal(hostEventDTO{
				Time: e.Time.Format(time.RFC3339), Kind: e.Kind, Operation: e.Operation,
				Type: e.Type, Outcome: e.Outcome, Detail: e.Detail,
			})
			fmt.Fprintln(f.IOStreams.Out, string(b))
			return
		}
		parts := []string{e.Time.Format("15:04:05"), e.Kind}
		for _, s := range []string{e.Type, e.Operation, e.Outcome} {
			if s != "" {
				parts = append(parts, s)
			}
		}
		line := strings.Join(parts, " ")
		if e.Detail != "" {
			line += ": " + e.Detail
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
	Enabled bool `json:"enabled"`
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
}

// heldTooLong is when `team host status` flags a worker's run of holds
// (cli#1178): the #2746 class held workers for two days with no readable
// reason.
const heldTooLong = 30 * time.Minute

// hostDeliveryStatus builds the delivery block from local state only.
func hostDeliveryStatus(cfg *hostpoller.Config, now time.Time) hostDeliveryDTO {
	d := hostDeliveryDTO{Enabled: cfg.Delivery.Enabled, Mode: "disabled", OrcaBin: cfg.Delivery.OrcaBin, Workers: []hostDeliveryWorkerDTO{}}
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
	if !d.Enabled {
		fmt.Fprintln(w, "delivery: disabled")
		return
	}
	line := "delivery: " + d.Mode + ", orca " + d.OrcaBin
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
30 minutes without a delivery. Codes only: no text, draft or proof.`,
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
