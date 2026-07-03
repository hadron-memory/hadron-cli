package memory

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdLs(f *cmdutil.Factory) *cobra.Command {
	var includeAgentSystem bool
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List memories you can access",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			// memories() hides the noisy agent system class unless the filter
			// names it explicitly (hadron-server#473) — the flag maps to
			// "every class, system included". Paged to exhaustion: the server
			// caps a page at 200 and this command's contract is "everything".
			var filter *gen.MemoryFilter
			if includeAgentSystem {
				filter = &gen.MemoryFilter{MemoryClasses: gen.AllMemoryClass}
			}
			items, err := api.CollectAll(func(limit, offset int) ([]*gen.MemoriesMemoriesMemoriesPageItemsMemory, int, error) {
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
				return err
			}

			memories := make([]memoryDTO, 0, len(items))
			for _, m := range items {
				if m == nil {
					continue
				}
				dto := memoryDTO{
					ID:               m.Id,
					URN:              m.Urn,
					Name:             m.Name,
					ShortDescription: m.ShortDescription,
					Class:            string(m.Class),
					OrganizationID:   m.OrganizationId,
					IsEncrypted:      m.IsEncrypted,
					UpdatedAt:        m.UpdatedAt,
				}
				if m.Visibility != nil {
					v := string(*m.Visibility)
					dto.Visibility = &v
				}
				memories = append(memories, dto)
			}

			return output.Write(f.IOStreams, f.JSON, memories, func(w io.Writer) error {
				t := output.NewTable(w, "URN", "NAME", "CLASS")
				for _, m := range memories {
					t.Row(m.URN, m.Name, m.Class)
				}
				return t.Flush()
			})
		},
	}
	cmd.Flags().BoolVar(&includeAgentSystem, "include-agent-system", false, "include agent system memories")
	return cmd
}
