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
// support yet — hadron-server#325). Resolution goes through users(filter:
// { query }) (hadron-server#473, the searchUsers successor), which is itself
// access-scoped to the caller. A bare token the query can't match is passed
// through verbatim as a literal id so an explicit id always works; an
// email/handle that matches nothing is a not-found error.
func resolveUserID(cmd *cobra.Command, client graphql.Client, ref string) (string, error) {
	token := strings.TrimSpace(ref)
	for _, p := range []string{"hrn:user:", "urn:user:"} {
		if rest := strings.TrimPrefix(token, p); rest != token {
			token = strings.TrimSpace(rest)
			break
		}
	}
	// A leading "@" is a handle sigil, not part of the handle — strip it so it
	// matches both the users(filter.query) match and the stored handle/githubUsername.
	// (An email never leads with "@", so this is a no-op for emails.)
	token = strings.TrimPrefix(token, "@")
	if token == "" {
		return "", exitcode.Newf(exitcode.Usage, "empty user reference")
	}

	// users() is name-ascending and served in 200-cap pages, so with many
	// substring matches the EXACT handle/githubUsername/email match can sit
	// on a later page. Page until an exact stable-identifier match is in
	// hand (then stop — later pages can't beat it) or the scope is
	// exhausted; only a fully drained, exact-free set may be judged
	// fuzzy-ambiguous.
	items, err := api.CollectUntil(
		func(limit, offset int) ([]*gen.SearchUsersUsersUsersPageItemsUser, int, error) {
			resp, err := gen.SearchUsers(cmd.Context(), client, token, &limit, &offset)
			if err != nil {
				return nil, 0, api.MapError(err)
			}
			return resp.Users.Items, resp.Users.Total, nil
		},
		func(acc []*gen.SearchUsersUsersUsersPageItemsUser) bool {
			for _, u := range acc {
				if u != nil && userMatchesExactly(u.UserFields, token) {
					return true
				}
			}
			return false
		},
	)
	if err != nil {
		return "", err
	}

	// Prefer an exact match on a stable identifier; fall back to a sole fuzzy
	// hit. Ambiguity (multiple non-exact matches) is a usage error rather than
	// an arbitrary pick.
	var exact, fuzzy []*gen.SearchUsersUsersUsersPageItemsUser
	for _, u := range items {
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

func ambiguousUserErr(ref string, matches []*gen.SearchUsersUsersUsersPageItemsUser) error {
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
			"%q is not a fully-qualified resource URN — prefix it with its kind "+
				"(hrn:memory:, hrn:node:, hrn:app:, or hrn:agent:), e.g. hrn:memory:%[1]s; "+
				"a bare, colon-free id is read as an AiServiceConfig id", r)
	}
	return r, nil
}
