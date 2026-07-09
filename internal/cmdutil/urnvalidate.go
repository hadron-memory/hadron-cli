package cmdutil

import (
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// slugAtom mirrors the server's create-time URN atom grammar (spec
// 021-urn-composition FR-016/FR-017, hadron-server src/lib/urn.ts
// `validateAtomShape`): a slug atom is 1–64 characters of [A-Za-z0-9._-] that
// starts and ends with an alphanumeric. This is the check that would have
// rejected `Flow Lab` (issue #189) — a space is not in the charset.
//
// It intentionally accepts uppercase, exactly as the server does today, so a
// value the CLI accepts is one the server accepts (no client/server drift).
// Tightening the grammar to lowercase-only is tracked as hadron-server#575;
// mirror it here once the server does. Reserved-word rejection (FR-019) is left
// to the server for the same anti-drift reason — the CLI checks shape, the
// server remains the authority on policy.
//
// isSlugAtom is hand-rolled rather than a regexp so the hot path stays
// allocation-free and the rule reads literally.
func isSlugAtom(atom string) bool {
	if atom == "" || len(atom) > 64 {
		return false
	}
	for i := 0; i < len(atom); i++ {
		c := atom[i]
		alnum := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
		// First and last character must be alphanumeric; the interior may also
		// carry '.', '_' or '-'.
		if i == 0 || i == len(atom)-1 {
			if !alnum {
				return false
			}
			continue
		}
		if !alnum && c != '.' && c != '_' && c != '-' {
			return false
		}
	}
	return true
}

const slugRule = "1–64 characters of [A-Za-z0-9._-], starting and ending with a letter or digit (no spaces)"

// ValidateURNSlug checks a single URN slug atom (an org or app slug) supplied on
// a create/rename flag, rejecting the malformed input — spaces, empty,
// over-length, illegal characters — that the server would reject anyway, but
// before the network round-trip and with a clearer, flag-anchored message.
// `flag` names the source flag (e.g. "--urn") for the error.
func ValidateURNSlug(flag, slug string) error {
	if !isSlugAtom(slug) {
		return exitcode.Newf(exitcode.Usage, "%s %q is not a valid URN slug — use %s", flag, slug, slugRule)
	}
	return nil
}

// ValidateURNPath checks a colon-delimited URN path (a node loc, or an agent
// slug that may carry an author-org atom) the way the server's
// `validatePathSegment` does: every ':'-separated atom must be a valid slug
// atom. It rejects the empty path, leading/trailing/doubled colons (empty
// atoms), and any atom with a space or illegal character.
func ValidateURNPath(flag, path string) error {
	if path == "" {
		return exitcode.Newf(exitcode.Usage, "%s must not be empty", flag)
	}
	for _, atom := range strings.Split(path, ":") {
		if !isSlugAtom(atom) {
			return exitcode.Newf(exitcode.Usage,
				"%s %q is not valid — each colon-separated segment must be %s (%q is not)", flag, path, slugRule, atom)
		}
	}
	return nil
}
