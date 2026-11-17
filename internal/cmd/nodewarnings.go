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
