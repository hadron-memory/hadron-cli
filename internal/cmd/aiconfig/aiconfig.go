// Package aiconfig implements `hadron ai-config ...` — the AI service
// config surface. Today it exposes the masked resolvable-config picker
// (`ls`); CRUD and decrypted resolve can grow into this group later.
package aiconfig

import (
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
)

func NewCmdAiConfig(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "ai-config <command>",
		Aliases: []string{"ai-configs"},
		Short:   "Work with Hadron AI service configs",
	}
	cmd.AddCommand(newCmdLs(f))
	return cmd
}
