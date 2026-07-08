package spec

import (
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func newCmdImport(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "import <source>",
		Short: "Import specs from external sources",
		Long: `Import durable product rules into spec nodes from external sources.

These extractors are planned but not yet implemented — the commands exist
so the surface is stable for the follow-up work.`,
	}
	cmd.AddCommand(newCmdImportSpecKit(f))
	cmd.AddCommand(newCmdImportCode(f))
	return cmd
}

func newCmdImportSpecKit(_ *cmdutil.Factory) *cobra.Command {
	var memory string
	cmd := &cobra.Command{
		Use:   "spec-kit <path>",
		Short: "Import specs from a Spec Kit directory (not yet implemented)",
		Long: `Extract durable product rules from a Spec Kit specs/NNN-*/ working
directory into spec nodes, allocating citations and scaffolding the rubric.

Not yet implemented — planned for a follow-up effort.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := memoryURNFromFlag(memory); err != nil {
				return err
			}
			return exitcode.Newf(exitcode.Usage, "spec import spec-kit: not yet implemented (planned follow-up)")
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "target memory ID or fully-qualified URN (defaults to `hadron spec use`, then the active memory)")
	return cmd
}

func newCmdImportCode(_ *cmdutil.Factory) *cobra.Command {
	var memory string
	cmd := &cobra.Command{
		Use:   "code <path>",
		Short: "Derive spec stubs from source-code annotations (not yet implemented)",
		Long: `Scan source code for spec pointers (e.g. // Spec: msg:010:02) and
durable-rule annotations, and scaffold matching spec nodes.

Not yet implemented — planned for a follow-up effort.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := memoryURNFromFlag(memory); err != nil {
				return err
			}
			return exitcode.Newf(exitcode.Usage, "spec import code: not yet implemented (planned follow-up)")
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "target memory ID or fully-qualified URN (defaults to `hadron spec use`, then the active memory)")
	return cmd
}
