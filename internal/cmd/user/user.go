// Package user implements `hadron user ...` (look up other users) and
// `hadron profile ...` (the signed-in user's own profile). Both surface the
// User shape, so they share a small DTO here.
package user

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

// userDTO is the stable --json shape for a user. The identity fields are
// additive (#325): they let duplicate-account triage see which login each
// account actually authenticates with before `user merge` clears a
// colliding one — see cor:api:010:02.
type userDTO struct {
	ID               string   `json:"id"`
	Name             *string  `json:"name"`
	Email            *string  `json:"email"`
	Handle           *string  `json:"handle"`
	GithubUsername   *string  `json:"githubUsername"`
	Roles            []string `json:"roles"`
	IdentityProvider *string  `json:"identityProvider"`
	GithubID         *int     `json:"githubId"`
	ExternalID       *string  `json:"externalId"`
	ExternalAppID    *string  `json:"externalAppId"`
	LinkedAt         *string  `json:"linkedAt"`
}

func userDTOFromFields(u gen.UserFields) userDTO {
	roles := make([]string, 0, len(u.Roles))
	for _, r := range u.Roles {
		roles = append(roles, string(r))
	}
	return userDTO{
		ID:               u.Id,
		Name:             u.Name,
		Email:            u.Email,
		Handle:           u.Handle,
		GithubUsername:   u.GithubUsername,
		Roles:            roles,
		IdentityProvider: u.IdentityProvider,
		GithubID:         u.GithubId,
		ExternalID:       u.ExternalId,
		ExternalAppID:    u.ExternalAppId,
		LinkedAt:         u.LinkedAt,
	}
}

func dash(s *string) string {
	if s == nil || *s == "" {
		return "—"
	}
	return *s
}

// NewCmdUser builds `hadron user ...`.
func NewCmdUser(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "user <command>",
		Aliases: []string{"users"},
		Short:   "Look up users",
	}
	cmd.AddCommand(newCmdSearch(f))
	cmd.AddCommand(newCmdSetRoles(f))
	cmd.AddCommand(newCmdMerge(f))
	return cmd
}

// newCmdMerge builds `hadron user merge <source> --into <target>` over the
// server's mergeUsers mutation — a global, irreversible consolidation of a
// duplicate source user into a surviving target. The direction is fixed:
// <source> is soft-deleted, --into <target> survives.
func newCmdMerge(f *cmdutil.Factory) *cobra.Command {
	var into string
	var yes bool
	cmd := &cobra.Command{
		Use:   "merge <source> --into <target> [--yes]",
		Short: "Merge a duplicate user into a surviving one (global, irreversible)",
		Long: `Consolidate a duplicate SOURCE user into a surviving TARGET user.

The direction is unmistakable: <source> is consumed and soft-deleted; the
--into <target> survives. Each reference accepts a user id, a bare handle, or a
fully-qualified hrn:user:<handle> URN, and is passed through verbatim — the
server resolves it.

The source's identities, memberships, owned data, credentials, grants, and
connections move to the target; the source loses its unique login identifiers
and is soft-deleted. This is a global, irreversible consolidation with no
server-side dry-run, so it prompts for confirmation on a terminal and requires
--yes to run non-interactively — the confirmation is the last local safety
boundary.

Authorization is enforced by the server (platform ADMIN/OWNER, or an
organization ADMIN/OWNER when source and target are both live members of an
organization you administer); the CLI adds no local authorization or collision
checks. Spec cor:api:010:02.`,
		Example: `  hadron user merge dup-handle --into canonical-handle --yes
  hadron user merge usr_0abc --into alice --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Validate refs before anything else so a bad invocation is a usage
			// error, and confirm before any GraphQL request so a cancellation
			// makes no call. References pass through verbatim; the server resolves
			// and authorizes them.
			// Normalize both refs once (trim surrounding whitespace) and use the
			// normalized values everywhere — the prompt, and the GraphQL variables.
			// MarkFlagRequired below catches a missing --into; this still catches a
			// whitespace-only one.
			source := strings.TrimSpace(args[0])
			into = strings.TrimSpace(into)
			if source == "" {
				return exitcode.Newf(exitcode.Usage, "source user must not be empty")
			}
			if into == "" {
				return exitcode.Newf(exitcode.Usage, "specify the surviving user with --into <ref> (id, handle, or hrn:user:<handle>)")
			}

			prompt := fmt.Sprintf("About to merge user %s into %s. %s will be soft-deleted; this is a global, irreversible consolidation.", source, into, source)
			if err := cmdutil.Confirm(f.IOStreams, yes, prompt); err != nil {
				return err
			}

			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.MergeUsers(cmd.Context(), client, source, into)
			if err != nil {
				return api.MapError(err)
			}
			// mergeUsers is declared User! so a conformant server never returns
			// null without an error, but guard the deref rather than panic on a
			// misbehaving one.
			if resp == nil || resp.MergeUsers == nil {
				return exitcode.Newf(exitcode.Error, "merge returned no user")
			}
			dto := userDTOFromFields(resp.MergeUsers.UserFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				// Lead with the surviving user's stable id (handle/email may be
				// empty), matching how the other user commands identify the entity.
				_, err := fmt.Fprintf(w, "✓ merged %s into surviving user %s (handle: %s, email: %s)\n",
					source, dto.ID, dash(dto.Handle), dash(dto.Email))
				return err
			})
		},
	}
	cmd.Flags().StringVar(&into, "into", "", "the surviving target user (id, handle, or hrn:user:<handle>)")
	_ = cmd.MarkFlagRequired("into")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}

func newCmdSearch(f *cmdutil.Factory) *cobra.Command {
	var limit, offset int
	cmd := &cobra.Command{
		Use:     "search [query]",
		Aliases: []string{"ls", "list"},
		Short:   "Search users, or list them all as a platform admin",
		Long: `Search users. Matching is enumeration-safe: substring on handle and
GitHub username, exact on email. Results are name-ascending.

Omit the query to list every user — the unfiltered listing a platform
ADMIN/OWNER may run (cor:acl:070:02). A caller without platform authority gets
an empty list rather than an error, since the server will not confirm that
accounts it won't show you exist.

Without an explicit --limit/--offset the listing pages to exhaustion, so a
population larger than one 200-row server page is never silently truncated.
Passing either flag selects a single explicit page instead.`,
		Example: `  hadron user search alice --json
  hadron user search --json          # every user (platform admin)`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// An absent query means "list all"; an explicitly empty one is a
			// typo, not a request to enumerate the platform.
			var query *string
			if len(args) == 1 {
				if strings.TrimSpace(args[0]) == "" {
					return exitcode.Newf(exitcode.Usage, "query must not be empty — omit it entirely to list all users")
				}
				query = &args[0]
			}
			if limit < 0 {
				return exitcode.Newf(exitcode.Usage, "limit must be non-negative")
			}
			if offset < 0 {
				return exitcode.Newf(exitcode.Usage, "offset must be non-negative")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}

			fetch := func(lim, off *int) ([]*gen.SearchUsersUsersUsersPageItemsUser, int, error) {
				resp, err := gen.SearchUsers(cmd.Context(), client, query, lim, off)
				if err != nil {
					return nil, 0, api.MapError(err)
				}
				if resp == nil || resp.Users == nil {
					return nil, 0, nil
				}
				return resp.Users.Items, resp.Users.Total, nil
			}

			var items []*gen.SearchUsersUsersUsersPageItemsUser
			paged := cmd.Flags().Changed("limit") || cmd.Flags().Changed("offset")
			if paged {
				var lim, off *int
				if cmd.Flags().Changed("limit") {
					lim = &limit
				}
				if cmd.Flags().Changed("offset") {
					off = &offset
				}
				items, _, err = fetch(lim, off)
			} else {
				items, err = api.CollectAll(func(lim, off int) ([]*gen.SearchUsersUsersUsersPageItemsUser, int, error) {
					return fetch(&lim, &off)
				})
			}
			if err != nil {
				return err
			}

			users := []userDTO{}
			for _, u := range items {
				if u == nil {
					continue
				}
				users = append(users, userDTOFromFields(u.UserFields))
			}
			return output.Write(f.IOStreams, f.JSON, users, func(w io.Writer) error {
				// PROVIDER earns a column because it explains an account's
				// origin — the cheapest signal that two rows are one human who
				// signed in two ways. It does NOT decide what a merge does to a
				// login: auth resolves by provider id then email, and
				// identityProvider is only the provider that created the row
				// (the linking path keeps the original value). The fields that
				// decide that are githubId/email and the --json-only ids.
				t := output.NewTable(w, "ID", "NAME", "EMAIL", "HANDLE", "GITHUB", "PROVIDER")
				for _, u := range users {
					t.Row(u.ID, dash(u.Name), dash(u.Email), dash(u.Handle), dash(u.GithubUsername), dash(u.IdentityProvider))
				}
				if err := t.Flush(); err != nil {
					return err
				}
				if query == nil && len(users) == 0 {
					_, err := fmt.Fprintln(w, "\nNo users listed. The unfiltered listing requires platform ADMIN/OWNER; pass a query to search instead.")
					return err
				}
				return nil
			})
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 0, "max results for a single explicit page (default: page to exhaustion)")
	cmd.Flags().IntVar(&offset, "offset", 0, "results to skip (selects a single explicit page)")
	return cmd
}

// NewCmdProfile builds `hadron profile ...` — the signed-in user's own profile.
func NewCmdProfile(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "profile <command>",
		Aliases: []string{"me"},
		Short:   "Manage your own user profile",
	}
	cmd.AddCommand(newCmdProfileSet(f))
	return cmd
}

func newCmdProfileSet(f *cmdutil.Factory) *cobra.Command {
	var name, email, handle string
	cmd := &cobra.Command{
		Use:     "set [--name <n>] [--email <e>] [--handle <h>]",
		Short:   "Update your own profile (only the fields you pass change)",
		Example: `  hadron profile set --name "Alice A" --handle alice`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			changed := cmd.Flags().Changed
			if !changed("name") && !changed("email") && !changed("handle") {
				return exitcode.Newf(exitcode.Usage, "nothing to update — pass at least one of --name, --email, or --handle")
			}
			// Validate a non-empty handle against the shared urn-lib rule (#692)
			// so a bad handle fails fast with a clear message; an empty value
			// (clear) defers to the server.
			if changed("handle") && handle != "" {
				if err := cmdutil.ValidateUserHandle("--handle", handle); err != nil {
					return err
				}
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			// Send only the fields the caller set, so an unset flag leaves the
			// field unchanged (an explicit --email "" is still sent, to clear it).
			var namePtr, emailPtr, handlePtr *string
			if changed("name") {
				namePtr = &name
			}
			if changed("email") {
				emailPtr = &email
			}
			if changed("handle") {
				handlePtr = &handle
			}
			resp, err := gen.UpdateMyProfile(cmd.Context(), client, namePtr, emailPtr, handlePtr)
			if err != nil {
				return api.MapError(err)
			}
			if resp.UpdateMyProfile == nil {
				return exitcode.Newf(exitcode.Error, "server returned no user")
			}
			dto := userDTOFromFields(resp.UpdateMyProfile.UserFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				// Lead with the stable user ID (email/handle may be empty or just
				// cleared), matching how other update commands identify the entity.
				_, err := fmt.Fprintf(w, "✓ updated your profile %s (handle: %s, email: %s)\n",
					dto.ID, dash(dto.Handle), dash(dto.Email))
				return err
			})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "display name")
	cmd.Flags().StringVar(&email, "email", "", "email address")
	cmd.Flags().StringVar(&handle, "handle", "", "unique handle")
	return cmd
}
