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

// cli#2510 (gap #58): the six remaining agent-lifecycle commands. Each verb is
// exactly one mutation with refs as typed; authority stays server-owned, and a
// refusal maps through api.MapError with no retry or follow-up call. The one
// exception is `session start`, whose server looks its agent up by ID only:
// a URN there is resolved with one read first.

// nonBlank refuses a blank argument before any call. TrimSpace only DETECTS
// blank input; the value is passed on exactly as typed.
func nonBlank(value, what string) error {
	if strings.TrimSpace(value) == "" {
		return exitcode.Newf(exitcode.Usage, "the %s is empty", what)
	}
	return nil
}

// --- agent import publish|rm ----------------------------------------------

type agentImportDTO struct {
	ParentAgentID   string `json:"parentAgentId"`
	ImportedAgentID string `json:"importedAgentId"`
	Position        int    `json:"position"`
	Required        bool   `json:"required"`
	CreatedAt       string `json:"createdAt"`
}

type agentImportRemovedDTO struct {
	ParentAgentRef   string `json:"parentAgentRef"`
	ImportedAgentRef string `json:"importedAgentRef"`
	Removed          bool   `json:"removed"`
}

func newCmdAgentImport(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "import",
		Short: "Bundle one Agent into another as a dependency",
		Long: `Manage an Agent's imports: dependency edges from a parent Agent to an imported
Agent, so installing the parent also installs the imported Agent. An import is
one level deep: an Agent that itself imports others cannot be imported. The
parent's organization needs a license grant for the imported Agent.

An import is not an Agent's base (see "hadron agent set-base"), which the Agent
extends instead of bundling.`,
	}
	cmd.AddCommand(newCmdAgentImportPublish(f), newCmdAgentImportRm(f))
	return cmd
}

func importArgs(args []string) (parent, imported string, err error) {
	if err := nonBlank(args[0], "parent agent reference"); err != nil {
		return "", "", err
	}
	if err := nonBlank(args[1], "imported agent reference"); err != nil {
		return "", "", err
	}
	return args[0], args[1], nil
}

func newCmdAgentImportPublish(f *cmdutil.Factory) *cobra.Command {
	var position int
	var required bool
	cmd := &cobra.Command{
		Use:   "publish <parent> <imported> --position <n> [--required=true|false]",
		Short: "Make one Agent import another",
		Long: `Publish an import edge from <parent> to <imported> (each an Agent ID or URN,
sent as typed). --position orders the parent's imports and is required.
--required says whether the import is required; without it the server
default (required) applies.

You need ADMIN or OWNER on the parent Agent's organization. The server refuses
the import when <imported> itself imports other Agents (imports are one level
deep), when the parent's organization holds no license grant for <imported>,
when <parent> and <imported> are the same Agent, or when the position is taken.

Prints "✓ <imported> imported into <parent> at position <n>"; --json is the
import edge: {"parentAgentId", "importedAgentId", "position", "required",
"createdAt"}.`,
		Example: `  hadron agent import publish hrn:agent:acme.com:suite hrn:agent:acme.com:triage --position 1
  hadron agent import publish hrn:agent:acme.com:suite hrn:agent:acme.com:notes --position 2 --required=false`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			parent, imported, err := importArgs(args)
			if err != nil {
				return err
			}
			if !cmd.Flags().Changed("position") {
				return exitcode.Newf(exitcode.Usage, "--position is required")
			}
			var requiredPtr *bool
			if cmd.Flags().Changed("required") {
				requiredPtr = &required
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.PublishAgentImport(cmd.Context(), client, parent, imported, position, requiredPtr)
			if err != nil {
				return api.MapError(err)
			}
			r := resp.PublishAgentImport
			if r == nil {
				return exitcode.Newf(exitcode.Error, "server returned no import")
			}
			dto := agentImportDTO{
				ParentAgentID:   r.ParentAgentId,
				ImportedAgentID: r.ImportedAgentId,
				Position:        r.Position,
				Required:        r.Required,
				CreatedAt:       r.CreatedAt,
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				kind := "required"
				if !dto.Required {
					kind = "optional"
				}
				_, err := fmt.Fprintf(w, "✓ %s imported into %s at position %d (%s)\n",
					output.SafeCell(imported), output.SafeCell(parent), dto.Position, kind)
				return err
			})
		},
	}
	cmd.Flags().IntVar(&position, "position", 0, "the import's position among the parent's imports (required)")
	cmd.Flags().BoolVar(&required, "required", true, "whether the import is required (omitted: the server default, required)")
	return cmd
}

func newCmdAgentImportRm(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "rm <parent> <imported>",
		Aliases: []string{"remove"},
		Short:   "Remove an import from an Agent",
		Long: `Remove the import edge from <parent> to <imported> (each an Agent ID or URN,
sent as typed). Removing an import that does not exist succeeds and changes
nothing. Apps that already installed <imported> keep it; the removal only stops
later installs of <parent> from bringing <imported> along.

Asks for confirmation on a terminal; a non-interactive caller must pass --yes.
Prints "✓ removed import of <imported> from <parent>"; --json is
{"parentAgentRef", "importedAgentRef", "removed": true}.`,
		Example: `  hadron agent import rm hrn:agent:acme.com:suite hrn:agent:acme.com:triage --yes`,
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			parent, imported, err := importArgs(args)
			if err != nil {
				return err
			}
			if err := cmdutil.ConfirmDeletion(f.IOStreams, yes, "the import of "+imported+" from agent "+parent); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.RemoveAgentImport(cmd.Context(), client, parent, imported)
			if err != nil {
				return api.MapError(err)
			}
			if !resp.RemoveAgentImport {
				return exitcode.Newf(exitcode.Error, "the import of %s from %s was not removed", imported, parent)
			}
			dto := agentImportRemovedDTO{ParentAgentRef: parent, ImportedAgentRef: imported, Removed: true}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ removed import of %s from %s\n", output.SafeCell(imported), output.SafeCell(parent))
				return err
			})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}

// --- agent ai-config clear --------------------------------------------------

func newCmdAgentAIConfig(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ai-config",
		Short: "Clear an Agent's AI provider configuration",
		Long: `Work with the AI provider configuration stored on an Agent. This is not the
top-level "hadron ai-config" group, which manages an organization's AI service
configs.`,
	}
	cmd.AddCommand(newCmdAgentAIConfigClear(f))
	return cmd
}

func newCmdAgentAIConfigClear(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "clear <agent>",
		Short: "Remove an Agent's AI provider configuration",
		Long: `Remove the AI provider configuration stored on an Agent (an ID or URN, sent as
typed). No key is returned or decrypted.

For a user-owned Agent, the clear removes every named configuration, disabled
ones included, and means the same whether its owner or a platform admin runs
it. For an organization-owned Agent, the server removes only the configuration
named "default". A platform admin can clear an Agent's AI configuration but
can never read or set one. Otherwise you need ADMIN access to the Agent.

Asks for confirmation on a terminal; a non-interactive caller must pass --yes.
Prints "✓ cleared the AI config of agent <name> (<urn>)"; --json is the Agent
as the server returned it.`,
		Example: `  hadron agent ai-config clear hrn:agent:acme.com:support-bot --yes`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := nonBlank(args[0], "agent reference"); err != nil {
				return err
			}
			if err := cmdutil.ConfirmDeletion(f.IOStreams, yes, "the AI configuration of agent "+args[0]); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.ClearAgentAIConfig(cmd.Context(), client, args[0])
			if err != nil {
				return api.MapError(err)
			}
			if resp.ClearAgentAIConfig == nil {
				return exitcode.Newf(exitcode.Error, "server returned no agent")
			}
			return emitAgent(f, agentDTOFromFields(resp.ClearAgentAIConfig.AgentFields), "✓ cleared the AI config of")
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}

// --- agent repair-system-memory ---------------------------------------------

func newCmdAgentRepairSystemMemory(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "repair-system-memory <agent>",
		Short: "Re-own an Agent's system memory to the Agent's owner (platform admin)",
		Long: `Give an Agent's system memory back to the Agent's own owner (its organization or
user) when it has ended up owned by another tenant. <agent> is an ID or URN,
sent as typed. Platform admin only: the server refuses everyone else, including
the Agent's own organization admins, and an impersonating session.

On a repair the server tries to give the memory its derived URN; if another
memory holds that URN, the current one is kept. Running it on an Agent whose
system memory is already owned correctly changes nothing.

Prints "✓ repaired the system memory of agent <name> (<urn>)"; --json is the
Agent as the server returned it.`,
		Example: `  hadron agent repair-system-memory hrn:agent:acme.com:support-bot`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := nonBlank(args[0], "agent reference"); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.RepairAgentSystemMemory(cmd.Context(), client, args[0])
			if err != nil {
				return api.MapError(err)
			}
			if resp.RepairAgentSystemMemory == nil {
				return exitcode.Newf(exitcode.Error, "server returned no agent")
			}
			return emitAgent(f, agentDTOFromFields(resp.RepairAgentSystemMemory.AgentFields), "✓ repaired the system memory of")
		},
	}
}

// --- agent session start|end ------------------------------------------------

type agentSessionDTO struct {
	ID        string  `json:"id"`
	AgentID   *string `json:"agentId"`
	AppID     *string `json:"appId"`
	MemoryID  *string `json:"memoryId"`
	UserID    *string `json:"userId"`
	Type      string  `json:"type"`
	State     string  `json:"state"`
	IsLive    bool    `json:"isLive"`
	StartedAt string  `json:"startedAt"`
	ExpiresAt *string `json:"expiresAt"`
}

type agentSessionEndedDTO struct {
	SessionID string `json:"sessionId"`
	Ended     bool   `json:"ended"`
}

func newCmdAgentSession(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Start or end an App's session with an Agent",
		Long: `Start and end the sessions an App opens with an Agent installed in it, for the
App's end users. These are not worker sessions: to bind or end a team worker's
session, use "hadron team session".

Both verbs need an App key as the credential (for example in HADRON_TOKEN),
and act only on that key's own App, its Agents and its sessions; --app does
not choose a different App. With a personal sign-in instead of an App key the
server refuses (exit 8). Apps that identify their users by a secret key are
not supported here.`,
	}
	cmd.AddCommand(newCmdAgentSessionStart(f), newCmdAgentSessionEnd(f))
	return cmd
}

func newCmdAgentSessionStart(f *cmdutil.Factory) *cobra.Command {
	var externalUser string
	var timeoutSeconds int
	cmd := &cobra.Command{
		Use:   "start <agent> [--external-user <id>] [--timeout-seconds <n>]",
		Short: "Open an App session with one of the App's Agents",
		Long: `Open a session between the calling App and <agent>, an Agent installed in that
App. Needs an App key. The server looks the Agent up by ID, so when <agent> is
a URN the CLI first reads the Agent to find its ID, then sends that.

--external-user names the App's end user by the App's own user id. If the App
has no such user, the server creates one only when the App allows implicit user
creation, and refuses otherwise. Without it the session is anonymous.
--timeout-seconds sets the session's timeout; it must be positive. The server
refuses an Agent owned by a user rather than an organization.

Prints "✓ started session <id> for agent <agent-id> (<state>)"; --json is the
session: {"id", "agentId", "appId", "memoryId", "userId", "type", "state",
"isLive", "startedAt", "expiresAt"}.`,
		Example: `  HADRON_TOKEN=$APP_KEY hadron agent session start 01a0091679447ebb87ffec2301cd12c1 --external-user u-42
  HADRON_TOKEN=$APP_KEY hadron agent session start hrn:agent:acme.com:support-bot --timeout-seconds 900 --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := args[0]
			if err := nonBlank(ref, "agent reference"); err != nil {
				return err
			}
			var userPtr *string
			if cmd.Flags().Changed("external-user") {
				userPtr = &externalUser
			}
			var timeoutPtr *int
			if cmd.Flags().Changed("timeout-seconds") {
				timeoutPtr = &timeoutSeconds
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			agentID := ref
			if strings.Contains(ref, ":") {
				r, err := gen.AgentSessionAgentID(cmd.Context(), client, ref)
				if err != nil {
					return api.MapError(err)
				}
				if r.Agent == nil {
					return exitcode.Newf(exitcode.NotFound, "agent %q not found, or this credential cannot read it; pass the agent ID instead", ref)
				}
				agentID = r.Agent.Id
			}
			resp, err := gen.StartAgentSession(cmd.Context(), client, agentID, userPtr, timeoutPtr)
			if err != nil {
				return api.MapError(err)
			}
			s := resp.StartAgentSession
			if s == nil {
				return exitcode.Newf(exitcode.Error, "server returned no session")
			}
			dto := agentSessionDTO{
				ID: s.Id, AgentID: s.AgentId, AppID: s.AppId, MemoryID: s.MemoryId, UserID: s.UserId,
				Type: s.Type, State: string(s.State), IsLive: s.IsLive, StartedAt: s.StartedAt, ExpiresAt: s.ExpiresAt,
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ started session %s for agent %s (%s)\n",
					output.SafeCell(dto.ID), output.SafeCell(agentID), output.SafeCell(dto.State))
				return err
			})
		},
	}
	cmd.Flags().StringVar(&externalUser, "external-user", "", "the App's own id for its end user (omitted: an anonymous session)")
	cmd.Flags().IntVar(&timeoutSeconds, "timeout-seconds", 0, "the session timeout in seconds, positive (omitted: the server default)")
	return cmd
}

func newCmdAgentSessionEnd(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "end <session-id>",
		Short: "End an App session with an Agent",
		Long: `End a session the calling App opened with "hadron agent session start". Needs
the same App key. Ending a session that has already ended succeeds and changes
nothing; a session of another App answers as not found.

Prints "✓ ended session <id>"; --json is {"sessionId", "ended": true}.`,
		Example: `  HADRON_TOKEN=$APP_KEY hadron agent session end 01a1f5fb56f2727a9052df532c43bee4`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := nonBlank(args[0], "session id"); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.EndAgentSession(cmd.Context(), client, args[0])
			if err != nil {
				return api.MapError(err)
			}
			if !resp.EndAgentSession {
				return exitcode.Newf(exitcode.Error, "session %s was not ended", args[0])
			}
			dto := agentSessionEndedDTO{SessionID: args[0], Ended: true}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ ended session %s\n", output.SafeCell(args[0]))
				return err
			})
		},
	}
}
