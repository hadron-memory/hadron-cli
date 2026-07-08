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

func newCmdSubscription(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "subscription <command>",
		Aliases: []string{"subscriptions", "sub"},
		Short:   "Manage organization subscriptions to a memory",
		Long: `Manage organization subscriptions to a memory.

A subscription grants an entire organization a role on the memory — the
org-level counterpart to ` + "`memory member`" + ` (team) and ` + "`memory share`" + ` (per
user). The role is the full set: owner, admin, contributor, or reader.`,
	}
	cmd.AddCommand(newCmdSubscriptionLs(f))
	cmd.AddCommand(newCmdSubscriptionCreate(f))
	cmd.AddCommand(newCmdSubscriptionSetRole(f))
	cmd.AddCommand(newCmdSubscriptionRm(f))
	return cmd
}

func newCmdSubscriptionLs(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:     "ls <memory>",
		Aliases: []string{"list"},
		Short:   "List a memory's organization subscriptions",
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
			resp, err := gen.MemorySubscriptions(cmd.Context(), client, memID)
			if err != nil {
				return api.MapError(err)
			}
			if resp.Memory == nil {
				return exitcode.Newf(exitcode.NotFound, "memory %q not found", args[0])
			}
			subs := make([]subscriptionDTO, 0, len(resp.Memory.Subscriptions))
			for _, s := range resp.Memory.Subscriptions {
				if s == nil || s.Organization == nil {
					continue
				}
				subs = append(subs, subscriptionDTO{
					Role:      string(s.Role),
					Activated: s.Activated,
					Organization: orgRef{
						ID: s.Organization.Id, Name: s.Organization.Name, URN: s.Organization.Urn,
					},
				})
			}
			return output.Write(f.IOStreams, f.JSON, subs, func(w io.Writer) error {
				t := output.NewTable(w, "ORG ID", "NAME", "URN", "ROLE", "ACTIVE")
				for _, s := range subs {
					t.Row(s.Organization.ID, s.Organization.Name, s.Organization.URN, s.Role, activeMark(s.Activated))
				}
				return t.Flush()
			})
		},
	}
}

func newCmdSubscriptionCreate(f *cmdutil.Factory) *cobra.Command {
	var org, role string
	cmd := &cobra.Command{
		Use:     "create <memory> --org <org-id> --role <owner|admin|contributor|reader>",
		Short:   "Subscribe an organization to a memory (or update its role)",
		Example: `  hadron memory subscription create acme.com::kb --org partnerco.com --role reader`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := parseSubscriptionRole(role)
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
			resp, err := gen.CreateMemorySubscription(cmd.Context(), client, memID, org, r)
			if err != nil {
				return api.MapError(err)
			}
			if resp.CreateMemorySubscription == nil || resp.CreateMemorySubscription.Organization == nil {
				return exitcode.Newf(exitcode.Error, "server returned no subscription")
			}
			s := resp.CreateMemorySubscription
			return emitSubscription(f, "✓ subscribed", subscriptionDTO{
				Role: string(s.Role), Activated: s.Activated,
				Organization: orgRef{ID: s.Organization.Id, Name: s.Organization.Name, URN: s.Organization.Urn},
			})
		},
	}
	cmd.Flags().StringVar(&org, "org", "", "organization ID or URN to subscribe")
	cmd.Flags().StringVar(&role, "role", "", "role: owner, admin, contributor, or reader")
	_ = cmd.MarkFlagRequired("org")
	_ = cmd.MarkFlagRequired("role")
	return cmd
}

func newCmdSubscriptionSetRole(f *cmdutil.Factory) *cobra.Command {
	var org, role string
	cmd := &cobra.Command{
		Use:     "set-role <memory> --org <org-id> --role <owner|admin|contributor|reader>",
		Short:   "Change an organization subscription's role",
		Example: `  hadron memory subscription set-role acme.com::kb --org partnerco.com --role contributor`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := parseSubscriptionRole(role)
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
			resp, err := gen.UpdateMemorySubscription(cmd.Context(), client, memID, org, r)
			if err != nil {
				return api.MapError(err)
			}
			if resp.UpdateMemorySubscription == nil || resp.UpdateMemorySubscription.Organization == nil {
				return exitcode.Newf(exitcode.Error, "server returned no subscription")
			}
			s := resp.UpdateMemorySubscription
			return emitSubscription(f, "✓ set", subscriptionDTO{
				Role: string(s.Role), Activated: s.Activated,
				Organization: orgRef{ID: s.Organization.Id, Name: s.Organization.Name, URN: s.Organization.Urn},
			})
		},
	}
	cmd.Flags().StringVar(&org, "org", "", "subscribed organization ID or URN")
	cmd.Flags().StringVar(&role, "role", "", "new role: owner, admin, contributor, or reader")
	_ = cmd.MarkFlagRequired("org")
	_ = cmd.MarkFlagRequired("role")
	return cmd
}

func newCmdSubscriptionRm(f *cmdutil.Factory) *cobra.Command {
	var org string
	var yes bool
	cmd := &cobra.Command{
		Use:     "rm <memory> --org <org-id>",
		Aliases: []string{"delete", "unsubscribe"},
		Short:   "Remove an organization's subscription to a memory",
		Example: `  hadron memory subscription rm acme.com::kb --org partnerco.com --yes`,
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
			if err := cmdutil.ConfirmDeletion(f.IOStreams, yes, "subscription for org "+org+" on memory "+args[0]); err != nil {
				return err
			}
			if _, err := gen.DeleteMemorySubscription(cmd.Context(), client, memID, org); err != nil {
				return api.MapError(err)
			}
			dto := map[string]string{"memory": args[0], "org": org, "status": "removed"}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ removed subscription for org %s on memory %s\n", org, args[0])
				return err
			})
		},
	}
	cmd.Flags().StringVar(&org, "org", "", "subscribed organization ID or URN")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	_ = cmd.MarkFlagRequired("org")
	return cmd
}

func emitSubscription(f *cmdutil.Factory, verb string, dto subscriptionDTO) error {
	return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		label := dto.Organization.URN
		if label == "" {
			label = dto.Organization.ID
		}
		_, err := fmt.Fprintf(w, "%s %s as %s\n", verb, label, dto.Role)
		return err
	})
}

func activeMark(b bool) string {
	if b {
		return "✓"
	}
	return "—"
}
