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

func newCmdInvite(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "invite <command>",
		Aliases: []string{"invitation", "invitations"},
		Short:   "Create, inspect, and accept organization invitations",
	}
	cmd.AddCommand(newCmdInviteCreate(f))
	cmd.AddCommand(newCmdInviteAccept(f))
	cmd.AddCommand(newCmdInviteShow(f))
	return cmd
}

func newCmdInviteCreate(f *cmdutil.Factory) *cobra.Command {
	var org, role, name, github string
	var expiresDays, maxActivations int
	cmd := &cobra.Command{
		Use:   "create <email> --org <org-id> --role <role>",
		Short: "Invite a user to an organization",
		Long: `Invite a user to an organization by email.

The returned "slug" is the acceptance token — the invitee redeems it with
'hadron org invite accept <slug>'.`,
		Example: `  hadron org invite create alice@partner.com --org acme.com --role CONTRIBUTOR`,
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
			email := args[0]
			var namePtr, githubPtr *string
			if name != "" {
				namePtr = &name
			}
			if github != "" {
				githubPtr = &github
			}
			// Only send expiry/activation bounds the caller actually set, so an
			// unset flag leaves the server's default rather than forcing 0.
			var expPtr, maxPtr *int
			if cmd.Flags().Changed("expires-days") {
				expPtr = &expiresDays
			}
			if cmd.Flags().Changed("max-activations") {
				maxPtr = &maxActivations
			}
			resp, err := gen.CreateUserInvitation(cmd.Context(), client, org, r, &email, namePtr, githubPtr, expPtr, maxPtr)
			if err != nil {
				return api.MapError(err)
			}
			if resp.CreateUserInvitation == nil {
				return exitcode.Newf(exitcode.Error, "server returned no invitation")
			}
			return emitInvitation(f, invDTOFromFields(resp.CreateUserInvitation.InvitationFields), "✓ invited")
		},
	}
	cmd.Flags().StringVar(&org, "org", "", "organization ID or URN")
	cmd.Flags().StringVar(&role, "role", "", "member role: OWNER, ADMIN, CONTRIBUTOR, or READER")
	cmd.Flags().StringVar(&name, "name", "", "invitee display name (optional)")
	cmd.Flags().StringVar(&github, "github", "", "invitee GitHub username (optional)")
	cmd.Flags().IntVar(&expiresDays, "expires-days", 0, "days until the invitation expires (server default when unset)")
	cmd.Flags().IntVar(&maxActivations, "max-activations", 0, "max times the invitation can be accepted (server default when unset)")
	_ = cmd.MarkFlagRequired("org")
	_ = cmd.MarkFlagRequired("role")
	return cmd
}

func newCmdInviteAccept(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:     "accept <slug>",
		Short:   "Accept an organization invitation",
		Example: `  hadron org invite accept inv_abc123`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.AcceptInvitation(cmd.Context(), client, args[0])
			if err != nil {
				return api.MapError(err)
			}
			// A `false` return is a failed accept (expired / already used / revoked)
			// — surface it as a non-zero exit so automation doesn't read it as
			// success, rather than printing a cheerful line and exiting 0.
			if !resp.AcceptInvitation {
				return exitcode.Newf(exitcode.Error, "invitation %s was not accepted (it may be expired, already used, or revoked)", args[0])
			}
			dto := map[string]any{"slug": args[0], "accepted": true}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ accepted invitation %s\n", args[0])
				return err
			})
		},
	}
}

func newCmdInviteShow(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:     "show <slug>",
		Aliases: []string{"get"},
		Short:   "Show an organization invitation",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.GetInvitation(cmd.Context(), client, args[0])
			if err != nil {
				return api.MapError(err)
			}
			if resp.Invitation == nil {
				return exitcode.Newf(exitcode.NotFound, "invitation %q not found", args[0])
			}
			return emitInvitation(f, invDTOFromFields(resp.Invitation.InvitationFields), "")
		},
	}
}

func emitInvitation(f *cmdutil.Factory, dto invitationDTO, verb string) error {
	return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		who := inviteeLabel(dto)
		if verb != "" {
			fmt.Fprintf(w, "%s %s as %s\n", verb, who, dto.MemberRole)
		} else {
			fmt.Fprintf(w, "invitation %s\n  invitee: %s\n  role: %s\n", dto.Slug, who, dto.MemberRole)
		}
		fmt.Fprintf(w, "  accept with: hadron org invite accept %s\n", dto.Slug)
		return nil
	})
}

// inviteeLabel renders who an invitation is for: email, GitHub handle, or both —
// never a bare dash next to a real handle.
func inviteeLabel(dto invitationDTO) string {
	hasEmail := dto.Email != nil && *dto.Email != ""
	hasGithub := dto.GithubUsername != nil && *dto.GithubUsername != ""
	switch {
	case hasEmail && hasGithub:
		return *dto.Email + " (gh:" + *dto.GithubUsername + ")"
	case hasEmail:
		return *dto.Email
	case hasGithub:
		return "gh:" + *dto.GithubUsername
	default:
		return "—"
	}
}
