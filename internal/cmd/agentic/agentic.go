// Package agentic implements `hadron agentic-usage` (D8): a single
// document an agent can read to learn the CLI.
package agentic

import (
	_ "embed"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
)

//go:embed agentic-usage.md
var usageDoc string

// Doc returns the embedded agent-facing usage document — the authoritative
// command contract. Exposed so a test can assert every command is documented.
func Doc() string { return usageDoc }

func NewCmdAgenticUsage(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "agentic-usage",
		Short: "Print the agent-facing usage reference",
		Long:  "Print a single document that teaches an AI agent how to drive this CLI: output contract, exit codes, command surface, and recipes.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprint(f.IOStreams.Out, usageDoc)
			return err
		},
	}
}
