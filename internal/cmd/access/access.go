// Package access implements `hadron access` — inspecting who can do what.
// Its one subcommand today, `check <user> <resource>`, is the client side of
// hadron-server's authoritative effectiveAccess resolver (#323): it answers
// "what access does this user have to this resource?" without re-deriving the
// server's authorization rules.
package access

import (
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// NewCmdAccess builds the `access` command group.
func NewCmdAccess(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "access <command>",
		Short: "Inspect access permissions",
	}
	cmd.AddCommand(newCmdCheck(f))
	return cmd
}

// resolveUserID turns a user reference (id, email, handle, or an
// hrn:user:/urn:user: URN wrapping one of those) into a User ID, which is the
// only form effectiveAccess(user:) accepts today (the server has no user-URN
// support yet — hadron-server#325). Resolution goes through searchUsers, which
// is itself access-scoped to the caller. A bare token that searchUsers can't
// match is passed through verbatim as a literal id so an explicit id always
// works; an email/handle that matches nothing is a not-found error.
func resolveUserID(cmd *cobra.Command, client graphql.Client, ref string) (string, error) {
	token := strings.TrimSpace(ref)
	for _, p := range []string{"hrn:user:", "urn:user:"} {
		if rest := strings.TrimPrefix(token, p); rest != token {
			token = strings.TrimSpace(rest)
			break
		}
	}
	if token == "" {
		return "", exitcode.Newf(exitcode.Usage, "empty user reference")
	}

	resp, err := gen.SearchUsers(cmd.Context(), client, token)
	if err != nil {
		return "", api.MapError(err)
	}

	// Prefer an exact match on a stable identifier; fall back to a sole fuzzy
	// hit. Ambiguity (multiple non-exact matches) is a usage error rather than
	// an arbitrary pick.
	var exact, fuzzy []*gen.SearchUsersSearchUsersUser
	for _, u := range resp.SearchUsers {
		if u == nil {
			continue
		}
		if userMatchesExactly(u.UserFields, token) {
			exact = append(exact, u)
		}
		fuzzy = append(fuzzy, u)
	}
	switch {
	case len(exact) == 1:
		return exact[0].Id, nil
	case len(exact) > 1:
		return "", ambiguousUserErr(ref, exact)
	case len(fuzzy) == 1:
		return fuzzy[0].Id, nil
	case len(fuzzy) > 1:
		return "", ambiguousUserErr(ref, fuzzy)
	}

	// No matches. An email is unambiguous about intent, so report not-found
	// rather than silently treating it as an id.
	if strings.Contains(token, "@") {
		return "", exitcode.Newf(exitcode.NotFound, "no user matches %q", ref)
	}
	return token, nil
}

func userMatchesExactly(u gen.UserFields, token string) bool {
	if u.Id == token {
		return true
	}
	return eqFold(u.Email, token) || eqFold(u.Handle, token) || eqFold(u.GithubUsername, token)
}

func eqFold(field *string, token string) bool {
	return field != nil && *field != "" && strings.EqualFold(*field, token)
}

func ambiguousUserErr(ref string, matches []*gen.SearchUsersSearchUsersUser) error {
	labels := make([]string, 0, len(matches))
	for _, m := range matches {
		labels = append(labels, m.Id)
	}
	return exitcode.Newf(exitcode.Usage,
		"%q matches multiple users (%s) — pass an exact id", ref, strings.Join(labels, ", "))
}

// normalizeResourceRef shapes a resource reference into what
// effectiveAccess(resource:) expects: a fully-qualified hrn:<type>: URN for a
// memory/node/app/agent, or a bare id for an AiServiceConfig (the one URN-less
// kind). A scheme-prefixed ref passes through verbatim — the server dispatches
// on its type. An unprefixed ref containing ":" is an under-qualified shorthand
// (e.g. acme.com::kb): the server would misread it as a config id, so reject it
// with guidance instead.
func normalizeResourceRef(ref string) (string, error) {
	r := strings.TrimSpace(ref)
	if r == "" {
		return "", exitcode.Newf(exitcode.Usage, "empty resource reference")
	}
	if strings.HasPrefix(r, "hrn:") || strings.HasPrefix(r, "urn:") {
		return r, nil
	}
	if strings.Contains(r, ":") {
		return "", exitcode.Newf(exitcode.Usage,
			"%q is not a fully-qualified resource URN — prefix it with its kind, "+
				"e.g. hrn:memory:%[1]s, hrn:node:%[1]s::<loc>, or hrn:app:/hrn:agent:… "+
				"(a bare, colon-free id is read as an AiServiceConfig id)", r)
	}
	return r, nil
}
