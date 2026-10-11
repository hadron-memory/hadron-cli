package team

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

func newCmdWorkerSetReportsTo(f *cmdutil.Factory) *cobra.Command {
	var clear bool
	cmd := &cobra.Command{
		Use:   "set-reports-to <worker> [<target>] [--clear]",
		Short: "Set, hand over or clear who a worker reports to",
		Long:  "The reporting relation lives on the worker and survives session changes. The server decides who may change it; its typed refusals are returned without retrying. Pass a target worker name, URN or id, or --clear to report to nobody.",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 && len(args) != 2 {
				return exitcode.Newf(exitcode.Usage, "pass <worker> <target>, or <worker> --clear")
			}
			if clear && len(args) != 1 || !clear && len(args) != 2 {
				return exitcode.Newf(exitcode.Usage, "pass exactly one target or --clear")
			}
			if !clear && strings.TrimSpace(args[1]) == "" {
				return exitcode.Newf(exitcode.Usage, "target must name a worker")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			worker, err := workerForArg(cmd, f, args[0])
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			var target *string
			if !clear {
				target = &args[1]
			}
			// Target names resolve on the server in this worker's App. Never infer permission here.
			result, err := gen.SetWorkerReportsTo(cmd.Context(), client, worker.Id, target)
			if err != nil {
				return api.MapWorkerReportingError(err)
			}
			if result == nil || result.SetWorkerReportsTo == nil {
				return exitcode.Newf(exitcode.Unavailable, "reporting operation returned no result; read the worker before retrying")
			}
			dto := workerDTOFromFields(result.SetWorkerReportsTo.WorkerFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "%s\n  reports to: %s\n", dto.Name, reportsToLabel(dto.ReportsTo, dto.ReportsToRetired, dto.ReportsTo != nil || workingStateVisible(dto.HasLiveSession)))
				return err
			})
		},
	}
	cmd.Flags().BoolVar(&clear, "clear", false, "clear the reporting relation instead of naming a target")
	return cmd
}

// The binding identifies the worker, but never caches its mutable relation.
// An unavailable read keeps compaction recovery usable and reports unknown.
func loadReporting(ctx context.Context, f *cmdutil.Factory, b *binding, dto *whoamiDTO) {
	if b.WorkerID == "" || b.Server == "" && f.ServerFlag == "" {
		return
	}
	if err := checkBindingServer(f, b); err != nil {
		return
	}
	client, err := f.GraphQLClient()
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	r, err := gen.WorkerReporting(ctx, client, b.WorkerID)
	if err != nil || r == nil || r.Worker == nil {
		return
	}
	w := r.Worker
	dto.ReportingKnown = w.ReportsTo != nil || workingStateVisible(w.HasLiveSession)
	dto.ReportsToRetired = w.ReportsToRetired
	if w.ReportsTo != nil {
		dto.ReportsTo = reportingRef(w.ReportsTo.WorkerReportingRefFields)
	}
}
