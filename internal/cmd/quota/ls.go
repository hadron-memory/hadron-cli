package quota

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdLs(f *cmdutil.Factory) *cobra.Command {
	var org, app, user string
	cmd := &cobra.Command{
		Use:     "list (--org <org> [--app <app>] | --app <app> | --user <id>)",
		Aliases: []string{"ls"},
		Short:   "Show quota counters for an org, an App or a person",
		Long: `Show daily quota counters: one row per scope and action, with today's use, the
limit and when it resets. Name exactly one of these forms; the server decides
who may use each.

  --org <org>             the org's counters. Needs ADMIN or OWNER in the org
                          (else exit 8).
  --org <org> --app <app> the org's and that App's. Same access; --app must
                          belong to the org (else exit 4).
  --app <app>             an App on its own, no org. An organization's App: the
                          org's ADMIN or OWNER, and it shows the App's counters
                          and the organization's. A person's App: its owner, a
                          member with the owner role, or a platform admin; it
                          shows the App's counters, plus the owner's own (user)
                          counters only to the owner or a platform admin (an
                          owner-role member sees the App's rows only). Every
                          refusal of this form is exit 4, the same as an App
                          that does not exist (App keys, impersonated sessions
                          and runs included), so it never says whether the App
                          exists.
  --user <id>             a person's counters, by user id: that person or a
                          platform admin. Anyone else gets exit 8, whether or
                          not the person exists (App keys, impersonated
                          sessions and runs included); only a platform admin
                          naming a person who does not exist gets exit 4.

--user cannot be combined with --org or --app, and one form is required
(exit 2). The App context and the active org fill nothing in; every ref is sent
as typed. Counters that were never used are created with the platform defaults
the first time they are shown. For a scope you have not used, the limit shown
is the platform default. A person's limit is shared by all their Apps and
applies once a person's App can run.

--json gives an array of id, scopeKind (org, app or user), scopeId, action,
window, used, limit and resetAt; an empty list is [].`,
		Example: `  hadron quota list --org acme.com
  hadron quota list --org acme.com --app hrn:app:acme.com:assistant --json
  hadron quota list --app hrn:app:acme.com:assistant
  hadron quota list --user <user-id>`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			refs, err := readScope(cmd, org, app, user)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			// The org forms keep their original document so they still work against a server that
			// predates the person and App-alone forms; only the new forms use the scoped one.
			rows := []counterDTO{}
			if refs.org != nil {
				resp, err := gen.QuotaCounters(cmd.Context(), client, *refs.org, refs.app)
				if err != nil {
					return api.MapError(err)
				}
				if resp != nil {
					for _, q := range resp.QuotaCounters {
						if q != nil {
							rows = append(rows, dtoFromFields(q.QuotaCounterFields))
						}
					}
				}
			} else {
				resp, err := gen.QuotaCountersScoped(cmd.Context(), client, refs.app, refs.user)
				if err != nil {
					return api.MapError(err)
				}
				if resp != nil {
					for _, q := range resp.QuotaCounters {
						if q != nil {
							rows = append(rows, dtoFromFields(q.QuotaCounterFields))
						}
					}
				}
			}
			return output.Write(f.IOStreams, f.JSON, rows, func(w io.Writer) error {
				return writeTable(w, rows)
			})
		},
	}
	addScopeFlags(cmd, &org, &app, &user, "an App's counters: with --org it must belong to that org; on its own it names an organization's or a person's App (id or URN). Here --app picks counters to show, it is not the App context")
	return cmd
}
