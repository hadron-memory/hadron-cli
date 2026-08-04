package auth

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

type whoamiResult struct {
	ID             string             `json:"id"`
	Name           string             `json:"name,omitempty"`
	Email          string             `json:"email,omitempty"`
	Handle         string             `json:"handle,omitempty"`
	GithubUsername string             `json:"githubUsername,omitempty"`
	Roles          []string           `json:"roles"`
	PrincipalType  string             `json:"principalType,omitempty"`
	AppID          string             `json:"appId,omitempty"`
	AgentID        string             `json:"agentId,omitempty"`
	Impersonation  *impersonationInfo `json:"impersonation,omitempty"`
}

// impersonationInfo mirrors AuthContext.impersonation for whoami/status — the
// acting-as facts when the request authenticated with an impersonation token.
type impersonationInfo struct {
	SessionID      string `json:"sessionId"`
	OrganizationID string `json:"organizationId"`
	ExpiresAt      string `json:"expiresAt"`
	ReadOnly       bool   `json:"readOnly"`
	ActorHandle    string `json:"actorHandle,omitempty"`
	ActorName      string `json:"actorName,omitempty"`
}

func newCmdWhoami(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show the signed-in user",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.AuthContext(cmd.Context(), client)
			if err != nil {
				return api.MapError(err)
			}
			if resp.AuthContext == nil {
				return exitcode.Newf(exitcode.AuthRequired, "token was not accepted — run `hadron auth login`")
			}
			ac := resp.AuthContext

			dto := whoamiResult{Roles: []string{}, PrincipalType: string(ac.PrincipalType)}
			if ac.User != nil {
				dto.ID = ac.User.Id
				if ac.User.Name != nil {
					dto.Name = *ac.User.Name
				}
				if ac.User.Email != nil {
					dto.Email = *ac.User.Email
				}
				if ac.User.Handle != nil {
					dto.Handle = *ac.User.Handle
				}
				if ac.User.GithubUsername != nil {
					dto.GithubUsername = *ac.User.GithubUsername
				}
				for _, r := range ac.User.Roles {
					dto.Roles = append(dto.Roles, string(r))
				}
			}
			if ac.AppId != nil {
				dto.AppID = *ac.AppId
			}
			if ac.AgentId != nil {
				dto.AgentID = *ac.AgentId
			}
			if ac.Impersonation != nil {
				imp := ac.Impersonation
				info := &impersonationInfo{
					SessionID:      imp.SessionId,
					OrganizationID: imp.OrganizationId,
					ExpiresAt:      imp.ExpiresAt,
					ReadOnly:       imp.ReadOnly,
				}
				if imp.ActorUser != nil {
					if imp.ActorUser.Handle != nil {
						info.ActorHandle = *imp.ActorUser.Handle
					}
					if imp.ActorUser.Name != nil {
						info.ActorName = *imp.ActorUser.Name
					}
				}
				dto.Impersonation = info
			}

			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				// An App/Agent key resolves to no user — name the principal
				// instead of erroring.
				if ac.User == nil {
					_, err := fmt.Fprintln(w, nonUserLabel(dto.AppID, dto.AgentID, dto.PrincipalType))
					return err
				}
				label := dto.Name
				if label == "" {
					label = dto.Handle
				}
				if label == "" {
					label = dto.ID
				}
				line := label
				if dto.Email != "" {
					line = fmt.Sprintf("%s (%s)", label, dto.Email)
				}
				// Admin impersonation: make the acting-as state loud so a
				// scripted or forgetful operator can't mistake it for their own.
				if dto.Impersonation != nil {
					actor := dto.Impersonation.ActorName
					if actor == "" {
						actor = dto.Impersonation.ActorHandle
					}
					if actor == "" {
						actor = "another admin"
					}
					_, err := fmt.Fprintf(w,
						"%s — IMPERSONATED (read-only), org %s, as %s, expires %s\n",
						line, dto.Impersonation.OrganizationID, actor, dto.Impersonation.ExpiresAt)
					return err
				}
				_, err := fmt.Fprintln(w, line)
				return err
			})
		},
	}
}
