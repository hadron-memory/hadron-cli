package user

import (
	"fmt"
	"io"
	"strings"
	"unicode"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// userDeleteDTO is the stable --json shape for `user delete`. user is the
// ref the caller named, or null for the caller's own account.
type userDeleteDTO struct {
	Deleted bool    `json:"deleted"`
	User    *string `json:"user"`
}

// newCmdDelete builds `hadron user delete [--user <ref>] [--reason <text>]
// --yes` over deleteUser (#1344). Who may delete whom is the server's rule
// (cor:api:240:01): the CLI sends the ref and the reason and maps whatever the
// server answers. In particular it never says whether a named user exists — a
// caller who may not name a target gets the server's one FORBIDDEN either way.
func newCmdDelete(f *cmdutil.Factory) *cobra.Command {
	var userRef, reason string
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete [--user <user>] [--reason <text>]",
		Short: "Delete your own account, or (platform admin) another user's",
		Long: `Delete a user account for good. With no --user it deletes YOUR OWN account,
which needs no role. A platform admin may name another account with --user
(a user id, handle, @handle or hrn:user:<handle>). Everyone else who names a
target gets the same refusal (exit 8) whether or not that user exists.

This is irreversible erasure: your personal and user-owned memories, Apps and
Agents, sessions, provider connections, grants, shares and secrets are erased,
API keys are revoked, and the account can no longer sign in. The server keeps a
scrubbed record for audit and billing. An organization where you are the only
OWNER but others remain blocks the deletion (server code SOLE_ORG_OWNER,
nothing is deleted): transfer ownership first. Needs a user login; an App or
agent key is refused.

--reason (1-500 characters) is required by the server when you name another
user, and optional for your own account. It asks for confirmation on a
terminal; in a script or agent pass --yes, or it refuses with exit 2.

After deleting your own account, your stored sign-in no longer works: run
hadron auth logout to clear it. --json prints {"deleted": true, "user": <the
--user you named, or null for your own account>}.`,
		Example: `  hadron user delete --yes                                   # your own account
  hadron user delete --user @old-handle --reason "GDPR erasure request #42" --yes`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			named := cmd.Flags().Changed("user")
			if named && blankAsServerTrims(userRef) {
				return exitcode.Newf(exitcode.Usage, "--user needs a user id, handle or hrn:user:<handle>; omit it to delete your own account")
			}
			if cmd.Flags().Changed("reason") && reason == "" {
				return exitcode.Newf(exitcode.Usage, "--reason is empty; give 1-500 characters or omit it")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			what := "your account"
			var refArg, reasonArg *string
			var shown *string
			if named {
				what = "account " + userRef
				refArg = &userRef
				shown = &userRef
			}
			if cmd.Flags().Changed("reason") {
				reasonArg = &reason
			}
			if err := cmdutil.ConfirmDeletion(f.IOStreams, yes, what); err != nil {
				return err
			}
			resp, err := gen.DeleteUser(cmd.Context(), client, reasonArg, refArg)
			if err != nil {
				return api.MapErrorKeepingCode(err)
			}
			if resp == nil || !resp.DeleteUser {
				return exitcode.Newf(exitcode.Error, "server did not confirm the deletion")
			}
			dto := userDeleteDTO{Deleted: true, User: shown}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if !named {
					_, err := fmt.Fprintln(w, "✓ Deleted your account. Your sign-in no longer works: run hadron auth logout to clear it.")
					return err
				}
				_, err := fmt.Fprintf(w, "✓ Deleted account %s\n", output.SafeCell(userRef))
				return err
			})
		},
	}
	cmd.Flags().StringVar(&userRef, "user", "", "the account to delete (platform admin); omit for your own")
	cmd.Flags().StringVar(&reason, "reason", "", "why (1-500 characters); required by the server when naming another user")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}

// blankAsServerTrims reports whether ref is empty after the server's
// JavaScript String.trim(), which also strips U+FEFF (Go's TrimSpace keeps it).
// The server reads such a ref as "no target" and selects the caller's own
// account, so it must be refused here rather than sent as a named deletion.
func blankAsServerTrims(ref string) bool {
	return strings.TrimFunc(ref, func(r rune) bool { return unicode.IsSpace(r) || r == '\uFEFF' }) == ""
}
