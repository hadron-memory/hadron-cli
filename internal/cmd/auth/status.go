package auth

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	authpkg "github.com/hadron-memory/hadron-cli/internal/auth"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

type statusResult struct {
	Server        string `json:"server"`
	Authenticated bool   `json:"authenticated"`
	TokenSource   string `json:"tokenSource,omitempty"`
	TokenStorage  string `json:"tokenStorage,omitempty"`
	User          string `json:"user,omitempty"`
}

func newCmdStatus(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show authentication state for the current server",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			server, err := f.Server()
			if err != nil {
				return err
			}
			token, source, err := f.Token()
			if err != nil {
				return err
			}

			dto := statusResult{Server: server}
			if source == authpkg.SourceNone {
				err := output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
					_, err := fmt.Fprintf(w, "%s: not signed in — run `hadron auth login`\n", server)
					return err
				})
				if err != nil {
					return err
				}
				return exitcode.Silent(exitcode.AuthRequired)
			}

			dto.TokenSource = string(source)
			if source == authpkg.SourceStore {
				dto.TokenStorage = f.TokenStore().Name()
			}

			client, err := api.NewClient(server, token, f.HTTPClient)
			if err != nil {
				return err
			}
			resp, err := gen.Me(cmd.Context(), client)
			if err == nil && resp.Me != nil {
				dto.Authenticated = true
				if resp.Me.Name != nil {
					dto.User = *resp.Me.Name
				} else if resp.Me.Email != nil {
					dto.User = *resp.Me.Email
				} else {
					dto.User = resp.Me.Id
				}
			}

			writeErr := output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if dto.Authenticated {
					_, err := fmt.Fprintf(w, "✓ %s: signed in as %s (token from %s)\n", server, dto.User, describeSource(dto))
					return err
				}
				_, err := fmt.Fprintf(w, "✗ %s: token from %s was rejected — run `hadron auth login`\n", server, describeSource(dto))
				return err
			})
			if writeErr != nil {
				return writeErr
			}
			if !dto.Authenticated {
				return exitcode.Silent(exitcode.AuthRequired)
			}
			return nil
		},
	}
}

func describeSource(dto statusResult) string {
	if dto.TokenSource == string(authpkg.SourceEnv) {
		return "HADRON_TOKEN"
	}
	return dto.TokenStorage
}
