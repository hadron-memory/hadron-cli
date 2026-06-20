package memory

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
		Short:   "Manage a memory's members (team access)",
	}
	cmd.AddCommand(newCmdMemberLs(f))
	cmd.AddCommand(newCmdMemberAdd(f))
	cmd.AddCommand(newCmdMemberSetRole(f))
	cmd.AddCommand(newCmdMemberRm(f))
	return cmd
}

func newCmdMemberLs(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:     "ls <memory>",
		Aliases: []string{"list"},
		Short:   "List a memory's members",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			memID, err := resolveMemoryID(cmd, client, args[0])
			if err != nil {
				return err
			}
			resp, err := gen.MemoryMembers(cmd.Context(), client, memID)
			if err != nil {
				return api.MapError(err)
			}
			if resp.Memory == nil {
				return exitcode.Newf(exitcode.NotFound, "memory %q not found", args[0])
			}
			members := make([]memberDTO, 0, len(resp.Memory.Members))
			for _, m := range resp.Memory.Members {
				if m == nil {
					continue
				}
				members = append(members, memberDTO{Role: string(m.Role), User: userFromMemFields(m.User.MemUserFields)})
			}
			return output.Write(f.IOStreams, f.JSON, members, func(w io.Writer) error {
				t := output.NewTable(w, "USER ID", "NAME", "EMAIL", "ROLE")
				for _, m := range members {
					t.Row(m.User.ID, accessDash(m.User.Name), accessDash(m.User.Email), m.Role)
				}
				return t.Flush()
			})
		},
	}
}

func newCmdMemberAdd(f *cmdutil.Factory) *cobra.Command {
	var user, role string
	cmd := &cobra.Command{
		Use:     "add <memory> --user <user-id> --role <owner|writer|reader>",
		Short:   "Add (or upsert) a member on a memory",
		Example: `  hadron memory member add acme.com:kb --user usr_456 --role writer`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := parseMemberRole(role)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			memID, err := resolveMemoryID(cmd, client, args[0])
			if err != nil {
				return err
			}
			resp, err := gen.AddMemoryMember(cmd.Context(), client, memID, user, r)
			if err != nil {
				return api.MapError(err)
			}
			m := resp.AddMemoryMember.MemoryMember
			return emitMember(f, "✓ added", memberDTO{Role: string(m.Role), User: userFromMemFields(m.User.MemUserFields)})
		},
	}
	cmd.Flags().StringVar(&user, "user", "", "user ID")
	cmd.Flags().StringVar(&role, "role", "", "role: owner, writer, or reader")
	_ = cmd.MarkFlagRequired("user")
	_ = cmd.MarkFlagRequired("role")
	return cmd
}

func newCmdMemberSetRole(f *cmdutil.Factory) *cobra.Command {
	var user, role string
	cmd := &cobra.Command{
		Use:     "set-role <memory> --user <user-id> --role <owner|writer|reader>",
		Short:   "Change a member's role",
		Example: `  hadron memory member set-role acme.com:kb --user usr_456 --role reader`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := parseMemberRole(role)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			memID, err := resolveMemoryID(cmd, client, args[0])
			if err != nil {
				return err
			}
			resp, err := gen.UpdateMemoryMemberRole(cmd.Context(), client, memID, user, r)
			if err != nil {
				return api.MapError(err)
			}
			m := resp.UpdateMemoryMemberRole.MemoryMember
			return emitMember(f, "✓ set", memberDTO{Role: string(m.Role), User: userFromMemFields(m.User.MemUserFields)})
		},
	}
	cmd.Flags().StringVar(&user, "user", "", "user ID")
	cmd.Flags().StringVar(&role, "role", "", "new role: owner, writer, or reader")
	_ = cmd.MarkFlagRequired("user")
	_ = cmd.MarkFlagRequired("role")
	return cmd
}

func newCmdMemberRm(f *cmdutil.Factory) *cobra.Command {
	var user string
	var yes bool
	cmd := &cobra.Command{
		Use:     "rm <memory> --user <user-id>",
		Aliases: []string{"remove"},
		Short:   "Remove a member from a memory",
		Example: `  hadron memory member rm acme.com:kb --user usr_456 --yes`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			memID, err := resolveMemoryID(cmd, client, args[0])
			if err != nil {
				return err
			}
			if err := cmdutil.ConfirmDeletion(f.IOStreams, yes, "member "+user+" from memory "+args[0]); err != nil {
				return err
			}
			if _, err := gen.RemoveMemoryMember(cmd.Context(), client, memID, user); err != nil {
				return api.MapError(err)
			}
			dto := map[string]string{"memory": args[0], "user": user, "status": "removed"}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ removed %s from memory %s\n", user, args[0])
				return err
			})
		},
	}
	cmd.Flags().StringVar(&user, "user", "", "user ID to remove")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	_ = cmd.MarkFlagRequired("user")
	return cmd
}
