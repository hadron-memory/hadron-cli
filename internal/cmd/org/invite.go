package org

import (
	"fmt"
	"io"
	"strings"

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
		Long: `Invite people to an organization with a link they accept themselves.

  1. An ADMIN or OWNER runs hadron org invite create; it prints a slug.
  2. You send the slug to the invitee yourself. Hadron sends no email.
  3. The invitee runs hadron org invite show <slug> to see what it is, then
     hadron org invite accept <slug> while signed in.

The slug is the key: anyone signed in who has it can accept, so share it only
with the invitee. To add an existing user directly, use hadron org member add.`,
		Example: `  hadron org invite create alice@partner.com --org acme.com --role CONTRIBUTOR
  hadron org invite show <slug>
  hadron org invite accept <slug>`,
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
		Use:   "create <email> --org <org> --role <role>",
		Short: "Invite a user to an organization",
		Long: `Create an invitation to an organization and print its slug, which the
invitee accepts with hadron org invite accept <slug>. Needs ADMIN or OWNER in
the org; an ADMIN may invite an OWNER.

No email is sent: deliver the slug to the invitee yourself, privately. The
email, --name and --github are recorded so you know who it was for; they are
not checked when it is accepted, so whoever holds the slug can accept it.

By default an invitation can be accepted once and never expires.
--max-activations sets how many times it can be accepted (0 = unlimited);
--expires-days sets how many days it stays valid (0 = never).

Prints "✓ invited <email> as <role>" and "accept with: hadron org invite accept
<slug>"; --json gives the invitation, including slug, maxActivations and
expiresAt. Its id is internal: the slug is what the other commands take.`,
		Example: `  hadron org invite create alice@partner.com --org acme.com --role CONTRIBUTOR
  hadron org invite create team@partner.com --org acme.com --role READER --max-activations 5 --expires-days 7`,
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
	cmd.Flags().StringVar(&org, "org", "", "organization ID or URN (required)")
	cmd.Flags().StringVar(&role, "role", "", "member role: OWNER, ADMIN, CONTRIBUTOR, or READER (required)")
	cmd.Flags().StringVar(&name, "name", "", "invitee display name (optional)")
	cmd.Flags().StringVar(&github, "github", "", "invitee GitHub username (optional)")
	cmd.Flags().IntVar(&expiresDays, "expires-days", 0, "days until the invitation expires (default and 0: never)")
	cmd.Flags().IntVar(&maxActivations, "max-activations", 0, "times the invitation can be accepted (default 1; 0: unlimited)")
	_ = cmd.MarkFlagRequired("org")
	_ = cmd.MarkFlagRequired("role")
	return cmd
}

func newCmdInviteAccept(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "accept <slug>",
		Short: "Accept an organization invitation",
		Long: `Accept an invitation and join the organization with the role it carries.
<slug> is the code the inviter sent you (the 16-character value printed by
invite create), not the invitation id; check it first with hadron org invite
show <slug>.

You must be signed in, as the account that should join: the invitation is not
tied to an email, so it adds whoever accepts it. If you are already a member
your role stays the same, but the acceptance still counts as one use. Someone
removed earlier is reinstated. It does not change your active org.

Prints "✓ accepted invitation <slug>". An expired, used-up or revoked
invitation is refused; an invitation is revoked when its sender leaves the org
or stops being an ADMIN or OWNER.`,
		Example: `  hadron org invite show <slug>
  hadron org invite accept <slug>`,
		Args: cobra.ExactArgs(1),
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
		Long: `Show the public invitation details available to the slug holder.

The result contains the role, activation limit, expiry, sender display, and
organization name. It does not expose the invitee's identity. The slug is
the acceptance token; treat it as a secret. An unknown or revoked slug exits 4.`,
		Example: `  hadron org invite show <slug>
  hadron org invite show <slug> --json`,
		Args: cobra.ExactArgs(1),
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
			inv := resp.Invitation
			dto := publicInvitationDTO{
				Slug: args[0], MemberRole: string(inv.MemberRole),
				MaxActivations: inv.MaxActivations, ActivationCount: inv.ActivationCount,
				ExpiresAt: inv.ExpiresAt,
			}
			if inv.Sender != nil {
				dto.Sender = &publicInvitationSenderDTO{Name: inv.Sender.Name, GithubUsername: inv.Sender.GithubUsername}
			}
			if inv.Organization != nil {
				dto.Organization = &publicInvitationOrganizationDTO{Name: inv.Organization.Name}
			}
			return emitPublicInvitation(f, dto)
		},
	}
}

// The public lookup is a different contract from createUserInvitation. It
// includes only fields valid on both UserInvitation and PublicInvitation, so
// adding a field here requires checking both server schemas.
type publicInvitationDTO struct {
	Slug            string                           `json:"slug"`
	MemberRole      string                           `json:"memberRole"`
	MaxActivations  *int                             `json:"maxActivations"`
	ActivationCount int                              `json:"activationCount"`
	ExpiresAt       *string                          `json:"expiresAt"`
	Sender          *publicInvitationSenderDTO       `json:"sender"`
	Organization    *publicInvitationOrganizationDTO `json:"organization"`
}

type publicInvitationSenderDTO struct {
	Name           *string `json:"name"`
	GithubUsername *string `json:"githubUsername"`
}

type publicInvitationOrganizationDTO struct {
	Name string `json:"name"`
}

func emitPublicInvitation(f *cmdutil.Factory, dto publicInvitationDTO) error {
	return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		orgName := "—"
		if dto.Organization != nil {
			orgName = dto.Organization.Name
		}
		max := "—"
		if dto.MaxActivations != nil {
			max = fmt.Sprint(*dto.MaxActivations)
		}
		_, err := fmt.Fprintf(w,
			"invitation %s\n  organization: %s\n  sent by: %s\n  role: %s\n  activations: %d / %s\n  expires: %s\n  accept with: hadron org invite accept %s\n",
			output.SafeCell(dto.Slug), output.SafeCell(orgName), output.SafeCell(publicInviteSenderLabel(dto.Sender)), output.SafeCell(dto.MemberRole),
			dto.ActivationCount, max, output.SafeCell(output.Dash(dto.ExpiresAt)), output.SafeCell(dto.Slug))
		return err
	})
}

func publicInviteSenderLabel(sender *publicInvitationSenderDTO) string {
	if sender == nil {
		return "—"
	}
	name := ""
	if sender.Name != nil {
		name = strings.TrimSpace(*sender.Name)
	}
	github := ""
	if sender.GithubUsername != nil {
		github = strings.TrimSpace(*sender.GithubUsername)
	}
	switch {
	case name != "" && github != "":
		return name + " (gh:" + github + ")"
	case name != "":
		return name
	case github != "":
		return "gh:" + github
	default:
		return "—"
	}
}

func emitInvitation(f *cmdutil.Factory, dto invitationDTO, verb string) error {
	return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		who := inviteeLabel(dto)
		if verb != "" {
			fmt.Fprintf(w, "%s %s as %s\n", output.SafeCell(verb), output.SafeCell(who), output.SafeCell(dto.MemberRole))
		} else {
			fmt.Fprintf(w, "invitation %s\n  invitee: %s\n  role: %s\n", output.SafeCell(dto.Slug), output.SafeCell(who), output.SafeCell(dto.MemberRole))
		}
		fmt.Fprintf(w, "  accept with: hadron org invite accept %s\n", output.SafeCell(dto.Slug))
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
