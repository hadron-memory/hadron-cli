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

// tokenDTO is the stable --json shape for a personal API token. Never key
// material — only keyPreview; the raw token is shown once, by `token create`.
type tokenDTO struct {
	ID         string  `json:"id"`
	Label      *string `json:"label"`
	KeyPreview string  `json:"keyPreview"`
	IssuedVia  *string `json:"issuedVia"`
	CreatedAt  string  `json:"createdAt"`
	LastUsedAt *string `json:"lastUsedAt"`
	RevokedAt  *string `json:"revokedAt"`
	Revoked    bool    `json:"revoked"`
}

// tokenCreateDTO extends tokenDTO with the raw key, returned exactly once.
type tokenCreateDTO struct {
	tokenDTO
	RawKey string `json:"rawKey"`
}

func toTokenDTO(k gen.UserApiKeyFields) tokenDTO {
	return tokenDTO{
		ID:         k.Id,
		Label:      k.Label,
		KeyPreview: k.KeyPreview,
		IssuedVia:  k.IssuedVia,
		CreatedAt:  k.CreatedAt,
		LastUsedAt: k.LastUsedAt,
		RevokedAt:  k.RevokedAt,
		Revoked:    k.RevokedAt != nil,
	}
}

func newCmdToken(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token <command>",
		Short: "Mint and manage personal API tokens",
		Long: `Mint, list, and revoke personal access tokens for headless and CI use.

A token is minted for the signed-in user, so you sign in once interactively
(hadron auth login) and then mint long-lived tokens to use via the
HADRON_TOKEN env var or "hadron auth login --with-token". Token operations
require a user login — an app or agent key cannot manage user tokens.`,
	}
	cmd.AddCommand(newCmdTokenCreate(f))
	cmd.AddCommand(newCmdTokenLs(f))
	cmd.AddCommand(newCmdTokenValidate(f))
	cmd.AddCommand(newCmdTokenRevoke(f))
	return cmd
}

// validateResult is the stable --json shape for `token validate`. `valid` is
// the load-bearing field; the identity fields are populated only when valid.
// principalType/appId/key come from the server's authContext, so validate is
// credential-type-agnostic (a valid App key is valid, not a false negative) and
// can name the exact user key presented.
type validateResult struct {
	Valid         bool      `json:"valid"`
	PrincipalType string    `json:"principalType,omitempty"`
	UserID        string    `json:"userId,omitempty"`
	Name          string    `json:"name,omitempty"`
	Email         string    `json:"email,omitempty"`
	Handle        string    `json:"handle,omitempty"`
	Roles         []string  `json:"roles"`
	AppID         string    `json:"appId,omitempty"`
	Key           *tokenDTO `json:"key,omitempty"`
}

func newCmdTokenValidate(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Check whether a token is valid (reads it from stdin)",
		Long: `Check whether a personal access token authenticates, without storing it.

The token is read from standard input, so it never lands in your shell history
or the process table. A valid token prints the user it belongs to and exits 0;
a rejected or revoked token exits 3.`,
		Example: `  echo $TOKEN | hadron auth token validate
  hadron auth token validate --json < token.txt`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			token, err := readToken(f.IOStreams.In, "token validate")
			if err != nil {
				return err
			}
			server, err := f.Server()
			if err != nil {
				return err
			}
			client, err := api.NewClient(server, token, f.HTTPClient)
			if err != nil {
				return err
			}

			dto := validateResult{Roles: []string{}}
			resp, err := gen.AuthContext(cmd.Context(), client)
			switch {
			case err != nil:
				// A rejected token is a definitive "invalid" answer, not a CLI
				// failure; anything else (transport, server error) we can't judge.
				mapped := api.MapError(err)
				if exitcode.FromError(mapped) != exitcode.AuthRequired {
					return mapped
				}
			case resp.AuthContext != nil:
				ac := resp.AuthContext
				dto.Valid = true
				dto.PrincipalType = string(ac.PrincipalType)
				if ac.User != nil {
					dto.UserID = ac.User.Id
					if ac.User.Name != nil {
						dto.Name = *ac.User.Name
					}
					if ac.User.Email != nil {
						dto.Email = *ac.User.Email
					}
					if ac.User.Handle != nil {
						dto.Handle = *ac.User.Handle
					}
					for _, r := range ac.User.Roles {
						dto.Roles = append(dto.Roles, string(r))
					}
				}
				if ac.AppId != nil {
					dto.AppID = *ac.AppId
				}
				if ac.ApiKey != nil {
					k := toTokenDTO(ac.ApiKey.UserApiKeyFields)
					dto.Key = &k
				}
			}

			writeErr := output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if !dto.Valid {
					_, err := fmt.Fprintln(w, "✗ Token is invalid, revoked, or expired")
					return err
				}
				if dto.AppID != "" {
					_, err := fmt.Fprintf(w, "✓ Token is valid — App %s\n", dto.AppID)
					return err
				}
				label := dto.Name
				if label == "" {
					label = dto.Handle
				}
				if label == "" {
					label = dto.UserID
				}
				if dto.Email != "" {
					fmt.Fprintf(w, "✓ Token is valid — %s (%s)\n", label, dto.Email)
				} else {
					fmt.Fprintf(w, "✓ Token is valid — %s\n", label)
				}
				if dto.Key != nil {
					_, err := fmt.Fprintf(w, "  key %s, last used %s\n", keyLabel(*dto.Key), orText(dto.Key.LastUsedAt, "never"))
					return err
				}
				return nil
			})
			if writeErr != nil {
				return writeErr
			}
			if !dto.Valid {
				return exitcode.Silent(exitcode.AuthRequired)
			}
			return nil
		},
	}
	return cmd
}

func newCmdTokenCreate(f *cmdutil.Factory) *cobra.Command {
	var label string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Mint a new personal API token (shown once)",
		Long: `Mint a new personal access token for the signed-in user.

The raw token is printed ONCE and is never recoverable — store it now (e.g. in
a CI secret). Only the token's hash is kept server-side.`,
		Example: `  hadron auth token create --label ci-deploy
  TOKEN=$(hadron auth token create --label ci --json | jq -r .rawKey)`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			var labelArg *string
			if label != "" {
				labelArg = &label
			}
			resp, err := gen.CreateUserApiKey(cmd.Context(), client, labelArg)
			if err != nil {
				return api.MapError(err)
			}
			if resp.CreateUserApiKey == nil || resp.CreateUserApiKey.UserApiKey == nil {
				return exitcode.Newf(exitcode.Error, "server returned no token")
			}
			dto := tokenCreateDTO{
				tokenDTO: toTokenDTO(resp.CreateUserApiKey.UserApiKey.UserApiKeyFields),
				RawKey:   resp.CreateUserApiKey.RawKey,
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				fmt.Fprintf(w, "✓ Created API token %s\n\n  %s\n\n", dto.ID, dto.RawKey)
				fmt.Fprintln(w, "This is the only time the token is shown — store it now")
				fmt.Fprintln(w, "(e.g. set HADRON_TOKEN, or save it in your CI secret store).")
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&label, "label", "", "human label for the token")
	return cmd
}

func newCmdTokenLs(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List your personal API tokens",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.MyUserApiKeys(cmd.Context(), client)
			if err != nil {
				return api.MapError(err)
			}
			tokens := make([]tokenDTO, 0, len(resp.MyUserApiKeys))
			for _, k := range resp.MyUserApiKeys {
				if k == nil {
					continue
				}
				tokens = append(tokens, toTokenDTO(k.UserApiKeyFields))
			}
			return output.Write(f.IOStreams, f.JSON, tokens, func(w io.Writer) error {
				t := output.NewTable(w, "ID", "LABEL", "PREVIEW", "CREATED", "LAST USED", "STATUS")
				for _, k := range tokens {
					status := "active"
					if k.Revoked {
						status = "revoked"
					}
					t.Row(k.ID, orDash(k.Label), k.KeyPreview, k.CreatedAt, orText(k.LastUsedAt, "never"), status)
				}
				return t.Flush()
			})
		},
	}
}

func newCmdTokenRevoke(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "revoke <id>",
		Short:   "Revoke a personal API token",
		Example: `  hadron auth token revoke uak_123 --yes`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			if err := cmdutil.Confirm(f.IOStreams, yes, "Revoke API token "+args[0]+"? Anything using it will stop working."); err != nil {
				return err
			}
			resp, err := gen.RevokeUserApiKey(cmd.Context(), client, args[0])
			if err != nil {
				return api.MapError(err)
			}
			if resp.RevokeUserApiKey == nil {
				return exitcode.Newf(exitcode.Error, "server returned no token")
			}
			dto := toTokenDTO(resp.RevokeUserApiKey.UserApiKeyFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ Revoked API token %s\n", dto.ID)
				return err
			})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}

// keyLabel renders a token for a human line: its preview, plus its label in
// parentheses when set (e.g. `hdrk_ab1 (ci-deploy)`).
func keyLabel(k tokenDTO) string {
	if k.Label != nil && *k.Label != "" {
		return fmt.Sprintf("%s (%s)", k.KeyPreview, *k.Label)
	}
	return k.KeyPreview
}

func orDash(s *string) string {
	if s == nil || *s == "" {
		return "—"
	}
	return *s
}

func orText(s *string, fallback string) string {
	if s == nil || *s == "" {
		return fallback
	}
	return *s
}
