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
	var includeAgentSystem, sharedWithMe, ownedByMe bool
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List memories you can access",
		Long: `List memories you can access.

By default: the memories of your organizations, the ones your organizations
subscribe to, and your own personal and private memories.

--owned-by-me narrows that to the memories that belong to you and to no
organization, such as the ones made with memory set --owner-me. A
knowledge-class memory under your own handle is included; a memory of an
organization never is, even a personal one. Your active organization (org use)
does not change it. Only a person gets an answer: with an App key, or while
acting as another user, the list is empty.

--shared-with-me lists instead the memories other users have
shared with you (with memory share), with the role you were given and who
shared it. It does not overlap the default listing: your own memories are not
in it, and shared ones are not in the default listing.`,
		Example: `  hadron memory ls
  hadron memory ls --owned-by-me
  hadron memory ls --owned-by-me --json
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
				memories, err = listOwnUnion(cmd, client, includeAgentSystem, ownedByMe)
			}
			if err != nil {
				return err
			}

			return output.Write(f.IOStreams, f.JSON, memories, func(w io.Writer) error {
				if sharedWithMe {
					t := output.NewTable(w, "URN", "NAME", "ROLE", "SHARED BY")
					for _, m := range memories {
						t.Row(output.SafeCell(m.URN), output.SafeCell(m.Name), output.SafeCell(accessDash(m.ShareRole)), output.SafeCell(grantorLabel(m.SharedBy)))
					}
					return t.Flush()
				}
				t := output.NewTable(w, "URN", "NAME", "CLASS")
				for _, m := range memories {
					t.Row(output.SafeCell(m.URN), output.SafeCell(m.Name), output.SafeCell(m.Class))
				}
				return t.Flush()
			})
		},
	}
	cmd.Flags().BoolVar(&includeAgentSystem, "include-agent-system", false, "include agent system memories")
	cmd.Flags().BoolVar(&sharedWithMe, "shared-with-me", false, "list memories shared with you instead of your own")
	cmd.Flags().BoolVar(&ownedByMe, "owned-by-me", false, "list only the org-less memories you own")
	// A slice selection can't be narrowed by the other slice's knob: shared
	// memories are personal-class by definition, so combining the two would
	// quietly mean nothing. Reject it rather than ignore it.
	cmd.MarkFlagsMutuallyExclusive("shared-with-me", "include-agent-system")
	// Same shape, and here the server says so itself: a grantee is never their
	// own grantor, so the shared slice excludes owned memories and the
	// intersection is empty BY CONSTRUCTION, not by what happens to be stored.
	// An empty page is the one answer a caller can't tell from a real result,
	// so refuse instead — the same call the server makes in rejecting
	// ownedByMe on PublicAgentFilter rather than returning nothing.
	cmd.MarkFlagsMutuallyExclusive("shared-with-me", "owned-by-me")
	return cmd
}

// listOwnUnion lists the caller's own union. memories() hides the noisy agent
// system class unless the filter names it explicitly (hadron-server#473) — the
// flag maps to "every class, system included". Paged to exhaustion: the server
// caps a page at 200 and this command's contract is "everything".
//
// ownedByMe forwards hadron-server#1215's predicate untouched. It is NOT
// reproducible client-side and must not be approximated: "mine" is keyed on
// two different owner columns depending on class, and the class pair that
// reads like an ownership test is only a proxy — which was the original
// defect (#1176). A list whose name is a relationship and whose query is a
// category is correct only while the two coincide.
func listOwnUnion(cmd *cobra.Command, client graphql.Client, includeAgentSystem, ownedByMe bool) ([]memoryDTO, error) {
	var filter *gen.MemoryFilter
	if includeAgentSystem || ownedByMe {
		filter = &gen.MemoryFilter{}
		if includeAgentSystem {
			filter.MemoryClasses = gen.AllMemoryClass
		}
		// Only ever set true: the clauses compose by AND, so a literal false
		// would be a no-op the server still has to read, and omitting it keeps
		// the unfiltered request byte-identical to what it has always been.
		if ownedByMe {
			filter.OwnedByMe = &ownedByMe
		}
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
		resp, err := gen.MemoriesSharedWithMe(cmd.Context(), client, &limit, &offset, nil)
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
