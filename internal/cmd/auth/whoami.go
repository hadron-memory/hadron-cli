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
	ID             string   `json:"id"`
	Name           string   `json:"name,omitempty"`
	Email          string   `json:"email,omitempty"`
	Handle         string   `json:"handle,omitempty"`
	GithubUsername string   `json:"githubUsername,omitempty"`
	Roles          []string `json:"roles"`
	PrincipalType  string   `json:"principalType,omitempty"`
	AppID          string   `json:"appId,omitempty"`
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

			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				// An App key resolves to no user — name the App instead of erroring.
				if ac.User == nil {
					if dto.AppID != "" {
						_, err := fmt.Fprintf(w, "App %s\n", dto.AppID)
						return err
					}
					_, err := fmt.Fprintln(w, dto.PrincipalType)
					return err
				}
				label := dto.Name
				if label == "" {
					label = dto.Handle
				}
				if label == "" {
					label = dto.ID
				}
				if dto.Email != "" {
					_, err := fmt.Fprintf(w, "%s (%s)\n", label, dto.Email)
					return err
				}
				_, err := fmt.Fprintln(w, label)
				return err
			})
		},
	}
}
