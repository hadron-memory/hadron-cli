package quota

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
	var org, app string
	cmd := &cobra.Command{
		Use:     "list --org <org> [--app <app>]",
		Aliases: []string{"ls"},
		Short:   "Show an org's (and one App's) quota counters",
		Long: `Show the org's daily quota counters and, with --app, that App's as well: one
row per scope and action, with today's use, the limit and when it resets.

Needs ADMIN or OWNER in the org (else exit 8). --app must belong to the org
(else exit 4). Counters that were never used are created with the platform
defaults the first time they are shown, so the list is never empty for a
scope that exists.

--json gives an array of id, scopeKind (org or app), scopeId, action, window,
used, limit and resetAt; an empty list is [].`,
		Example: `  hadron quota list --org acme.com
  hadron quota list --org acme.com --app hrn:app:acme.com:assistant --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(org) == "" {
				return exitcode.Newf(exitcode.Usage, "--org is required")
			}
			var appRef *string
			if cmd.Flags().Changed("app") {
				if strings.TrimSpace(app) == "" {
					return exitcode.Newf(exitcode.Usage, "--app is empty; pass an App, or leave --app out for the org's counters only")
				}
				appRef = &app
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.QuotaCounters(cmd.Context(), client, org, appRef)
			if err != nil {
				return api.MapError(err)
			}
			rows := []counterDTO{}
			if resp != nil {
				for _, q := range resp.QuotaCounters {
					if q == nil {
						continue
					}
					rows = append(rows, dtoFromFields(q.QuotaCounterFields))
				}
			}
			return output.Write(f.IOStreams, f.JSON, rows, func(w io.Writer) error {
				return writeTable(w, rows)
			})
		},
	}
	cmd.Flags().StringVar(&org, "org", "", "organization id, URN or root (required)")
	cmd.Flags().StringVar(&app, "app", "", "also show this App's counters (id or URN; must belong to --org)")
	return cmd
}
