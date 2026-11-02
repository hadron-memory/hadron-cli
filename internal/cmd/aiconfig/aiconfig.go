// Package aiconfig implements `hadron ai-config ...` — the AI service
// config management surface: masked reads, CRUD, endpoints and saved probes.
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
	cmd.AddCommand(newCmdGet(f))
	cmd.AddCommand(newCmdEndpoints(f))
	cmd.AddCommand(newCmdTest(f))
	cmd.AddCommand(newCmdCreate(f))
	cmd.AddCommand(newCmdUpdate(f))
	cmd.AddCommand(newCmdRm(f))
	return cmd
}
