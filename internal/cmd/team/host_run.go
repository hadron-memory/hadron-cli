package team

// `team host run|status` — the host poller (hadron-cli#831, cor:agt:070):
// the host's own side of host operations. It authenticates with an App key
// carrying host:operate for ONE host (cor:agt:070:04), read from the host
// configuration's key_file (or HADRON_HOST_KEY) — never from HADRON_TOKEN or
// the token store, which the agents it launches use as their holder's own
// credential. The host key never reaches an agent: worker.start hands it the
// single-use launch grant and nothing else.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// hostServer is the poller's Server over GraphQL, with the host key.
type hostServer struct {
	client graphql.Client
	app    string
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

func (s *hostServer) Collect(ctx context.Context) ([]hostpoller.Operation, error) {
	resp, err := gen.CollectHostOperations(ctx, s.client, s.app, nil)
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
		// The server bounds a reason at 2000 characters; a git error can run
		// longer, and a refused report would leave the operation in flight.
		if r := []rune(reason); len(r) > 2000 {
			reason = string(r[:1999]) + "…"
		}
		in.FailureReason = &reason
	}
	resp, err := gen.ReportHostOperation(ctx, s.client, in)
	if err != nil {
		if api.HasErrorCode(err, "HOST_OPERATION_NOT_DONE") {
			return hostpoller.Operation{}, hostpoller.ErrNotDone
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
  worktree.remove  refused for uncommitted or untracked changes, no
                   upstream, unpushed commits, a running agent, or a tree
                   whose worker.end failed

An unknown type is refused here as well as by the server, and reported
failed. There is no shell passthrough of any kind.

THE HOST KEY never reaches a launched agent. It is read from key_file (or
HADRON_HOST_KEY), never from HADRON_TOKEN or the token store; the agent
gets the parent environment minus HADRON_TOKEN, HADRON_HOST_KEY and any
variable carrying the key, plus the grant. The agent binds on its holder's
own stored token.

A worker.start waiting for its bind is re-delivered by each collect and
skipped as in flight. The server must not re-mint its grant meanwhile (a
re-collect omits a start whose grant is still live): run this against a
server with that fix.

The configuration (YAML):

  app: hrn:app:<root>:<slug>
  label: orca1                      # default: hostname
  poll_interval_seconds: 30
  key_file: /etc/hadron/host.key    # an App key with host:operate
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
			emit := hostEventPrinter(f)
			p := hostpoller.New(cfg, srv, hostpoller.ExecHost{}, state, key, emit)
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			if once {
				err := p.Step(ctx)
				p.Wait()
				if err != nil {
					return hostStepError(err)
				}
				return nil
			}
			return p.Run(ctx)
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

func hostEventPrinter(f *cmdutil.Factory) func(hostpoller.Event) {
	return func(e hostpoller.Event) {
		if f.JSON {
			b, _ := json.Marshal(e)
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
}

func newCmdTeamHostStatus(f *cmdutil.Factory) *cobra.Command {
	var configPath string
	cmd := &cobra.Command{
		Use:   "status --config <host.yaml>",
		Short: "Check a host configuration and show its registration",
		Long: `Check a host poller's configuration and key, and show what it would
register (each worker's family, launch and end argv, and worktrees, created
ones included), the worktrees it keeps because a worker.end on them failed,
and the host's registration as the server holds it — read with the host key,
which reaches only its own host (cor:agt:070:04). It changes nothing.`,
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
				matches := 0
				for _, r := range resp.HostRegistrations {
					if r != nil && r.Label == cfg.Label {
						matches++
					}
				}
				if matches > 1 {
					// A label names a host to people and is not unique; showing
					// one of several would be a guess.
					msg := fmt.Sprintf("%d registrations carry the label %q; `team host list` shows them all", matches, cfg.Label)
					dto.ReadError = &msg
				}
				for _, r := range resp.HostRegistrations {
					if matches == 1 && r != nil && r.Label == cfg.Label {
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
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				fmt.Fprintf(w, "host %s for %s, checking in every %ds\n", dto.Label, dto.App, dto.PollIntervalSeconds)
				t := output.NewTable(w, "WORKER", "FAMILY", "LAUNCH", "WORKTREES")
				for _, wk := range dto.Workers {
					t.Row(wk.Worker, wk.Family, strings.Join(wk.Launch, " "), strings.Join(wk.Worktrees, ","))
				}
				if err := t.Flush(); err != nil {
					return err
				}
				for _, tok := range sortedKeys(dto.Kept) {
					fmt.Fprintf(w, "kept: %s (%s)\n", tok, dto.Kept[tok])
				}
				switch {
				case dto.ReadError != nil:
					fmt.Fprintf(w, "registration: not readable (%s)\n", *dto.ReadError)
				case dto.Registration == nil:
					fmt.Fprintln(w, "registration: none on the server yet")
				default:
					live := "not live"
					if dto.Registration.Live {
						live = "live"
					}
					fmt.Fprintf(w, "registration: host %s, %s, refreshed %s\n", dto.Registration.HostID, live, dto.Registration.RefreshedAt)
				}
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

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
