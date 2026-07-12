package connection

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

func newCmdCreate(f *cmdutil.Factory) *cobra.Command {
	var (
		connection, app, expires string
		scopes                   []string
	)
	cmd := &cobra.Command{
		Use:   "create --connection <ref> --app <ref> --scopes <scope>[,...]",
		Short: "Delegate scoped access on your connection to an App (owner-only)",
		Long: `Grant an App install scoped access to a connection you own (owner-only).

Scopes are drawn from mail.read, mail.send, calendar.freebusy, calendar.read
(the server validates the set). --app accepts an App PK or URN. --expires-at is
an optional ISO-8601 timestamp with timezone and must be in the future; omit it
for a perpetual grant (until revoked).`,
		Example: `  hadron connection grant create --connection conn_123 --app acme.com:inbox-bot --scopes mail.read
  hadron connection grant create --connection conn_123 --app hrn:app:acme.com::inbox-bot \
    --scopes mail.read,mail.send --expires-at 2027-01-01T00:00:00Z`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// --scopes is repeatable and comma-splittable; normalize + dedupe.
			seen := map[string]bool{}
			var scopeSet []string
			for _, s := range scopes {
				for _, part := range strings.Split(s, ",") {
					part = strings.TrimSpace(part)
					if part == "" || seen[part] {
						continue
					}
					seen[part] = true
					scopeSet = append(scopeSet, part)
				}
			}
			if len(scopeSet) == 0 {
				return exitcode.Newf(exitcode.Usage, "at least one --scopes entry is required (e.g. --scopes mail.read)")
			}

			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			var expiresAt *string
			if expires != "" {
				expiresAt = &expires
			}
			resp, err := gen.CreateConnectionGrant(cmd.Context(), client, connection, app, scopeSet, expiresAt)
			if err != nil {
				return api.MapError(err)
			}
			// The schema marks the payload non-null, so this only fires on a
			// malformed response — guard rather than panic.
			if resp == nil || resp.CreateConnectionGrant == nil {
				return exitcode.Newf(exitcode.Error, "server returned no grant payload")
			}
			dto := dtoFromFields(resp.CreateConnectionGrant.ConnectionGrantFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ granted %s on %s to %s (grant %s, %s)\n",
					dto.scopeList(), dto.ConnectionID, dto.grantee(), dto.ID, dto.expiry())
				return err
			})
		},
	}
	cmd.Flags().StringVar(&connection, "connection", "", "connection to delegate on (ID or ref; required)")
	cmd.Flags().StringVar(&app, "app", "", "grantee App (ID or URN; required)")
	cmd.Flags().StringSliceVar(&scopes, "scopes", nil, "scope to grant (repeatable or comma-separated; mail.read|mail.send|calendar.freebusy|calendar.read; required)")
	cmd.Flags().StringVar(&expires, "expires-at", "", "ISO-8601 expiry timestamp with timezone (omit for perpetual)")
	_ = cmd.MarkFlagRequired("connection")
	_ = cmd.MarkFlagRequired("app")
	_ = cmd.MarkFlagRequired("scopes")
	return cmd
}
