package scope

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

type scopeListItem = gen.ScopesScopesScopesPageItemsScope

func newCmdList(f *cmdutil.Factory) *cobra.Command {
	var (
		org   string
		app   string
		agent string
		name  string
	)
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List named search scopes",
		Long: `List the search scopes you can see.

Narrow by owner with exactly one of --owner-org, --owner-app or --owner-agent,
or by --name. Without a filter this spans every scope visible to you.

The owner flags FILTER; the persistent --app flag sets the App context used to
resolve a scope name elsewhere and does not narrow this listing.

The MEMORIES column counts what the scope lists in total. When some of those
are not readable by you, the count of hidden entries is reported below the
table — by count only, never by name.`,
		Example: `  hadron scope list
  hadron scope list --owner-org acme.com
  hadron scope list --owner-app hrn:app:acme.com:dev-team`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Local flag validation runs BEFORE the client: resolving it
			// requires credentials, so an unauthenticated run with bad flags
			// would report AuthRequired instead of the usage error these
			// checks exist to give.
			filter, err := ownerFilter(org, app, agent, name)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			// Paged to exhaustion — the contract is "every scope you can see".
			items, err := api.CollectAll(func(limit, offset int) ([]*scopeListItem, int, error) {
				off := offset
				resp, err := gen.Scopes(cmd.Context(), client, filter, &limit, &off, nil)
				if err != nil {
					return nil, 0, api.MapError(err)
				}
				if resp == nil || resp.Scopes == nil {
					return nil, 0, nil
				}
				return resp.Scopes.Items, resp.Scopes.Total, nil
			})
			if err != nil {
				return err
			}
			scopes := make([]scopeDTO, 0, len(items))
			totalHidden := 0
			for _, s := range items {
				if s == nil {
					continue
				}
				d := dtoFromFields(s.ScopeFields)
				totalHidden += d.HiddenMemoryCount
				scopes = append(scopes, d)
			}
			return output.Write(f.IOStreams, f.JSON, scopes, func(w io.Writer) error {
				t := output.NewTable(w, "NAME", "OWNER", "MEMORIES", "ID")
				for _, s := range scopes {
					t.Row(s.Name, s.OwnerType+" "+ownerLabel(s.OwnerURN, s.OwnerID), itoa(s.MemoryCount), s.ID)
				}
				if err := t.Flush(); err != nil {
					return err
				}
				// Aggregate disclosure: without it a reader compares MEMORIES
				// against a shorter `scope get` listing and concludes the CLI
				// lost rows.
				if note := hiddenNoteIn(totalHidden, "these scopes"); note != "" {
					if _, err := io.WriteString(w, note+"\n"); err != nil {
						return err
					}
				}
				return nil
			})
		},
	}
	// NOT --app: that is a persistent flag meaning the App CONTEXT, and a local
	// flag of the same name would shadow it for this command only. The owner
	// flags are spelled out so the two ideas cannot be confused — the same
	// confusion cmdutil.NoteAppIsContextOnly exists to warn about elsewhere.
	cmd.Flags().StringVar(&org, "owner-org", "", "only scopes owned by this organization (ID or URN)")
	cmd.Flags().StringVar(&app, "owner-app", "", "only scopes owned by this App (ID or URN)")
	cmd.Flags().StringVar(&agent, "owner-agent", "", "only scopes owned by this installed Agent (ID or URN)")
	cmd.Flags().StringVar(&name, "name", "", "only scopes with this name")
	return cmd
}
