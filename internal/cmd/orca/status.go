package orca

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
	"github.com/hadron-memory/hadron-cli/internal/output"
	ts "github.com/hadron-memory/hadron-cli/internal/terminalstatus"
	"github.com/spf13/cobra"
)

// bindingFileName is the worker-session binding `hadron team session start`
// writes into a worktree's git directory.
const bindingFileName = "hadron-team-session.json"

// Bound is the worker a worktree's session binding names.
type Bound struct {
	WorkerID   string
	WorkerName string
	AppID      string
}

// Env is everything the command touches outside itself: tests inject fakes.
type Env struct {
	Orca    orcaterm.Runner
	Now     func() time.Time
	Reads   func(ctx context.Context, app string) (Reads, error)
	BoundAt func(ctx context.Context, worktree string) (Bound, bool)
}

func defaultEnv(f *cmdutil.Factory) Env {
	return Env{
		Orca: orcaterm.Exec("orca", 15*time.Second),
		Now:  time.Now,
		Reads: func(ctx context.Context, app string) (Reads, error) {
			client, err := f.GraphQLClient()
			if err != nil {
				return nil, err
			}
			return graphReads{client: client, app: app}, nil
		},
		BoundAt: boundAt,
	}
}

// boundAt reads the binding in worktree's git directory.
func boundAt(ctx context.Context, worktree string) (Bound, bool) {
	if !filepath.IsAbs(worktree) {
		return Bound{}, false
	}
	raw, err := exec.CommandContext(ctx, "git", "-C", worktree, "rev-parse", "--absolute-git-dir").Output()
	if err != nil {
		return Bound{}, false
	}
	data, err := os.ReadFile(filepath.Join(strings.TrimSpace(string(raw)), bindingFileName)) // #nosec G304 — the worktree's own git dir
	if err != nil {
		return Bound{}, false
	}
	var b struct {
		WorkerID   string `json:"workerId"`
		WorkerName string `json:"workerName"`
		AppID      string `json:"appId"`
	}
	if json.Unmarshal(data, &b) != nil || b.WorkerID == "" {
		return Bound{}, false
	}
	return Bound{WorkerID: b.WorkerID, WorkerName: b.WorkerName, AppID: b.AppID}, true
}

func newCmdStatus(f *cmdutil.Factory, env Env) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "status (<worker|terminal-id> | --all)",
		Short: "Classify a worker's Orca terminal as busy|idle|locked|dark",
		Long: `Orca integration: reads Orca terminals, so it works only on the host that runs
Orca and needs the Orca CLI on PATH. It never types into a terminal.

Classifies one worker's Orca terminal (found by the worker's session binding in
its worktree, or taken directly by terminal id) and prints the result as the
versioned terminal-status@1 contract:

  status  busy   a turn is running
          idle   an empty prompt, safe to push one line
          locked an identifiable block (reason menu_or_question, ...) or, fail
                 closed, a screen no rule recognizes (reason unrecognized)
          dark   broken: a ticker owed this worker a turn and none came
                 (no_turn_for_due_ticker), or no terminal / a bare shell / no
                 agent (no_terminal, bare_shell, no_agent)
  prompt  empty | not_empty | menu | unknown   (the terminal-level fact)

Precedence: locked, then busy, then dark, then idle. A worker with nothing due
is never dark. Push only when prompt is empty and status is idle or dark.

--all prints ONE JSON array, one entry per active worker of the App.

Exit 0 whenever a status was produced; the status is in the output. Exit 10
means Orca is not reachable (off the Orca host), distinct from a worker with no
terminal, which is dark/no_terminal and exit 0. The detail line (the ticker and
due time behind a dark, or why a read was refused) is printed in text mode only
and is never part of the JSON.`,
		Example: `  hadron orca terminal status Joshua
  hadron orca terminal status Joshua --json
  hadron orca terminal status term_50a6 --json
  hadron orca terminal status --all --json`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if all == (len(args) == 1) {
				return exitcode.Newf(exitcode.Usage, "give exactly one of <worker|terminal-id> or --all")
			}
			return runStatus(cmd.Context(), f, env, args, all)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "every active worker of the App: one JSON array")
	return cmd
}

// target is one worker (or an unbound terminal) to report on.
type target struct {
	worker Worker // ID may be "" for a terminal not bound to a worker
	term   *orcaterm.Terminal
}

func runStatus(ctx context.Context, f *cmdutil.Factory, env Env, args []string, all bool) error {
	client := orcaterm.NewClient(env.Orca)
	terms, err := client.List(ctx)
	if err != nil {
		return exitcode.Newf(exitcode.OrcaUnreachable, "Orca is not reachable on this host: `hadron orca` reads Orca terminals and needs the Orca CLI and app here")
	}
	bound := map[string]Bound{} // handle -> binding
	for _, t := range terms {
		if b, ok := env.BoundAt(ctx, t.WorktreePath); ok {
			bound[t.Handle] = b
		}
	}
	var targets []target
	var reads Reads
	needReads := func() (Reads, error) {
		if reads != nil {
			return reads, nil
		}
		app, err := f.App()
		if err != nil {
			return nil, err
		}
		if app == "" {
			for _, b := range bound {
				if b.AppID != "" {
					app = b.AppID
					break
				}
			}
		}
		if app == "" {
			return nil, exitcode.Newf(exitcode.Usage, "no App: pass --app, or run where a worker-session binding names one")
		}
		r, err := env.Reads(ctx, app)
		if err != nil {
			return nil, api.MapError(err)
		}
		reads = r
		return r, nil
	}
	switch {
	case all:
		r, err := needReads()
		if err != nil {
			return err
		}
		ws, err := r.Workers(ctx)
		if err != nil {
			return err
		}
		for _, w := range ws {
			targets = append(targets, target{worker: w})
		}
	default:
		arg := strings.TrimSpace(args[0])
		if t := terminalByHandle(terms, arg); t != nil {
			w := Worker{}
			if b, ok := bound[t.Handle]; ok {
				w = Worker{ID: b.WorkerID, Name: b.WorkerName}
			}
			targets = append(targets, target{worker: w, term: t})
			break
		}
		r, err := needReads()
		if err != nil {
			return err
		}
		ws, err := r.Workers(ctx)
		if err != nil {
			return err
		}
		var found *Worker
		for i := range ws {
			if strings.EqualFold(ws[i].Name, arg) || ws[i].ID == arg {
				found = &ws[i]
			}
		}
		if found == nil {
			return exitcode.Newf(exitcode.NotFound, "no active worker or terminal %q", arg)
		}
		targets = append(targets, target{worker: *found})
	}

	results := make([]ts.Result, len(targets))
	var idle []int
	for i, tg := range targets {
		results[i] = observe(ctx, client, env, terms, bound, tg)
		if results[i].Status == ts.Idle {
			idle = append(idle, i)
		}
	}
	if len(idle) > 0 {
		applyDarkRule(ctx, env, needReads, targets, results, idle)
	}

	var dto any = results
	if f.JSON && !all {
		dto = results[0]
	}
	return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		for _, r := range results {
			if _, err := fmt.Fprintln(w, textLine(r)); err != nil {
				return err
			}
		}
		return nil
	})
}

func terminalByHandle(terms []orcaterm.Terminal, arg string) *orcaterm.Terminal {
	for i := range terms {
		if terms[i].Handle == arg {
			return &terms[i]
		}
	}
	return nil
}

// textLine is one line per worker: "Joshua idle", "Joshua dark (reason)".
func textLine(r ts.Result) string {
	name := r.Worker
	if name == "" {
		name = r.Terminal
	}
	line := fmt.Sprintf("%s %s", name, r.Status)
	if r.Reason != nil {
		line += " (" + string(*r.Reason) + ")"
	}
	if r.Detail != "" {
		line += ": " + r.Detail
	}
	return line
}

// observe reads one target's terminal. A worker's terminal is the one in a
// worktree whose binding names the worker.
func observe(ctx context.Context, client *orcaterm.Client, env Env, terms []orcaterm.Terminal, bound map[string]Bound, tg target) ts.Result {
	now := env.Now()
	res := ts.Result{Worker: tg.worker.Name, Prompt: ts.PromptUnknown, CheckedAt: now}
	own := []orcaterm.Terminal{}
	if tg.term != nil {
		own = append(own, *tg.term)
	} else {
		for _, t := range terms {
			if b, ok := bound[t.Handle]; ok && b.WorkerID == tg.worker.ID {
				own = append(own, t)
			}
		}
	}
	if len(own) == 0 {
		res.Status, res.Reason = ts.Dark, reasonPtr(ts.NoTerminal)
		return res
	}
	type seen struct {
		t   orcaterm.Terminal
		obs orcaterm.Observation
	}
	var all []seen
	for _, t := range own {
		all = append(all, seen{t, orcaterm.Classify(t, client.ReadScreen(ctx, t.Handle), now)})
	}
	// Agents outrank shells: a worktree may also hold a plain shell.
	var agents []seen
	for _, s := range all {
		if s.obs.Reason != ts.BareShell {
			agents = append(agents, s)
		}
	}
	pick := all[0]
	switch {
	case len(agents) > 1:
		res.Terminal = "" // ambiguous: no single terminal to name
		res.Status, res.Prompt, res.Reason = ts.Locked, ts.PromptUnknown, reasonPtr(ts.Unrecognized)
		res.Detail = "more than one agent terminal is bound to this worker"
		return res
	case len(agents) == 1:
		pick = agents[0]
	}
	res.Terminal = pick.t.Handle
	res.Status, res.Prompt, res.LastTurnAt = pick.obs.Status, pick.obs.Prompt, pick.obs.LastTurnAt
	if pick.obs.Reason != "" {
		res.Reason = reasonPtr(pick.obs.Reason)
	}
	return res
}

func reasonPtr(r ts.Reason) *ts.Reason { return &r }

// applyDarkRule turns a terminal that looks idle into dark when a ticker owed
// its worker a turn and none came. Only the due-wake evidence counts. When the
// evidence cannot be read, or the server refuses it for this caller, an idle
// worker is never reported idle: it fails closed to locked/unrecognized, with
// the cause in the local detail. A busy or locked worker keeps its status.
func applyDarkRule(ctx context.Context, env Env, needReads func() (Reads, error), targets []target, results []ts.Result, idle []int) {
	fail := func(err error) {
		for _, i := range idle {
			results[i].Status, results[i].Reason = ts.Locked, reasonPtr(ts.Unrecognized)
			results[i].Detail = "due-wake evidence unavailable: " + errText(err)
		}
	}
	var ids []string
	for _, i := range idle {
		if id := targets[i].worker.ID; id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return // terminals bound to no worker: no ticker evidence exists
	}
	reads, err := needReads()
	if err != nil {
		fail(err)
		return
	}
	subs, err := reads.Subscriptions(ctx)
	if err != nil {
		fail(err)
		return
	}
	due, err := reads.DueTicks(ctx, ids)
	if err != nil {
		fail(err)
		return
	}
	now := env.Now()
	for _, i := range idle {
		id := targets[i].worker.ID
		if id == "" {
			continue
		}
		if dark, detail := ts.IsDark(now, subs[id], due[id]); dark {
			results[i].Status, results[i].Reason, results[i].Detail = ts.Dark, reasonPtr(ts.NoTurnForDueTicker), detail
		}
	}
}

func errText(err error) string {
	var coded *exitcode.CodedError
	if errors.As(err, &coded) {
		return coded.Err.Error()
	}
	return err.Error()
}
