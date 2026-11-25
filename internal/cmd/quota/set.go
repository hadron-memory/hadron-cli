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
	var org, app, user, action string
	var limit int
	cmd := &cobra.Command{
		Use:   "set (--org <org> [--app <app>] | --app <app> | --user <id>) --action <action> --limit <n>",
		Short: "Set the daily limit for one action",
		Long: `Set the daily limit for one metered action, for an org, an App or a person.
The new limit applies at once; today's use is kept. Name exactly one of these
forms; the server decides who may use each.

  --org <org>             the org's limit. Needs ADMIN or OWNER in the org
                          (else exit 8).
  --org <org> --app <app> that App's limit. Same access; --app must belong to
                          the org (else exit 4).
  --app <app>             an App on its own, no org. An organization's App: the
                          org's ADMIN or OWNER. A person's App: its owner, a
                          member with the owner role, or a platform admin.
                          Every refusal of this form is exit 4, the same as an
                          App that does not exist (App keys and runs
                          included), so it never says whether
                          the App exists.
  --user <id>             a person's limit, by user id: that person or a
                          platform admin. Anyone else gets exit 8, whether or
                          not the person exists (App keys and runs
                          included); only a platform admin
                          naming a person who does not exist gets exit 4.

--user cannot be combined with --org or --app, and one form is required
(exit 2). The App context and the active org fill nothing in; every ref is sent
as typed. An impersonated session cannot set a limit at all: it is refused
first, with exit 1 (read-only), whatever the form. --app alone and --user need
a server that supports them; an older server refuses them as a schema mismatch
(exit 2), and the org forms keep working.

--action is run.execute, llm.tokens, sms.send or email.send. --limit is a
whole number from 0 up. For an org or an App, 0 stops that action for the scope
until the limit is raised (for run.execute or llm.tokens, every run launch is
refused). A person's limit is shared by all their Apps and applies once a
person's App can run; until then setting it only stores the number. The server
refuses other actions and out-of-range limits (exit 2).

Prints the counter as it now stands; --json gives one counter, with the same
fields as a hadron quota list row (scopeKind is org, app or user).`,
		Example: `  hadron quota set --org acme.com --action run.execute --limit 500
  # stops every run launch for that App until you raise the limit
  hadron quota set --org acme.com --app hrn:app:acme.com:assistant --action llm.tokens --limit 0
  hadron quota set --user <user-id> --action email.send --limit 20`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(action) == "" || !cmd.Flags().Changed("limit") {
				return exitcode.Newf(exitcode.Usage, "--action and --limit are required")
			}
			refs, err := readScope(cmd, org, app, user)
			if err != nil {
				return err
			}
			input := &gen.SetQuotaLimitInput{OrgRef: refs.org, AppRef: refs.app, UserRef: refs.user, Action: action, Limit: limit}
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
	addScopeFlags(cmd, &org, &app, &user, "an App's limit: with --org it must belong to that org; on its own it names an organization's or a person's App (id or URN). Here --app names the scope, it is not the App context")
	cmd.Flags().StringVar(&action, "action", "", "run.execute, llm.tokens, sms.send or email.send (required)")
	cmd.Flags().IntVar(&limit, "limit", 0, "the new daily limit, 0 or more (required)")
	return cmd
}
