// Package memory implements `hadron memory ...`.
package memory

import (
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
)

// memoryDTO is the stable --json shape for a memory. Field changes
// here are contract changes (see docs/agentic-usage.md).
type memoryDTO struct {
	ID               string  `json:"id"`
	URN              string  `json:"urn"`
	Name             string  `json:"name"`
	ShortDescription *string `json:"shortDescription"`
	Class            string  `json:"class"`
	Visibility       *string `json:"visibility"`
	OrganizationID   string  `json:"organizationId"`
	IsEncrypted      bool    `json:"isEncrypted"`
	UpdatedAt        string  `json:"updatedAt"`
}

func NewCmdMemory(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "memory <command>",
		Aliases: []string{"memories"},
		Short:   "Work with Hadron memories",
	}
	cmd.AddCommand(newCmdLs(f))
	cmd.AddCommand(newCmdGet(f))
	cmd.AddCommand(cmdutil.NewStubCommand("set", "Create or update a memory"))
	cmd.AddCommand(cmdutil.NewStubCommand("rm <memory>", "Delete a memory"))
	return cmd
}
