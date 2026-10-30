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
require a user login — an app or agent key cannot manage user tokens.

A token acts as you, with your access, until you revoke it; it has no expiry of
its own. Only "create" ever shows the token itself; "list" shows metadata
(id, label, a short preview, created and last-used times, status), and the id
is what "revoke" takes.`,
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
	AgentID       string    `json:"agentId,omitempty"`
	Key           *tokenDTO `json:"key,omitempty"`
	// RejectedReason is `auth status`'s field, with the same values (#681):
	// set only alongside valid:false, when the CLI knows why.
	RejectedReason string `json:"rejectedReason,omitempty"`
}

func newCmdTokenValidate(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Check whether a token is valid (reads it from stdin)",
		Long: `Check whether a personal access token authenticates, without storing it.

The token is read from standard input, so it never lands in your shell history
or the process table. A valid token prints the user it belongs to and exits 0;
a rejected or revoked token exits 3. A network or server failure is an error
(not "invalid"), so a script can tell the two apart.

--json gives valid, then for a valid token principalType, userId, name, email,
handle, roles and key (its id, label, preview and times; never the token).`,
		Example: `  hadron auth token validate < ci-token
  printf '%s' "$HADRON_TOKEN" | hadron auth token validate --json`,
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
				// #681: an MCP-only key is not invalid — it works, just not
				// here. "Invalid, revoked, or expired" would send its owner
				// looking for a fault the key does not have.
				if kind := api.OAuthScopeRefusal(err); kind != "" {
					dto.RejectedReason = string(kind)
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
				if ac.AgentId != nil {
					dto.AgentID = *ac.AgentId
				}
				if ac.ApiKey != nil {
					k := toTokenDTO(ac.ApiKey.UserApiKeyFields)
					dto.Key = &k
				}
			}

			writeErr := output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				switch api.ScopeRefusal(dto.RejectedReason) {
				case api.ScopeMCPOnly:
					_, err := fmt.Fprintln(w, "✗ Token is limited to the MCP surface — it works for MCP clients, but the CLI cannot use it.\n"+
						"  A key created on the portal's API keys page (/app/account/api-keys) has no scope limit.")
					return err
				case api.ScopeUnsupported:
					_, err := fmt.Fprintln(w, "✗ Token carries an OAuth scope this server does not support, so it is refused everywhere.\n"+
						"  A key created on the portal's API keys page (/app/account/api-keys) has no scope limit.")
					return err
				}
				if !dto.Valid {
					_, err := fmt.Fprintln(w, "✗ Token is invalid, revoked, or expired")
					return err
				}
				// A non-user principal (App/Agent key) has no user subject or key.
				if dto.UserID == "" {
					_, err := fmt.Fprintf(w, "✓ Token is valid — %s\n", output.SafeCell(nonUserLabel(dto.AppID, dto.AgentID, dto.PrincipalType)))
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
					if _, err := fmt.Fprintf(w, "✓ Token is valid — %s (%s)\n", output.SafeCell(label), output.SafeCell(dto.Email)); err != nil {
						return err
					}
				} else if _, err := fmt.Fprintf(w, "✓ Token is valid — %s\n", output.SafeCell(label)); err != nil {
					return err
				}
				if dto.Key != nil {
					_, err := fmt.Fprintf(w, "  key %s, last used %s\n", output.SafeCell(keyLabel(*dto.Key)), output.SafeCell(orText(dto.Key.LastUsedAt, "never")))
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
		Long: `Mint a new personal access token for the signed-in user (requires a user
login; see hadron auth token). It acts as you, with your access, until revoked.

The raw token is printed ONCE and is never recoverable — store it now (e.g. in
a CI secret). Only the token's hash is kept server-side. The printout also
shows the token's id, which hadron auth token revoke takes. Give each use its
own --label, so the list tells you what to revoke.

Avoid leaving the token in terminal scrollback or logs: --json with jq, as
below, writes it straight to a file.`,
		Example: `  hadron auth token create --label ci-deploy
  (umask 077; hadron auth token create --label ci --json | jq -r .rawKey > ci-token)   # file is private from the start`,
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
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List your personal API tokens",
		Long: `List the personal tokens you have minted, including revoked ones. Shows
metadata only, never the token itself: ID (what revoke takes), LABEL, PREVIEW
(the last characters, to recognise a token), CREATED, LAST USED and STATUS
(active or revoked).

Requires a user login. You see only your own tokens. --json gives the same
fields as id, label, keyPreview, issuedVia, createdAt, lastUsedAt, revokedAt
and revoked; an empty list is [].`,
		Example: `  hadron auth token list
  hadron auth token list --json | jq -r '.[] | select(.revoked | not) | .id'`,
		Args: cobra.NoArgs,
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
					t.Row(output.SafeCell(k.ID), output.SafeCell(orDash(k.Label)), output.SafeCell(k.KeyPreview), output.SafeCell(k.CreatedAt), output.SafeCell(orText(k.LastUsedAt, "never")), status)
				}
				return t.Flush()
			})
		},
	}
}

func newCmdTokenRevoke(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "revoke <id>",
		Short: "Revoke a personal API token",
		Long: `Revoke one of your personal tokens, so anything using it stops working
at once, together with every token minted from it. Revoking cannot be undone;
mint a new token if you need one.

<id> is the token's ID from hadron auth token list (not its label and not the
token itself). You can revoke only your own tokens: an unknown id, or someone
else's, exits 4. Revoking a token that is already revoked succeeds again.

On a terminal it asks for confirmation; in a script or agent pass --yes, or it
refuses with exit 2. Prints "✓ Revoked API token <id>". The token stays in the
list with STATUS revoked.`,
		Example: `  hadron auth token list
  hadron auth token revoke 01a18cf6400b7fcc8d2f51e7401c55c1
  hadron auth token revoke 01a18cf6400b7fcc8d2f51e7401c55c1 --yes   # non-interactive`,
		Args: cobra.ExactArgs(1),
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

// nonUserLabel names a principal that resolves to no user (an App or Agent
// key), falling back to the bare principal type so nothing renders empty.
func nonUserLabel(appID, agentID, principalType string) string {
	switch {
	case appID != "":
		return "App " + appID
	case agentID != "":
		return "Agent " + agentID
	default:
		return principalType
	}
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
