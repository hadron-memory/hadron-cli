package team

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/config"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
	"github.com/hadron-memory/hadron-cli/internal/hostup"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/hadron-memory/hadron-cli/internal/teamrouter"
	"github.com/spf13/cobra"
)

// `team host up` (cli#1389, epic hadron-server#4229): the command over the
// hostup reconcile engine. This file holds the three seams' real halves:
//
//	the desired-state source   a JSON file for now (--desired-state-file);
//	                           the server's hostDesiredState replaces it once
//	                           hadron-server#4222 and #4224 land
//	the observer               the App's host registrations, the worktrees
//	                           root on disk, and Orca's terminal list
//	the actuator               worker onboard's typed-queue steps (cor:agt:070:05)
//
// Every read here uses the command's plain context: none carries the worker
// session (api.WithSession), so a sweep from a bound worktree never
// heartbeats or moves read state for the worker bound there.

type hostUpOptions struct {
	host, worker, desiredFile, root, orcaBin string
	apply, dryRun                            bool
	timeout                                  time.Duration
}

// hostUpDTO is the --json shape. Every list is always present.
type hostUpDTO struct {
	AppRef string `json:"appRef"`
	HostID string `json:"hostId"`
	DryRun bool   `json:"dryRun"`
	// SweepMode is the App's sweep mode as the desired state reported it;
	// null when the desired state could not be read.
	SweepMode *string         `json:"sweepMode"`
	Planned   []hostUpItemDTO `json:"planned"`
	Applied   []hostUpItemDTO `json:"applied"`
	Skipped   []hostUpItemDTO `json:"skipped"`
	Failed    []hostUpItemDTO `json:"failed"`
	// Error is why the run ended non-zero, else null.
	Error *string `json:"error"`
}

type hostUpItemDTO struct {
	// WorkerID is null only for a --worker the host is not meant to run.
	WorkerID *string `json:"workerId"`
	Worker   string  `json:"worker"`
	// Action is the step; for a skipped worker, the step not taken, or null.
	Action *string           `json:"action"`
	Reason string            `json:"reason"`
	Args   map[string]string `json:"args"`
	// OperationID is the host operation an applied or failed step reached.
	OperationID *string `json:"operationId"`
	Detail      *string `json:"detail"`
}

func newCmdTeamHostUp(f *cmdutil.Factory) *cobra.Command {
	var o hostUpOptions
	cmd := &cobra.Command{
		Use:   "up --host <id> --worktrees-root <path> --desired-state-file <file> [--dry-run | --apply] [--worker <ref>]",
		Short: "Bring a host's workers to the state they should be in",
		Long: `Reconcile one host: for every worker the host should run, check its host
registration, its worktree, its agent terminal and its worker session, and
plan only what is missing. The default is a dry run: it prints the plan and
issues nothing. --apply carries the plan out through the typed host-operation
queue (cor:agt:070:05): worktree.create when the worktree is missing, then
worker.start. There is no direct Orca create or launch call.

WHERE THE DESIRED STATE COMES FROM:
  --desired-state-file <file> is required for now. The server will hold each
  host's desired state (hadron-server#4222, #4224); until that ships, this
  flag reads the same shape from a JSON file, and it goes away when the
  server source lands. The file's hostId must equal --host. Shape:
    {"hostId":"orca1","sweepMode":"APPLY","workers":[{"workerId":"…",
     "name":"Iris","desired":"RUNNING","reason":"LONG_LIVED","wouldBe":null,
     "sessionId":null,"setup":{"worktree":"hadron-cli/iris",
     "repo":"hadron-memory/hadron-cli","baseBranch":"main",
     "branch":"iris/work","family":"claude"}}]}

WHAT IT NEVER DOES:
  It never casts, binds, rebinds or ends a worker, and never removes anything.
  A worker whose reason is LEAVE_ALONE, HALTED, PAUSED, TEAM_PAUSED, RESETTING,
  RETIRED or SWEEP_OFF is reported and not touched. Nothing is planned unless
  the desired state's sweepMode is APPLY. A worker with an open session is
  never started again: if its agent terminal is gone it is reported
  (RELAUNCH_UNAVAILABLE) until the host-local relaunch ships (hadron-cli#2060).
  A worker it could not observe is reported as failed and not acted on.

Each worker appears once per list, with a reason code:
  planned  WORKTREE_MISSING, NO_SESSION
  applied  the planned reason, with the host operation's id
  skipped  IN_SYNC, SWEEP_NOT_APPLY, NOT_ON_HOST, NOT_REGISTERED, STOP_PENDING,
           RELAUNCH_UNAVAILABLE, SESSION_WITHOUT_WORKTREE,
           TERMINAL_WITHOUT_SESSION, DUPLICATE_AGENTS, NO_WORKTREE_SOURCE,
           SETUP_INVALID, UNKNOWN_DESIRED, UNKNOWN_REASON, ACTION_UNAVAILABLE,
           or the platform's own reason
  failed   OBSERVE_FAILED, ACTION_FAILED, REFUSED, NOT_ATTEMPTED

--apply takes a per-host lock: a second applying run on the same host exits 5
at once instead of waiting. An operation already in flight for a worker is
adopted, not issued twice. Every operation is issued as you, never as a
worker bound to this worktree, and no request carries the worker session:
starting a worker needs whoever manages the App. A refusal of your
credential, on the registration read or on an operation, stops the run:
nothing further is issued (exit 3 or 8).

Exit codes: 0 nothing failed; 1 a step failed; 2 usage, or Orca is not
reachable here; 4 --worker names a worker this host should not run; 5 another
run holds the lock.`,
		Example: `  hadron team host up --app acme.com:eng-team --host orca1 --worktrees-root /home/orca/orca/workspaces --desired-state-file desired.json
  hadron team host up --app acme.com:eng-team --host orca1 --worktrees-root /home/orca/orca/workspaces --desired-state-file desired.json --worker Iris --apply`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runHostUp(cmd, f, o) },
	}
	cmd.Flags().StringVar(&o.host, "host", "", "registered host id (required)")
	cmd.Flags().StringVar(&o.root, "worktrees-root", "", "absolute root the host's worktrees live under (required)")
	cmd.Flags().StringVar(&o.desiredFile, "desired-state-file", "", "JSON file holding the host's desired state (required until the server holds it)")
	cmd.Flags().StringVar(&o.worker, "worker", "", "reconcile only this worker: its id, or a name only one worker has")
	cmd.Flags().BoolVar(&o.dryRun, "dry-run", false, "print the plan and issue nothing (the default)")
	cmd.Flags().BoolVar(&o.apply, "apply", false, "carry the plan out")
	cmd.Flags().StringVar(&o.orcaBin, "orca-bin", "orca", "the Orca CLI to read terminals with")
	cmd.Flags().DurationVar(&o.timeout, "timeout", 2*time.Minute, "bound for each queue operation (1s to 30m)")
	cmd.MarkFlagsMutuallyExclusive("dry-run", "apply")
	return cmd
}

func runHostUp(cmd *cobra.Command, f *cmdutil.Factory, o hostUpOptions) error {
	for _, v := range []struct{ name, value string }{{"host", o.host}, {"worktrees-root", o.root}, {"desired-state-file", o.desiredFile}} {
		if strings.TrimSpace(v.value) == "" {
			return exitcode.Newf(exitcode.Usage, "--%s is required", v.name)
		}
	}
	if !filepath.IsAbs(o.root) || o.timeout < time.Second || o.timeout > 30*time.Minute {
		return exitcode.Newf(exitcode.Usage, "--worktrees-root must be absolute; --timeout must be 1s to 30m")
	}
	ctx := cmd.Context()
	source, err := loadHostUpFile(o.desiredFile)
	if err != nil {
		return err
	}
	b, err := readBindingOrNilWithApp(ctx, f)
	if err != nil {
		return err
	}
	scope, err := resolveTeamAppScope(ctx, f, b)
	if err != nil {
		return err
	}
	runner := teamrouter.ExecOrca(o.orcaBin)
	if err := onboardOrcaAvailable(ctx, runner); err != nil {
		return err
	}
	client, err := f.GraphQLClient()
	if err != nil {
		return err
	}
	if o.apply {
		dir, err := config.Dir()
		if err != nil {
			return exitcode.New(exitcode.Error, err)
		}
		unlock, err := hostup.Lock(dir, o.host)
		if errors.Is(err, hostup.ErrLocked) {
			return exitcode.Newf(exitcode.Conflict, "another host up run holds the lock for host %s; nothing issued", o.host)
		}
		if err != nil {
			return exitcode.Newf(exitcode.Error, "the host up lock: %v", err)
		}
		defer unlock()
	}
	engine := hostup.Engine{
		Source:   source,
		Observer: &hostUpObserver{client: client, app: scope.Ref, host: o.host, root: o.root, runner: runner},
		Actuator: &hostUpQueue{client: client, app: scope.Ref, host: o.host, timeout: o.timeout},
	}
	res, runErr := engine.Run(ctx, hostup.Options{HostID: o.host, Apply: o.apply, Only: o.worker})

	dto := hostUpDTO{AppRef: scope.Ref, HostID: o.host, DryRun: !o.apply,
		Planned: hostUpItems(res.Planned), Applied: hostUpItems(res.Applied), Skipped: hostUpItems(res.Skipped), Failed: hostUpItems(res.Failed)}
	if res.SweepMode != "" {
		dto.SweepMode = &res.SweepMode
	}
	switch {
	case runErr != nil:
		// The engine's own refusals (another host's document, an ambiguous
		// --worker) are plain errors and usage; a server refusal arrives
		// already coded and keeps its code.
		var coded *exitcode.CodedError
		if !errors.As(runErr, &coded) {
			runErr = exitcode.New(exitcode.Usage, runErr)
		}
	case len(res.Failed) > 0:
		runErr = exitcode.Newf(exitcode.Error, "%d step(s) failed on host %s; see the failed list", len(res.Failed), o.host)
	case o.worker != "" && len(res.Skipped) == 1 && res.Skipped[0].Reason == hostup.ReasonNotOnHost:
		runErr = exitcode.Newf(exitcode.NotFound, "host %s is not meant to run a worker %q; nothing created", o.host, o.worker)
	}
	if runErr != nil {
		message := runErr.Error()
		dto.Error = &message
	}
	if err := writeHostUp(f, dto, lazyAppLabel(ctx, f, scope)); err != nil {
		return err
	}
	return runErr
}

func hostUpItems(items []hostup.Item) []hostUpItemDTO {
	out := []hostUpItemDTO{}
	for _, it := range items {
		d := hostUpItemDTO{WorkerID: nz(it.WorkerID), Worker: it.Worker, Action: nz(it.Action), Reason: it.Reason,
			Args: map[string]string{}, OperationID: nz(it.Ref), Detail: nz(it.Detail)}
		for k, v := range it.Args {
			d.Args[k] = v
		}
		out = append(out, d)
	}
	return out
}

func writeHostUp(f *cmdutil.Factory, dto hostUpDTO, appLabel func() string) error {
	return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		mode := "dry-run (nothing issued or changed)"
		if !dto.DryRun {
			mode = "apply"
		}
		if _, err := fmt.Fprintf(w, "app: %s\nhost: %s   mode: %s   sweep mode: %s\n", appLabel(), output.SafeCell(dto.HostID), mode, output.SafeCell(output.Dash(dto.SweepMode))); err != nil {
			return err
		}
		if len(dto.Planned)+len(dto.Applied)+len(dto.Skipped)+len(dto.Failed) == 0 {
			_, err := fmt.Fprintln(w, "no workers for this host")
			return err
		}
		t := output.NewTable(w, "STATE", "WORKER", "ACTION", "REASON", "OPERATION", "DETAIL")
		for _, group := range []struct {
			state string
			items []hostUpItemDTO
		}{{"planned", dto.Planned}, {"applied", dto.Applied}, {"failed", dto.Failed}, {"skipped", dto.Skipped}} {
			for _, it := range group.items {
				t.Row(group.state, output.SafeCell(it.Worker), output.SafeCell(output.Dash(it.Action)), output.SafeCell(it.Reason), output.SafeCell(output.Dash(it.OperationID)), output.SafeCell(output.Dash(it.Detail)))
			}
		}
		if err := t.Flush(); err != nil {
			return err
		}
		if len(dto.Planned) == 0 {
			_, err := fmt.Fprintln(w, "nothing to do")
			return err
		}
		return nil
	})
}

// hostUpFile is the interim desired-state source: the shape the server's
// hostDesiredState is drafted with (hadron-server#4224), read from a file.
type hostUpFile struct {
	HostID    string             `json:"hostId"`
	SweepMode string             `json:"sweepMode"`
	Workers   []hostUpFileWorker `json:"workers"`
}

type hostUpFileWorker struct {
	WorkerID  string  `json:"workerId"`
	Name      string  `json:"name"`
	Desired   string  `json:"desired"`
	Reason    string  `json:"reason"`
	WouldBe   *string `json:"wouldBe"`
	SessionID *string `json:"sessionId"`
	Setup     struct {
		Worktree   string `json:"worktree"`
		Repo       string `json:"repo"`
		BaseBranch string `json:"baseBranch"`
		Branch     string `json:"branch"`
		Family     string `json:"family"`
	} `json:"setup"`
}

type hostUpFileSource struct{ snap hostup.Snapshot }

func (s hostUpFileSource) DesiredState(_ context.Context, _ string) (hostup.Snapshot, error) {
	return s.snap, nil
}

// loadHostUpFile reads the file strictly: an unknown key, a worker without an
// id or a name, a duplicate worker, or a setup token that is not a host name
// token refuses the whole file. Nothing in it reaches a command line or a path
// unchecked.
func loadHostUpFile(path string) (hostup.Source, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, exitcode.Newf(exitcode.Usage, "--desired-state-file: %v", err)
	}
	var file hostUpFile
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&file); err != nil {
		return nil, exitcode.Newf(exitcode.Usage, "--desired-state-file is not a desired-state document: %v", err)
	}
	// dec.More reports only whether an array or object continues, so a stray
	// "]" or "}" after the document passed it (cli#2064 blind P2). The end of
	// input is the only thing allowed after the document.
	var extra json.RawMessage
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, exitcode.Newf(exitcode.Usage, "--desired-state-file holds something after its JSON document")
	}
	snap := hostup.Snapshot{HostID: file.HostID, SweepMode: file.SweepMode}
	seen := map[string]bool{}
	for i, w := range file.Workers {
		if strings.TrimSpace(w.WorkerID) == "" || strings.TrimSpace(w.Name) == "" {
			return nil, exitcode.Newf(exitcode.Usage, "--desired-state-file: workers[%d] needs a workerId and a name", i)
		}
		if seen[w.WorkerID] {
			return nil, exitcode.Newf(exitcode.Usage, "--desired-state-file: worker %s is listed twice", w.WorkerID)
		}
		seen[w.WorkerID] = true
		for _, tok := range []struct{ name, value string }{{"worktree", w.Setup.Worktree}, {"repo", w.Setup.Repo}, {"baseBranch", w.Setup.BaseBranch}, {"branch", w.Setup.Branch}, {"family", w.Setup.Family}} {
			if tok.value != "" && !hostpoller.ValidNameToken(tok.value) {
				return nil, exitcode.Newf(exitcode.Usage, "--desired-state-file: workers[%d].setup.%s must be a host name token", i, tok.name)
			}
		}
		hw := hostup.Worker{WorkerID: w.WorkerID, Name: w.Name, Desired: w.Desired, Reason: w.Reason,
			Setup: hostup.Setup{Worktree: w.Setup.Worktree, Repo: w.Setup.Repo, BaseBranch: w.Setup.BaseBranch, Branch: w.Setup.Branch, Family: w.Setup.Family}}
		if w.WouldBe != nil {
			hw.WouldBe = *w.WouldBe
		}
		if w.SessionID != nil {
			hw.SessionID = *w.SessionID
		}
		snap.Workers = append(snap.Workers, hw)
	}
	return hostUpFileSource{snap: snap}, nil
}

// hostUpObserver reads what the host shows for a worker: the App's live
// registration for this host (read once per run), the worktree directory, and
// Orca's agent terminals in it.
type hostUpObserver struct {
	client     graphql.Client
	app, host  string
	root       string
	runner     teamrouter.OrcaRunner
	registered map[string]bool
}

func (o *hostUpObserver) Observe(ctx context.Context, w hostup.Worker) (hostup.Observation, error) {
	var obs hostup.Observation
	if o.registered == nil {
		r, err := gen.HostRegistrations(ctx, o.client, o.app)
		if err != nil {
			// The host's own registration is the same read for every worker:
			// a refusal ends the run with its code, read once (§10 note).
			return obs, &hostup.AbortError{Err: api.MapError(err)}
		}
		registered := map[string]bool{}
		for _, h := range r.HostRegistrations {
			if h == nil || h.HostId != o.host || !h.Live {
				continue
			}
			for _, hw := range h.Workers {
				if hw != nil {
					registered[hw.WorkerId] = true
				}
			}
		}
		o.registered = registered
	}
	obs.Registered = o.registered[w.WorkerID]
	if w.Setup.Worktree == "" {
		return obs, nil
	}
	dir := filepath.Join(o.root, filepath.FromSlash(w.Setup.Worktree))
	st, err := os.Stat(dir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return obs, nil
	case err != nil:
		return obs, fmt.Errorf("reading the worktree path: %w", err)
	case !st.IsDir():
		return obs, fmt.Errorf("%s is not a directory", dir)
	}
	obs.WorktreeExists = true
	res := o.runner(ctx, []string{"terminal", "list", "--worktree", "path:" + dir, "--json"})
	var env struct {
		OK     bool `json:"ok"`
		Result struct {
			Terminals []teamrouter.Terminal `json:"terminals"`
		} `json:"result"`
	}
	if res.Code != 0 || json.Unmarshal([]byte(res.Stdout), &env) != nil || !env.OK {
		return obs, fmt.Errorf("orca did not list the terminals of %s", dir)
	}
	for _, term := range env.Result.Terminals {
		// Connected or not: a terminal Orca lost the connection to may still
		// hold a running agent, and a start beside it could make two
		// (cli#2064 blind P3). Uncertain counts as present.
		if filepath.Clean(term.WorktreePath) == filepath.Clean(dir) && term.AgentIdentity != nil && *term.AgentIdentity != "" {
			obs.AgentTerminals++
		}
	}
	return obs, nil
}

// hostUpQueue carries a planned step out with worker onboard's queue steps:
// adopt the identical operation still in flight, else issue it, then wait for
// its verified outcome. It supports only the two typed operations; the
// host-local relaunch (cli#2060) is not one of them.
type hostUpQueue struct {
	client    graphql.Client
	app, host string
	timeout   time.Duration
}

func (q *hostUpQueue) Supports(action string) bool {
	return action == hostup.ActionWorktreeCreate || action == hostup.ActionWorkerStart
}

func (q *hostUpQueue) Apply(ctx context.Context, w hostup.Worker, s hostup.Step) (string, error) {
	// Always as the person running the command, never as the worker bound
	// to this worktree (cli#2064 §10 P2): starting a worker is the App
	// manager's, which no worker actor is, so a worker-attributed run could
	// create a worktree and then be refused the start. As the person, no
	// request this command makes carries X-Hadron-Session.
	ctx = withAsPerson(ctx)
	queue := &onboardQueue{client: q.client, app: q.app, host: q.host, worker: w.WorkerID, activeOnly: true}
	err := queue.ensure(ctx, onboardStep{Type: s.Action, Args: s.Args}, q.timeout)
	ref := ""
	if queue.last != nil {
		ref = queue.last.Id
	}
	if code := exitcode.FromError(err); code == exitcode.AuthRequired || code == exitcode.Forbidden {
		return ref, &hostup.AbortError{Err: err}
	}
	return ref, err
}
