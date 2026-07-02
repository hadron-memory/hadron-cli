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
	}
	cmd.AddCommand(newCmdMemberLs(f))
	cmd.AddCommand(newCmdMemberAdd(f))
	cmd.AddCommand(newCmdMemberSetRole(f))
	cmd.AddCommand(newCmdMemberRm(f))
	return cmd
}

func newCmdMemberLs(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:     "ls <org-id>",
		Aliases: []string{"list"},
		Short:   "List an organization's members",
		Args:    cobra.ExactArgs(1),
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
					t.Row(m.User.ID, orDash(m.User.Name), orDash(m.User.Email), m.Role)
				}
				return t.Flush()
			})
		},
	}
}

func newCmdMemberAdd(f *cmdutil.Factory) *cobra.Command {
	var user, role string
	cmd := &cobra.Command{
		Use:     "add <org-id> --user <user-id> --role <role>",
		Short:   "Add a user to an organization",
		Example: `  hadron org member add org_123 --user usr_456 --role CONTRIBUTOR`,
		Args:    cobra.ExactArgs(1),
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
	cmd.Flags().StringVar(&user, "user", "", "user ID to add")
	cmd.Flags().StringVar(&role, "role", "", "role: OWNER, ADMIN, CONTRIBUTOR, or READER")
	_ = cmd.MarkFlagRequired("user")
	_ = cmd.MarkFlagRequired("role")
	return cmd
}

func newCmdMemberSetRole(f *cmdutil.Factory) *cobra.Command {
	var user, role string
	cmd := &cobra.Command{
		Use:     "set-role <org-id> --user <user-id> --role <role>",
		Short:   "Change a member's role",
		Example: `  hadron org member set-role org_123 --user usr_456 --role ADMIN`,
		Args:    cobra.ExactArgs(1),
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
	cmd.Flags().StringVar(&user, "user", "", "user ID")
	cmd.Flags().StringVar(&role, "role", "", "new role: OWNER, ADMIN, CONTRIBUTOR, or READER")
	_ = cmd.MarkFlagRequired("user")
	_ = cmd.MarkFlagRequired("role")
	return cmd
}

func newCmdMemberRm(f *cmdutil.Factory) *cobra.Command {
	var user string
	var yes bool
	cmd := &cobra.Command{
		Use:     "rm <org-id> --user <user-id>",
		Aliases: []string{"remove"},
		Short:   "Remove a user from an organization",
		Example: `  hadron org member rm org_123 --user usr_456 --yes`,
		Args:    cobra.ExactArgs(1),
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
	cmd.Flags().StringVar(&user, "user", "", "user ID to remove")
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
