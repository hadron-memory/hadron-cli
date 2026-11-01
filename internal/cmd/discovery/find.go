// Package discovery exposes cross-entity discovery and public catalogue reads.
// The server decides matching, ranking and visibility; the CLI only renders.
package discovery

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

type hitDTO struct {
	ID             string  `json:"id"`
	EntityType     string  `json:"entityType"`
	Name           *string `json:"name"`
	URN            *string `json:"urn"`
	Description    *string `json:"description"`
	Score          float64 `json:"score"`
	MatchedField   string  `json:"matchedField"`
	MemoryID       *string `json:"memoryId"`
	OrganizationID *string `json:"organizationId"`
}
type scopeDTO struct {
	Kind         string   `json:"kind"`
	Label        string   `json:"label"`
	Source       string   `json:"source"`
	OwnerURN     *string  `json:"ownerUrn"`
	MemoryURNs   []string `json:"memoryUrns"`
	DroppedCount int      `json:"droppedCount"`
}
type resultDTO struct {
	Hits     []hitDTO  `json:"hits"`
	Total    int       `json:"total"`
	Degraded []string  `json:"degraded"`
	Scope    *scopeDTO `json:"scope"`
}

// NewCmdFind builds the cross-entity counterpart of node-only search.
func NewCmdFind(f *cmdutil.Factory) *cobra.Command {
	var types, fields []string
	var org, scope, app string
	var limit, offset int
	cmd := &cobra.Command{Use: "find <query>", Short: "Find entities across the resources you may see",
		Long: `Find organizations, memories, nodes, agents, Apps, AI configs and users in one
server-ranked list. Unlike search (node-only, vector-aware), this is a
cross-entity lookup. No App, org or saved scope is applied implicitly.

The server sniffs the query: a primary-key shape matches an exact id, a URN
shape matches URNs, otherwise name + URN. --field overrides that sniff; content
is opt-in and skips encrypted memories. --entity-type restricts the kinds.
Both flags accept comma-separated values or repeated uses. Each hit's
matchedField says what matched. The CLI never adds or fetches extra hits.

--org narrows org-anchored entities to one org; users are not org-owned and
remain subject to their normal visibility rules. A missing or non-member org
has the same empty answer. --scope narrows only memories + nodes; a bare scope
name or scope app needs --app. --app is used only when explicitly passed here.
The server returns the applied scope and droppedCount, which --json preserves.

One page, --limit 1..200 (default 20), --offset 0+. Total is BOUNDED by the
server's per-entity candidate cap, not a complete database count. Results keep
the server's order. A next-page hint uses the number actually returned.
Degraded notes report reduced search fidelity (not "no results").

--json is {hits:[{id,entityType,name,urn,description,score,matchedField,memoryId,
organizationId}],total,degraded:[],scope}; nullable fields stay null. A user
without a legal handle and an AI config may have no URN. Credentials are used
when available; the server decides what anonymous callers may see.`,
		Example: `  hadron find 'nightly digest' --entity-type agent,app
  hadron find hrn:mem:acme.com:kb --field urn --json
  hadron find 'routing' --scope work --app acme.com:ops --limit 10`,
		Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(args[0]) == "" {
				return exitcode.Newf(exitcode.Usage, "query is empty")
			}
			if limit < 1 || limit > 200 || offset < 0 {
				return exitcode.Newf(exitcode.Usage, "--limit must be 1..200 and --offset non-negative")
			}
			var entityTypes []gen.SearchEntityType
			if cmd.Flags().Changed("entity-type") {
				if len(types) == 0 {
					return exitcode.Newf(exitcode.Usage, "--entity-type is empty")
				}
				for _, v := range types {
					e := gen.SearchEntityType(strings.TrimSpace(v))
					valid := false
					for _, allowed := range gen.AllSearchEntityType {
						if e == allowed {
							valid = true
							break
						}
					}
					if !valid {
						return exitcode.Newf(exitcode.Usage, "invalid --entity-type %q; see find --help", v)
					}
					entityTypes = append(entityTypes, e)
				}
			}
			var searchFields []gen.SearchField
			if cmd.Flags().Changed("field") {
				if len(fields) == 0 {
					return exitcode.Newf(exitcode.Usage, "--field is empty")
				}
				for _, v := range fields {
					e := gen.SearchField(strings.TrimSpace(v))
					valid := false
					for _, allowed := range gen.AllSearchField {
						if e == allowed {
							valid = true
							break
						}
					}
					if !valid {
						return exitcode.Newf(exitcode.Usage, "invalid --field %q; see find --help", v)
					}
					searchFields = append(searchFields, e)
				}
			}
			var orgPtr, scopePtr, appPtr *string
			if cmd.Flags().Changed("org") {
				if strings.TrimSpace(org) == "" {
					return exitcode.Newf(exitcode.Usage, "--org is empty (an unset variable?); omit it for cross-org discovery")
				}
				orgPtr = &org
			}
			if cmd.Flags().Changed("scope") {
				if strings.TrimSpace(scope) == "" {
					return exitcode.Newf(exitcode.Usage, "--scope is empty (an unset variable?); omit it for the accessible set")
				}
				scopePtr = &scope
			}
			if cmd.Flags().Changed("app") {
				canonical, err := cmdutil.CanonicalAppRef("--app", app)
				if err != nil {
					return err
				}
				appPtr = &canonical
			}
			client, _, err := f.PublicGraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.GlobalSearch(cmd.Context(), client, args[0], entityTypes, searchFields, orgPtr, limit, offset, scopePtr, appPtr)
			if err != nil {
				return api.MapError(err)
			}
			if resp == nil || resp.GlobalSearch == nil || resp.GlobalSearch.Hits == nil || resp.GlobalSearch.Degraded == nil {
				return exitcode.Newf(exitcode.Unavailable, "server returned an incomplete discovery page")
			}
			page := resp.GlobalSearch
			dto := resultDTO{Hits: []hitDTO{}, Total: page.Total, Degraded: append([]string{}, page.Degraded...)}
			for _, h := range page.Hits {
				if h == nil {
					return exitcode.Newf(exitcode.Unavailable, "server returned a null discovery hit")
				}
				dto.Hits = append(dto.Hits, hitDTO{ID: h.Id, EntityType: string(h.EntityType), Name: h.Name, URN: h.Urn, Description: h.Description, Score: h.Score, MatchedField: string(h.MatchedField), MemoryID: h.MemoryId, OrganizationID: h.OrganizationId})
			}
			if page.Scope != nil {
				s := page.Scope.DiscoveryScopeFields
				if s.MemoryUrns == nil {
					return exitcode.Newf(exitcode.Unavailable, "server returned an incomplete discovery scope")
				}
				dto.Scope = &scopeDTO{Kind: s.Kind, Label: s.Label, Source: s.Source, OwnerURN: s.OwnerUrn, MemoryURNs: append([]string{}, s.MemoryUrns...), DroppedCount: s.DroppedCount}
			}
			if err := output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if dto.Scope != nil {
					if _, err := fmt.Fprintf(w, "scope: %s (from --scope; server source %s; dropped %d)\n", output.SafeCell(dto.Scope.Label), output.SafeCell(dto.Scope.Source), dto.Scope.DroppedCount); err != nil {
						return err
					}
				}
				if _, err := fmt.Fprintf(w, "Ranked matches (bounded): %d\n", dto.Total); err != nil {
					return err
				}
				t := output.NewTable(w, "TYPE", "ID", "NAME", "URN", "SCORE", "MATCHED")
				for _, h := range dto.Hits {
					t.Row(output.SafeCell(h.EntityType), output.SafeCell(h.ID), output.SafeCell(output.Dash(h.Name)), output.SafeCell(output.Dash(h.URN)), fmt.Sprint(h.Score), output.SafeCell(h.MatchedField))
				}
				return t.Flush()
			}); err != nil {
				return err
			}
			for _, d := range dto.Degraded {
				if _, err := fmt.Fprintf(f.IOStreams.ErrOut, "degraded: %s\n", output.SafeCell(d)); err != nil {
					return err
				}
			}
			return nextPage(f, "bounded ranked matches", offset, len(dto.Hits), dto.Total, limit)
		}}
	cmd.Flags().StringSliceVar(&types, "entity-type", nil, "organization,memory,node,agent,app,aiServiceConfig,user (comma-separated or repeated)")
	cmd.Flags().StringSliceVar(&fields, "field", nil, "pk,urn,name,description,content; overrides server sniff (comma-separated or repeated)")
	cmd.Flags().StringVar(&org, "org", "", "narrow org-anchored kinds to this org id or ref (does not narrow users)")
	cmd.Flags().StringVar(&scope, "scope", "", "memory/node scope id, name, app or global (never a saved default)")
	cmd.Flags().StringVar(&app, "app", "", "explicit App id or URN for resolving --scope")
	cmd.Flags().IntVar(&limit, "limit", 20, "one page of 1..200 ranked hits")
	cmd.Flags().IntVar(&offset, "offset", 0, "skip this many ranked hits")
	return cmd
}
func nextPage(f *cmdutil.Factory, label string, offset, count, total, limit int) error {
	if limit > 0 && count > 0 && offset+count < total {
		_, err := fmt.Fprintf(f.IOStreams.ErrOut, "showing %d of %d %s; next page: --offset %d --limit %d\n", count, total, label, offset+count, limit)
		return err
	}
	return nil
}
