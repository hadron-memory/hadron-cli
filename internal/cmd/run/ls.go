package run

import (
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/api/gqltypes"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdLs(f *cmdutil.Factory) *cobra.Command {
	var app, org, status, parent, sort, dir string
	var statuses []string
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List App runs (the audit surface)",
		Long: `List App runs — status, trigger kind, and the run id (cor:agt:010:02).

Scope with --app or --org (mutually exclusive; --app defaults to the App
context). Filter with --status (one of PENDING, RUNNING, COMPLETED, FAILED,
CANCELLED, TIMED_OUT), or --statuses (comma-separated; not with --status).
--parent filters by parent run id. --sort accepts createdAt, startedAt,
finishedAt or status; --dir is asc or desc. Omitted sort flags use the
server defaults.
The list pages to exhaustion. COST is the run's priced
cost ("unknown" = no figure, not zero); REASON is the failure kind of a failed,
timed-out or cancelled run ("unknown" if the server gave none; "run get" has
the full reason).`,
		Example: `  hadron run list --app acme.com:ops --status FAILED
  hadron run list --org acme.com --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if app != "" && org != "" {
				return exitcode.Newf(exitcode.Usage, "--app and --org are mutually exclusive")
			}
			var appPtr, orgPtr *string
			switch {
			case org != "":
				orgPtr = &org
			default:
				appRef, err := cmdutil.ResolveAppRef(f, app)
				if err != nil {
					return err
				}
				appPtr = &appRef
			}
			var statusEnum *gen.AppRunStatus
			if status != "" {
				upper := strings.ToUpper(strings.TrimSpace(status))
				if !isValidStatus(upper) {
					return exitcode.Newf(exitcode.Usage, "invalid --status %q — one of %s", status, strings.Join(statusValues, ", "))
				}
				s := gen.AppRunStatus(upper)
				statusEnum = &s
			}

			if cmd.Flags().Changed("status") && cmd.Flags().Changed("statuses") {
				return exitcode.Newf(exitcode.Usage, "--status and --statuses are mutually exclusive")
			}
			var many []gen.AppRunStatus
			if cmd.Flags().Changed("statuses") {
				if len(statuses) == 0 {
					return exitcode.Newf(exitcode.Usage, "--statuses cannot be empty")
				}
				for _, v := range statuses {
					u := strings.ToUpper(strings.TrimSpace(v))
					if !isValidStatus(u) {
						return exitcode.Newf(exitcode.Usage, "invalid --statuses value %q", v)
					}
					many = append(many, gen.AppRunStatus(u))
				}
			}
			var parentPtr *string
			if cmd.Flags().Changed("parent") {
				if strings.TrimSpace(parent) == "" {
					return exitcode.Newf(exitcode.Usage, "--parent cannot be empty")
				}
				parentPtr = &parent
			}
			var sortPtr *gen.AppRunSortField
			if cmd.Flags().Changed("sort") {
				switch sort {
				case "createdAt", "startedAt", "finishedAt", "status":
				default:
					return exitcode.Newf(exitcode.Usage, "--sort must be createdAt, startedAt, finishedAt or status")
				}
				v := gen.AppRunSortField(sort)
				sortPtr = &v
			}
			var dirPtr *gqltypes.SortDirection
			if cmd.Flags().Changed("dir") {
				if dir != "asc" && dir != "desc" {
					return exitcode.Newf(exitcode.Usage, "--dir must be asc or desc")
				}
				v := gqltypes.SortDirection(dir)
				dirPtr = &v
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			// Paged { items, total } envelope (#473), drained to exhaustion.
			items, err := api.CollectAll(func(limit, offset int) ([]*gen.AppRunsAppRunsAppRunsPageItemsAppRun, int, error) {
				resp, err := gen.AppRuns(cmd.Context(), client, appPtr, orgPtr, statusEnum, many, parentPtr, sortPtr, dirPtr, &limit, &offset)
				if err != nil {
					return nil, 0, api.MapError(err)
				}
				if resp == nil || resp.AppRuns == nil {
					return nil, 0, nil
				}
				return resp.AppRuns.Items, resp.AppRuns.Total, nil
			})
			if err != nil {
				return err
			}

			runs := make([]appRunDTO, 0, len(items))
			for _, r := range items {
				if r == nil {
					continue
				}
				runs = append(runs, dtoFromFields(r.AppRunFields))
			}

			return output.Write(f.IOStreams, f.JSON, runs, func(w io.Writer) error {
				t := output.NewTable(w, "ID", "STATUS", "TRIGGER", "ENTRY", "FINISHED", "COST", "REASON")
				for _, r := range runs {
					t.Row(output.SafeCell(r.ID), output.SafeCell(r.Status), output.SafeCell(r.TriggerKind), output.SafeCell(r.EntryNodeURN), output.SafeCell(output.Dash(r.FinishedAt)), costCell(r), reasonCell(r))
				}
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVar(&app, "app", "", "App to scope to (ID or URN; defaults to the App context)")
	cmd.Flags().StringVar(&org, "org", "", "organization to scope to (ID or URN)")
	cmd.Flags().StringVar(&status, "status", "", "filter by status (PENDING, RUNNING, COMPLETED, FAILED, CANCELLED, TIMED_OUT)")
	cmd.Flags().StringSliceVar(&statuses, "statuses", nil, "filter by several statuses (comma-separated; not with --status)")
	cmd.Flags().StringVar(&parent, "parent", "", "filter by parent run id")
	cmd.Flags().StringVar(&sort, "sort", "", "createdAt, startedAt, finishedAt or status (server default when omitted)")
	cmd.Flags().StringVar(&dir, "dir", "", "asc or desc (server default when omitted)")
	return cmd
}
