package app

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

// keyScopeInfoDTO is one scope of the server's closed list (cor:aut:010:03).
// The CLI keeps no copy of that list: adding a scope is a server change.
type keyScopeInfoDTO struct {
	Scope          string `json:"scope"`
	Description    string `json:"description"`
	RequiresHostID bool   `json:"requiresHostId"`
}

func newCmdKeyScopeCatalogue(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "catalogue",
		Short: "List the scopes an App key can be granted",
		Long: `List every scope an App key can be granted, as the server defines them: the
scope name that "app key scope grant" and "revoke" take, one line on what
holding it permits, and whether granting it needs --host-id. Any signed-in
caller may read it; it is the same for everyone and names no App, key or host.

This is what can be granted, not what you may do: changing a key's scopes stays
with whoever manages the App. Text prints a table in the server's order; --json
is an array of {"scope", "description", "requiresHostId"}, [] when the server
lists none.`,
		Example: `  hadron app key scope catalogue
  hadron app key scope catalogue --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.AppKeyScopeCatalogue(cmd.Context(), client)
			if err != nil {
				return api.MapError(err)
			}
			// A list that is null (an answer with no data, or a null field) is a
			// malformed reply, not "the server lists no scopes": say so, exit 1.
			if resp == nil || resp.AppKeyScopeCatalogue == nil {
				return exitcode.Newf(exitcode.Error, "server returned no scope catalogue")
			}
			dto := make([]keyScopeInfoDTO, 0, len(resp.AppKeyScopeCatalogue))
			for _, s := range resp.AppKeyScopeCatalogue {
				// The SDL says [AppKeyScopeInfo!]!, so a null element is not
				// possible from a conforming server; refuse it rather than panic.
				if s == nil {
					return exitcode.Newf(exitcode.Error, "server returned a null scope in the catalogue")
				}
				dto = append(dto, keyScopeInfoDTO{Scope: s.Scope, Description: s.Description, RequiresHostID: s.RequiresHostId})
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if len(dto) == 0 {
					_, e := fmt.Fprintln(w, "no scopes")
					return e
				}
				table := output.NewTable(w, "SCOPE", "NEEDS HOST", "WHAT IT PERMITS")
				for _, s := range dto {
					needs := "no"
					if s.RequiresHostID {
						needs = "yes"
					}
					table.Row(output.SafeCell(s.Scope), needs, output.SafeCell(s.Description))
				}
				return table.Flush()
			})
		},
	}
}
