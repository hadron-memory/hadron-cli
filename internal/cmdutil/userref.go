package cmdutil

import (
	"fmt"
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
	fields, _, err := ResolveUser(cmd, client, ref)
	if err != nil {
		return "", err
	}
	return fields.Id, nil
}

// ResolveUser is ResolveUserID's underlying lookup, returning everything the
// resolution already read instead of just the id — both paths fetch the full
// UserFields, so a caller that also needs the user's current state (roles,
// handle) gets it without a second round trip.
//
// found reports whether a user was actually matched. It is false only for the
// pass-through case described above, where an unmatched bare token is returned
// verbatim as a literal id and fields carries nothing but that Id — the caller
// decides whether that is enough to act on.
func ResolveUser(cmd *cobra.Command, client graphql.Client, ref string) (fields gen.UserFields, found bool, err error) {
	token, wasQualified, err := normalizeUserToken(ref)
	if err != nil {
		return gen.UserFields{}, false, err
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
				return gen.UserFields{}, false, api.MapError(err)
			}
			if resp != nil && resp.User != nil {
				return resp.User.UserFields, true, nil
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
			// Always a concrete query here — ref resolution never wants the
			// unfiltered admin list.
			resp, err := gen.SearchUsers(cmd.Context(), client, &token, &limit, &offset)
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
		return gen.UserFields{}, false, err
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
		return exact[0].UserFields, true, nil
	case len(exact) > 1:
		return gen.UserFields{}, false, ambiguousUserErr(ref, exact)
	case len(fuzzy) == 1:
		return fuzzy[0].UserFields, true, nil
	case len(fuzzy) > 1:
		return gen.UserFields{}, false, ambiguousUserErr(ref, fuzzy)
	}

	// No matches. An email is unambiguous about intent, so report not-found
	// rather than silently treating it as an id. User search is access-scoped
	// (self / co-members / admin), so a grantee outside the caller's visibility
	// (e.g. sharing a personal memory cross-org) can't be found by email/handle —
	// point the caller at the id, which always works.
	if strings.Contains(token, "@") {
		return gen.UserFields{}, false, exitcode.Newf(exitcode.NotFound, "no user matches %q — if they are outside your organization, pass their user id", ref)
	}
	return gen.UserFields{Id: token}, false, nil
}

// normalizeUserToken strips the scheme prefix and "@" sigil from a user ref,
// reporting whether either was present — a qualified ref names a handle, never
// a PK. Shared by ResolveUser and ResolveUserExactly so both judge the same
// token.
func normalizeUserToken(ref string) (token string, qualified bool, err error) {
	token = strings.TrimSpace(ref)
	for _, p := range []string{"hrn:user:", "urn:user:"} {
		if rest := strings.TrimPrefix(token, p); rest != token {
			token = strings.TrimSpace(rest)
			qualified = true
			break
		}
	}
	// A leading "@" is a handle sigil, not part of the handle — strip it so it
	// matches both the users(filter.query) match and the stored handle/githubUsername.
	// (An email never leads with "@", so this is a no-op for emails.)
	if rest := strings.TrimPrefix(token, "@"); rest != token {
		token = rest
		qualified = true
	}
	if token == "" {
		return "", false, exitcode.Newf(exitcode.Usage, "empty user reference")
	}
	return token, qualified, nil
}

// ResolveUserExactly is ResolveUser restricted to an EXACT identifier match.
//
// ResolveUser deliberately falls back to a sole substring hit, which is right
// for additive operations (sharing a memory with "alic" when only "alice"
// matches is a convenience). It is wrong for a destructive global write: a typo
// would silently retarget a different account. Commands that overwrite or
// remove state should resolve through this instead, so a partial match is a
// usage error naming what it matched rather than a silent retarget.
//
// The pass-through case is preserved: a bare token nothing matched is still
// returned verbatim as a literal id with found=false, so an explicit id the
// caller cannot read still works.
func ResolveUserExactly(cmd *cobra.Command, client graphql.Client, ref string) (fields gen.UserFields, found bool, err error) {
	token, _, err := normalizeUserToken(ref)
	if err != nil {
		return gen.UserFields{}, false, err
	}
	fields, found, err = ResolveUser(cmd, client, ref)
	if err != nil {
		return gen.UserFields{}, false, err
	}
	if found && !userMatchesExactly(fields, token) {
		return gen.UserFields{}, false, exitcode.Newf(exitcode.Usage,
			"%q only matched user %s partially — pass an exact id, handle, or email", ref, DescribeUser(fields))
	}
	return fields, found, nil
}

// DescribeUser renders a user for a confirmation prompt or an error: the id
// always, plus whichever human identifiers exist. The id alone is unreadable
// and the handle alone is not unique enough to confirm against.
func DescribeUser(u gen.UserFields) string {
	labels := []string{}
	if u.Handle != nil && *u.Handle != "" {
		labels = append(labels, "@"+*u.Handle)
	}
	if u.Email != nil && *u.Email != "" {
		labels = append(labels, *u.Email)
	}
	if len(labels) == 0 {
		return u.Id
	}
	return fmt.Sprintf("%s (%s)", u.Id, strings.Join(labels, ", "))
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
