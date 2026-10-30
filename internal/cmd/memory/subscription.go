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
		Use:     "list <memoryRef>",
		Aliases: []string{"ls"},
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
				if s == nil {
					continue
				}
				dto := subscriptionDTO{Role: string(s.Role), Activated: s.Activated}
				if s.Organization == nil {
					// Surfaced, not dropped: a subscription the caller cannot
					// see the org of still exists (#1246).
					dto.OrganizationHidden = true
				} else {
					dto.Organization = orgRef{ID: s.Organization.Id, Name: s.Organization.Name, URN: s.Organization.Urn}
				}
				subs = append(subs, dto)
			}
			return output.Write(f.IOStreams, f.JSON, subs, func(w io.Writer) error {
				t := output.NewTable(w, "ORG ID", "NAME", "URN", "ROLE", "ACTIVE")
				for _, s := range subs {
					id := output.SafeCell(s.Organization.ID)
					if s.OrganizationHidden {
						id = "(hidden)"
					}
					t.Row(id, output.SafeCell(s.Organization.Name), output.SafeCell(s.Organization.URN), output.SafeCell(s.Role), activeMark(s.Activated))
				}
				return t.Flush()
			})
		},
	}
}

func newCmdSubscriptionCreate(f *cmdutil.Factory) *cobra.Command {
	var org, role string
	cmd := &cobra.Command{
		Use:     "create <memoryRef> --org <org> --role <owner|admin|contributor|reader>",
		Short:   "Subscribe an organization to a memory (or update its role)",
		Example: `  hadron memory subscription create hrn:mem:acme.com:kb --org partnerco.com --role reader`,
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
			if resp.CreateMemorySubscription == nil {
				return exitcode.Newf(exitcode.Error, "server returned no subscription")
			}
			s := resp.CreateMemorySubscription
			var o *orgRef
			if s.Organization != nil {
				o = &orgRef{ID: s.Organization.Id, Name: s.Organization.Name, URN: s.Organization.Urn}
			}
			return emitSubscription(f, "✓ subscribed", mutatedSubscription(s.Role, s.Activated, o, org))
		},
	}
	cmd.Flags().StringVar(&org, "org", "", "organization to subscribe (ID, slug, or hrn:org:<slug> URN) (required)")
	cmd.Flags().StringVar(&role, "role", "", "role: owner, admin, contributor, or reader (required)")
	_ = cmd.MarkFlagRequired("org")
	_ = cmd.MarkFlagRequired("role")
	return cmd
}

func newCmdSubscriptionSetRole(f *cmdutil.Factory) *cobra.Command {
	var org, role string
	cmd := &cobra.Command{
		Use:     "set-role <memoryRef> --org <org> --role <owner|admin|contributor|reader>",
		Short:   "Change an organization subscription's role",
		Example: `  hadron memory subscription set-role hrn:mem:acme.com:kb --org partnerco.com --role contributor`,
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
			if resp.UpdateMemorySubscription == nil {
				return exitcode.Newf(exitcode.Error, "server returned no subscription")
			}
			s := resp.UpdateMemorySubscription
			var o *orgRef
			if s.Organization != nil {
				o = &orgRef{ID: s.Organization.Id, Name: s.Organization.Name, URN: s.Organization.Urn}
			}
			return emitSubscription(f, "✓ set", mutatedSubscription(s.Role, s.Activated, o, org))
		},
	}
	cmd.Flags().StringVar(&org, "org", "", "subscribed organization (ID, slug, or hrn:org:<slug> URN) (required)")
	cmd.Flags().StringVar(&role, "role", "", "new role: owner, admin, contributor, or reader (required)")
	_ = cmd.MarkFlagRequired("org")
	_ = cmd.MarkFlagRequired("role")
	return cmd
}

func newCmdSubscriptionRm(f *cmdutil.Factory) *cobra.Command {
	var org string
	var yes bool
	cmd := &cobra.Command{
		Use:     "rm <memoryRef> --org <org>",
		Aliases: []string{"delete", "unsubscribe"},
		Short:   "Remove an organization's subscription to a memory",
		Example: `  hadron memory subscription rm hrn:mem:acme.com:kb --org partnerco.com --yes`,
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
	cmd.Flags().StringVar(&org, "org", "", "subscribed organization (ID, slug, or hrn:org:<slug> URN) (required)")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	_ = cmd.MarkFlagRequired("org")
	return cmd
}

// mutatedSubscription builds the create/set-role receipt. A nil organization
// is the server withholding an org the caller may not read, after the write
// committed (#1246): report success, labelled by the caller's own --org.
func mutatedSubscription(role gen.Role, activated bool, o *orgRef, org string) subscriptionDTO {
	dto := subscriptionDTO{Role: string(role), Activated: activated}
	if o == nil {
		dto.OrganizationHidden = true
		dto.OrgRef = org
		return dto
	}
	dto.Organization = *o
	return dto
}

func emitSubscription(f *cmdutil.Factory, verb string, dto subscriptionDTO) error {
	return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		label := dto.Organization.URN
		if label == "" {
			label = dto.Organization.ID
		}
		if label == "" {
			label = dto.OrgRef
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
