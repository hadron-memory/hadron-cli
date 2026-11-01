package discovery

import (
	"fmt"
	"io"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

type publicRefDTO struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	URN         string  `json:"urn"`
	Description *string `json:"description"`
}
type catalogueDTO struct {
	Items []publicRefDTO `json:"items"`
	Total int            `json:"total"`
}

// NewCmdMarketplace exposes only the server's opted-in public catalogue.
func NewCmdMarketplace(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{Use: "marketplace <command>", Short: "Browse the public resource catalogue", Long: `Browse agents, memories and organizations advertised in the public catalogue.
Listing is opt-in and separate from access: unlisted but public resources can
still be opened by link, but do not appear here (cor:acl:080:04). The server
applies every eligibility rule. No App/org/worktree filter is inherited.

These reads work signed out; stored credentials, when available, are attached.
Each page contains only id, name, urn and nullable description, never members,
private resource details or credentials. No follow-up resource reads are made.`, Example: `  hadron marketplace agents --limit 10
  hadron marketplace memories --json
  hadron marketplace orgs --limit 0`}
	for _, kind := range []string{"agents", "memories", "orgs"} {
		cmd.AddCommand(newCatalogue(f, kind))
	}
	return cmd
}
func newCatalogue(f *cmdutil.Factory, kind string) *cobra.Command {
	var limit, offset int
	cmd := &cobra.Command{Use: kind, Short: "Browse listed public " + kind, Long: `Read one page of the server's public catalogue, ordered by (name, id).
Only the server's listed, publicly accessible resources appear; the CLI does
not infer eligibility or supplement the result. --limit 0 is count-only;
otherwise 1..200 (default 20). --offset skips rows (default 0). No automatic
paging: stderr gives the next offset when more rows remain.

--json is {items:[{id,name,urn,description}],total}; an empty page has items:[]
and a null description stays null. For full resources, use the returned URN
with the corresponding resource command; catalogue listing grants no access.`, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if limit < 0 || limit > 200 || offset < 0 {
			return exitcode.Newf(exitcode.Usage, "--limit must be 0..200 and --offset non-negative")
		}
		client, _, err := f.PublicGraphQLClient()
		if err != nil {
			return err
		}
		d := catalogueDTO{Items: []publicRefDTO{}}
		appendRef := func(r gen.DiscoveryPublicRef) {
			d.Items = append(d.Items, publicRefDTO{ID: r.Id, Name: r.Name, URN: r.Urn, Description: r.Description})
		}
		switch kind {
		case "agents":
			r, e := gen.MarketplaceAgents(cmd.Context(), client, limit, offset)
			if e != nil {
				return api.MapError(e)
			}
			if r == nil || r.MarketplaceAgents == nil || r.MarketplaceAgents.Items == nil {
				return exitcode.Newf(exitcode.Unavailable, "server returned an incomplete catalogue page")
			}
			d.Total = r.MarketplaceAgents.Total
			for _, row := range r.MarketplaceAgents.Items {
				if row == nil {
					return exitcode.Newf(exitcode.Unavailable, "server returned a null catalogue row")
				}
				appendRef(row.DiscoveryPublicRef)
			}
		case "memories":
			r, e := gen.MarketplaceMemories(cmd.Context(), client, limit, offset)
			if e != nil {
				return api.MapError(e)
			}
			if r == nil || r.MarketplaceMemories == nil || r.MarketplaceMemories.Items == nil {
				return exitcode.Newf(exitcode.Unavailable, "server returned an incomplete catalogue page")
			}
			d.Total = r.MarketplaceMemories.Total
			for _, row := range r.MarketplaceMemories.Items {
				if row == nil {
					return exitcode.Newf(exitcode.Unavailable, "server returned a null catalogue row")
				}
				appendRef(row.DiscoveryPublicRef)
			}
		case "orgs":
			r, e := gen.MarketplaceOrganizations(cmd.Context(), client, limit, offset)
			if e != nil {
				return api.MapError(e)
			}
			if r == nil || r.MarketplaceOrganizations == nil || r.MarketplaceOrganizations.Items == nil {
				return exitcode.Newf(exitcode.Unavailable, "server returned an incomplete catalogue page")
			}
			d.Total = r.MarketplaceOrganizations.Total
			for _, row := range r.MarketplaceOrganizations.Items {
				if row == nil {
					return exitcode.Newf(exitcode.Unavailable, "server returned a null catalogue row")
				}
				appendRef(row.DiscoveryPublicRef)
			}
		}
		if err := output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error {
			if _, err := fmt.Fprintf(w, "Catalogue total: %d\n", d.Total); err != nil {
				return err
			}
			t := output.NewTable(w, "ID", "NAME", "URN", "DESCRIPTION")
			for _, r := range d.Items {
				t.Row(output.SafeCell(r.ID), output.SafeCell(r.Name), output.SafeCell(r.URN), output.SafeCell(output.Dash(r.Description)))
			}
			return t.Flush()
		}); err != nil {
			return err
		}
		return nextPage(f, "catalogue rows", offset, len(d.Items), d.Total, limit)
	}}
	if kind == "orgs" {
		cmd.Aliases = []string{"organizations"}
	}
	cmd.Flags().IntVar(&limit, "limit", 20, "page size 0..200; zero reads only total")
	cmd.Flags().IntVar(&offset, "offset", 0, "skip this many catalogue rows")
	return cmd
}
