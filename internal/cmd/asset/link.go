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
		Short: "Attach a file to the graph as a new node",
		Long: `Attach an uploaded file to the graph: create a new node that points at the
asset, so people and agents reading that part of the memory find the file.

--node is the URN of the NEW node to create (hrn:node:<org>:<memory>:<loc>),
not of an existing note to attach to. The loc places it in the tree:
...:kb:designs:report-v1 creates report-v1 under designs. If a node already
exists there, the command is refused (exit 5); pick a free loc.

The new node has type "reference" and keeps the asset's id, URN, filename, type
and size in its data (hadron node get shows them). Files that agents store
through Hadron's MCP tools (hadron_store_file) get the same kind of node.

The node can be in a different memory from the asset: you need read access to
the asset's memory and write access to the node's.

Deleting the asset later leaves the node in place, with the file's details, but
the file can no longer be downloaded. Restore the asset within 24 hours (asset
restore), or remove the node with hadron node rm.`,
		Example: `  hadron asset link 01a189a840f4717c98af75cf4d7aed09 --node hrn:node:acme.com:kb:designs:report-v1
  hadron asset link hrn:asset:acme.com:kb:assets:01a189a840f4717c98af75cf4d7aed09 --node hrn:node:acme.com:kb:designs:report-v1 --name "Q3 report"`,
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
				fmt.Fprintf(w, "linked asset %s as %s\n%s\n", output.SafeCell(dto.AssetID), output.SafeCell(dto.Name), output.SafeCell(dto.NodeURN))
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&nodeURN, "node", "", "fully-qualified URN of the reference node to create (required) — not a parent")
	cmd.Flags().StringVar(&name, "name", "", "name for the reference node (default: the asset's filename)")
	cmd.Flags().StringVar(&description, "description", "", "description for the reference node")
	return cmd
}
