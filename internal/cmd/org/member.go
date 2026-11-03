package org

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

func newCmdMember(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "member <command>",
		Aliases: []string{"members"},
		Short:   "Manage an organization's members",
		Long: `See and change who belongs to an organization, and with which role.

member add puts an existing user in the org at once, with no invitation and
no notification. To bring in someone by a link they accept themselves, use
hadron org invite create. Adding, changing and removing members needs ADMIN or
OWNER in the org. Roles, highest first: OWNER, ADMIN, CONTRIBUTOR, READER.`,
		Example: `  hadron org member list acme.com
  hadron org member add acme.com --user alice@acme.com --role CONTRIBUTOR`,
	}
	cmd.AddCommand(newCmdMemberLs(f))
	cmd.AddCommand(newCmdMemberAdd(f))
	cmd.AddCommand(newCmdMemberSetRole(f))
	cmd.AddCommand(newCmdMemberRm(f))
	return cmd
}

func newCmdMemberLs(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:     "list <org>",
		Aliases: []string{"ls"},
		Short:   "List an organization's members",
		Long: `List every member of an organization (the whole list, not a page). You
must be a member yourself, or a platform admin. <org> is its id, URN or root.

The table shows USER ID, NAME, EMAIL and ROLE. ROLE is filled in only for your
own row, unless you are an ADMIN or OWNER of the org or a platform admin.
--json gives an array of {id, role, canInvite, user}; the user id is what
member set-role and member rm take.`,
		Example: `  hadron org member list acme.com
  hadron org member list hrn:org:acme.com --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.OrgMembers(cmd.Context(), client, args[0])
			if err != nil {
				return api.MapError(err)
			}
			if resp.Organization == nil {
				return exitcode.Newf(exitcode.NotFound, "organization %q not found", args[0])
			}
			members := make([]memberDTO, 0, len(resp.Organization.Members))
			for _, m := range resp.Organization.Members {
				if m == nil || m.User == nil {
					continue
				}
				ci := m.CanInvite
				members = append(members, memberDTO{
					ID:        m.Id,
					Role:      roleString(m.Role),
					CanInvite: &ci,
					User:      userDTOFromFields(m.User.UserFields),
				})
			}
			return output.Write(f.IOStreams, f.JSON, members, func(w io.Writer) error {
				t := output.NewTable(w, "USER ID", "NAME", "EMAIL", "ROLE")
				for _, m := range members {
					t.Row(output.SafeCell(m.User.ID), output.SafeCell(orDash(m.User.Name)), output.SafeCell(orDash(m.User.Email)), output.SafeCell(m.Role))
				}
				return t.Flush()
			})
		},
	}
}

func newCmdMemberAdd(f *cmdutil.Factory) *cobra.Command {
	var user, role string
	cmd := &cobra.Command{
		Use:   "add <org> --user <user> --role <role>",
		Short: "Add a user to an organization",
		Long: `Add an existing user to an organization with a role, at once. No invitation
is created and no message is sent. Needs ADMIN or OWNER in the org; an ADMIN
may add an OWNER.

<org> is the org's id, URN or root. --user is the user's id, email, handle or
hrn:user:<handle> (hadron user search finds them). Someone removed earlier is
reinstated. A user who is already a member is refused: use member set-role to
change their role.`,
		Example: `  hadron user search alice
  hadron org member add acme.com --user alice@acme.com --role CONTRIBUTOR`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := parseRole(role)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.AddOrgMember(cmd.Context(), client, args[0], user, r)
			if err != nil {
				return api.MapError(err)
			}
			if resp.AddOrgMember == nil {
				return exitcode.Newf(exitcode.Error, "server returned no member")
			}
			return emitMember(f, "✓ added", resp.AddOrgMember.Id, roleString(resp.AddOrgMember.Role), resp.AddOrgMember.User.UserFields)
		},
	}
	cmd.Flags().StringVar(&user, "user", "", "user id, email, handle or hrn:user URN (required)")
	cmd.Flags().StringVar(&role, "role", "", "role: OWNER, ADMIN, CONTRIBUTOR, or READER (required)")
	_ = cmd.MarkFlagRequired("user")
	_ = cmd.MarkFlagRequired("role")
	return cmd
}

func newCmdMemberSetRole(f *cmdutil.Factory) *cobra.Command {
	var user, role string
	cmd := &cobra.Command{
		Use:   "set-role <org> --user <user> --role <role>",
		Short: "Change a member's role",
		Long: `Replace a member's org role; the change applies at once and there is no
prompt. Needs ADMIN or OWNER in the org; an ADMIN may promote to or demote from
OWNER. The org's last OWNER cannot be demoted. Demoting an ADMIN or OWNER to
CONTRIBUTOR or READER revokes the invitations they sent.

<org> is the org's id, URN or root; --user is the member's id, email, handle
or hrn:user:<handle> (see hadron org member list). Someone who is not a member
is refused: use member add.`,
		Example: `  hadron org member list acme.com
  hadron org member set-role acme.com --user alice@acme.com --role ADMIN`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := parseRole(role)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.UpdateOrgMember(cmd.Context(), client, args[0], user, r)
			if err != nil {
				return api.MapError(err)
			}
			if resp.UpdateOrgMember == nil {
				return exitcode.Newf(exitcode.Error, "server returned no member")
			}
			return emitMember(f, "✓ set", resp.UpdateOrgMember.Id, roleString(resp.UpdateOrgMember.Role), resp.UpdateOrgMember.User.UserFields)
		},
	}
	cmd.Flags().StringVar(&user, "user", "", "member's user id, email, handle or hrn:user URN (required)")
	cmd.Flags().StringVar(&role, "role", "", "new role: OWNER, ADMIN, CONTRIBUTOR, or READER (required)")
	_ = cmd.MarkFlagRequired("user")
	_ = cmd.MarkFlagRequired("role")
	return cmd
}

func newCmdMemberRm(f *cmdutil.Factory) *cobra.Command {
	var user string
	var yes bool
	cmd := &cobra.Command{
		Use:     "rm <org> --user <user>",
		Aliases: []string{"remove"},
		Short:   "Remove a user from an organization",
		Long: `Remove a member from an organization. Needs ADMIN or OWNER in the org;
you may remove yourself. The org's last OWNER cannot be removed.

The member loses the access their membership gave them, at once. The
invitations they sent are revoked, and their grants in the org stop applying
(they apply again if the user is re-added before they expire). Their content
is not touched. Removing someone already removed succeeds again.

<org> is the org's id, URN or root; --user is the member's id, email, handle
or hrn:user:<handle>. On a terminal it asks for confirmation; in a script or
agent pass --yes, or it refuses with exit 2.`,
		Example: `  hadron org member list acme.com
  hadron org member rm acme.com --user alice@acme.com
  hadron org member rm acme.com --user alice@acme.com --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			if err := cmdutil.ConfirmDeletion(f.IOStreams, yes, "member "+user+" from organization "+args[0]); err != nil {
				return err
			}
			if _, err := gen.RemoveOrgMember(cmd.Context(), client, args[0], user); err != nil {
				return api.MapError(err)
			}
			dto := map[string]string{"org": args[0], "user": user, "status": "removed"}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ removed %s from organization %s\n", user, args[0])
				return err
			})
		},
	}
	cmd.Flags().StringVar(&user, "user", "", "member's user id, email, handle or hrn:user URN (required)")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	_ = cmd.MarkFlagRequired("user")
	return cmd
}

// roleString renders a nullable OrgMember.role (#384 field-level visibility:
// the server nulls it when the viewer isn't an org ADMIN/OWNER). An empty
// string keeps the --json Role field's type stable.
func roleString(r *gen.Role) string {
	if r == nil {
		return ""
	}
	return string(*r)
}

// emitMember renders an add/set-role result (the OrgMember projection lacks
// canInvite, so the DTO leaves it null here).
func emitMember(f *cmdutil.Factory, verb, memberID, role string, user gen.UserFields) error {
	dto := memberDTO{ID: memberID, Role: role, User: userDTOFromFields(user)}
	return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		label := dto.User.ID
		switch {
		case dto.User.Email != nil && *dto.User.Email != "":
			label = *dto.User.Email
		case dto.User.Name != nil && *dto.User.Name != "":
			label = *dto.User.Name
		}
		_, err := fmt.Fprintf(w, "%s %s as %s\n", verb, label, role)
		return err
	})
}
