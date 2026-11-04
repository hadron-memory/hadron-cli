package app

// `app user create` — createAppUser (cli#1627, cor:api:240:02, cor:dmo:040:01).
// An App integrator vouches for a user by the App's own externalId: the server
// creates the User or returns the one it already knows, keyed by
// (externalAppId, externalId). App Key callers only — the CLI does not
// pre-judge the credential; any refusal is the server's, passed through typed.

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

// appUserDTO is the stable --json shape of `app user create`. It deliberately
// has no created/existing flag: createAppUser returns User! and does not say
// which happened. Null means the server returned no value for the field.
type appUserDTO struct {
	ID         string  `json:"id"`
	Handle     *string `json:"handle"`
	Name       *string `json:"name"`
	URN        *string `json:"urn"`
	ExternalID *string `json:"externalId"`
}

func newCmdUser(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "user <command>",
		Short: "Vouch for the users an App knows (App key only)",
	}
	cmd.AddCommand(newCmdUserCreate(f))
	return cmd
}

func newCmdUserCreate(f *cmdutil.Factory) *cobra.Command {
	var externalID, handle, name string
	cmd := &cobra.Command{
		Use:   "create --external-id <id> [--handle <h>] [--name <n>]",
		Short: "Create or return the user an App knows by its external id (App key only)",
		Long: `Vouch for a user: the server creates the user the App knows by --external-id,
or returns the one it already has — the same external id always gives the same
user id (the identity is the App plus that external id, cor:dmo:040:01).

Run this with an App key credential (for example in HADRON_TOKEN). It is for
App Key callers only: with a personal token the server refuses,
and that refusal is printed as it comes (typed error, nonzero exit) — the CLI
does not check the credential first.

The server's answer does not say whether the user was just created or already
existed, so this prints only the user (id, handle, name, URN, external id — a field the
server does not return shows as -), never a "created" or "existing" claim. --handle and --name are optional and left
unsent when omitted. There is no --email: the server accepts only empty values
for it, and an address is established through verified sign-in. Starting or
ending a session for the user is not part of this command.

The credential is never printed.`,
		Example: `  hadron app user create --external-id crm-4411
  hadron app user create --external-id crm-4411 --handle ada --name "Ada L." --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(externalID) == "" {
				return exitcode.Newf(exitcode.Usage, "--external-id is required (the id your App knows the user by)")
			}
			// The external id is the idempotency key: " ext-1" would mint a
			// SECOND user beside "ext-1", so surrounding whitespace is refused,
			// never silently trimmed or sent.
			if externalID != strings.TrimSpace(externalID) {
				return exitcode.Newf(exitcode.Usage, "--external-id has leading or trailing whitespace; it is the identity key, so pass it exactly as your App knows it")
			}
			for _, fl := range []struct{ name, val string }{{"handle", handle}, {"name", name}} {
				if cmd.Flags().Changed(fl.name) && fl.val == "" {
					return exitcode.Newf(exitcode.Usage, "--%s is empty; omit the flag to leave it unsent", fl.name)
				}
			}
			var handleArg, nameArg *string
			if cmd.Flags().Changed("handle") {
				handleArg = &handle
			}
			if cmd.Flags().Changed("name") {
				nameArg = &name
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.CreateAppUser(cmd.Context(), client, externalID, handleArg, nameArg)
			if err != nil {
				return api.MapErrorKeepingCode(err)
			}
			if resp == nil || resp.CreateAppUser == nil {
				return exitcode.Newf(exitcode.Unavailable, "the server returned no user")
			}
			u := resp.CreateAppUser
			dto := appUserDTO{ID: u.Id, Handle: u.Handle, Name: u.Name, URN: u.Urn, ExternalID: u.ExternalId}
			return output.Write(f.IOStreams, f.JSON, dto, func(out io.Writer) error {
				_, err := fmt.Fprintf(out, "user %s  handle %s  name %s  urn %s  external id %s\n",
					output.SafeCell(dto.ID), userCell(dto.Handle), userCell(dto.Name), userCell(dto.URN), userCell(dto.ExternalID))
				return err
			})
		},
	}
	cmd.Flags().StringVar(&externalID, "external-id", "", "the id your App knows the user by (required)")
	cmd.Flags().StringVar(&handle, "handle", "", "handle for the user (left unsent when omitted)")
	cmd.Flags().StringVar(&name, "name", "", "display name for the user (left unsent when omitted)")
	return cmd
}

func userCell(s *string) string {
	if s == nil {
		return "-"
	}
	return output.SafeCell(*s)
}
