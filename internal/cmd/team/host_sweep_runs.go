package team

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/hostup"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

type sweepRecordDTO struct {
	Recorded bool   `json:"recorded"`
	Reason   string `json:"reason"`
	RunID    string `json:"runId"`
}

var sweepWorkerToken = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:@-]{0,127}$`)
var sweepReasonCode = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)
var sweepRunToken = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$`)

// Record with a distinct host-only client. Reporting never borrows the manager
// client's credential, and never changes the reconciliation exit code.
func recordSweepRun(ctx context.Context, f *cmdutil.Factory, configPath, app, host, runID string, started time.Time, apply bool, res hostup.Result) sweepRecordDTO {
	ctx = api.WithoutSession(ctx)
	status := sweepRecordDTO{Reason: "NO_HOST_KEY", RunID: runID}
	if configPath == "" {
		return status
	}
	cfg, _, server, err := loadHostPoller(f, configPath)
	if err != nil {
		status.Reason = "HOST_KEY_UNAVAILABLE"
		return status
	}
	reconcileServer, err := f.Server()
	if err != nil || (cfg.Server != "" && strings.TrimRight(cfg.Server, "/") != strings.TrimRight(reconcileServer, "/")) {
		status.Reason = "SERVER_MISMATCH"
		return status
	}
	// Resolve through the host-key gate, not by comparing aliases. This also
	// proves the key belongs to the host whose reconciliation is being recorded.
	for _, ref := range []string{cfg.App, app} {
		identity, err := gen.HostSweepIdentity(ctx, server.client, ref, host)
		if err != nil {
			status.Reason = "HOST_IDENTITY_FAILED"
			if code := api.ServerCodeOf(err); sweepReasonCode.MatchString(code) {
				status.Reason = code
			}
			return status
		}
		if identity == nil || identity.HostDesiredState == nil || identity.HostDesiredState.HostId != host {
			status.Reason = "HOST_SCOPE_MISMATCH"
			return status
		}
		if cfg.App == app {
			break
		}
	}
	if res.SweepMode == "OFF" {
		status.Reason = "SWEEP_OFF"
		return status
	}
	if res.SweepMode != "APPLY" && res.SweepMode != "OBSERVE" {
		status.Reason = "SWEEP_MODE_UNKNOWN"
		return status
	}
	mode := gen.SweepModeObserve
	if apply && res.SweepMode == "APPLY" {
		mode = gen.SweepModeApply
	}
	in := &gen.RecordHostSweepRunInput{AppRef: cfg.App, RunId: runID, StartedAt: started.UTC().Format(time.RFC3339Nano), EndedAt: time.Now().UTC().Format(time.RFC3339Nano), Mode: mode, Planned: sweepWorkerCount(res.Planned), Applied: sweepWorkerCount(res.Applied), Skipped: sweepWorkerCount(res.Skipped), Failed: sweepWorkerCount(res.Failed), Lines: []*gen.HostSweepRunLineInput{}}
	// Prefer failure/action results over plan rows; one line per worker. Never
	// forward names/details as fallback text when they are not valid tokens.
	seen := map[string]bool{}
	for _, group := range []struct {
		items   []hostup.Item
		outcome gen.SweepRunOutcome
	}{{res.Failed, gen.SweepRunOutcomeFailed}, {res.Applied, gen.SweepRunOutcomeApplied}, {res.Skipped, gen.SweepRunOutcomeSkipped}, {res.Planned, gen.SweepRunOutcomeSkipped}} {
		for _, it := range group.items {
			worker := it.WorkerID
			if !sweepWorkerToken.MatchString(worker) {
				worker = it.Worker
			}
			if !sweepWorkerToken.MatchString(worker) || seen[worker] || len(in.Lines) == 50 {
				continue
			}
			reason := it.Reason
			if !sweepReasonCode.MatchString(reason) {
				reason = "UNKNOWN_REASON"
			}
			in.Lines = append(in.Lines, &gen.HostSweepRunLineInput{Worker: worker, Outcome: group.outcome, Reason: reason})
			seen[worker] = true
		}
	}
	reply, err := gen.RecordHostSweepRun(ctx, server.client, in)
	if err != nil {
		status.Reason = "RECORD_FAILED"
		if code := api.ServerCodeOf(err); sweepReasonCode.MatchString(code) {
			status.Reason = code
		}
		if api.IsFrameworkValidationOnlyFor(err, "recordHostSweepRun") {
			status.Reason = "S10_UNAVAILABLE"
		}
		return status
	}
	if reply == nil || reply.RecordHostSweepRun == nil || reply.RecordHostSweepRun.RunId != runID || reply.RecordHostSweepRun.Id == "" {
		status.Reason = "RECORD_UNCONFIRMED"
		return status
	}
	status.Recorded = true
	status.Reason = "RECORDED"
	return status
}

// One worker can need multiple queue steps; S10 counts workers, not steps.
func sweepWorkerCount(items []hostup.Item) int {
	seen := map[string]bool{}
	for _, it := range items {
		key := it.WorkerID
		if key == "" {
			key = it.Worker
		}
		seen[key] = true
	}
	return len(seen)
}

type sweepLineDTO struct {
	Worker  string `json:"worker"`
	Outcome string `json:"outcome"`
	Reason  string `json:"reason"`
}
type sweepRunDTO struct {
	ID         string         `json:"id"`
	HostID     string         `json:"hostId"`
	RunID      string         `json:"runId"`
	StartedAt  string         `json:"startedAt"`
	EndedAt    string         `json:"endedAt"`
	Mode       string         `json:"mode"`
	Planned    int            `json:"planned"`
	Applied    int            `json:"applied"`
	Skipped    int            `json:"skipped"`
	Failed     int            `json:"failed"`
	RecordedAt string         `json:"recordedAt"`
	Lines      []sweepLineDTO `json:"lines"`
}

func newCmdTeamHostSweepRuns(f *cmdutil.Factory) *cobra.Command {
	var host string
	var limit int
	c := &cobra.Command{Use: "sweep-runs --host <id> [--limit N]", Short: "Read this host's recorded sweep runs", Long: "Read the newest hostSweepRuns (S10), newest first. Uses the normal App read credential, without a worker session. The server decides read authority. A server without S10 refuses this read; no fallback data is invented.", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if strings.TrimSpace(host) == "" || limit < 1 || limit > 100 {
			return exitcode.Newf(exitcode.Usage, "--host is required; --limit must be 1..100")
		}
		ctx := api.WithoutSession(cmd.Context())
		b, err := readBindingOrNilWithApp(ctx, f)
		if err != nil {
			return err
		}
		scope, err := resolveTeamAppScope(ctx, f, b)
		if err != nil {
			return err
		}
		client, err := f.GraphQLClient()
		if err != nil {
			return err
		}
		r, err := gen.HostSweepRuns(ctx, client, scope.Ref, host, &limit)
		if err != nil {
			return api.WithServerCode(api.MapError(err), err)
		}
		if r == nil || r.HostSweepRuns == nil {
			return exitcode.Newf(exitcode.Error, "server returned no sweep runs response")
		}
		runs := []sweepRunDTO{}
		for _, v := range r.HostSweepRuns {
			if v == nil {
				return exitcode.Newf(exitcode.Error, "server returned an empty sweep run row")
			}
			d := sweepRunDTO{ID: v.Id, HostID: v.HostId, RunID: v.RunId, StartedAt: v.StartedAt, EndedAt: v.EndedAt, Mode: string(v.Mode), Planned: v.Planned, Applied: v.Applied, Skipped: v.Skipped, Failed: v.Failed, RecordedAt: v.RecordedAt, Lines: []sweepLineDTO{}}
			for _, l := range v.Lines {
				if l == nil {
					return exitcode.Newf(exitcode.Error, "server returned an empty sweep run line")
				}
				d.Lines = append(d.Lines, sweepLineDTO{l.Worker, string(l.Outcome), l.Reason})
			}
			runs = append(runs, d)
		}
		return output.Write(f.IOStreams, f.JSON, runs, func(w io.Writer) error {
			if _, err := fmt.Fprintf(w, "app: %s\n", lazyAppLabel(ctx, f, scope)()); err != nil {
				return err
			}
			t := output.NewTable(w, "RUN", "STARTED", "MODE", "PLANNED", "APPLIED", "SKIPPED", "FAILED")
			for _, r := range runs {
				t.Row(output.SafeCell(r.RunID), output.SafeCell(r.StartedAt), output.SafeCell(r.Mode), fmt.Sprint(r.Planned), fmt.Sprint(r.Applied), fmt.Sprint(r.Skipped), fmt.Sprint(r.Failed))
			}
			return t.Flush()
		})
	}}
	c.Flags().StringVar(&host, "host", "", "host id (required)")
	c.Flags().IntVar(&limit, "limit", 20, "newest runs to read (1..100)")
	return c
}
