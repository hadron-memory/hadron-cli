package cmdutil

import (
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// ResolveUserID turns a user reference (id, email, handle, or an
// hrn:user:/urn:user: URN wrapping one of those) into a User ID — the form the
// server's user-keyed mutations accept today (there is no user-URN support yet,
// hadron-server#325). An id- or handle-shaped ref resolves via the uniform
// user(ref:) find-one first (one round trip, no paging); anything else — or a
// find-one miss — goes through users(filter: { query }), which is itself
// access-scoped to the caller. A bare token neither path can match is passed
// through verbatim as a literal id so an explicit id always works; an
// email/handle that matches nothing is a not-found error.
//
// Shared by `access check` (resource authorization) and `memory share`
// (grantee), so both accept the same ref forms (hadron-cli#280).
func ResolveUserID(cmd *cobra.Command, client graphql.Client, ref string) (string, error) {
	token := strings.TrimSpace(ref)
	wasQualified := false // an hrn:user:/urn:user: or @-sigil ref names a handle, never a PK
	for _, p := range []string{"hrn:user:", "urn:user:"} {
		if rest := strings.TrimPrefix(token, p); rest != token {
			token = strings.TrimSpace(rest)
			wasQualified = true
			break
		}
	}
	// A leading "@" is a handle sigil, not part of the handle — strip it so it
	// matches both the users(filter.query) match and the stored handle/githubUsername.
	// (An email never leads with "@", so this is a no-op for emails.)
	if rest := strings.TrimPrefix(token, "@"); rest != token {
		token = rest
		wasQualified = true
	}
	if token == "" {
		return "", exitcode.Newf(exitcode.Usage, "empty user reference")
	}

	// Fast path (PR #133 review): user(ref:) dispatches a PK or an
	// hrn:user:<handle> URN in one round trip, with denied and missing both
	// null — so try the exact reads before any paged search. Emails are
	// excluded (user(ref:) never matches an email); a bare token could be
	// either a PK or a handle, so try both forms.
	if !strings.Contains(token, "@") {
		refs := []string{"hrn:user:" + token}
		if !wasQualified {
			refs = []string{token, "hrn:user:" + token}
		}
		for _, r := range refs {
			resp, err := gen.GetUser(cmd.Context(), client, r)
			if err != nil {
				return "", api.MapError(err)
			}
			if resp != nil && resp.User != nil {
				return resp.User.Id, nil
			}
		}
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
			if resp == nil || resp.Users == nil {
				return nil, 0, nil
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
	// rather than silently treating it as an id. User search is access-scoped
	// (self / co-members / admin), so a grantee outside the caller's visibility
	// (e.g. sharing a personal memory cross-org) can't be found by email/handle —
	// point the caller at the id, which always works.
	if strings.Contains(token, "@") {
		return "", exitcode.Newf(exitcode.NotFound, "no user matches %q — if they are outside your organization, pass their user id", ref)
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
