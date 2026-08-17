package memory

import (
	"errors"
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdClone(f *cmdutil.Factory) *cobra.Command {
	var targetURN string
	cmd := &cobra.Command{
		Use:     "clone <memoryRef> --target-urn <new-urn>",
		Aliases: []string{"cp"},
		Short:   "Clone a memory into a new memory (optionally in another org)",
		Long: `Clone a memory into a new memory at a target URN.

--target-urn names the clone as a fully-qualified memory URN,
hrn:mem:<root>:<slug> (the short <root>:<slug> and legacy
<root>::<slug> spellings are accepted too). Its root segment may
differ from the source's, cloning the memory into another
organization (you must be a non-reader member of that target org).
The clone's display name is derived from the slug.

Copies the memory plus all live nodes, edges, and pending edges;
references to the source memory's URN inside node content and
abstracts are rewritten to the clone's URN. Version history,
subscriptions, shares, assets, and git-sync config are not copied —
the clone starts DB-only.

Encrypted memories and agent system / app memories cannot be cloned.`,
		Example: `  hadron memory clone acme.com:project-kb --target-urn hrn:mem:acme.com:project-kb-fork
  hadron memory clone acme.com:project-kb --target-urn hrn:mem:other-org:project-kb`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// The gate used to be `strings.Contains(targetURN, "::")`, which
			// rejected the canonical hrn:mem:<root>:<slug> the server itself
			// documents for targetUrn — so a caller held to fully-qualified refs
			// (hadron-portal#728) could not use this command at all (#372).
			// MemoryParts accepts every spelling and still rejects a relative
			// value, which is all the gate was ever for.
			root, slug, ok := cmdutil.MemoryParts(targetURN)
			if !ok {
				return exitcode.Newf(exitcode.Usage,
					"--target-urn must be a fully-qualified memory URN, hrn:mem:<root>:<slug>")
			}
			// Sent in the legacy "::" shape the server has always been handed
			// here. Widening the INPUT is what #372 needs; changing the wire
			// value is a separate, verifiable step (see docs/plans/urn-v2-help-text.md).
			targetURN = root + "::" + slug
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
				MaxRevCount: cloned.MaxRevCount, UpdatedAt: cloned.UpdatedAt,
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
	cmd.Flags().StringVar(&targetURN, "target-urn", "", "fully-qualified memory URN for the clone, hrn:mem:<root>:<slug> (required)")
	_ = cmd.MarkFlagRequired("target-urn")
	return cmd
}
