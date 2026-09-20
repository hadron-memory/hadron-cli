package app

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

// appDTO is the stable --json shape for an App.
type appDTO struct {
	ID          string  `json:"id"`
	URN         string  `json:"urn"`
	Name        string  `json:"name"`
	AppType     string  `json:"appType"`
	AgentID     *string `json:"agentId"`
	MemberCount int     `json:"memberCount"`
	CreatedAt   string  `json:"createdAt"`
}

func newCmdLs(f *cmdutil.Factory) *cobra.Command {
	var org string
	var ownedByMe bool
	cmd := &cobra.Command{
		Use:     "list (--org <id> | --owned-by-me)",
		Aliases: []string{"ls"},
		Short:   "List Apps in an organization, or the Apps you own (--owned-by-me)",
		Long: `List Apps in an organization (requires org membership), or the Apps you
own outright.

--owned-by-me lists your own org-less Apps: no organizationId, owned by
you. It is the "my Apps" slice, answered by the server — the same predicate
` + "`agent list --owned-by-me`" + ` and ` + "`memory list --owned-by-me`" + ` forward.

It is org-less by definition, so it does not combine with --org and your
active organization is not consulted. An App-key caller gets an empty list:
this is a question about a user. (To CREATE an App in this slice, see
` + "`app install --owner-me`" + `.)`,
		Example: `  hadron app list --org acme.com
  hadron app list --owned-by-me
  hadron app list --owned-by-me --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Exactly one of the two, mirroring `app install (--org | --owner-me)`.
			// Hand-rolled rather than MarkFlagsMutuallyExclusive so the refusal
			// can name BOTH alternatives — cobra's group message only lists the
			// flags. Keyed on Changed(), not on the value: `--org=` is "asked
			// for nothing", which is a different mistake from not asking
			// (review:an-empty-flag-is-not-an-absent-flag), and a value test
			// would silently read it as the latter.
			orgGiven := cmd.Flags().Changed("org")
			switch {
			case ownedByMe && orgGiven:
				return exitcode.Newf(exitcode.Usage, "--owned-by-me lists the Apps you own, which have no organization; drop --org (or drop --owned-by-me to list an organization's Apps)")
			case !ownedByMe && !orgGiven:
				return exitcode.Newf(exitcode.Usage, "specify --org <id> to list an organization's Apps, or --owned-by-me to list the Apps you own")
			case orgGiven && strings.TrimSpace(org) == "":
				return exitcode.Newf(exitcode.Usage, "--org needs an organization ID or URN; it was given an empty value")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			var orgPtr *string
			if org != "" {
				orgPtr = &org
			}
			// Only ever set true: the clause composes by AND, so a literal false
			// would be a no-op the server still has to read.
			var filter *gen.AppFilter
			if ownedByMe {
				filter = &gen.AppFilter{OwnedByMe: &ownedByMe}
			}
			// Paged { items, total } envelope (hadron-server#473), drained to
			// exhaustion — this command's contract is "all Apps in the slice".
			items, err := api.CollectAll(func(limit, offset int) ([]*gen.AppsAppsAppsPageItemsApp, int, error) {
				resp, err := gen.Apps(cmd.Context(), client, orgPtr, filter, &limit, &offset)
				if err != nil {
					return nil, 0, api.MapError(err)
				}
				if resp == nil || resp.Apps == nil {
					return nil, 0, nil
				}
				return resp.Apps.Items, resp.Apps.Total, nil
			})
			if err != nil {
				return err
			}

			apps := make([]appDTO, 0, len(items))
			for _, a := range items {
				if a == nil {
					continue
				}
				apps = append(apps, appDTO{
					ID:          a.Id,
					URN:         a.Urn,
					Name:        a.Name,
					AppType:     string(a.AppType),
					AgentID:     a.AgentId,
					MemberCount: a.MemberCount,
					CreatedAt:   a.CreatedAt,
				})
			}

			return output.Write(f.IOStreams, f.JSON, apps, func(w io.Writer) error {
				t := output.NewTable(w, "ID", "NAME", "TYPE", "URN")
				for _, a := range apps {
					t.Row(a.ID, a.Name, a.AppType, a.URN)
				}
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVar(&org, "org", "", "organization ID or URN (or use --owned-by-me)")
	cmd.Flags().BoolVar(&ownedByMe, "owned-by-me", false, "list only the org-less Apps you own")
	// NOT MarkFlagRequired("org") any more: --owned-by-me is the other way to
	// name a slice. And NOT MarkFlagsMutuallyExclusive either — cobra validates
	// flag groups before RunE, so registering the group would pre-empt the
	// refusal above with a message that names the flags but not the choice.
	return cmd
}
