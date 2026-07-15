package memory

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// memoryDetailDTO extends the list shape with detail-only fields.
type memoryDetailDTO struct {
	memoryDTO
	Description        *string  `json:"description"`
	Tags               []string `json:"tags"`
	Source             *string  `json:"source"`
	SyncStatus         string   `json:"syncStatus"`
	VectorIndexEnabled bool     `json:"vectorIndexEnabled"`
	CreatedAt          string   `json:"createdAt"`
}

func newCmdGet(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "get <memory-id-or-urn>",
		Short: "Show a memory",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			// memory(ref:) dispatches PKs and URNs server-side
			// (hadron-server#473); normalize a bare org::slug / org:slug into the
			// canonical URN first so the short forms resolve consistently (#108).
			resp, err := gen.GetMemory(cmd.Context(), client, cmdutil.CanonicalMemoryRef(args[0]))
			if err != nil {
				return api.MapError(err)
			}
			if resp == nil || resp.Memory == nil {
				return notFoundMemory(args[0])
			}

			m := resp.Memory
			dto := memoryDetailDTO{
				memoryDTO: memoryDTO{
					ID:               m.Id,
					URN:              m.Urn,
					Name:             m.Name,
					ShortDescription: m.ShortDescription,
					Class:            string(m.Class),
					OrganizationID:   m.OrganizationId,
					IsEncrypted:      m.IsEncrypted,
					MaxRevCount:      m.MaxRevCount,
					UpdatedAt:        m.UpdatedAt,
				},
				Description:        m.Description,
				Tags:               m.Tags,
				Source:             m.Source,
				SyncStatus:         string(m.SyncStatus),
				VectorIndexEnabled: m.VectorIndexEnabled,
				CreatedAt:          m.CreatedAt,
			}
			if m.Visibility != nil {
				v := string(*m.Visibility)
				dto.Visibility = &v
			}

			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				fmt.Fprintf(w, "%s\n  urn: %s\n  class: %s\n  org: %s\n", dto.Name, dto.URN, dto.Class, output.Dash(dto.OrganizationID))
				if dto.ShortDescription != nil && *dto.ShortDescription != "" {
					fmt.Fprintf(w, "  about: %s\n", *dto.ShortDescription)
				}
				if len(dto.Tags) > 0 {
					fmt.Fprintf(w, "  tags: %v\n", dto.Tags)
				}
				fmt.Fprintf(w, "  max revisions: %d\n", dto.MaxRevCount)
				fmt.Fprintf(w, "  updated: %s\n", dto.UpdatedAt)
				return nil
			})
		},
	}
}
