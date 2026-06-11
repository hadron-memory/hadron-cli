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
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List memories you can access",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.MyMemories(cmd.Context(), client, &includeAgentSystem)
			if err != nil {
				return api.MapError(err)
			}

			memories := make([]memoryDTO, 0, len(resp.MyMemories))
			for _, m := range resp.MyMemories {
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
