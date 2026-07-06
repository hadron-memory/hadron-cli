package run

import (
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdLs(f *cmdutil.Factory) *cobra.Command {
	var app, org, status string
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List App runs (the audit surface)",
		Long: `List App runs — status, trigger kind, and the run id (cor:agt:010:02).

Scope with --app or --org (mutually exclusive; --app defaults to the App
context). Filter with --status (one of PENDING, RUNNING, COMPLETED, FAILED,
CANCELLED, TIMED_OUT). The list pages to exhaustion.`,
		Example: `  hadron run ls --app acme.com:ops --status FAILED
  hadron run ls --org acme.com --json`,
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

			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			// Paged { items, total } envelope (#473), drained to exhaustion.
			items, err := api.CollectAll(func(limit, offset int) ([]*gen.AppRunsAppRunsAppRunsPageItemsAppRun, int, error) {
				resp, err := gen.AppRuns(cmd.Context(), client, appPtr, orgPtr, statusEnum, &limit, &offset)
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
				t := output.NewTable(w, "ID", "STATUS", "TRIGGER", "ENTRY", "FINISHED")
				for _, r := range runs {
					t.Row(r.ID, r.Status, r.TriggerKind, r.EntryNodeURN, output.Dash(r.FinishedAt))
				}
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVar(&app, "app", "", "App to scope to (ID or URN; defaults to the App context)")
	cmd.Flags().StringVar(&org, "org", "", "organization to scope to (ID or URN)")
	cmd.Flags().StringVar(&status, "status", "", "filter by status (PENDING, RUNNING, COMPLETED, FAILED, CANCELLED, TIMED_OUT)")
	return cmd
}
