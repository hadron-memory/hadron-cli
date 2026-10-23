package agent

import (
	"fmt"
	"io"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

func newCmdClone(f *cmdutil.Factory) *cobra.Command {
	var target, name, slug string
	cmd := &cobra.Command{
		Use:   "clone <source> --to <org> [--name N] [--slug S]",
		Short: "Clone an Agent into an organization you administer",
		Long: `Clone a PUBLIC Agent, or one you manage in its own organization, into an
organization you administer. The server creates a new PERSONAL-draft Agent
with its own copy of the system memory, and copies its links and imports.
Linked memories stay links, not copied knowledge. The target must be an
organization. Optional name and slug overrides are decided by the server.

Prints the new Agent URN returned by the server; --json returns the normal
Agent fields.

One clone mutation only: no install, manual-copy fallback or automatic retry.
A source-changed conflict may be marked retryable by the server. If the
connection is lost, the outcome may be unknown: check before cloning again.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(args[0]) == "" || strings.TrimSpace(target) == "" {
				return exitcode.Newf(exitcode.Usage, "give a nonempty source and --to organization")
			}
			var nameArg, slugArg *string
			if cmd.Flags().Changed("name") {
				nameArg = &name
			}
			if cmd.Flags().Changed("slug") {
				slugArg = &slug
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.CloneAgent(cmd.Context(), client, args[0], target, nameArg, slugArg)
			if err != nil {
				return api.MapCloneAgentError(err)
			}
			dto := agentDTOFromFields(resp.CloneAgent.AgentFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error { _, err := fmt.Fprintln(w, dto.URN); return err })
		},
	}
	cmd.Flags().StringVar(&target, "to", "", "destination organization id or URN (required)")
	cmd.Flags().StringVar(&name, "name", "", "name for the clone (server default when omitted)")
	cmd.Flags().StringVar(&slug, "slug", "", "slug for the clone (server default when omitted)")
	return cmd
}
