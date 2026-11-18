package cmd

import (
	"fmt"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

// Install at the assembled command tree so generic/kind doors, import (including
// tree imports), replacements and revision restore share the same receipt path.
// Only advice for a node-writing mutation actually selected in that request is
// collected. No read/probe is added and team task warnings keep their own sink.
// Both output modes leave stdout/JSON untouched; advice goes to stderr only.
//
// The set is the mutation roots that carry plain-pin response advice, not every
// mutation whose name contains "Node" (#1152; a new node writer must be classified
// in nodewarnings_inventory_1152_test.go, where each root is pinned by name):
//   - createAssetReferenceNode is left out on purpose. It writes through the same
//     node write, but the reference node's content is authored by the server (file
//     name, type, size and asset URN) and carries no include in normal use, so no
//     plain-pin advice is expected. A file name that itself contains include syntax
//     could in principle produce advice, which this set does not display: that is
//     not a guarantee, and whether to add the name is a separate decision (#2470).
//     It is not added for symmetry.
//   - updateNodeRevision is left out on purpose: it edits a revision's label only
//     and writes no content.
//
// A later failed write suppresses the advice already collected for an earlier
// committed one: the failed-command contract. Changing that is its own decision.
func installNodeWriteWarnings(cmd *cobra.Command, f *cmdutil.Factory) {
	if run := cmd.RunE; run != nil {
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			original := cmd.Context()
			ctx, sink := api.WithWarningSinkForFields(original,
				"createNode", "createSpecNode", "createTaskNode", "createReviewNode",
				"updateNode", "updateSpecNode", "updateTaskNode", "updateReviewNode",
				"importNode", "mergeNodes", "cloneNode", "moveNode", "updateNodeData", "restoreNode", "restoreNodeRevision",
				"searchReplaceInNodes", "searchReplaceInSpecNodes")
			cmd.SetContext(ctx)
			defer cmd.SetContext(original)
			if err := run(cmd, args); err != nil {
				return err
			}
			for _, warning := range sink.All() {
				// An advisory must never turn a committed write into a failure/retry,
				// including when the diagnostic stream itself cannot be written.
				_, _ = fmt.Fprintf(f.IOStreams.ErrOut, "warning: [%s] %s\n", output.SafeCell(warning.Code), output.SafeCell(warning.Text))
			}
			return nil
		}
	}
	for _, child := range cmd.Commands() {
		installNodeWriteWarnings(child, f)
	}
}
