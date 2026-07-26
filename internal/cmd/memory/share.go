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
	cmd.AddCommand(newCmdShareRm(f))
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
				if s == nil || s.Grantee == nil {
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
		Use:   "create <memory> --grantee <user> --role <writer|reader>",
		Short: "Share a memory with a user (or update their share role)",
		Example: `  hadron memory share create acme.com::kb --grantee usr_789 --role reader
  hadron memory share create acme.com::kb --grantee jane@acme.com --role writer
  hadron memory share create acme.com::kb --grantee @jane --role reader`,
		Args: cobra.ExactArgs(1),
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
			granteeID, err := cmdutil.ResolveUserID(cmd, client, grantee)
			if err != nil {
				return err
			}
			resp, err := gen.CreateMemoryShare(cmd.Context(), client, memID, granteeID, r)
			if err != nil {
				return api.MapError(err)
			}
			if resp.CreateMemoryShare == nil || resp.CreateMemoryShare.MemoryShare == nil {
				return exitcode.Newf(exitcode.Error, "server returned no share")
			}
			s := resp.CreateMemoryShare.MemoryShare
			return emitShare(f, "✓ shared with", shareDTO{Role: string(s.Role), Grantee: userFromMemFields(s.Grantee.MemUserFields)})
		},
	}
	cmd.Flags().StringVar(&grantee, "grantee", "", "user to share with (ID, email, handle, or hrn:user:<handle>)")
	cmd.Flags().StringVar(&role, "role", "", "role: writer or reader")
	_ = cmd.MarkFlagRequired("grantee")
	_ = cmd.MarkFlagRequired("role")
	return cmd
}

func newCmdShareSetRole(f *cmdutil.Factory) *cobra.Command {
	var grantee, role string
	cmd := &cobra.Command{
		Use:     "set-role <memory> --grantee <user> --role <writer|reader>",
		Short:   "Change a share's role",
		Example: `  hadron memory share set-role acme.com::kb --grantee usr_789 --role writer`,
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
			granteeID, err := cmdutil.ResolveUserID(cmd, client, grantee)
			if err != nil {
				return err
			}
			resp, err := gen.UpdateMemoryShareRole(cmd.Context(), client, memID, granteeID, r)
			if err != nil {
				return api.MapError(err)
			}
			if resp.UpdateMemoryShareRole == nil || resp.UpdateMemoryShareRole.MemoryShare == nil {
				return exitcode.Newf(exitcode.Error, "server returned no share")
			}
			s := resp.UpdateMemoryShareRole.MemoryShare
			return emitShare(f, "✓ set", shareDTO{Role: string(s.Role), Grantee: userFromMemFields(s.Grantee.MemUserFields)})
		},
	}
	cmd.Flags().StringVar(&grantee, "grantee", "", "grantee user (ID, email, handle, or hrn:user:<handle>)")
	cmd.Flags().StringVar(&role, "role", "", "new role: writer or reader")
	_ = cmd.MarkFlagRequired("grantee")
	_ = cmd.MarkFlagRequired("role")
	return cmd
}

func newCmdShareRm(f *cmdutil.Factory) *cobra.Command {
	var grantee string
	var yes bool
	cmd := &cobra.Command{
		Use:     "rm <memory> [--grantee <user>]",
		Aliases: []string{"revoke", "delete"},
		Short:   "Remove a share on a memory (yours, or a grantee's if you own it)",
		Long: `Remove a MemoryShare.

With --grantee, the memory's owner removes that user's share. Without it, you
remove YOUR OWN share of a memory someone shared with you (leave) — that path
needs no ownership, only that the share is yours.`,
		Example: `  hadron memory share rm acme.com::kb --grantee usr_789 --yes
  hadron memory share rm acme.com::kb --yes   # leave a memory shared with you`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			memID, err := resolveMemoryID(cmd, client, args[0])
			if err != nil {
				return err
			}
			// Resolve (and validate) the grantee ref before prompting, so a
			// typo fails fast rather than after the confirmation. No --grantee
			// means "mine": leave granteeID nil and the server keys the delete
			// on the caller (hadron-server#785).
			var granteeID *string
			target := "your share"
			if grantee != "" {
				id, err := cmdutil.ResolveUserID(cmd, client, grantee)
				if err != nil {
					return err
				}
				granteeID = &id
				target = "share for " + grantee
			}
			if err := cmdutil.ConfirmDeletion(f.IOStreams, yes, target+" on memory "+args[0]); err != nil {
				return err
			}
			if _, err := gen.DeleteMemoryShare(cmd.Context(), client, memID, granteeID); err != nil {
				return api.MapError(err)
			}
			dto := map[string]string{"memory": args[0], "grantee": grantee, "status": "removed"}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ removed %s on memory %s\n", target, args[0])
				return err
			})
		},
	}
	cmd.Flags().StringVar(&grantee, "grantee", "", "grantee user (ID, email, handle, or hrn:user:<handle>); omit to remove your own share")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}
