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

// cli#2510 (gap #58): `agent session start|end`, the two agent-lifecycle
// commands not delivered by cli#2729 (which built ai-config clear, import
// publish|rm and repair-system-memory in lifecycle.go). Each verb is exactly
// one mutation with refs as typed; authority stays server-owned, and a refusal
// maps through api.MapError with no retry or follow-up call. The one exception
// is `session start`, whose server looks its agent up by ID only: a URN there
// is resolved with one read first.

// nonBlank refuses a blank argument before any call. TrimSpace only DETECTS
// blank input; the value is passed on exactly as typed.
func nonBlank(value, what string) error {
	if strings.TrimSpace(value) == "" {
		return exitcode.Newf(exitcode.Usage, "the %s is empty", what)
	}
	return nil
}

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
creation, and refuses otherwise. Without it the session is anonymous. An
empty or blank --external-user is a usage error, not an anonymous session.
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
			// cli#2895 F1/F2: flag values the help already rules out are
			// refused locally, before the URN read or any mutation, so a bad
			// invocation sends zero requests.
			if cmd.Flags().Changed("timeout-seconds") && timeoutSeconds <= 0 {
				return exitcode.Newf(exitcode.Usage, "--timeout-seconds must be positive, got %d", timeoutSeconds)
			}
			if cmd.Flags().Changed("external-user") && strings.TrimSpace(externalUser) == "" {
				return exitcode.Newf(exitcode.Usage, "--external-user is empty or blank")
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
					return exitcode.Newf(exitcode.NotFound, "agent not found, or this credential cannot read it (the ref given is not shown); pass the agent ID instead")
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
	cmd.Flags().StringVar(&externalUser, "external-user", "", "the App's own id for its end user, non-blank (omitted: an anonymous session)")
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
				return exitcode.Newf(exitcode.Error, "the session was not ended (the session id given is not shown)")
			}
			dto := agentSessionEndedDTO{SessionID: args[0], Ended: true}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ ended session %s\n", output.SafeCell(args[0]))
				return err
			})
		},
	}
}
