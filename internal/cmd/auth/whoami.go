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
			resp, err := gen.Me(cmd.Context(), client)
			if err != nil {
				return api.MapError(err)
			}
			if resp.Me == nil {
				return exitcode.Newf(exitcode.AuthRequired, "token was not accepted — run `hadron auth login`")
			}

			dto := whoamiResult{ID: resp.Me.Id, Roles: []string{}}
			if resp.Me.Name != nil {
				dto.Name = *resp.Me.Name
			}
			if resp.Me.Email != nil {
				dto.Email = *resp.Me.Email
			}
			if resp.Me.Handle != nil {
				dto.Handle = *resp.Me.Handle
			}
			if resp.Me.GithubUsername != nil {
				dto.GithubUsername = *resp.Me.GithubUsername
			}
			for _, r := range resp.Me.Roles {
				dto.Roles = append(dto.Roles, string(r))
			}

			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
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
