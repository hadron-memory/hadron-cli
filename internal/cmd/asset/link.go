package asset

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// assetLinkDTO is the stable --json shape for the reference node created by
// `asset link`.
type assetLinkDTO struct {
	NodeID   string `json:"nodeId"`
	NodeURN  string `json:"nodeUrn"`
	Loc      string `json:"loc"`
	Name     string `json:"name"`
	NodeType string `json:"nodeType"`
	MemoryID string `json:"memoryId"`
	AssetID  string `json:"assetId"`
}

func newCmdLink(f *cmdutil.Factory) *cobra.Command {
	var nodeURN, name, description string
	cmd := &cobra.Command{
		Use:   "link <asset-ref> --node <new-node-urn>",
		Short: "Attach an asset to the graph as a reference node",
		Long: `Create a reference node that points at an asset.

The new node has nodeType "reference" and carries the asset's id, urn,
filename, mimeType and sizeBytes in data.asset — the same shape an agent's
hadron_store_file writes, so a reader handles both origins identically.

--node is the FULLY-QUALIFIED URN OF THE NODE TO CREATE, not of a parent to
append under: hrn:node:<root>:<memory>:<loc>. The loc's colon-separated prefix
places it in the tree, so ":designs:logo-v3" lands under ":designs". Naming an
existing live node is refused by the server with a loc conflict — pick a free
loc rather than expecting an append.

Its memory is where the node lands, which need not be the asset's own: this
needs READ on the asset's holding memory and WRITE on the target memory.

The pointer is a soft reference. There is no schema-level asset-to-node link,
so deleting the asset leaves the reference node in place with its asset
resolving to null — the node records that a file WAS attached, which is often
what you want in an audit trail.`,
		Example: `  hadron asset link 01j2x… --node hrn:node:acme.com:kb:designs:logo-v3
  hadron asset link hrn:asset:acme.com:kb:assets:01j2x… --node hrn:node:acme.com:kb:designs:logo-v3 --name "Logo v3"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := parseAssetRef(args[0])
			if err != nil {
				return err
			}
			if nodeURN == "" {
				return exitcode.Newf(exitcode.Usage,
					"--node <new-node-urn> is required — the fully-qualified URN of the reference node to CREATE (hrn:node:<root>:<memory>:<loc>), not a parent to append under")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			// The asset ref is passed through as given rather than as the bare
			// id: the server accepts either, and forwarding the URN keeps its
			// memory qualification, which matters when the reference lands in
			// a different memory from the asset.
			resp, err := gen.CreateAssetReferenceNode(cmd.Context(), client, args[0], nodeURN,
				optString(name), optString(description))
			if err != nil {
				return api.MapError(err)
			}
			n := resp.CreateAssetReferenceNode
			if n == nil {
				return exitcode.Newf(exitcode.Error, "the server returned no reference node")
			}
			dto := assetLinkDTO{
				NodeID: n.Id, NodeURN: n.Urn, Loc: n.Loc, Name: n.Name,
				NodeType: n.NodeType, MemoryID: n.MemoryId, AssetID: ref.ID,
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				fmt.Fprintf(w, "linked asset %s as %s\n%s\n", dto.AssetID, dto.Name, dto.NodeURN)
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&nodeURN, "node", "", "fully-qualified URN of the reference node to create (required) — not a parent")
	cmd.Flags().StringVar(&name, "name", "", "name for the reference node (default: the asset's filename)")
	cmd.Flags().StringVar(&description, "description", "", "description for the reference node")
	return cmd
}
