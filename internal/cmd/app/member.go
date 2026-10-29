package app

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// appMemberUserDTO identifies a member without the fields the server withholds
// (name, email): only id, handle and URN are selected.
type appMemberUserDTO struct {
	ID     string  `json:"id"`
	Handle *string `json:"handle"`
	URN    *string `json:"urn"`
}

// appMemberRowDTO is one visible member in `app member list`.
type appMemberRowDTO struct {
	User      appMemberUserDTO `json:"user"`
	Role      string           `json:"role"`
	CreatedAt string           `json:"createdAt"`
}

// appMemberListDTO is the stable --json shape of `app member list`. Members is
// the VISIBLE rows: an empty list never proves the App has no members.
type appMemberListDTO struct {
	AppID   string            `json:"appId"`
	AppURN  string            `json:"appUrn"`
	Members []appMemberRowDTO `json:"members"`
}

// appMemberAddDTO is the stable --json shape of a confirmed add/upsert.
type appMemberAddDTO struct {
	App       string           `json:"app"`
	UserRef   string           `json:"userRef"`
	User      appMemberUserDTO `json:"user"`
	Role      string           `json:"role"`
	CreatedAt string           `json:"createdAt"`
	Status    string           `json:"status"`
}

// appMemberRemoveDTO is the stable --json shape of a remove. The server answers
// true whether or not a row existed, so Status is "absent" — the state now —
// never "removed".
type appMemberRemoveDTO struct {
	App     string `json:"app"`
	UserRef string `json:"userRef"`
	Status  string `json:"status"`
}

func newCmdMember(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "member <command>",
		Aliases: []string{"members"},
		Short:   "Manage an App's human members",
		Long: `Add, remove and list the human members of an App (cor:dmo:050:04).

The App is the root --app flag, else the active App (` + "`hadron app use`" + `);
there is no positional App. --user is passed to the server unchanged — an id,
email, handle or hrn:user: URN — and is never resolved here.

The roles an App offers are the union of its installed Agents' member roles
(cor:dmo:050:04); ` + "`hadron app member roles`" + ` prints them, and
` + "`hadron agent update <ref> --member-roles …`" + ` changes an Agent's. Roles are
validated by the server only, so ` + "`add --role`" + ` is never checked locally: an
invalid role is refused with the server's own message. Authority is the
server's too: an App member manager, or the member themselves for remove.`,
	}
	cmd.AddCommand(newCmdMemberList(f))
	cmd.AddCommand(newCmdMemberRoles(f))
	cmd.AddCommand(newCmdMemberAdd(f))
	cmd.AddCommand(newCmdMemberRemove(f))
	return cmd
}

// memberTargetApp resolves the ONE App a member command acts on; the rule
// lives in cmdutil.TargetApp so every command that writes consent or
// membership against an App resolves it the same way.
func memberTargetApp(cmd *cobra.Command, f *cmdutil.Factory) (app, source string, err error) {
	return cmdutil.TargetApp(cmd, f)
}

// requireMemberUser refuses a blank --user. The value itself is passed on
// unchanged — no trimming, case folding or resolving.
func requireMemberUser(user string) error {
	if strings.TrimSpace(user) == "" {
		return exitcode.Newf(exitcode.Usage, "--user is required: an id, email, handle or hrn:user: URN")
	}
	return nil
}

// memberWriteError classifies a failed membership write. A definite refusal
// keeps the server's wording and exit code; everything else is outcome UNKNOWN
// (see api.AppMemberWriteRefused) — non-zero, no success DTO, never retried.
//
// `partial` is true when the response carried a result next to the error: the
// resolver ran and returned data, so the write may have committed whatever the
// error says. The recovery command repeats the frozen target App.
func memberWriteError(err error, partial bool, appRef string) error {
	if !partial && api.AppMemberWriteRefused(err) {
		return api.MapError(err)
	}
	return exitcode.Newf(exitcode.Unavailable,
		"outcome unknown: %s. The membership change may or may not have been applied and was not retried — "+
			"run %s to check (a list showing no row is not proof: rows you cannot see are withheld)",
		api.MapError(err), memberListHint(appRef))
}

// memberListHint is the copy-pasteable check for the App a write was aimed at.
func memberListHint(appRef string) string {
	return "`hadron app member list --app " + shellQuote(appRef) + "`"
}

func shellQuote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789:._/@-") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func memberUserDTO(id string, handle, urn *string) appMemberUserDTO {
	return appMemberUserDTO{ID: id, Handle: handle, URN: urn}
}

func newCmdMemberList(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List the members of an App that you can see",
		Long: `List the members of the App (--app, else the active App) that you can see.

An empty list means "no members visible to you", never "no members": the server
returns [] when the App's member list is not readable by you.`,
		Example: `  hadron app member list
  hadron app member list --app hrn:app:acme.com:eng-team --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			appRef, source, err := memberTargetApp(cmd, f)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.AppMembers(cmd.Context(), client, appRef)
			if err != nil {
				return api.MapError(err)
			}
			if resp == nil || resp.App == nil {
				return exitcode.Newf(exitcode.NotFound, "app %q not found, or not visible to you", appRef)
			}
			dto := appMemberListDTO{AppID: resp.App.Id, AppURN: resp.App.Urn, Members: []appMemberRowDTO{}}
			for _, m := range resp.App.Members {
				if m == nil || m.User == nil {
					return exitcode.Newf(exitcode.Error, "server returned a malformed member row for %q", appRef)
				}
				dto.Members = append(dto.Members, appMemberRowDTO{
					User:      memberUserDTO(m.User.Id, m.User.Handle, m.User.Urn),
					Role:      m.Role,
					CreatedAt: m.CreatedAt,
				})
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if _, err := fmt.Fprintf(w, "app: %s (%s)\n", memberText(dto.AppURN), source); err != nil {
					return err
				}
				if len(dto.Members) == 0 {
					_, err := fmt.Fprintln(w, "no members visible to you")
					return err
				}
				t := output.NewTable(w, "USER ID", "HANDLE", "ROLE", "ADDED")
				for _, m := range dto.Members {
					t.Row(output.SafeCell(m.User.ID), memberCell(m.User.Handle), memberText(m.Role), output.SafeCell(m.CreatedAt))
				}
				return t.Flush()
			})
		},
	}
}

// newCmdMemberRoles reads App.availableMemberRoles (#1226, R211): the roles
// `member add --role` may name. The list is the server's, in its order.
func newCmdMemberRoles(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "roles",
		Short: "List the member roles an App offers",
		Long: `List the member roles the App (--app, else the active App) offers: the union of
its installed Agents' member roles, primary Agent first (cor:dmo:050:04). These
are the values ` + "`app member add --role`" + ` accepts. Change an Agent's roles with
` + "`hadron agent update <ref> --member-roles …`" + `.

Text is one role per line; --json is an array of role strings.`,
		Example: `  hadron app member roles
  hadron app member roles --app hrn:app:acme.com:eng-team --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			appRef, _, err := memberTargetApp(cmd, f)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.AppMemberRoles(cmd.Context(), client, appRef)
			if err != nil {
				return api.MapError(err)
			}
			if resp == nil || resp.App == nil {
				return exitcode.Newf(exitcode.NotFound, "app %q not found, or not visible to you", appRef)
			}
			roles := append([]string{}, resp.App.AvailableMemberRoles...)
			return output.Write(f.IOStreams, f.JSON, roles, func(w io.Writer) error {
				if len(roles) == 0 {
					_, err := fmt.Fprintln(w, "no member roles offered")
					return err
				}
				for _, r := range roles {
					if _, err := fmt.Fprintln(w, memberText(r)); err != nil {
						return err
					}
				}
				return nil
			})
		},
	}
}

func newCmdMemberAdd(f *cmdutil.Factory) *cobra.Command {
	var user, role string
	var yes bool
	cmd := &cobra.Command{
		Use:   "add --user <ref> --role <role>",
		Short: "Add a user to an App, or change their role",
		Long: `Add a user to the App (--app, else the active App) with the given role.

This is an idempotent upsert: the same role is a repeat, a different role
changes the membership. Because the command never reads the list first, EVERY
add is confirmed — pass --yes to skip the prompt; a non-interactive caller
without --yes is refused before anything is sent.

If the response is lost or ambiguous the outcome is reported as unknown and the
write is not retried; check with ` + "`hadron app member list`" + `.`,
		Example: `  hadron app member add --user ada@acme.com --role member --yes
  hadron app member add --app hrn:app:acme.com:eng-team --user hrn:user:ada --role owner`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireMemberUser(user); err != nil {
				return err
			}
			if strings.TrimSpace(role) == "" {
				return exitcode.Newf(exitcode.Usage, "--role is required: roles are defined by the server and no default is assumed")
			}
			appRef, source, err := memberTargetApp(cmd, f)
			if err != nil {
				return err
			}
			prompt := fmt.Sprintf("Add user %s to app %s (%s) with role %s?", strconv.Quote(user), appRef, source, strconv.Quote(role))
			if err := cmdutil.Confirm(f.IOStreams, yes, prompt); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.EnsureAppMember(cmd.Context(), client, appRef, user, role)
			if err != nil {
				return memberWriteError(err, resp != nil && resp.EnsureAppMember != nil, appRef)
			}
			if resp == nil || resp.EnsureAppMember == nil || resp.EnsureAppMember.User == nil {
				return exitcode.Newf(exitcode.Unavailable,
					"outcome unknown: the server returned no member row. Run %s to check (a list showing no row is not proof: rows you cannot see are withheld)", memberListHint(appRef))
			}
			m := resp.EnsureAppMember
			dto := appMemberAddDTO{
				App:       appRef,
				UserRef:   user,
				User:      memberUserDTO(m.User.Id, m.User.Handle, m.User.Urn),
				Role:      m.Role,
				CreatedAt: m.CreatedAt,
				Status:    "member",
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ %s is a member of %s (%s) with role %s\n", memberText(user), appRef, source, memberText(dto.Role))
				return err
			})
		},
	}
	cmd.Flags().StringVar(&user, "user", "", "user to add: an id, email, handle or hrn:user: URN, passed to the server unchanged (required)")
	cmd.Flags().StringVar(&role, "role", "", "the member's role, as the server defines roles (required)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}

func newCmdMemberRemove(f *cmdutil.Factory) *cobra.Command {
	var user string
	var yes bool
	cmd := &cobra.Command{
		Use:     "remove --user <ref>",
		Aliases: []string{"rm"},
		Short:   "Remove a user from an App",
		Long: `Remove a user from the App (--app, else the active App). Reversible: add the
user again to restore the membership.

The server answers the same whether or not the user was a member, so success
means "not a member now", not "was removed". An App member manager may remove
anyone; anyone else may remove only themselves, and every other target gets the
same refusal — which is printed as-is, so it cannot be used to test whether a
user exists.

Confirmed first: pass --yes to skip the prompt; a non-interactive caller
without --yes is refused before anything is sent.`,
		Example: `  hadron app member remove --user ada@acme.com --yes`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireMemberUser(user); err != nil {
				return err
			}
			appRef, source, err := memberTargetApp(cmd, f)
			if err != nil {
				return err
			}
			// Confirm, not ConfirmDeletion: removal is reversible (add the user
			// again; their personal memory is retained), so "cannot be undone"
			// would be false.
			prompt := fmt.Sprintf("Remove user %s from app %s (%s)?", strconv.Quote(user), appRef, source)
			if err := cmdutil.Confirm(f.IOStreams, yes, prompt); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.RemoveAppMember(cmd.Context(), client, appRef, user)
			if err != nil {
				return memberWriteError(err, resp != nil && resp.RemoveAppMember != nil, appRef)
			}
			if resp == nil || resp.RemoveAppMember == nil || !*resp.RemoveAppMember {
				return exitcode.Newf(exitcode.Unavailable,
					"outcome unknown: the server did not confirm the removal. Run %s to check (a list showing no row is not proof: rows you cannot see are withheld)", memberListHint(appRef))
			}
			dto := appMemberRemoveDTO{App: appRef, UserRef: user, Status: "absent"}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ %s is not a member of %s (%s) now\n", memberText(user), appRef, source)
				return err
			})
		},
	}
	cmd.Flags().StringVar(&user, "user", "", "user to remove: an id, email, handle or hrn:user: URN, passed to the server unchanged (required)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}

// memberText neutralizes a server- or user-supplied value inside a detail
// line with output.SafeCell's rune classes and 256-rune cut — the cli#1104
// gate as ruled for detail/error text too (#1104 authors + gate holders,
// team chat #39296/#39297/#39300). The old strconv.Quote guard only caught
// C0/C1 controls — Cf bidi overrides and Zl/Zp separators passed raw, and
// quoting never bounded length (Charles's cli#1110 QA probe). Text only:
// --json keeps the source value.
func memberText(s string) string {
	return output.SafeCell(s)
}

// memberCell renders a nullable table cell: a nil handle as the dash, any
// present value through output.SafeCell. An explicit empty string stays
// blank, not a dash — memberText rendered it blank at 5face16 and the
// rendered bytes are contract (Chip's cli#1110 QA P3).
func memberCell(s *string) string {
	if s == nil {
		return "—"
	}
	return output.SafeCell(*s)
}
