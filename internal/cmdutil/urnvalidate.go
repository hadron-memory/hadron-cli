package cmdutil

import (
	"errors"
	"fmt"
	"strings"

	urn "github.com/hadron-memory/urn-lib-go"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

const slugRule = "a valid Hadron URN slug atom"

func urnUsageError(flag, value string, err error) error {
	var pe *urn.ParseError
	if errors.As(err, &pe) {
		if pe.OffendingSegment != "" && pe.OffendingSegment != value {
			return exitcode.Newf(exitcode.Usage, "%s %q is not valid: %s at %q", flag, value, pe.Reason, pe.OffendingSegment)
		}
		return exitcode.Newf(exitcode.Usage, "%s %q is not valid: %s", flag, value, pe.Reason)
	}
	return exitcode.Newf(exitcode.Usage, "%s %q is not valid: %v", flag, value, err)
}

// ValidateURNSlug checks a single URN slug atom supplied on a create/rename
// flag. The shared urn-lib owns the create-time slug policy.
func ValidateURNSlug(flag, slug string) error {
	if err := urn.ValidateUserSlug(slug); err != nil {
		return urnUsageError(flag, slug, err)
	}
	return nil
}

// ValidateURNPath checks a colon-delimited node loc. A loc is a single
// hierarchy leaf whose atoms are separated by single colons.
func ValidateURNPath(flag, path string) error {
	if path == "" {
		return exitcode.Newf(exitcode.Usage, "%s must not be empty", flag)
	}
	for _, atom := range strings.Split(path, ":") {
		if err := urn.ValidateAtomShape(path, atom); err != nil {
			return exitcode.Newf(exitcode.Usage,
				"%s %q is not valid — each colon-separated segment must be %s (%q is not)", flag, path, slugRule, atom)
		}
	}
	return nil
}

// ValidateAgentURNPath checks an agent slug path supplied as a partial URN
// component. Unlike a node loc, an agent path may include owner/author context;
// urn-lib-go owns those shape rules.
func ValidateAgentURNPath(flag, path string) error {
	if path == "" {
		return exitcode.Newf(exitcode.Usage, "%s must not be empty", flag)
	}
	check := strings.TrimPrefix(path, "agent:")
	if strings.HasPrefix(check, "@") && !strings.Contains(check, ":") {
		return exitcode.Newf(exitcode.Usage, "%s %q is not valid: handle owner context requires a slug", flag, path)
	}
	if _, err := urn.FormatCanonicalUrn("agent", check); err != nil {
		return urnUsageError(flag, path, err)
	}
	return nil
}

// CanonicalizeURN validates a scheme-prefixed Hadron URN and returns its
// parser-canonical form. It is intentionally kept as a thin wrapper for the
// spec-047 golden tests, which pin CLI parser parity to urn-lib-go.
func CanonicalizeURN(flag, input string) (string, error) {
	if !urn.HasSchemePrefix(input) {
		return "", exitcode.Newf(exitcode.Usage, "%s %q is not a valid URN: malformed grammar", flag, input)
	}
	canon, err := urn.ToParserCanonical(input)
	if err != nil {
		return "", urnUsageError(flag, input, err)
	}
	if canon == "" {
		return "", fmt.Errorf("canonicalizing %s: empty result", input)
	}
	return canon, nil
}
