// Package memory implements `hadron memory ...`.
package memory

import (
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
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
	cmd.AddCommand(newCmdSet(f))
	cmd.AddCommand(newCmdSetActive(f))
	cmd.AddCommand(newCmdRm(f))
	cmd.AddCommand(newCmdClone(f))
	cmd.AddCommand(newCmdExport(f))
	cmd.AddCommand(newCmdMember(f))
	cmd.AddCommand(newCmdShare(f))
	return cmd
}

// resolveMemoryID maps a memory URN to its ID via memory(ref:), which
// dispatches PKs and URNs server-side (hadron-server#473). The mutations
// this feeds (updateMemory, member/share writes) still accept PK ids only.
func resolveMemoryID(cmd *cobra.Command, client graphql.Client, ref string) (string, error) {
	if !strings.Contains(ref, ":") {
		return ref, nil
	}
	resp, err := gen.GetMemory(cmd.Context(), client, ref)
	if err != nil {
		return "", api.MapError(err)
	}
	if resp.Memory == nil {
		return "", exitcode.Newf(exitcode.NotFound, "memory %q not found", ref)
	}
	return resp.Memory.Id, nil
}
