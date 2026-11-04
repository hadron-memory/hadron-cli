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
		Short: "Print the instructions an AI assistant needs to use this CLI",
		Long: `Print one long Markdown document that teaches an AI assistant (an agent) how
to run hadron for you: what each command prints with --json, what each exit
code means, every command and its flags, and worked recipes.

It is built into the CLI, so it needs no network or sign-in and always matches
the version you have. It is long (several thousand lines), so save it to a
file rather than reading it on screen:

  hadron agentic-usage > hadron-cli.md

then give that file to your assistant, or add it to its instructions. An
assistant that can run commands itself can simply run 'hadron agentic-usage'.
The output is always Markdown; --json has no effect.`,
		Example: `  hadron agentic-usage > hadron-cli.md
  hadron agentic-usage | grep -n "Exit codes"`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprint(f.IOStreams.Out, usageDoc)
			return err
		},
	}
}
