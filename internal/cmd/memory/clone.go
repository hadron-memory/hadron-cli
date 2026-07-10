package memory

import (
	"errors"
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdClone(f *cmdutil.Factory) *cobra.Command {
	var targetURN string
	cmd := &cobra.Command{
		Use:     "clone <memory-id-or-urn> --target-urn <new-urn>",
		Aliases: []string{"cp"},
		Short:   "Clone a memory into a new memory (optionally in another org)",
		Long: `Clone a memory into a new memory at a target URN.

--target-urn is a fully-qualified "org::slug" memory URN naming the
clone. Its org segment may differ from the source's, cloning the
memory into another organization (you must be a non-reader member of
that target org). The clone's display name is derived from the slug.

Copies the memory plus all live nodes, edges, and pending edges;
references to the source memory's URN inside node content and
abstracts are rewritten to the clone's URN. Version history,
subscriptions, shares, assets, and git-sync config are not copied —
the clone starts DB-only.

Encrypted memories and agent system / app memories cannot be cloned.`,
		Example: `  hadron memory clone acme.com:project-kb --target-urn acme.com::project-kb-fork
  hadron memory clone acme.com:project-kb --target-urn other-org::project-kb`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.CloneMemory(cmd.Context(), client, args[0], targetURN)
			if err != nil {
				return api.MapError(err)
			}
			cloned := resp.CloneMemory
			if cloned == nil {
				return errors.New("server returned an empty cloneMemory response")
			}
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
	cmd.Flags().StringVar(&targetURN, "target-urn", "", "fully-qualified \"org::slug\" URN for the clone (required)")
	_ = cmd.MarkFlagRequired("target-urn")
	return cmd
}
