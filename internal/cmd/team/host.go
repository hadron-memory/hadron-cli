package team

// `team host` — a team App's hosts (#871 over server #1876, #1709 T6 slice 1):
// the registrations its hosts keep (cor:agt:070:01), the operations issued to
// them (cor:agt:070:02), and issuing one (cor:agt:070:05). These are the
// reader and manager surfaces; a host's own side (register, collect, report)
// is the poller's: `team host run|status` in host_run.go (#831). No argument, flag or output here carries a
// credential: an operation's arguments are ids and name tokens only
// (cor:agt:070:03), and a plan login never travels in one (Holger, #10777).

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"runtime"
	"strconv"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

const (
	hostOpsDefaultLimit = 20
	// hostOpsPageCap is the server's page ceiling; a larger --limit would be
	// clamped silently, so the CLI refuses it instead (like `team events`).
	hostOpsPageCap = 200
)

type hostWorkerDTO struct {
	WorkerID   string   `json:"workerId"`
	WorkerName *string  `json:"workerName"`
	Family     string   `json:"family"`
	Worktrees  []string `json:"worktrees"`
}

type hostRegistrationDTO struct {
	HostID              string          `json:"hostId"`
	Label               string          `json:"label"`
	PollIntervalSeconds int             `json:"pollIntervalSeconds"`
	RegisteredAt        string          `json:"registeredAt"`
	RefreshedAt         string          `json:"refreshedAt"`
	Live                bool            `json:"live"`
	Workers             []hostWorkerDTO `json:"workers"`
}

type hostOperationDTO struct {
	ID               string          `json:"id"`
	AppID            string          `json:"appId"`
	HostID           string          `json:"hostId"`
	Type             string          `json:"type"`
	Args             json.RawMessage `json:"args"`
	WorkerID         string          `json:"workerId"`
	WorkerName       *string         `json:"workerName"`
	Status           string          `json:"status"`
	IssuedBy         string          `json:"issuedBy"`
	IssuedByUserID   *string         `json:"issuedByUserId"`
	IssuedByWorkerID *string         `json:"issuedByWorkerId"`
	// IssuedReason is why the platform issued an operation no task asked for
	// (DESIRED_RUNNING, #4231). Absent, not null, when the server names none, so
	// an operation without one keeps its shape.
	IssuedReason  *string         `json:"issuedReason,omitempty"`
	IssuedAt      string          `json:"issuedAt"`
	CollectedAt   *string         `json:"collectedAt"`
	FinishedAt    *string         `json:"finishedAt"`
	Result        json.RawMessage `json:"result"`
	FailureReason *string         `json:"failureReason"`
}

const hostOpsNoReasonWarning = "issuedReason is not shown: this server predates hadron-server#4257 and has no such field"

// readHostOperations reads one page of the App's host operations with the
// server's reason for each (#4231), and, when the server is older than #4257 and
// rejects that one field, reads it again without. Any other refusal is the
// answer. The poller's own operations never select the field.
func readHostOperations(ctx context.Context, client graphql.Client, appRef string, hostID *string, status *gen.HostOperationStatus, worker *string, first *int, after *string) (hostOperationPageDTO, error) {
	page := hostOperationPageDTO{Items: []hostOperationDTO{}}
	resp, err := gen.HostOperationsWithReason(ctx, client, appRef, hostID, status, worker, first, after)
	if err == nil {
		page.HasNextPage, page.EndCursor = resp.HostOperations.HasNextPage, resp.HostOperations.EndCursor
		for _, o := range resp.HostOperations.Items {
			if o != nil {
				dto := hostOperationDTOFrom(&o.HostOperationFields)
				dto.IssuedReason = o.IssuedReason
				page.Items = append(page.Items, dto)
			}
		}
		return page, nil
	}
	if !api.IsFrameworkValidationOnlyFor(err, "issuedReason") {
		return page, err
	}
	plain, err := gen.HostOperations(ctx, client, appRef, hostID, status, worker, first, after)
	if err != nil {
		return page, err
	}
	page.Warnings = []string{hostOpsNoReasonWarning}
	page.HasNextPage, page.EndCursor = plain.HostOperations.HasNextPage, plain.HostOperations.EndCursor
	for _, o := range plain.HostOperations.Items {
		if o != nil {
			page.Items = append(page.Items, hostOperationDTOFrom(&o.HostOperationFields))
		}
	}
	return page, nil
}

// hostOpByCell is who issued the operation, and, when the server says why no
// task did (#4231), the reason beside it: PLATFORM (DESIRED_RUNNING). Without
// a reason it is the issuer alone, as before.
func hostOpByCell(o hostOperationDTO) string {
	if o.IssuedReason == nil || *o.IssuedReason == "" {
		return o.IssuedBy
	}
	return o.IssuedBy + " (" + *o.IssuedReason + ")"
}

type hostOperationPageDTO struct {
	Items       []hostOperationDTO `json:"items"`
	HasNextPage bool               `json:"hasNextPage"`
	EndCursor   *string            `json:"endCursor"`
	// Warnings is present only when the list was read without something the
	// server could not give: a server older than #4257 has no issuedReason.
	Warnings []string `json:"warnings,omitempty"`
}

// jsonOrNull keeps a JSON field present as `null` rather than dropping it or
// rendering an empty RawMessage, which does not marshal.
func jsonOrNull(m *json.RawMessage) json.RawMessage {
	if m == nil || len(*m) == 0 {
		return json.RawMessage("null")
	}
	return *m
}

func hostOperationDTOFrom(o *gen.HostOperationFields) hostOperationDTO {
	dto := hostOperationDTO{
		ID:               o.Id,
		AppID:            o.AppId,
		HostID:           o.HostId,
		Type:             o.Type,
		Args:             jsonOrNull(&o.Args),
		WorkerID:         o.WorkerId,
		Status:           string(o.Status),
		IssuedBy:         string(o.IssuedBy),
		IssuedByUserID:   o.IssuedByUserId,
		IssuedByWorkerID: o.IssuedByWorkerId,
		IssuedAt:         o.IssuedAt,
		CollectedAt:      o.CollectedAt,
		FinishedAt:       o.FinishedAt,
		Result:           jsonOrNull(o.Result),
		FailureReason:    o.FailureReason,
	}
	if o.Worker != nil {
		dto.WorkerName = &o.Worker.Name
	}
	return dto
}

func newCmdTeamHost(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "host <command>",
		Short: "A team App's hosts and the operations issued to them",
		Long: `The machines that launch a team App's workers. A host is one machine
running this CLI as a poller: it registers which workers it can run, as
which model family and in which worktree, collects the operations queued
for it, carries them out and reports the outcome.

By goal: see what hosts exist and who they run (` + "`list`" + `, ` + "`setup`" + `); check
one host's configuration and registration (` + "`status`" + `); read or issue queued
operations (` + "`ops`" + `, ` + "`issue`" + `); record who runs what
(` + "`assign`" + `, ` + "`unassign`" + `, ` + "`setup`" + `, ` + "`import`" + `); reconcile workers to that
record (` + "`up`" + `, ` + "`sweep`" + `, and the recorded outcomes in ` + "`sweep-runs`" + `); keep one
host running (` + "`run`" + `, ` + "`report-stops`" + `, ` + "`availability`" + `). The three
reconcile forms differ in who drives: ` + "`run`" + ` is the long-lived poller loop
that keeps one host's workers up; ` + "`up`" + ` performs one reconcile pass now,
from a desired state you name; ` + "`sweep`" + ` is the same pass keyed to the
host's registered id and host.yaml — the form its installed timer runs.

Prerequisites: sign in (` + "`hadron auth login`" + `), a team App context (pass ` + "`--app`" + `
or run from a bound worktree), and for the host-local commands (` + "`run`" + `,
` + "`sweep`" + `, ` + "`availability`" + `, ` + "`report-stops`" + `) a host.yaml — its shape is
documented by ` + "`hadron team host run --help`" + `. ` + "`up`" + ` needs none: its --config is
only an optional reporting key. A host's server key is granted
with ` + "`hadron app key scope grant <key-id> host:operate --host-id <host>`" + `.`,
	}
	cmd.AddCommand(newCmdTeamHostLs(f))
	cmd.AddCommand(newCmdTeamHostOps(f))
	cmd.AddCommand(newCmdTeamHostIssue(f))
	cmd.AddCommand(newCmdTeamHostRun(f))
	cmd.AddCommand(newCmdTeamHostAvailability(f))
	cmd.AddCommand(newCmdTeamHostStatus(f))
	cmd.AddCommand(newCmdTeamHostReportStops(f))
	cmd.AddCommand(newCmdTeamHostUp(f))
	cmd.AddCommand(newCmdTeamHostSweepRuns(f))
	cmd.AddCommand(newCmdTeamHostSweep(f))
	cmd.AddCommand(newCmdTeamHostAssign(f))
	cmd.AddCommand(newCmdTeamHostUnassign(f))
	cmd.AddCommand(newCmdTeamHostSetup(f))
	cmd.AddCommand(newCmdTeamHostImport(f))
	return cmd
}

func newCmdTeamHostLs(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List the App's host registrations",
		Long: `List the App's host registrations: each host's id and label, how often it
checks in, whether it is live now (refreshed within the App's host-lapse
count of its check-ins), and the workers it can launch with
their family and worktrees. Whoever reads the App's tasks and events may.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			b, err := readBindingOrNilWithApp(ctx, f)
			if err != nil {
				return err
			}
			scope, err := resolveTeamAppScope(ctx, f, b)
			if err != nil {
				return err
			}
			appLabel := lazyAppLabel(ctx, f, scope)
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.HostRegistrations(ctx, client, scope.Ref)
			if err != nil {
				return api.MapError(err)
			}
			hosts := []hostRegistrationDTO{}
			for _, r := range resp.HostRegistrations {
				if r == nil {
					continue
				}
				dto := hostRegistrationDTO{
					HostID:              r.HostId,
					Label:               r.Label,
					PollIntervalSeconds: r.PollIntervalSeconds,
					RegisteredAt:        r.RegisteredAt,
					RefreshedAt:         r.RefreshedAt,
					Live:                r.Live,
					Workers:             []hostWorkerDTO{},
				}
				for _, w := range r.Workers {
					if w == nil {
						continue
					}
					hw := hostWorkerDTO{WorkerID: w.WorkerId, Family: w.Family, Worktrees: []string{}}
					if w.Worker != nil {
						hw.WorkerName = &w.Worker.Name
					}
					hw.Worktrees = append(hw.Worktrees, w.Worktrees...)
					dto.Workers = append(dto.Workers, hw)
				}
				hosts = append(hosts, dto)
			}
			return output.Write(f.IOStreams, f.JSON, hosts, func(w io.Writer) error {
				if _, err := fmt.Fprintf(w, "app: %s\n", appLabel()); err != nil {
					return err
				}
				if len(hosts) == 0 {
					_, err := fmt.Fprintln(w, "no hosts registered")
					return err
				}
				t := output.NewTable(w, "HOST", "LABEL", "LIVE", "CHECK-IN", "REFRESHED", "WORKERS")
				for _, h := range hosts {
					live := "no"
					if h.Live {
						live = "yes"
					}
					t.Row(output.SafeCell(h.HostID), output.SafeCell(h.Label), output.SafeCell(live), output.SafeCell(strconv.Itoa(h.PollIntervalSeconds)+"s"), output.SafeCell(h.RefreshedAt), output.SafeCell(hostWorkersCell(h.Workers)))
				}
				return t.Flush()
			})
		},
	}
}

// hostWorkersCell renders a registration's workers as name/family[worktrees].
func hostWorkersCell(ws []hostWorkerDTO) string {
	if len(ws) == 0 {
		return "—"
	}
	parts := make([]string, 0, len(ws))
	for _, w := range ws {
		name := w.WorkerID
		if w.WorkerName != nil && *w.WorkerName != "" {
			name = *w.WorkerName
		}
		cell := name + "/" + w.Family
		if len(w.Worktrees) > 0 {
			cell += "[" + strings.Join(w.Worktrees, ",") + "]"
		}
		parts = append(parts, cell)
	}
	return strings.Join(parts, " ")
}

var hostOperationStatuses = []string{"ISSUED", "COLLECTED", "SUCCEEDED", "FAILED"}

func newCmdTeamHostOps(f *cmdutil.Factory) *cobra.Command {
	var hostID, status, worker, after string
	var limit int
	cmd := &cobra.Command{
		Use:   "ops",
		Short: "List the operations issued to the App's hosts",
		Long: `List the App's host operations, NEWEST first: each one's
type, host, worker, status, who issued it and, once reported, its result or
failure reason. When the platform issued it for no task, the BY cell adds the
server's reason, as PLATFORM (DESIRED_RUNNING): a long-lived worker that should
run and has no session; --json carries it as issuedReason, absent when the
server names none. A server older than #4257 has no such field: the list then
reads without it and says so once (a stderr note, or a warnings entry in
--json). Whoever reads the App's tasks and events may.

--host, --status (issued, collected, succeeded, failed) and --worker (id,
URN or name) narrow it. --limit takes 1 to ` + strconv.Itoa(hostOpsPageCap) + `, the server's page cap.
When more follow, the receipt names the --after that continues the list;
--json carries it as endCursor, with hasNextPage. If display sanitizes or
shortens the cursor, the text omits the reusable --after suggestion: obtain
the original cursor from --json.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			// A filter flag given empty (an unset shell variable, usually) is
			// refused, never read as "no filter": that would list everything.
			for _, fl := range []struct{ name, val string }{{"host", hostID}, {"status", status}, {"worker", worker}, {"after", after}} {
				if cmd.Flags().Changed(fl.name) && strings.TrimSpace(fl.val) == "" {
					return exitcode.Newf(exitcode.Usage, "--%s was given empty (an unset variable?); leave it out to list all", fl.name)
				}
			}
			if limit < 1 || limit > hostOpsPageCap {
				return exitcode.Newf(exitcode.Usage, "--limit takes 1 to %d (the server's page cap) — %d would be clamped silently", hostOpsPageCap, limit)
			}
			var statusArg *gen.HostOperationStatus
			if status != "" {
				s := strings.ToUpper(strings.TrimSpace(status))
				if !containsString(hostOperationStatuses, s) {
					return exitcode.Newf(exitcode.Usage, "--status takes issued, collected, succeeded or failed, got %q", status)
				}
				st := gen.HostOperationStatus(s)
				statusArg = &st
			}
			b, err := readBindingOrNilWithApp(ctx, f)
			if err != nil {
				return err
			}
			scope, err := resolveTeamAppScope(ctx, f, b)
			if err != nil {
				return err
			}
			appLabel := lazyAppLabel(ctx, f, scope)
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			page, err := readHostOperations(ctx, client, scope.Ref, optStr(strings.TrimSpace(hostID)), statusArg, optStr(strings.TrimSpace(worker)), &limit, optStr(strings.TrimSpace(after)))
			if err != nil {
				return api.MapError(err)
			}
			if !f.JSON {
				for _, n := range page.Warnings {
					fmt.Fprintln(f.IOStreams.ErrOut, "note: "+n)
				}
			}
			return output.Write(f.IOStreams, f.JSON, page, func(w io.Writer) error {
				if _, err := fmt.Fprintf(w, "app: %s\n", appLabel()); err != nil {
					return err
				}
				t := output.NewTable(w, "ID", "ISSUED", "TYPE", "HOST", "WORKER", "STATUS", "BY", "OUTCOME")
				for _, o := range page.Items {
					t.Row(output.SafeCell(o.ID), output.SafeCell(o.IssuedAt), output.SafeCell(o.Type), output.SafeCell(o.HostID), output.SafeCell(hostOpWorkerCell(o)), output.SafeCell(o.Status), output.SafeCell(hostOpByCell(o)), output.SafeCell(hostOpOutcomeCell(o)))
				}
				if err := t.Flush(); err != nil {
					return err
				}
				if page.HasNextPage && page.EndCursor != nil {
					err := writeHostCursorHint(w, *page.EndCursor, runtime.GOOS)
					return err
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&hostID, "host", "", "one host's operations, by its host id")
	cmd.Flags().StringVar(&status, "status", "", "issued, collected, succeeded or failed")
	cmd.Flags().StringVar(&worker, "worker", "", "one worker's operations: its id, URN or name")
	cmd.Flags().StringVar(&after, "after", "", "the endCursor of the previous page")
	cmd.Flags().IntVar(&limit, "limit", hostOpsDefaultLimit, "page size (server cap 200)")
	return cmd
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func hostOpWorkerCell(o hostOperationDTO) string {
	if o.WorkerName != nil && *o.WorkerName != "" {
		return *o.WorkerName
	}
	return o.WorkerID
}

// hostOpOutcomeCell says what a reported operation came to: a failure's
// reason, a success's result; nothing while it is still out.
func hostOpOutcomeCell(o hostOperationDTO) string {
	if o.FailureReason != nil && *o.FailureReason != "" {
		return *o.FailureReason
	}
	if len(o.Result) > 0 && string(o.Result) != "null" {
		return string(o.Result)
	}
	return "—"
}

// parseHostOpArgs reads repeatable --arg key=value pairs into the operation's
// argument object. Every value is a string (the server's arguments are ids
// and name tokens); a key given twice is refused rather than silently last-wins.
func parseHostOpArgs(pairs []string) (*json.RawMessage, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	m := map[string]string{}
	for _, p := range pairs {
		k, v, ok := strings.Cut(p, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			return nil, exitcode.Newf(exitcode.Usage, "--arg takes key=value, got %q", p)
		}
		if _, dup := m[k]; dup {
			return nil, exitcode.Newf(exitcode.Usage, "--arg %s is given twice", k)
		}
		m[k] = v
	}
	// encoding/json sorts map keys, so the wire bytes are deterministic.
	enc, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	raw := json.RawMessage(enc)
	return &raw, nil
}

func newCmdTeamHostIssue(f *cmdutil.Factory) *cobra.Command {
	var hostID, worker string
	var argPairs []string
	var asPersonFlag bool
	cmd := &cobra.Command{
		Use:   "issue <type> --host <host-id> --worker <worker> [--arg key=value]... [--as-person]",
		Short: "Issue an operation to one of the App's hosts",
		Long: `Issue one queued operation to a registered host. The type is one of a
closed set; its arguments are ids and name tokens, never a command and
never a credential:

  worktree.create  --arg repo=… --arg baseBranch=… --arg branchName=… --arg label=…
  worktree.remove  --arg worktree=…   (a worktree the host registers for the worker)
  worker.start     no --arg (the host launches the worker's recorded launch line)
  worker.end       no --arg, for a worker with an open session

The worker is named with --worker (id, URN or name), never inside --arg.
Issuing worker.start by hand is the one-off form; the usual way to start
workers is a reconcile — ` + "`team host up --apply`" + ` starts every worker the
host's record says it runs, but plans nothing unless the App's sweepMode is
APPLY (see ` + "`team host up --help`" + `). worker.deliver is not issued until a host has a
delivery path for it (hadron-cli#829), refusing HOST_OPERATION_NOT_ISSUABLE
(exit 2). Any other type is HOST_OPERATION_TYPE_UNKNOWN (exit 2), and a
missing, extra or malformed argument BAD_HOST_OPERATION_ARGS (exit 2). An
unregistered host is HOST_NOT_FOUND (exit 4).

On success the command prints the issued operation (id, type, status,
worker); follow it to completion with ` + "`hadron team host ops --worker <worker>`" + `.

WHO ISSUES IT: worktree.create and worktree.remove are issued by the worker's
holder, the worker it reports to in the App's roster, or whoever manages the
App; worker.start and worker.end are issued by whoever manages the App only —
the holder and the reports-to worker are refused (else
HOST_OPERATION_NOT_PERMITTED, exit 8). In a worktree bound to a worker, the
operation is that worker's: it qualifies only as the target's reports-to.
Pass --as-person to issue it as yourself instead — as the target's holder or
the App's manager — with no worker session. With --instruction the write
goes out through a verified manager-held worker session with the holder's
claim, as the shared instruction help below describes.`,
		Example: `  hadron team host issue worktree.create --host orca1 --worker Jonas \
    --arg repo=hadron-memory/hadron-cli --arg baseBranch=main --arg branchName=jonas/871 --arg label=jonas-3
  hadron team host issue worktree.remove --host orca1 --worker Jonas --arg worktree=hadron-memory/hadron-cli/jonas-3 --as-person`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// cli#2774: parse the claim and snapshot the binding before the
			// first lookup, so an instructed write can verify what it read.
			claim, instructed, err := managerInstruction(cmd, f)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			opType := strings.TrimSpace(args[0])
			if opType == "" {
				return exitcode.Newf(exitcode.Usage, "name the operation type, e.g. worktree.create")
			}
			if strings.TrimSpace(hostID) == "" {
				return exitcode.Newf(exitcode.Usage, "--host is required: the registered host's id (see `hadron team host list`)")
			}
			if strings.TrimSpace(worker) == "" {
				return exitcode.Newf(exitcode.Usage, "--worker is required: the worker the operation is for (id, URN or name)")
			}
			opArgs, err := parseHostOpArgs(argPairs)
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
			appLabel := lazyAppLabel(ctx, f, scope)
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			if asPersonFlag {
				ctx = withAsPerson(ctx)
			}
			if claim != nil {
				// cli#2491 F1: an explicit instruction owns attribution, so the
				// authored-write middleware must not attach a binding the claim
				// did not verify; the instructed context attaches the verified
				// session (or none, with the claim still sent, as the person).
				ctx = withAsPerson(ctx)
				var e error
				ctx, _, e = instructed(ctx, "", scope.Ref)
				if e != nil {
					return e
				}
			}
			resp, err := sendIssueHostOperation(ctx, client, &gen.IssueHostOperationInput{
				AppRef: scope.Ref,
				HostId: strings.TrimSpace(hostID),
				Worker: strings.TrimSpace(worker),
				Type:   opType,
				Args:   opArgs,
			}, claim)
			if err != nil {
				return api.MapError(err)
			}
			if resp == nil || resp.IssueHostOperation == nil {
				return exitcode.Newf(exitcode.Error, "server returned no host operation")
			}
			op := hostOperationDTOFrom(&resp.IssueHostOperation.HostOperationFields)
			return output.Write(f.IOStreams, f.JSON, op, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ issued %s %s to host %s for %s (%s, by %s) in %s\n",
					op.Type, op.ID, op.HostID, hostOpWorkerCell(op), op.Status, op.IssuedBy, appLabel())
				return err
			})
		},
	}
	cmd.Flags().StringVar(&hostID, "host", "", "the registered host to run it (required)")
	cmd.Flags().StringVar(&worker, "worker", "", "the worker it is for: id, URN or name (required)")
	cmd.Flags().StringArrayVar(&argPairs, "arg", nil, "an argument as key=value, repeatable")
	cmd.Flags().BoolVar(&asPersonFlag, "as-person", false, "issue as yourself, not as the bound worktree's worker")
	addManagerInstruction(cmd)
	// --as-person promises no worker session on this write; --instruction
	// claims the verified session's attribution. Together they contradict
	// each other, and before this the claim silently won (#2774 review):
	// refuse the pair instead of picking a winner.
	cmd.MarkFlagsMutuallyExclusive("as-person", "instruction")
	return cmd
}
