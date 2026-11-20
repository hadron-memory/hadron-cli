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

// cli#58 (four-command cut): `agent ai-config clear`, `agent import publish|rm`
// and `agent repair-system-memory`. Each is exactly one server call with the
// references passed as typed (ID, URN or handle); who may run it, and every
// other rule, stays the server's (cor:api:240:01).

// refArg refuses a blank reference before any call is made. TrimSpace only
// DETECTS blank input: the reference is returned exactly as typed.
func refArg(what, v string) (string, error) {
	if strings.TrimSpace(v) == "" {
		return "", exitcode.Newf(exitcode.Usage, "the %s reference is empty", what)
	}
	return v, nil
}

// emitAgentReceipt renders the Agent a verb returned. A null result (the
// mutation is declared non-null, but a gateway or server fault can still send
// one) is an error, never a dereference.
func emitAgentReceipt(f *cmdutil.Factory, a *gen.AgentFields, line string) error {
	if a == nil {
		return exitcode.Newf(exitcode.Error, "server returned no agent")
	}
	dto := agentDTOFromFields(*a)
	return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		_, err := fmt.Fprintf(w, "%s %s (%s)\n", line, output.SafeCell(dto.Name), output.SafeCell(dto.URN))
		return err
	})
}

// newCmdAgentAIConfig is the `agent ai-config` group. It is NOT the top-level
// `ai-config` group, which edits AI service configuration rows by id.
func newCmdAgentAIConfig(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ai-config",
		Short: "Manage an Agent's own AI provider configuration",
		Long: `Manage the AI provider configuration stored on one Agent. This is not the
top-level "hadron ai-config" group, which edits AI service configuration rows by id.`,
	}
	cmd.AddCommand(newCmdAgentAIConfigClear(f))
	return cmd
}

func newCmdAgentAIConfigClear(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "clear <agent>",
		Short: "Clear an Agent's AI provider configuration",
		Long: `Clear the AI provider configuration stored on an Agent, in one server call. It
cannot be undone. For a user-owned Agent the server removes every named
configuration, disabled ones included; for an org-owned Agent it removes only the
one named "default". The server decides what is removed.

Whoever manages the Agent may run it: for an org-owned Agent, its organization's
ADMINs and OWNERs; for a user-owned Agent, its owner; and platform admins. The
server decides, and a refusal is reported as it came.

It never reads or prints a key. Prints "✓ cleared AI configuration of <name>
(<urn>)"; --json is the Agent as the server returned it. Without a terminal you
must pass --yes.`,
		Example: `  hadron agent ai-config clear hrn:agent:acme.com:bot --yes`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			agentRef, err := refArg("agent", args[0])
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			if err := cmdutil.ConfirmDeletion(f.IOStreams, yes, "the AI configuration of agent "+output.SafeCell(agentRef)); err != nil {
				return err
			}
			resp, err := gen.ClearAgentAIConfig(cmd.Context(), client, agentRef)
			if err != nil {
				return api.MapError(err)
			}
			var a *gen.AgentFields
			if r := resp.ClearAgentAIConfig; r != nil {
				a = &r.AgentFields
			}
			return emitAgentReceipt(f, a, "✓ cleared AI configuration of")
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}

// newCmdAgentImport is `agent import publish|rm`.
func newCmdAgentImport(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "import",
		Short: "Publish or remove an Agent's dependency on another Agent",
		Long: `Manage imports: a parent Agent's dependency on another Agent, which installing the
parent into an App carries along. Each verb makes one server call and passes both
Agents exactly as typed (ID, URN or handle).`,
	}
	cmd.AddCommand(newCmdAgentImportPublish(f), newCmdAgentImportRm(f))
	return cmd
}

// importDTO is the --json shape of a published import.
type importDTO struct {
	ParentAgentID   string `json:"parentAgentId"`
	ImportedAgentID string `json:"importedAgentId"`
	Position        int    `json:"position"`
	Required        bool   `json:"required"`
	CreatedAt       string `json:"createdAt"`
}

func newCmdAgentImportPublish(f *cmdutil.Factory) *cobra.Command {
	var position int
	var required bool
	cmd := &cobra.Command{
		Use:   "publish <parent> <imported> --position <n> [--required[=false]]",
		Short: "Make a parent Agent depend on an imported Agent",
		Long: `Publish an import: <parent> depends on <imported>, at --position (an integer;
required). Installing the parent into an App then carries the imported Agent
along. The parent's organization must hold an active grant for the imported
Agent, and imports go one level deep (an imported Agent cannot itself be a
parent). Whoever manages the parent Agent may publish: its organization's ADMINs
and OWNERs, and platform admins; the server decides and reports its refusals as
they came.

--required says whether the dependency is mandatory; without the flag the server
default (required) stands, and --required=false sends false. Prints "✓ imported
<imported> into <parent> at position <n>"; --json is the import as the server
returned it (parentAgentId, importedAgentId, position, required, createdAt).`,
		Example: `  hadron agent import publish hrn:agent:acme.com:suite hrn:agent:acme.com:helper --position 1
  hadron agent import publish hrn:agent:acme.com:suite hrn:agent:acme.com:helper --position 2 --required=false`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			parent, err := refArg("parent agent", args[0])
			if err != nil {
				return err
			}
			imported, err := refArg("imported agent", args[1])
			if err != nil {
				return err
			}
			if !cmd.Flags().Changed("position") {
				return exitcode.Newf(exitcode.Usage, "--position is required")
			}
			var reqPtr *bool
			if cmd.Flags().Changed("required") {
				reqPtr = &required
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.PublishAgentImport(cmd.Context(), client, parent, imported, position, reqPtr)
			if err != nil {
				return api.MapError(err)
			}
			r := resp.PublishAgentImport
			if r == nil {
				return exitcode.Newf(exitcode.Error, "server returned no import")
			}
			dto := importDTO{
				ParentAgentID: r.ParentAgentId, ImportedAgentID: r.ImportedAgentId,
				Position: r.Position, Required: r.Required, CreatedAt: r.CreatedAt,
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ imported %s into %s at position %d\n",
					output.SafeCell(imported), output.SafeCell(parent), dto.Position)
				return err
			})
		},
	}
	cmd.Flags().IntVar(&position, "position", 0, "the import's position among the parent's imports (required)")
	cmd.Flags().BoolVar(&required, "required", true, "whether the dependency is mandatory (omitted: the server default, required; --required=false sends false)")
	return cmd
}

// removeImportDTO is the --json shape of `agent import rm`: the server's own
// Boolean, not a local assumption.
type removeImportDTO struct {
	Removed bool `json:"removed"`
}

func newCmdAgentImportRm(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "rm <parent> <imported>",
		Aliases: []string{"remove"},
		Short:   "Remove a parent Agent's dependency on an imported Agent",
		Long: `Remove the import of <imported> from <parent>, in one server call. It is
idempotent: removing an import that does not exist is not an error.

Removing it stops future installs of the parent from carrying the imported Agent
along. Installs that already happened stay. But if the imported Agent was
installed in an App only through this import, removing the import also ends its
grants there, and reinstalling does not revive them. Whoever manages the parent
Agent may remove: its organization's ADMINs and OWNERs, and platform admins; the
server decides. Without a terminal you must pass --yes.

Prints "✓ removed import ..." when the server reports it removed one; --json is
{"removed": <the server's boolean>}.`,
		Example: `  hadron agent import rm hrn:agent:acme.com:suite hrn:agent:acme.com:helper --yes`,
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			parent, err := refArg("parent agent", args[0])
			if err != nil {
				return err
			}
			imported, err := refArg("imported agent", args[1])
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			what := "the import of " + output.SafeCell(imported) + " from agent " + output.SafeCell(parent) +
				" (this can also end the imported agent's grants in any App where it was installed only through this import, and reinstalling does not revive them)"
			if err := cmdutil.ConfirmDeletion(f.IOStreams, yes, what); err != nil {
				return err
			}
			resp, err := gen.RemoveAgentImport(cmd.Context(), client, parent, imported)
			if err != nil {
				return api.MapError(err)
			}
			dto := removeImportDTO{Removed: resp.RemoveAgentImport}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if !dto.Removed {
					_, err := fmt.Fprintf(w, "the server reported no import of %s from %s removed\n",
						output.SafeCell(imported), output.SafeCell(parent))
					return err
				}
				_, err := fmt.Fprintf(w, "✓ removed import of %s from %s\n", output.SafeCell(imported), output.SafeCell(parent))
				return err
			})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}

func newCmdAgentRepairSystemMemory(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "repair-system-memory <agent>",
		Short: "Repair an Agent's system-Memory owner pairing (platform admins)",
		Long: `Repair an Agent whose system Memory ended up owned by a different tenant than the
Agent: the server re-pairs the owner and keeps the Memory's URN consistent. It is
idempotent: an already-consistent pair is a no-op. Only platform admins may run
it; the server refuses anyone else (FORBIDDEN) and reports AGENT_HAS_NO_SYSTEM_MEMORY
or SYSTEM_MEMORY_NOT_FOUND as they came.

Prints "✓ repaired system memory of <name> (<urn>)"; --json is the Agent as the
server returned it. It asks no confirmation because it changes nothing that is
already consistent.`,
		Example: `  hadron agent repair-system-memory hrn:agent:acme.com:bot`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			agentRef, err := refArg("agent", args[0])
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.RepairAgentSystemMemory(cmd.Context(), client, agentRef)
			if err != nil {
				return api.MapError(err)
			}
			var a *gen.AgentFields
			if r := resp.RepairAgentSystemMemory; r != nil {
				a = &r.AgentFields
			}
			return emitAgentReceipt(f, a, "✓ repaired system memory of")
		},
	}
	return cmd
}
