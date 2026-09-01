package user

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// setRolesDTO is the stable --json shape for `user set-roles`. It carries
// the user in the shared shape plus the before/after role sets, so a caller
// can see what the replacement actually displaced without a second read.
type setRolesDTO struct {
	User          userDTO  `json:"user"`
	PreviousRoles []string `json:"previousRoles"`
	Roles         []string `json:"roles"`
	// Changed is null when the prior roles could not be read (the
	// pass-through case). Reporting false there would claim nothing
	// changed when the truth is that we cannot tell; previousRoles is
	// then [] because it is unknown, not because it was empty.
	Changed *bool `json:"changed"`
}

// platformRoles is the server's Role enum. Validating here turns a typo into
// a usage error naming the valid set, instead of a GraphQL enum-coercion
// error from the wire.
var platformRoles = []gen.Role{gen.RoleOwner, gen.RoleAdmin, gen.RoleContributor, gen.RoleReader}

func newCmdSetRoles(f *cmdutil.Factory) *cobra.Command {
	var roles []string
	var yes bool
	cmd := &cobra.Command{
		Use:   "set-roles <userRef> --role <role>... [--yes]",
		Short: "Replace a user's platform roles (platform admin only)",
		Long: `Set the platform-level roles on a user account.

This REPLACES the user's whole role set — it is not additive. Every role the
user should keep must be passed, because any role you omit is removed. The
command reads the current roles first and shows before → after, so the
replacement is visible before you confirm it.

Platform roles (owner, admin, contributor, reader) are global standing on the
Hadron platform, NOT membership in an organization or a memory. To change what
someone can do inside one org or memory, use ` + "`hadron org member set-role`" + ` or
` + "`hadron memory member set-role`" + ` instead.

Requires the platform ADMIN role; every other caller is refused by the server.

<userRef> accepts a user id, an email, a bare or @-prefixed handle, or an
hrn:user:<handle> URN. It is resolved to an id before the update, because
updateUserRoles takes a raw id and does no ref resolution of its own.`,
		Example: `  hadron user set-roles @jane --role admin --yes
  hadron user set-roles usr_789 --role contributor --role reader --json --yes
  hadron user set-roles jane@acme.com --role reader --yes   # demote: drops every other role`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := strings.TrimSpace(args[0])
			if ref == "" {
				return exitcode.Newf(exitcode.Usage, "user reference must not be empty")
			}
			wanted, err := parseRoles(roles)
			if err != nil {
				return err
			}
			// The confirmation below needs the user's current roles, so it
			// can't run until after the resolving read. Refuse an unattended
			// run up front instead, so a caller that could never answer the
			// prompt fails deterministically without any network work. Mirrors
			// cmdutil.Confirm's own non-interactive rule.
			if !yes && !f.IOStreams.IsInputTerminal() {
				return exitcode.Newf(exitcode.Usage,
					"refusing to replace platform roles without --yes in non-interactive mode")
			}

			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			// updateUserRoles takes a raw PK, so resolve the ref ourselves —
			// unlike mergeUsers, the server won't do it. ResolveUser hands
			// back the fields the resolution already read, so the current
			// roles come for free rather than costing a second round trip.
			// Exactly, not fuzzily: ResolveUser falls back to a sole substring
			// hit, which is a convenience for additive commands but a hazard
			// here — a typo like "alic" would silently replace alice's global
			// roles (review on #300).
			current, found, err := cmdutil.ResolveUserExactly(cmd, client, ref)
			if err != nil {
				return err
			}
			// A ref that didn't match is passed through as a literal id; its
			// prior roles are genuinely unknown, so leave them empty rather
			// than claiming the user has none.
			var previous []string
			if found {
				previous = userDTOFromFields(current).Roles
			}
			// Confirm against the RESOLVED account, never the typed ref: the
			// whole point of the prompt is to show which account is about to
			// change.
			label := cmdutil.DescribeUser(current)

			next := roleStrings(wanted)
			if err := cmdutil.Confirm(f.IOStreams, yes, setRolesPrompt(label, previous, found, next)); err != nil {
				return err
			}

			resp, err := gen.UpdateUserRoles(cmd.Context(), client, current.Id, wanted)
			if err != nil {
				return api.MapError(err)
			}
			// updateUserRoles is declared User! so a conformant server never
			// returns null without an error; guard the deref anyway.
			if resp == nil || resp.UpdateUserRoles == nil {
				return exitcode.Newf(exitcode.Error, "role update returned no user")
			}
			dto := setRolesDTO{
				User:          userDTOFromFields(resp.UpdateUserRoles.UserFields),
				PreviousRoles: previous,
			}
			if found {
				// Set membership, not slice order: --role contributor --role
				// admin over an existing [ADMIN, CONTRIBUTOR] grants the same
				// permissions, so reporting changed:true would be wrong.
				changed := !sameRoleSet(previous, next)
				dto.Changed = &changed
			}
			dto.Roles = dto.User.Roles
			if dto.PreviousRoles == nil {
				dto.PreviousRoles = []string{}
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				was := joinRoles(dto.PreviousRoles)
				if !found {
					was = "unknown — the previous roles could not be read"
				}
				_, err := fmt.Fprintf(w, "✓ %s now has roles: %s (was: %s)\n",
					dto.User.ID, joinRoles(dto.Roles), was)
				return err
			})
		},
	}
	cmd.Flags().StringArrayVar(&roles, "role", nil,
		"a role to set (repeatable): owner, admin, contributor, or reader — the flags together REPLACE the user's roles (required)")
	_ = cmd.MarkFlagRequired("role")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}

// parseRoles validates and normalizes --role values, preserving the order
// given and dropping exact duplicates. Matching is case-insensitive so both
// `--role admin` and `--role ADMIN` work.
func parseRoles(raw []string) ([]gen.Role, error) {
	out := make([]gen.Role, 0, len(raw))
	for _, r := range raw {
		token := strings.ToUpper(strings.TrimSpace(r))
		if token == "" {
			return nil, exitcode.Newf(exitcode.Usage, "--role must not be empty; valid roles are %s", validRoleList())
		}
		role := gen.Role(token)
		if !slices.Contains(platformRoles, role) {
			return nil, exitcode.Newf(exitcode.Usage, "unknown role %q — valid roles are %s", r, validRoleList())
		}
		if !slices.Contains(out, role) {
			out = append(out, role)
		}
	}
	if len(out) == 0 {
		// MarkFlagRequired catches a missing flag; this catches `--role ""`
		// repeated, which would otherwise clear every role silently.
		return nil, exitcode.Newf(exitcode.Usage, "at least one --role is required; valid roles are %s", validRoleList())
	}
	return out, nil
}

func validRoleList() string {
	names := make([]string, 0, len(platformRoles))
	for _, r := range platformRoles {
		names = append(names, strings.ToLower(string(r)))
	}
	return strings.Join(names, ", ")
}

func roleStrings(roles []gen.Role) []string {
	out := make([]string, 0, len(roles))
	for _, r := range roles {
		out = append(out, string(r))
	}
	return out
}

func joinRoles(roles []string) string {
	if len(roles) == 0 {
		return "none"
	}
	return strings.Join(roles, ", ")
}

// sameRoleSet compares role sets by MEMBERSHIP, not slice order: the server
// grants permissions from the set, so [ADMIN, CONTRIBUTOR] and
// [CONTRIBUTOR, ADMIN] are the same standing (review on #300).
func sameRoleSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	x, y := slices.Clone(a), slices.Clone(b)
	slices.Sort(x)
	slices.Sort(y)
	return slices.Equal(x, y)
}

// grantsPlatformAdmin reports whether a role set can run this command.
// requireRole compares the caller's HIGHEST role against ADMIN on the server's
// ROLE_ORDER (READER 0 < CONTRIBUTOR 1 < ADMIN 2 < OWNER 3), so OWNER
// qualifies too — dropping OWNER while keeping ADMIN is not a lockout.
func grantsPlatformAdmin(roles []string) bool {
	return slices.Contains(roles, string(gen.RoleAdmin)) || slices.Contains(roles, string(gen.RoleOwner))
}

// setRolesPrompt spells out the replacement against the RESOLVED account.
// Naming the roles being REMOVED is the point: the failure mode of a
// replace-semantics command is dropping a role you didn't realize the user had.
//
// previousKnown is false when the prior roles couldn't be read. Rendering that
// as "none" would tell an administrator the account is unprivileged when it may
// hold ADMIN or OWNER, so it is stated as unknown instead.
func setRolesPrompt(label string, previous []string, previousKnown bool, next []string) string {
	var b strings.Builder
	if !previousKnown {
		fmt.Fprintf(&b, "Replace platform roles on %s with %s."+
			" Their current roles could not be read, so this replaces whatever they hold — possibly including %s or %s.",
			label, joinRoles(next), gen.RoleAdmin, gen.RoleOwner)
	} else {
		fmt.Fprintf(&b, "Replace platform roles on %s: %s → %s.", label, joinRoles(previous), joinRoles(next))
		var removed []string
		for _, p := range previous {
			if !slices.Contains(next, p) {
				removed = append(removed, p)
			}
		}
		if len(removed) > 0 {
			fmt.Fprintf(&b, " This REMOVES %s.", joinRoles(removed))
		}
	}
	// Only a platform admin can run this command, so warn when the RESULT
	// leaves the user unable to — not merely when some privileged role is
	// dropped. Replacing [OWNER, ADMIN] with [ADMIN] keeps them able to undo it.
	if !grantsPlatformAdmin(next) && (!previousKnown || grantsPlatformAdmin(previous)) {
		b.WriteString(" The resulting roles cannot run this command, so this user could not undo it themselves.")
	}
	return b.String()
}
