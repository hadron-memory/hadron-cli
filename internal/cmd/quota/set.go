package quota

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdSet(f *cmdutil.Factory) *cobra.Command {
	var org, app, action string
	var limit int
	cmd := &cobra.Command{
		Use:   "set --org <org> [--app <app>] --action <action> --limit <n>",
		Short: "Set the enforced daily limit for one action",
		Long: `Set the daily limit for one metered action, for the org or, with --app, for
one of its Apps. The new limit applies at once; today's use is kept.

--action is run.execute, llm.tokens, sms.send or email.send. --limit is a
whole number from 0 up; 0 stops that action for the scope until the limit is
raised (for run.execute or llm.tokens, every run launch is refused). The
server refuses other actions and out-of-range limits (exit 2).

Needs ADMIN or OWNER in the org (else exit 8). --app must belong to the org
(else exit 4).

Prints the counter as it now stands; --json gives one counter, with the same
fields as a hadron quota list row.`,
		Example: `  hadron quota set --org acme.com --action run.execute --limit 500
  hadron quota set --org acme.com --app hrn:app:acme.com:assistant --action llm.tokens --limit 0`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(org) == "" || strings.TrimSpace(action) == "" || !cmd.Flags().Changed("limit") {
				return exitcode.Newf(exitcode.Usage, "--org, --action and --limit are required")
			}
			input := &gen.SetQuotaLimitInput{OrgRef: org, Action: action, Limit: limit}
			if cmd.Flags().Changed("app") {
				if strings.TrimSpace(app) == "" {
					return exitcode.Newf(exitcode.Usage, "--app is empty; pass an App, or leave --app out to set the org-level limit")
				}
				input.AppRef = &app
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.SetQuotaLimit(cmd.Context(), client, input)
			if err != nil {
				return api.MapError(err)
			}
			if resp == nil || resp.SetQuotaLimit == nil {
				return exitcode.Newf(exitcode.Error, "server returned no quota counter")
			}
			dto := dtoFromFields(resp.SetQuotaLimit.QuotaCounterFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ set %s limit for %s:%s to %d (used %d today; resets %s)\n",
					output.SafeCell(dto.Action), output.SafeCell(dto.ScopeKind), output.SafeCell(dto.ScopeID),
					dto.Limit, dto.Used, output.SafeCell(dto.ResetAt))
				return err
			})
		},
	}
	cmd.Flags().StringVar(&org, "org", "", "organization id, URN or root (required)")
	cmd.Flags().StringVar(&app, "app", "", "set this App's limit instead of the org's (id or URN; must belong to --org)")
	cmd.Flags().StringVar(&action, "action", "", "run.execute, llm.tokens, sms.send or email.send (required)")
	cmd.Flags().IntVar(&limit, "limit", 0, "the new daily limit, 0 or more; 0 stops the action for the scope (required)")
	return cmd
}
