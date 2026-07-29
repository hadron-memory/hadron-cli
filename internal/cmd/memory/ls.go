package memory

import (
	"io"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// The generated item types are deeply nested; alias the two list projections.
type (
	listedMemory = gen.MemoriesMemoriesMemoriesPageItemsMemory
	sharedMemory = gen.MemoriesSharedWithMeMemoriesMemoriesPageItemsMemory
)

func newCmdLs(f *cmdutil.Factory) *cobra.Command {
	var includeAgentSystem, sharedWithMe bool
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List memories you can access",
		Long: `List memories you can access.

By default that is your own union: org-owned, org-subscribed, and your own
personal/private memories.

--shared-with-me SWITCHES the listing to the memories other users have
shared with you (via ` + "`memory share`" + `). That is a separate slice rather
than a subset, so the two listings never overlap — your own memories are
absent from it, and shared ones are absent from the default listing. It
also reports the role you were granted and who shared it.`,
		Example: `  hadron memory ls
  hadron memory ls --shared-with-me
  hadron memory ls --shared-with-me --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			var memories []memoryDTO
			if sharedWithMe {
				memories, err = listSharedWithMe(cmd, client)
			} else {
				memories, err = listOwnUnion(cmd, client, includeAgentSystem)
			}
			if err != nil {
				return err
			}

			return output.Write(f.IOStreams, f.JSON, memories, func(w io.Writer) error {
				if sharedWithMe {
					t := output.NewTable(w, "URN", "NAME", "ROLE", "SHARED BY")
					for _, m := range memories {
						t.Row(m.URN, m.Name, accessDash(m.ShareRole), grantorLabel(m.SharedBy))
					}
					return t.Flush()
				}
				t := output.NewTable(w, "URN", "NAME", "CLASS")
				for _, m := range memories {
					t.Row(m.URN, m.Name, m.Class)
				}
				return t.Flush()
			})
		},
	}
	cmd.Flags().BoolVar(&includeAgentSystem, "include-agent-system", false, "include agent system memories")
	cmd.Flags().BoolVar(&sharedWithMe, "shared-with-me", false, "list memories shared with you instead of your own")
	// A slice selection can't be narrowed by the other slice's knob: shared
	// memories are personal-class by definition, so combining the two would
	// quietly mean nothing. Reject it rather than ignore it.
	cmd.MarkFlagsMutuallyExclusive("shared-with-me", "include-agent-system")
	return cmd
}

// listOwnUnion lists the caller's own union. memories() hides the noisy agent
// system class unless the filter names it explicitly (hadron-server#473) — the
// flag maps to "every class, system included". Paged to exhaustion: the server
// caps a page at 200 and this command's contract is "everything".
func listOwnUnion(cmd *cobra.Command, client graphql.Client, includeAgentSystem bool) ([]memoryDTO, error) {
	var filter *gen.MemoryFilter
	if includeAgentSystem {
		filter = &gen.MemoryFilter{MemoryClasses: gen.AllMemoryClass}
	}
	items, err := api.CollectAll(func(limit, offset int) ([]*listedMemory, int, error) {
		resp, err := gen.Memories(cmd.Context(), client, filter, &limit, &offset)
		if err != nil {
			return nil, 0, api.MapError(err)
		}
		if resp == nil || resp.Memories == nil {
			return nil, 0, nil
		}
		return resp.Memories.Items, resp.Memories.Total, nil
	})
	if err != nil {
		return nil, err
	}
	memories := make([]memoryDTO, 0, len(items))
	for _, m := range items {
		if m == nil {
			continue
		}
		memories = append(memories, dtoFromMemory(m))
	}
	return memories, nil
}

// listSharedWithMe lists the memories shared WITH the caller, carrying each
// row's granted role and grantor from Memory.myShare (#316). Same exhaustive
// paging as the default listing.
func listSharedWithMe(cmd *cobra.Command, client graphql.Client) ([]memoryDTO, error) {
	items, err := api.CollectAll(func(limit, offset int) ([]*sharedMemory, int, error) {
		resp, err := gen.MemoriesSharedWithMe(cmd.Context(), client, &limit, &offset)
		if err != nil {
			return nil, 0, api.MapError(err)
		}
		if resp == nil || resp.Memories == nil {
			return nil, 0, nil
		}
		return resp.Memories.Items, resp.Memories.Total, nil
	})
	if err != nil {
		return nil, err
	}
	memories := make([]memoryDTO, 0, len(items))
	for _, m := range items {
		if m == nil {
			continue
		}
		dto := dtoFromMemory(m)
		// myShare is null for a non-grantee or an App-key caller. It shouldn't
		// be null in this slice, but a missing share must not cost the row.
		if s := m.MyShare; s != nil {
			role := string(s.Role)
			dto.ShareRole = &role
			if s.Grantor != nil {
				u := userFromMemFields(s.Grantor.MemUserFields)
				dto.SharedBy = &u
			}
		}
		memories = append(memories, dto)
	}
	return memories, nil
}

// grantorLabel names the sharer for the human table. accessLabel is the shared
// email → handle → name → id fallback the member/share tables use, so it can't
// come up empty; a nil grantor still gets the accessDash em dash rather than a
// blank cell.
func grantorLabel(u *accessUserDTO) string {
	if u == nil {
		return accessDash(nil)
	}
	return accessLabel(*u)
}
