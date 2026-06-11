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
	cmd.AddCommand(newCmdRm(f))
	return cmd
}

// resolveMemoryID maps a memory URN to its ID via myMemories.
// Query.memory and updateMemory only accept PK ids today (unlike
// deleteMemory, which dispatches URNs server-side) — remove this once
// the server grows 007-style dispatch on those resolvers.
func resolveMemoryID(cmd *cobra.Command, client graphql.Client, ref string) (string, error) {
	if !strings.Contains(ref, ":") {
		return ref, nil
	}
	includeAgentSystem := true
	resp, err := gen.MyMemories(cmd.Context(), client, &includeAgentSystem)
	if err != nil {
		return "", api.MapError(err)
	}
	for _, m := range resp.MyMemories {
		if m.Urn == ref {
			return m.Id, nil
		}
	}
	return "", exitcode.Newf(exitcode.NotFound, "memory %q not found", ref)
}
