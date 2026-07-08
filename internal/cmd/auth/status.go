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
	Server        string    `json:"server"`
	Authenticated bool      `json:"authenticated"`
	TokenSource   string    `json:"tokenSource,omitempty"`
	TokenStorage  string    `json:"tokenStorage,omitempty"`
	User          string    `json:"user,omitempty"`
	PrincipalType string    `json:"principalType,omitempty"`
	Key           *tokenDTO `json:"key,omitempty"`
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
			resp, err := gen.AuthContext(cmd.Context(), client)
			if err == nil && resp.AuthContext != nil {
				ac := resp.AuthContext
				dto.Authenticated = true
				dto.PrincipalType = string(ac.PrincipalType)
				switch {
				case ac.User != nil && ac.User.Name != nil:
					dto.User = *ac.User.Name
				case ac.User != nil && ac.User.Email != nil:
					dto.User = *ac.User.Email
				case ac.User != nil:
					dto.User = ac.User.Id
				case ac.AppId != nil:
					dto.User = "App " + *ac.AppId
				}
				if ac.ApiKey != nil {
					k := toTokenDTO(ac.ApiKey.UserApiKeyFields)
					dto.Key = &k
				}
			}

			writeErr := output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if !dto.Authenticated {
					_, err := fmt.Fprintf(w, "✗ %s: token from %s was rejected — run `hadron auth login`\n", server, describeSource(dto))
					return err
				}
				fmt.Fprintf(w, "✓ %s: signed in as %s (token from %s)\n", server, dto.User, describeSource(dto))
				if dto.Key != nil {
					_, err := fmt.Fprintf(w, "  key %s, last used %s\n", keyLabel(*dto.Key), orText(dto.Key.LastUsedAt, "never"))
					return err
				}
				return nil
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
