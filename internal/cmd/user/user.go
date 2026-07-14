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

// userDTO is the stable --json shape for a user.
type userDTO struct {
	ID             string   `json:"id"`
	Name           *string  `json:"name"`
	Email          *string  `json:"email"`
	Handle         *string  `json:"handle"`
	GithubUsername *string  `json:"githubUsername"`
	Roles          []string `json:"roles"`
}

func userDTOFromFields(u gen.UserFields) userDTO {
	roles := make([]string, 0, len(u.Roles))
	for _, r := range u.Roles {
		roles = append(roles, string(r))
	}
	return userDTO{ID: u.Id, Name: u.Name, Email: u.Email, Handle: u.Handle, GithubUsername: u.GithubUsername, Roles: roles}
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
		Use:   "search <query>",
		Short: "Search users by handle, GitHub username, or exact email",
		Long: `Search users. Matching is enumeration-safe: substring on handle and
GitHub username, exact on email. Results are name-ascending.`,
		Example: `  hadron user search alice --json`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(args[0]) == "" {
				return exitcode.Newf(exitcode.Usage, "query must not be empty")
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
			var lim, off *int
			if cmd.Flags().Changed("limit") {
				lim = &limit
			}
			if cmd.Flags().Changed("offset") {
				off = &offset
			}
			resp, err := gen.SearchUsers(cmd.Context(), client, args[0], lim, off)
			if err != nil {
				return api.MapError(err)
			}
			users := []userDTO{}
			if resp.Users != nil {
				for _, u := range resp.Users.Items {
					if u == nil {
						continue
					}
					users = append(users, userDTOFromFields(u.UserFields))
				}
			}
			return output.Write(f.IOStreams, f.JSON, users, func(w io.Writer) error {
				t := output.NewTable(w, "ID", "NAME", "EMAIL", "HANDLE", "GITHUB")
				for _, u := range users {
					t.Row(u.ID, dash(u.Name), dash(u.Email), dash(u.Handle), dash(u.GithubUsername))
				}
				return t.Flush()
			})
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 0, "max results (server default when unset)")
	cmd.Flags().IntVar(&offset, "offset", 0, "results to skip")
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
