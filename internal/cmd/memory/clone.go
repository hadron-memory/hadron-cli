package memory

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdClone(f *cmdutil.Factory) *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:     "clone <memory-id-or-urn> --name <new-name>",
		Aliases: []string{"cp"},
		Short:   "Clone a memory into a new memory in the same org",
		Long: `Clone a memory into a new memory in the same org.

Copies the memory plus all live nodes and edges; references to the
source memory's URN inside node content are rewritten to the clone's
URN. Version history, subscriptions, shares, assets, and git-sync
config are not copied — the clone starts DB-only.

Encrypted memories and agent system / app memories cannot be cloned.`,
		Example: `  hadron memory clone acme.com:project-kb --name project-kb-fork`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.CloneMemory(cmd.Context(), client, args[0], name)
			if err != nil {
				return api.MapError(err)
			}
			cloned := resp.CloneMemory
			m := memoryDTO{
				ID: cloned.Id, URN: cloned.Urn, Name: cloned.Name,
				ShortDescription: cloned.ShortDescription, Class: string(cloned.Class),
				OrganizationID: cloned.OrganizationId, IsEncrypted: cloned.IsEncrypted,
				UpdatedAt: cloned.UpdatedAt,
			}
			if cloned.Visibility != nil {
				v := string(*cloned.Visibility)
				m.Visibility = &v
			}
			return output.Write(f.IOStreams, f.JSON, m, func(w io.Writer) error {
				t := output.NewTable(w)
				t.Row("✓ cloned", args[0], "→", m.URN)
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "name for the cloned memory (required)")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}
