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

func newCmdShare(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "share <command>",
		Aliases: []string{"shares"},
		Short:   "Share a memory with individual users",
	}
	cmd.AddCommand(newCmdShareLs(f))
	cmd.AddCommand(newCmdShareCreate(f))
	cmd.AddCommand(newCmdShareSetRole(f))
	cmd.AddCommand(newCmdShareRevoke(f))
	return cmd
}

func newCmdShareLs(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:     "ls <memory>",
		Aliases: []string{"list"},
		Short:   "List a memory's shares",
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
			resp, err := gen.MemoryShares(cmd.Context(), client, memID)
			if err != nil {
				return api.MapError(err)
			}
			if resp.Memory == nil {
				return exitcode.Newf(exitcode.NotFound, "memory %q not found", args[0])
			}
			shares := make([]shareDTO, 0, len(resp.Memory.Shares))
			for _, s := range resp.Memory.Shares {
				if s == nil {
					continue
				}
				shares = append(shares, shareDTO{Role: string(s.Role), Grantee: userFromMemFields(s.Grantee.MemUserFields)})
			}
			return output.Write(f.IOStreams, f.JSON, shares, func(w io.Writer) error {
				t := output.NewTable(w, "GRANTEE ID", "NAME", "EMAIL", "ROLE")
				for _, s := range shares {
					t.Row(s.Grantee.ID, accessDash(s.Grantee.Name), accessDash(s.Grantee.Email), s.Role)
				}
				return t.Flush()
			})
		},
	}
}

func newCmdShareCreate(f *cmdutil.Factory) *cobra.Command {
	var grantee, role string
	cmd := &cobra.Command{
		Use:     "create <memory> --grantee <user-id> --role <writer|reader>",
		Short:   "Share a memory with a user (or update their share role)",
		Example: `  hadron memory share create acme.com:kb --grantee usr_789 --role reader`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := parseShareRole(role)
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
			resp, err := gen.CreateMemoryShare(cmd.Context(), client, memID, grantee, r)
			if err != nil {
				return api.MapError(err)
			}
			s := resp.CreateMemoryShare.MemoryShare
			return emitShare(f, "✓ shared with", shareDTO{Role: string(s.Role), Grantee: userFromMemFields(s.Grantee.MemUserFields)})
		},
	}
	cmd.Flags().StringVar(&grantee, "grantee", "", "user ID to share with")
	cmd.Flags().StringVar(&role, "role", "", "role: writer or reader")
	_ = cmd.MarkFlagRequired("grantee")
	_ = cmd.MarkFlagRequired("role")
	return cmd
}

func newCmdShareSetRole(f *cmdutil.Factory) *cobra.Command {
	var grantee, role string
	cmd := &cobra.Command{
		Use:     "set-role <memory> --grantee <user-id> --role <writer|reader>",
		Short:   "Change a share's role",
		Example: `  hadron memory share set-role acme.com:kb --grantee usr_789 --role writer`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := parseShareRole(role)
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
			resp, err := gen.UpdateMemoryShareRole(cmd.Context(), client, memID, grantee, r)
			if err != nil {
				return api.MapError(err)
			}
			s := resp.UpdateMemoryShareRole.MemoryShare
			return emitShare(f, "✓ set", shareDTO{Role: string(s.Role), Grantee: userFromMemFields(s.Grantee.MemUserFields)})
		},
	}
	cmd.Flags().StringVar(&grantee, "grantee", "", "grantee user ID")
	cmd.Flags().StringVar(&role, "role", "", "new role: writer or reader")
	_ = cmd.MarkFlagRequired("grantee")
	_ = cmd.MarkFlagRequired("role")
	return cmd
}

func newCmdShareRevoke(f *cmdutil.Factory) *cobra.Command {
	var grantee string
	var yes bool
	cmd := &cobra.Command{
		Use:     "revoke <memory> --grantee <user-id>",
		Short:   "Revoke a user's share on a memory",
		Example: `  hadron memory share revoke acme.com:kb --grantee usr_789 --yes`,
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
			if err := cmdutil.ConfirmDeletion(f.IOStreams, yes, "share for "+grantee+" on memory "+args[0]); err != nil {
				return err
			}
			if _, err := gen.RevokeMemoryShare(cmd.Context(), client, memID, grantee); err != nil {
				return api.MapError(err)
			}
			dto := map[string]string{"memory": args[0], "grantee": grantee, "status": "revoked"}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ revoked share for %s on memory %s\n", grantee, args[0])
				return err
			})
		},
	}
	cmd.Flags().StringVar(&grantee, "grantee", "", "grantee user ID")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	_ = cmd.MarkFlagRequired("grantee")
	return cmd
}
