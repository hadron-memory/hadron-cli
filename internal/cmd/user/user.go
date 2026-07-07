// Package user implements `hadron user ...` (look up other users) and
// `hadron profile ...` (the signed-in user's own profile). Both surface the
// User shape, so they share a small DTO here.
package user

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
				_, err := fmt.Fprintf(w, "✓ updated your profile (%s)\n", dash(dto.Email))
				return err
			})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "display name")
	cmd.Flags().StringVar(&email, "email", "", "email address")
	cmd.Flags().StringVar(&handle, "handle", "", "unique handle")
	return cmd
}
