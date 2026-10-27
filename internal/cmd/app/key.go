package app

// `app key scope grant|revoke` — what an App key may do (#871 over server
// #1749 and #1876): grant or take back a scope (cor:aut:010:04), and grant
// `host:operate` for ONE host (cor:agt:070:04). Only whoever manages the App,
// as themselves: the server refuses a worker's session, an App key and an
// impersonation, so these writes never carry a worktree's session. The
// output is the key's id, label and live scopes — never its preview or any
// key material.

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

type appKeyScopeDTO struct {
	Scope     string  `json:"scope"`
	HostID    *string `json:"hostId"`
	GrantedAt string  `json:"grantedAt"`
	GrantedBy string  `json:"grantedBy"`
}

type appKeyScopesDTO struct {
	ID        string           `json:"id"`
	AppID     string           `json:"appId"`
	Label     *string          `json:"label"`
	RevokedAt *string          `json:"revokedAt"`
	Scopes    []appKeyScopeDTO `json:"scopes"`
}

func appKeyScopesDTOFrom(k *gen.AppKeyScopesFields) appKeyScopesDTO {
	dto := appKeyScopesDTO{ID: k.Id, AppID: k.AppId, Label: k.Label, RevokedAt: k.RevokedAt, Scopes: []appKeyScopeDTO{}}
	for _, s := range k.Scopes {
		if s != nil {
			dto.Scopes = append(dto.Scopes, appKeyScopeDTO{Scope: s.Scope, HostID: s.HostId, GrantedAt: s.GrantedAt, GrantedBy: s.GrantedBy})
		}
	}
	return dto
}

func newCmdKey(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "key <command>",
		Short: "Work with an App's keys",
	}
	cmd.AddCommand(newCmdKeyScope(f))
	cmd.AddCommand(newCmdKeyList(f))
	return cmd
}

func newCmdKeyScope(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scope <command>",
		Short: "Grant or revoke what an App key may do",
		Long: `An App key holds no scope until one is granted (cor:aut:010:03); a new or
rotated key starts with none. The scopes are attention:deliver and
host:operate. Only whoever manages the App may change them, as themselves —
never through a worker's session, an App key or an impersonation (FORBIDDEN,
exit 8).`,
	}
	cmd.AddCommand(newCmdKeyScopeGrant(f))
	cmd.AddCommand(newCmdKeyScopeRevoke(f))
	return cmd
}

func newCmdKeyScopeGrant(f *cmdutil.Factory) *cobra.Command {
	var hostID string
	cmd := &cobra.Command{
		Use:   "grant <key-id> <scope> [--host-id <host>]",
		Short: "Grant an App key a scope",
		Long: `Grant an App key a scope (cor:aut:010:04). Granting a scope the key already
holds changes nothing.

host:operate is granted for ONE host, named with --host-id (a host token): the
key then registers, collects and reports as that host and no other
(cor:agt:070:04). A key already operating as another host is refused
APP_KEY_SCOPE_HOST_CONFLICT (exit 5): revoke host:operate first, or use
another key. --host-id is refused for every other scope (exit 2).`,
		Example: `  hadron app key scope grant key_123 host:operate --host-id orca1
  hadron app key scope grant key_123 attention:deliver`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, scope := strings.TrimSpace(args[0]), strings.TrimSpace(args[1])
			if id == "" || scope == "" {
				return exitcode.Newf(exitcode.Usage, "pass the key id and the scope, e.g. `grant key_123 host:operate --host-id orca1`")
			}
			var host *string
			if cmd.Flags().Changed("host-id") {
				h := strings.TrimSpace(hostID)
				if h == "" {
					return exitcode.Newf(exitcode.Usage, "--host-id was given empty (an unset variable?); name the one host this key operates as")
				}
				host = &h
			}
			// Said here in the CLI's own terms: the server's refusal names its
			// GraphQL argument (hostId), not this flag.
			if scope == "host:operate" && host == nil {
				return exitcode.Newf(exitcode.Usage, "host:operate is granted for one host: name it with --host-id <host>")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.GrantAppKeyScope(cmd.Context(), client, id, scope, host)
			if err != nil {
				return api.MapError(err)
			}
			dto := appKeyScopesDTOFrom(&resp.GrantAppKeyScope.AppKeyScopesFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				msg := fmt.Sprintf("✓ key %s holds %s", dto.ID, scope)
				if host != nil {
					msg += " for host " + *host
				}
				if _, err := fmt.Fprintln(w, msg); err != nil {
					return err
				}
				return renderKeyScopes(w, dto)
			})
		},
	}
	cmd.Flags().StringVar(&hostID, "host-id", "", "host:operate only: the one host this key operates as")
	return cmd
}

func newCmdKeyScopeRevoke(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "revoke <key-id> <scope>",
		Short: "Take a scope back from an App key",
		Long: `Take a scope back from an App key. The key's next call that needs it is
refused. Revoking a scope the key does not hold changes nothing. Revoking
host:operate frees the key to be granted it for another host.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, scope := strings.TrimSpace(args[0]), strings.TrimSpace(args[1])
			if id == "" || scope == "" {
				return exitcode.Newf(exitcode.Usage, "pass the key id and the scope, e.g. `revoke key_123 host:operate`")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.RevokeAppKeyScope(cmd.Context(), client, id, scope)
			if err != nil {
				return api.MapError(err)
			}
			dto := appKeyScopesDTOFrom(&resp.RevokeAppKeyScope.AppKeyScopesFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if _, err := fmt.Fprintf(w, "✓ key %s no longer holds %s\n", dto.ID, scope); err != nil {
					return err
				}
				return renderKeyScopes(w, dto)
			})
		},
	}
}

func renderKeyScopes(w io.Writer, dto appKeyScopesDTO) error {
	if len(dto.Scopes) == 0 {
		_, err := fmt.Fprintln(w, "scopes: none")
		return err
	}
	t := output.NewTable(w, "SCOPE", "HOST", "GRANTED")
	for _, s := range dto.Scopes {
		host := "—"
		if s.HostID != nil {
			host = *s.HostID
		}
		t.Row(output.SafeCell(s.Scope), output.SafeCell(host), output.SafeCell(s.GrantedAt))
	}
	return t.Flush()
}
