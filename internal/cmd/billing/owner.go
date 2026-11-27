package billing

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// ownerFlags is how a billing command names whose ledger or limits it means: an
// organization (the positional), a person (--user), or yourself (--owner-me).
// Exactly one (cli#3072, cor:dmo:170:01). The CLI decides nothing about who may
// read or set them: the server answers with the same refusal either way.
type ownerFlags struct {
	user string
	me   bool
}

func (o *ownerFlags) register(cmd *cobra.Command) {
	cmd.Flags().StringVar(&o.user, "user", "", "a person's ledger or limits instead of an organization's: an id, @handle, hrn:user: URN or email. Resolved exactly; a service credential that cannot search users should pass the id")
	cmd.Flags().BoolVar(&o.me, "owner-me", false, "your own ledger or limits instead of an organization's")
}

// owner is the resolved selector: exactly one of the two is set, the other nil so
// the request omits it (never an explicit null).
type owner struct {
	orgRef  *string
	userRef *string
}

// label names the owner for a prompt, from what the caller typed: no request is
// made to ask a confirmation.
func (o *ownerFlags) label(args []string) string {
	switch {
	case o.me:
		return "your own ledger"
	case o.user != "":
		return "user " + promptValue(o.user)
	}
	return promptValue(args[0])
}

// check refuses, before any request, a command that does not name exactly one
// owner.
func (o *ownerFlags) check(cmd *cobra.Command, args []string) error {
	named := 0
	if len(args) == 1 {
		if args[0] == "" {
			return exitcode.Newf(exitcode.Usage, "the organization is empty")
		}
		named++
	}
	if cmd.Flags().Changed("user") {
		if o.user == "" {
			return exitcode.Newf(exitcode.Usage, "--user was given but is empty")
		}
		named++
	}
	if o.me {
		named++
	}
	if named != 1 {
		return exitcode.Newf(exitcode.Usage, "name exactly one owner: an organization as the argument, --user <person>, or --owner-me")
	}
	return nil
}

// resolve turns the selector into the refs the request carries. --user resolves
// through the shared exact lookup (a near-miss would show the wrong person's
// money); --owner-me reads the caller once.
func (o *ownerFlags) resolve(cmd *cobra.Command, f *cmdutil.Factory, args []string) (owner, error) {
	if len(args) == 1 {
		return owner{orgRef: &args[0]}, nil
	}
	client, err := f.GraphQLClient()
	if err != nil {
		return owner{}, err
	}
	if o.me {
		resp, err := gen.Me(cmd.Context(), client)
		if err != nil {
			return owner{}, api.MapError(err)
		}
		if resp == nil || resp.Me == nil {
			return owner{}, exitcode.Newf(exitcode.Forbidden, "--owner-me needs a signed-in person: this credential is not one, so it has no ledger or limits of its own. Name the person with --user")
		}
		id := resp.Me.Id
		return owner{userRef: &id}, nil
	}
	fields, _, err := cmdutil.ResolveUserExactly(cmd, client, o.user)
	if err != nil {
		return owner{}, err
	}
	id := fields.Id
	return owner{userRef: &id}, nil
}

// ownerText says whose ledger a result belongs to.
func ownerText(orgID, userID *string) string {
	switch {
	case userID != nil && *userID != "":
		return fmt.Sprintf("user %s", *userID)
	case orgID != nil:
		return fmt.Sprintf("organization %s", *orgID)
	}
	return "an unknown owner"
}
