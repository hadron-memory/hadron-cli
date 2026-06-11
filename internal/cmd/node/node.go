// Package node implements `hadron node ...` (all stubs in the v1
// skeleton; implementations land with the node command session).
package node

import (
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
)

func NewCmdNode(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "node <command>",
		Aliases: []string{"nodes"},
		Short:   "Work with nodes in a memory",
	}
	cmd.AddCommand(cmdutil.NewStubCommand("ls", "List nodes in a memory"))
	cmd.AddCommand(cmdutil.NewStubCommand("get <urn>", "Show a node"))
	cmd.AddCommand(cmdutil.NewStubCommand("add", "Create a node"))
	cmd.AddCommand(cmdutil.NewStubCommand("update <urn>", "Update a node"))
	cmd.AddCommand(cmdutil.NewStubCommand("rm <urn>", "Delete a node"))
	return cmd
}
