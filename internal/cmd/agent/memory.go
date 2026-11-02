package agent

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// newCmdAgentMemory is `agent memory add|set-role|rm` (cli#58 slice 1). Each
// verb is exactly one mutation; refs are passed unchanged, and authority,
// role validation and locking stay server-owned.
func newCmdAgentMemory(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "memory",
		Short: "Attach, re-role or detach a Memory on an Agent",
		Long: `Manage which Memories an Agent has. Each verb makes one server call and
passes the agent and memory references exactly as typed (ID, URN or handle);
the server resolves them, decides who may change an Agent's Memories, and
validates the role (for example read or read-write).`,
	}
	cmd.AddCommand(newCmdAgentMemoryAdd(f), newCmdAgentMemorySetRole(f), newCmdAgentMemoryRm(f))
	return cmd
}

// memoryArgs trims and refuses empty refs before any call is made.
func memoryArgs(args []string) (agentRef, memoryRef string, err error) {
	agentRef, memoryRef = strings.TrimSpace(args[0]), strings.TrimSpace(args[1])
	if agentRef == "" {
		return "", "", exitcode.Newf(exitcode.Usage, "the agent reference is empty")
	}
	if memoryRef == "" {
		return "", "", exitcode.Newf(exitcode.Usage, "the memory reference is empty")
	}
	return agentRef, memoryRef, nil
}

func emitAgentMemory(f *cmdutil.Factory, a gen.AgentFields, verb, memoryRef string) error {
	dto := agentDTOFromFields(a)
	return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		_, err := fmt.Fprintf(w, "%s %s on agent %s (%s)\n", verb,
			output.SafeCell(memoryRef), output.SafeCell(dto.Name), output.SafeCell(dto.URN))
		return err
	})
}

func newCmdAgentMemoryAdd(f *cmdutil.Factory) *cobra.Command {
	var role string
	cmd := &cobra.Command{
		Use:     "add <agent> <memory> [--role <role>]",
		Short:   "Attach a Memory to an Agent",
		Example: `  hadron agent memory add hrn:agent:acme.com:bot hrn:mem:acme.com:notes --role read`,
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			agentRef, memoryRef, err := memoryArgs(args)
			if err != nil {
				return err
			}
			var rolePtr *string
			if cmd.Flags().Changed("role") {
				rolePtr = &role
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.AddMemoryToAgent(cmd.Context(), client, agentRef, memoryRef, rolePtr)
			if err != nil {
				return api.MapError(err)
			}
			return emitAgentMemory(f, resp.AddMemoryToAgent.AgentFields, "✓ attached", memoryRef)
		},
	}
	cmd.Flags().StringVar(&role, "role", "", "the Memory's role on the Agent (omitted: the server default; the server validates)")
	return cmd
}

func newCmdAgentMemorySetRole(f *cmdutil.Factory) *cobra.Command {
	var role string
	cmd := &cobra.Command{
		Use:     "set-role <agent> <memory> --role <role>",
		Short:   "Change an attached Memory's role on an Agent",
		Example: `  hadron agent memory set-role hrn:agent:acme.com:bot hrn:mem:acme.com:notes --role read-write`,
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			agentRef, memoryRef, err := memoryArgs(args)
			if err != nil {
				return err
			}
			if !cmd.Flags().Changed("role") {
				return exitcode.Newf(exitcode.Usage, "--role is required")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.UpdateAgentMemoryRole(cmd.Context(), client, agentRef, memoryRef, role)
			if err != nil {
				return api.MapError(err)
			}
			return emitAgentMemory(f, resp.UpdateAgentMemoryRole.AgentFields, "✓ set role of", memoryRef)
		},
	}
	cmd.Flags().StringVar(&role, "role", "", "the new role (required; the server validates)")
	return cmd
}

func newCmdAgentMemoryRm(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "rm <agent> <memory>",
		Aliases: []string{"remove"},
		Short:   "Detach a Memory from an Agent",
		Example: `  hadron agent memory rm hrn:agent:acme.com:bot hrn:mem:acme.com:notes --yes`,
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			agentRef, memoryRef, err := memoryArgs(args)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			if err := cmdutil.ConfirmDeletion(f.IOStreams, yes, "memory "+memoryRef+" from agent "+agentRef); err != nil {
				return err
			}
			resp, err := gen.RemoveMemoryFromAgent(cmd.Context(), client, agentRef, memoryRef)
			if err != nil {
				return api.MapError(err)
			}
			return emitAgentMemory(f, resp.RemoveMemoryFromAgent.AgentFields, "✓ detached", memoryRef)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}
