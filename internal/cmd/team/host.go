package team

// `team host` — a team App's hosts (#871 over server #1876, #1709 T6 slice 1):
// the registrations its hosts keep (cor:agt:070:01), the operations issued to
// them (cor:agt:070:02), and issuing one (cor:agt:070:05). These are the
// reader and manager surfaces; a host's own side (register, collect, report)
// is the poller's: `team host run|status` in host_run.go (#831). No argument, flag or output here carries a
// credential: an operation's arguments are ids and name tokens only
// (cor:agt:070:03), and a plan login never travels in one (Holger, #10777).

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

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
	IssuedAt         string          `json:"issuedAt"`
	CollectedAt      *string         `json:"collectedAt"`
	FinishedAt       *string         `json:"finishedAt"`
	Result           json.RawMessage `json:"result"`
	FailureReason    *string         `json:"failureReason"`
}

type hostOperationPageDTO struct {
	Items       []hostOperationDTO `json:"items"`
	HasNextPage bool               `json:"hasNextPage"`
	EndCursor   *string            `json:"endCursor"`
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
		Long: `The machines that launch a team App's workers. A host registers which
workers it can run, as which model family and in which worktrees
(cor:agt:070:01); people and workers issue it typed operations
(cor:agt:070:05), which it collects and reports (cor:agt:070:02).
` + "`list`" + ` and ` + "`ops`" + ` read them; ` + "`issue`" + ` issues one. ` + "`run`" + ` is the
host's own poller, and ` + "`status`" + ` checks its configuration. A host's key is
granted with ` + "`hadron app key scope grant <key-id> host:operate --host-id <host>`" + `.`,
	}
	cmd.AddCommand(newCmdTeamHostLs(f))
	cmd.AddCommand(newCmdTeamHostOps(f))
	cmd.AddCommand(newCmdTeamHostIssue(f))
	cmd.AddCommand(newCmdTeamHostRun(f))
	cmd.AddCommand(newCmdTeamHostStatus(f))
	return cmd
}

func newCmdTeamHostLs(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List the App's host registrations",
		Long: `List the App's host registrations: each host's id and label, how often it
checks in, whether it is live now (refreshed within the App's host-lapse
count of its check-ins, cor:agt:070:01), and the workers it can launch with
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
		Long: `List the App's host operations, NEWEST first (cor:agt:070:02): each one's
type, host, worker, status, who issued it and, once reported, its result or
failure reason. Whoever reads the App's tasks and events may.

--host, --status (issued, collected, succeeded, failed) and --worker (id,
URN or name) narrow it. --limit takes 1 to ` + strconv.Itoa(hostOpsPageCap) + `, the server's page cap.
When more follow, the receipt names the --after that continues the list;
--json carries it as endCursor, with hasNextPage.`,
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
			resp, err := gen.HostOperations(ctx, client, scope.Ref, optStr(strings.TrimSpace(hostID)), statusArg, optStr(strings.TrimSpace(worker)), &limit, optStr(strings.TrimSpace(after)))
			if err != nil {
				return api.MapError(err)
			}
			page := hostOperationPageDTO{
				Items:       []hostOperationDTO{},
				HasNextPage: resp.HostOperations.HasNextPage,
				EndCursor:   resp.HostOperations.EndCursor,
			}
			for _, o := range resp.HostOperations.Items {
				if o != nil {
					page.Items = append(page.Items, hostOperationDTOFrom(&o.HostOperationFields))
				}
			}
			return output.Write(f.IOStreams, f.JSON, page, func(w io.Writer) error {
				if _, err := fmt.Fprintf(w, "app: %s\n", appLabel()); err != nil {
					return err
				}
				t := output.NewTable(w, "ID", "ISSUED", "TYPE", "HOST", "WORKER", "STATUS", "BY", "OUTCOME")
				for _, o := range page.Items {
					t.Row(output.SafeCell(o.ID), output.SafeCell(o.IssuedAt), output.SafeCell(o.Type), output.SafeCell(o.HostID), output.SafeCell(hostOpWorkerCell(o)), output.SafeCell(o.Status), output.SafeCell(o.IssuedBy), output.SafeCell(hostOpOutcomeCell(o)))
				}
				if err := t.Flush(); err != nil {
					return err
				}
				if page.HasNextPage && page.EndCursor != nil {
					_, err := fmt.Fprintf(w, "more follow — continue with --after %s\n", *page.EndCursor)
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
		Use:   "issue <type> --host <host-id> --worker <worker> [--arg key=value]...",
		Short: "Issue an operation to one of the App's hosts",
		Long: `Issue one typed operation to a registered host (cor:agt:070:05). The type
is one of a closed set (cor:agt:070:03); its arguments are ids and name
tokens, never a command and never a credential:

  worktree.create  --arg repo=… --arg baseBranch=… --arg branchName=… --arg label=…
  worktree.remove  --arg worktree=…   (a worktree the host registers for the worker)

The worker is named with --worker (id, URN or name), never inside --arg.
worker.start and worker.end are issued by whoever manages the App —
worker.end for an open session of the worker; worker.deliver is not issued
until a host has a delivery path for it (hadron-cli#829), refusing
HOST_OPERATION_NOT_ISSUABLE (exit 2). Any other type is
HOST_OPERATION_TYPE_UNKNOWN (exit 2), and a missing, extra or malformed
argument BAD_HOST_OPERATION_ARGS (exit 2). An unregistered host is
HOST_NOT_FOUND (exit 4).

WHO ISSUES IT: a worktree operation is issued by the worker's holder, the
worker it reports to, or whoever manages the App (else
HOST_OPERATION_NOT_PERMITTED, exit 8). In a worktree bound to a worker, the
operation is that worker's: it qualifies only as the target's reports-to.
Pass --as-person to issue it as yourself instead — as the target's holder or
the App's manager — with no worker session.`,
		Example: `  hadron team host issue worktree.create --host orca1 --worker Jonas \
    --arg repo=hadron-memory/hadron-cli --arg baseBranch=main --arg branchName=jonas/871 --arg label=jonas-3
  hadron team host issue worktree.remove --host orca1 --worker Jonas --arg worktree=hadron-memory/hadron-cli/jonas-3 --as-person`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
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
			resp, err := gen.IssueHostOperation(ctx, client, &gen.IssueHostOperationInput{
				AppRef: scope.Ref,
				HostId: strings.TrimSpace(hostID),
				Worker: strings.TrimSpace(worker),
				Type:   opType,
				Args:   opArgs,
			})
			if err != nil {
				return api.MapError(err)
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
	return cmd
}
