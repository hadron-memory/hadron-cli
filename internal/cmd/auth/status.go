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
	Impersonating bool      `json:"impersonating,omitempty"`
	// RejectedReason says WHY a present credential was rejected, when the
	// CLI knows (#681). Only ever set alongside authenticated:false. Its one
	// value today is rejectedMCPOnly; absent means "rejected, reason unknown".
	RejectedReason string `json:"rejectedReason,omitempty"`
}

// rejectedMCPOnly: the key is valid but its OAuth grant is `mcp` alone, so
// every CLI surface refuses it (#681).
const rejectedMCPOnly = "mcp-only-scope"

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
			if err != nil {
				// Only a rejected credential means "not signed in". Any other
				// failure — transport, or schema skew on an older self-hosted
				// server that lacks authContext — must surface, not masquerade as
				// a rejected token (Codex #192).
				//
				// #681: an MCP-only key is the case this command exists for — the
				// server knows who it is, and every other command fails on it — so
				// it is reported, with its reason and remedy, rather than returned
				// as the server's bare refusal.
				mapped := api.MapError(err)
				if api.IsMCPOnlyCredential(err) {
					dto.RejectedReason = rejectedMCPOnly
				} else if exitcode.FromError(mapped) != exitcode.AuthRequired {
					return mapped
				}
			} else if resp.AuthContext != nil {
				ac := resp.AuthContext
				dto.Authenticated = true
				dto.PrincipalType = string(ac.PrincipalType)
				dto.User = principalLabel(ac)
				if ac.ApiKey != nil {
					k := toTokenDTO(ac.ApiKey.UserApiKeyFields)
					dto.Key = &k
				}
				if ac.Impersonation != nil {
					dto.Impersonating = true
				}
			}

			writeErr := output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if dto.RejectedReason == rejectedMCPOnly {
					_, err := fmt.Fprintf(w, "✗ %s: the key from %s is limited to the MCP surface — the CLI cannot use it\n  %s\n",
						server, describeSource(dto), mcpOnlyRemedy(dto))
					return err
				}
				if !dto.Authenticated {
					_, err := fmt.Fprintf(w, "✗ %s: token from %s was rejected — run `hadron auth login`\n", server, describeSource(dto))
					return err
				}
				if dto.Impersonating {
					if _, err := fmt.Fprintf(w, "✓ %s: IMPERSONATING %s (read-only) — `hadron auth impersonate --stop` to end\n", server, dto.User); err != nil {
						return err
					}
				} else if _, err := fmt.Fprintf(w, "✓ %s: signed in as %s (token from %s)\n", server, dto.User, describeSource(dto)); err != nil {
					return err
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
			if !dto.Authenticated {
				return exitcode.Silent(exitcode.AuthRequired)
			}
			return nil
		},
	}
}

// mcpOnlyRemedy is api.MCPOnlyRemedy narrowed by what status knows and the
// generic path does not: where the key came from. A key in HADRON_TOKEN
// outranks the store, so `auth logout && auth login` would change nothing
// while the variable is set — naming it there would be a false remedy.
func mcpOnlyRemedy(dto statusResult) string {
	if dto.TokenSource == string(authpkg.SourceEnv) {
		return "HADRON_TOKEN holds an MCP-only key, and it takes precedence over any stored login. " +
			"Replace it with a key that has the `account` scope — a key created on the portal's API keys page (/app/account/api-keys) has no scope limit — or unset it and run `hadron auth login`."
	}
	return "Sign in again with `hadron auth logout && hadron auth login` (hadron v0.15.0 or later requests the `account` scope), " +
		"or create a key on the portal's API keys page (/app/account/api-keys) and run `hadron auth login --with-token` with it."
}

func describeSource(dto statusResult) string {
	if dto.TokenSource == string(authpkg.SourceEnv) {
		return "HADRON_TOKEN"
	}
	// TokenStorage is only set for a store-resolved credential, so fall back to
	// the source label — otherwise an impersonation token that the server
	// rejects renders as "token from  was rejected" (PR #345 review).
	if dto.TokenStorage == "" {
		return dto.TokenSource
	}
	return dto.TokenStorage
}

// principalLabel is the human subject for a resolved principal: the user's
// display name / email / id, else "App <id>" / "Agent <id>", else the bare
// principal type — so no principal ever renders as an empty subject.
func principalLabel(ac *gen.AuthContextAuthContext) string {
	switch {
	case ac.User != nil && ac.User.Name != nil && *ac.User.Name != "":
		return *ac.User.Name
	case ac.User != nil && ac.User.Email != nil && *ac.User.Email != "":
		return *ac.User.Email
	case ac.User != nil:
		return ac.User.Id
	default:
		appID, agentID := "", ""
		if ac.AppId != nil {
			appID = *ac.AppId
		}
		if ac.AgentId != nil {
			agentID = *ac.AgentId
		}
		return nonUserLabel(appID, agentID, string(ac.PrincipalType))
	}
}
