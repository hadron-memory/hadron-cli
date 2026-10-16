package team

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/hadron-memory/hadron-cli/internal/teamrouter"
	"github.com/spf13/cobra"
)

type onboardOptions struct {
	name, host, repo, base, branch, label, family, root, config, display string
	apply, asPerson, coordinator                                         bool
	timeout                                                              time.Duration
}

type onboardStep struct {
	Type string            `json:"type"`
	Args map[string]string `json:"args"`
}

type onboardDTO struct {
	AppID          string                `json:"appId"`
	AppRef         string                `json:"appRef"`
	DryRun         bool                  `json:"dryRun"`
	WorkerID       string                `json:"workerId"`
	Worktree       string                `json:"worktree"`
	Operations     []onboardStep         `json:"operations"`
	Verified       []string              `json:"verified"`
	HumanRemainder []string              `json:"humanRemainder"`
	Queue          []onboardOperationDTO `json:"queue"`
	Error          *string               `json:"error"`
}

type onboardOperationDTO struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	Status string `json:"status"`
}

func (o onboardOptions) steps() []onboardStep {
	return []onboardStep{
		{Type: hostpoller.OpWorktreeCreate, Args: map[string]string{"repo": o.repo, "baseBranch": o.base, "branchName": o.branch, "label": o.label}},
		{Type: hostpoller.OpWorkerStart, Args: map[string]string{"worktree": path.Base(o.repo) + "/" + o.label, "family": o.family}},
	}
}

func newCmdWorkerOnboard(f *cmdutil.Factory) *cobra.Command {
	var o onboardOptions
	cmd := &cobra.Command{
		Use:   "onboard --name <worker> --host <id> --repo <token> --branch <branch> --label <name> --family <family> --worktrees-root <path> --config <router.json>",
		Short: "Onboard an existing worker (Orca-hosted workers only)",
		Long: `Onboard an existing, held worker — Orca-hosted workers only.
The default is a dry-run: it submits NOTHING and changes no host files.
Pass --apply to issue worktree.create, wait for its verified outcome, then
issue worker.start through the typed host-operation queue (cor:agt:070:05).
There are no direct Orca worktree-create or terminal-create calls.

Operator prerequisites: a live host registration with this worker and family,
the host poller's repo mapping and launch argv, a matching worktrees root,
and the worker's mention-only register row on the router's Channel. The
worker must already be cast and held. This command never casts or binds it.
Starting a worker requires the App manager; --as-person uses your authority
instead of the bound worker's. Server authority checks still apply.

Only three host-local steps follow: roster entry, router reload confirmation,
and Orca display name. Run the current Go 'team router loop' on this config:
it reloads between polls and acknowledges the exact config fingerprint.
The timeout must allow its interval. The adopted attention token is untouched.
All waits are bounded. A retry reuses an identical existing queue operation;
a failed operation is reported, never silently retried. On an ambiguous
submission failure, inspect 'team host ops' before retrying.
After preflight, failures print a partial outcome (including known operation
ids and statuses) before returning nonzero. A timeout leaves queued work
running; it never cancels, rolls back, or reissues an ambiguous operation.

Orca must be reachable before any operation is queued, including on dry-run.
--repo is the registered queue token, not a local path. The printed human
remainder includes checking the worker's own session pickup and routing.`,
		Example: "  hadron team worker onboard --name Iris --host orca1 --repo hadron-memory/hadron-cli --branch iris/work --label iris --family codex --worktrees-root /home/orca/orca/workspaces --config /home/orca/router.json\n  # inspect the dry-run, then repeat with --apply --as-person",
		Args:    cobra.NoArgs,
		RunE:    func(cmd *cobra.Command, _ []string) error { return runWorkerOnboard(cmd, f, o) },
	}
	cmd.Flags().StringVar(&o.name, "name", "", "existing worker name, URN or id (required)")
	cmd.Flags().StringVar(&o.host, "host", "", "registered Orca host id (required)")
	cmd.Flags().StringVar(&o.repo, "repo", "", "host-configured repo token (required)")
	cmd.Flags().StringVar(&o.base, "base", "main", "base branch token")
	cmd.Flags().StringVar(&o.branch, "branch", "", "new branch token (required)")
	cmd.Flags().StringVar(&o.label, "label", "", "worktree label token (required)")
	cmd.Flags().StringVar(&o.family, "family", "", "host-configured model family (required)")
	cmd.Flags().StringVar(&o.root, "worktrees-root", "", "absolute root used by the host poller (required)")
	cmd.Flags().StringVar(&o.config, "config", "", "router config path (required)")
	cmd.Flags().StringVar(&o.display, "display-name", "", "Orca display name (default: worker name and cast role)")
	cmd.Flags().BoolVar(&o.apply, "apply", false, "apply the plan (default: dry-run)")
	cmd.Flags().BoolVar(&o.asPerson, "as-person", false, "issue as yourself, not the bound worker")
	cmd.Flags().BoolVar(&o.coordinator, "coordinator", false, "enable the roster coordinator watch")
	cmd.Flags().DurationVar(&o.timeout, "timeout", 2*time.Minute, "bound for each queue operation and router reload (1s to 30m)")
	return cmd
}

func runWorkerOnboard(cmd *cobra.Command, f *cmdutil.Factory, o onboardOptions) (retErr error) {
	for _, v := range []struct{ name, value string }{{"name", o.name}, {"host", o.host}, {"config", o.config}, {"family", o.family}} {
		if strings.TrimSpace(v.value) == "" {
			return exitcode.Newf(exitcode.Usage, "--%s is required", v.name)
		}
	}
	for _, v := range []struct{ name, value string }{{"repo", o.repo}, {"base", o.base}, {"branch", o.branch}, {"label", o.label}, {"family", o.family}} {
		if !hostpoller.ValidNameToken(v.value) {
			return exitcode.Newf(exitcode.Usage, "--%s must be a host name token", v.name)
		}
	}
	if !filepath.IsAbs(o.root) || o.timeout < time.Second || o.timeout > 30*time.Minute {
		return exitcode.Newf(exitcode.Usage, "--worktrees-root must be absolute; --timeout must be 1s to 30m")
	}
	canonicalConfig, err := filepath.EvalSymlinks(o.config)
	if err != nil {
		return exitcode.New(exitcode.Usage, err)
	}
	o.config, err = filepath.Abs(canonicalConfig)
	if err != nil {
		return exitcode.New(exitcode.Usage, err)
	}
	cfg, err := teamrouter.LoadConfig(o.config)
	if err != nil {
		return exitcode.New(exitcode.Usage, err)
	}
	runner := teamrouter.ExecOrca(cfg.OrcaCli)
	if err := onboardOrcaAvailable(cmd.Context(), runner); err != nil {
		return err
	}
	client, err := f.GraphQLClient()
	if err != nil {
		return err
	}
	// The required router config is the scope, even for a bare worker name.
	w, err := resolveWorker(cmd.Context(), client, cfg.App, o.name)
	if err != nil {
		return err
	}
	if w.RetiredAt != nil || w.HeldByUserId == nil {
		return exitcode.Newf(exitcode.Usage, "onboard requires an existing, held, non-retired worker; nothing queued")
	}
	// Resolve both app spellings through the server, never compare a name to an id.
	identity, err := gen.TeamAppIdentity(cmd.Context(), client, cfg.App)
	if err != nil {
		return api.MapError(err)
	}
	if identity.App == nil || identity.App.Id != w.AppId {
		return exitcode.Newf(exitcode.Usage, "router config belongs to a different App; nothing queued")
	}
	if err := onboardRegistration(cmd.Context(), client, w.AppId, o.host, w.Id, o.family); err != nil {
		return err
	}
	row, err := teamrouter.NewGraphQLServer(client, w.AppId, cfg.Channel).RegisterRow(cmd.Context(), w.Id)
	if err != nil {
		return api.MapError(err)
	}
	if row.NamedBy != "WORKER" || !row.MentionOnly {
		return exitcode.Newf(exitcode.Usage, "worker needs a mention-only Channel register row before onboarding; nothing queued")
	}
	steps := o.steps()
	dir := filepath.Join(o.root, filepath.FromSlash(steps[1].Args["worktree"]))
	entry := teamrouter.RosterEntry{Worker: w.Id, Name: w.Name, Worktree: dir, Coordinator: o.coordinator}
	if err := teamrouter.CheckRosterEntry(cfg, entry); err != nil {
		return exitcode.Newf(exitcode.Usage, "roster ownership conflict: %v; nothing queued", err)
	}
	dto := onboardDTO{DryRun: !o.apply, WorkerID: w.Id, Worktree: dir, Operations: steps, Verified: []string{"Orca reachable", "host registration", "mention-only register row"}, HumanRemainder: []string{"Worker binds its own session using the launch grant; this command never binds it.", "Verify the worker's pickup in team chat and its mention delivery after onboarding."}}
	dto.Queue = []onboardOperationDTO{}
	dto.AppID = w.AppId
	dto.AppRef = cfg.App
	if identity.App.Urn != "" {
		dto.AppRef = identity.App.Urn
	}
	rendered := false
	defer func() {
		if retErr != nil && !rendered {
			message := retErr.Error()
			dto.Error = &message
			if err := writeOnboard(f, dto, w.Name, o.host); err != nil {
				retErr = err
			}
		}
	}()
	if o.apply {
		// Serialize this workflow, including queue submissions, across local retries.
		lock := o.config + ".onboard-lock"
		if err := os.Mkdir(lock, 0o700); err != nil {
			return exitcode.Newf(exitcode.Error, "onboarding lock unavailable at %s; verify no other run is active before removing a stale lock", lock)
		}
		defer func() { _ = os.Remove(lock) }()
		ctx := cmd.Context()
		if o.asPerson {
			ctx = withAsPerson(ctx)
		}
		queue := &onboardQueue{client: client, app: w.AppId, host: o.host, worker: w.Id}
		for _, step := range steps {
			err := queue.ensure(ctx, step, o.timeout)
			if queue.last != nil {
				dto.Queue = append(dto.Queue, onboardOperationDTO{ID: queue.last.Id, Type: queue.last.Type, Status: string(queue.last.Status)})
			}
			if err != nil {
				return err
			}
			dto.Verified = append(dto.Verified, step.Type+" succeeded")
			if step.Type == hostpoller.OpWorktreeCreate {
				if err := onboardWorktree(ctx, runner, dir, o.branch, ""); err != nil {
					return err
				}
			}
			if step.Type == hostpoller.OpWorkerStart {
				current, err := gen.GetWorker(ctx, client, w.Id)
				if err != nil {
					return api.MapError(err)
				}
				if current.Worker == nil || current.Worker.Id != w.Id || current.Worker.HasLiveSession == nil || !*current.Worker.HasLiveSession {
					return exitcode.Newf(exitcode.Error, "worker.start succeeded previously, but a current live worker session is unverified; inspect team host ops before retrying")
				}
				if err := onboardTerminal(ctx, runner, dir); err != nil {
					return err
				}
			}
		}
		// Queue success alone cannot prove Orca manages the resulting worktree.
		if err := onboardWorktree(ctx, runner, dir, o.branch, ""); err != nil {
			return err
		}
		started := time.Now()
		fingerprint, err := teamrouter.PutRosterEntry(o.config, teamrouter.RosterEntry{Worker: w.Id, Name: w.Name, Worktree: dir, Coordinator: o.coordinator})
		if err != nil {
			return exitcode.New(exitcode.Error, fmt.Errorf("queue completed; roster update: %w", err))
		}
		dto.Verified = append(dto.Verified, "roster entry")
		if err := onboardWait(ctx, o.timeout, func() (bool, error) {
			r, err := teamrouter.ReadReload(cfg)
			return err == nil && r.Fingerprint == fingerprint && !r.At.Before(started), nil
		}); err != nil {
			return exitcode.Newf(exitcode.Error, "roster saved; router reload unverified: %v; run the current Go team router loop on this config, then retry", err)
		}
		dto.Verified = append(dto.Verified, "router reloaded exact roster")
		display := o.display
		if display == "" {
			display = w.Name
			if w.Role != nil && *w.Role != "" {
				display += " [" + *w.Role + "]"
			}
		}
		res := runner(ctx, []string{"worktree", "set", "--worktree", "path:" + dir, "--display-name", display, "--json"})
		if !onboardEnvelopeOK(res) {
			return exitcode.Newf(exitcode.Error, "queue and roster completed; Orca display-name update failed")
		}
		if err := onboardWorktree(ctx, runner, dir, o.branch, display); err != nil {
			return err
		}
		dto.Verified = append(dto.Verified, "Orca display name")
	}
	rendered = true
	return writeOnboard(f, dto, w.Name, o.host)
}

func writeOnboard(f *cmdutil.Factory, dto onboardDTO, name, host string) error {
	return output.Write(f.IOStreams, f.JSON, dto, func(out io.Writer) error {
		if _, err := fmt.Fprintf(out, "app: %s (from --config)\n", dto.AppRef); err != nil {
			return err
		}
		mode := "dry-run (nothing submitted or changed)"
		if !dto.DryRun {
			mode = "verified"
		}
		if dto.Error != nil {
			mode = "incomplete"
		}
		if _, err := fmt.Fprintf(out, "%s: %s on %s\n", mode, name, host); err != nil {
			return err
		}
		for _, s := range dto.Operations {
			b, _ := json.Marshal(s.Args)
			if _, err := fmt.Fprintf(out, "  queue %s %s\n", s.Type, b); err != nil {
				return err
			}
		}
		for _, op := range dto.Queue {
			if _, err := fmt.Fprintf(out, "  operation: %s %s %s\n", op.Type, op.ID, op.Status); err != nil {
				return err
			}
		}
		for _, v := range dto.Verified {
			if _, err := fmt.Fprintf(out, "  checked: %s\n", v); err != nil {
				return err
			}
		}
		for _, v := range dto.HumanRemainder {
			if _, err := fmt.Fprintf(out, "  human: %s\n", v); err != nil {
				return err
			}
		}
		return nil
	})
}

func onboardOrcaAvailable(ctx context.Context, runner teamrouter.OrcaRunner) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	r := runner(ctx, []string{"status", "--json"})
	var env struct {
		OK     bool `json:"ok"`
		Result struct {
			Runtime struct {
				Reachable bool `json:"reachable"`
			} `json:"runtime"`
		} `json:"result"`
	}
	if r.Code != 0 || json.Unmarshal([]byte(r.Stdout), &env) != nil || !env.OK || !env.Result.Runtime.Reachable {
		return exitcode.Newf(exitcode.Usage, "Orca-hosted workers only: an Orca CLI and reachable server are required; nothing was queued or changed")
	}
	return nil
}

func onboardEnvelopeOK(r teamrouter.RunResult) bool {
	var e struct {
		OK bool `json:"ok"`
	}
	return r.Code == 0 && json.Unmarshal([]byte(r.Stdout), &e) == nil && e.OK
}

func onboardWorktree(ctx context.Context, r teamrouter.OrcaRunner, dir, branch, display string) error {
	res := r(ctx, []string{"worktree", "show", "--worktree", "path:" + dir, "--json"})
	var e struct {
		OK     bool `json:"ok"`
		Result struct {
			Worktree struct {
				Path        string `json:"path"`
				DisplayName string `json:"displayName"`
				Branch      string `json:"branch"`
			} `json:"worktree"`
		} `json:"result"`
	}
	if res.Code != 0 || json.Unmarshal([]byte(res.Stdout), &e) != nil || !e.OK || filepath.Clean(e.Result.Worktree.Path) != filepath.Clean(dir) || e.Result.Worktree.Branch != "refs/heads/"+branch && e.Result.Worktree.Branch != branch || display != "" && e.Result.Worktree.DisplayName != display {
		return exitcode.Newf(exitcode.Error, "queue completed but Orca worktree/display verification failed for %s; no direct create or launch attempted", dir)
	}
	return nil
}

func onboardTerminal(ctx context.Context, r teamrouter.OrcaRunner, dir string) error {
	res := r(ctx, []string{"terminal", "list", "--worktree", "path:" + dir, "--json"})
	var env struct {
		OK     bool `json:"ok"`
		Result struct {
			Terminals []teamrouter.Terminal `json:"terminals"`
		} `json:"result"`
	}
	if res.Code == 0 && json.Unmarshal([]byte(res.Stdout), &env) == nil && env.OK {
		for _, term := range env.Result.Terminals {
			if filepath.Clean(term.WorktreePath) == filepath.Clean(dir) && term.AgentIdentity != nil && *term.AgentIdentity != "" && term.Connected && term.Writable {
				return nil
			}
		}
	}
	return exitcode.Newf(exitcode.Error, "worker.start completed but no live agent terminal is verified in Orca at %s; no direct launch attempted", dir)
}

func onboardRegistration(ctx context.Context, c graphql.Client, app, host, worker, family string) error {
	r, err := gen.HostRegistrations(ctx, c, app)
	if err != nil {
		return api.MapError(err)
	}
	for _, h := range r.HostRegistrations {
		if h == nil || h.HostId != host || !h.Live {
			continue
		}
		for _, w := range h.Workers {
			if w != nil && w.WorkerId == worker && w.Family == family {
				return nil
			}
		}
	}
	return exitcode.Newf(exitcode.Usage, "a live host registration for this worker/family is required; nothing queued")
}

type onboardQueue struct {
	client            graphql.Client
	app, host, worker string
	last              *gen.HostOperationFields
}

// Find every page: an older matching operation must not be re-issued merely
// because the default page hid it. Conflicting active work refuses explicitly.
func (q *onboardQueue) find(ctx context.Context, step onboardStep) (*gen.HostOperationFields, error) {
	var after *string
	first := 100
	seen := map[string]bool{}
	var found *gen.HostOperationFields
	for {
		r, err := gen.HostOperations(ctx, q.client, q.app, &q.host, nil, &q.worker, &first, after)
		if err != nil {
			return nil, api.MapError(err)
		}
		for _, item := range r.HostOperations.Items {
			if item == nil {
				continue
			}
			op := item.HostOperationFields
			if op.Type != step.Type || op.HostId != q.host || op.WorkerId != q.worker {
				continue
			}
			var args map[string]string
			if json.Unmarshal(op.Args, &args) != nil {
				return nil, exitcode.Newf(exitcode.Error, "cannot interpret existing host operation %s", op.Id)
			}
			delete(args, "workerId")
			if reflect.DeepEqual(args, step.Args) {
				if found == nil {
					copied := op
					found = &copied
				}
			} else if op.Status == gen.HostOperationStatusIssued || op.Status == gen.HostOperationStatusCollected {
				return nil, exitcode.Newf(exitcode.Usage, "conflicting active %s operation %s; inspect team host ops", step.Type, op.Id)
			}
		}
		if !r.HostOperations.HasNextPage {
			return found, nil
		}
		after = r.HostOperations.EndCursor
		if after == nil || *after == "" || seen[*after] {
			return nil, exitcode.Newf(exitcode.Error, "host-operation pagination did not advance")
		}
		seen[*after] = true
	}
}

func (q *onboardQueue) ensure(ctx context.Context, step onboardStep, timeout time.Duration) error {
	q.last = nil
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	op, err := q.find(ctx, step)
	if err != nil {
		return err
	}
	if op == nil {
		b, err := json.Marshal(step.Args)
		if err != nil {
			return err
		}
		raw := json.RawMessage(b)
		r, err := gen.IssueHostOperation(ctx, q.client, &gen.IssueHostOperationInput{AppRef: q.app, HostId: q.host, Worker: q.worker, Type: step.Type, Args: &raw})
		if err != nil {
			return api.MapError(err)
		}
		op = &r.IssueHostOperation.HostOperationFields
	}
	q.last = op
	if op.Id == "" || op.Type != step.Type || op.HostId != q.host || op.WorkerId != q.worker {
		return exitcode.Newf(exitcode.Error, "queue returned an unexpected operation; inspect team host ops before retrying")
	}
	id := op.Id
	return onboardWait(ctx, timeout, func() (bool, error) {
		if op.Status == gen.HostOperationStatusFailed {
			return false, exitcode.Newf(exitcode.Error, "%s %s failed; inspect team host ops before retrying", step.Type, id)
		}
		if op.Status == gen.HostOperationStatusSucceeded {
			if step.Type == hostpoller.OpWorktreeCreate {
				var result struct {
					Worktree string `json:"worktree"`
				}
				if op.Result == nil || json.Unmarshal(*op.Result, &result) != nil || result.Worktree != path.Base(step.Args["repo"])+"/"+step.Args["label"] {
					return false, exitcode.Newf(exitcode.Error, "worktree.create %s returned an unexpected worktree; worker.start was not submitted", id)
				}
			}
			return true, nil
		}
		current, err := q.find(ctx, step)
		if err != nil {
			return false, err
		}
		if current == nil || current.Id != id {
			return false, exitcode.Newf(exitcode.Error, "operation %s disappeared or was replaced; inspect team host ops", id)
		}
		op = current
		q.last = op
		// Re-enter the terminal-state checks (including create-result proof).
		return false, nil
	})
}

func onboardWait(ctx context.Context, timeout time.Duration, check func() (bool, error)) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		done, err := check()
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
