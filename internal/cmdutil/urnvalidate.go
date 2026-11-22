package cmdutil

import (
	"errors"
	"strings"

	urn "github.com/hadron-memory/urn-lib-go"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

const slugRule = "1-64 characters of [A-Za-z0-9._-], starting and ending with a letter or digit"

// urnUsageError refuses a flag value without echoing it (cli#2902, the #2898 policy): at flag validation no
// secret is known to redact, so a credential pasted in the wrong place would print to the terminal, the
// --json envelope and CI logs. It names the flag and the library's reason (a rule, never the input) and says
// the value is not shown; the operator has the value in their own command line. The offending segment is
// dropped for the same reason. pe.Reason is the urn-lib's typed Reason, a fixed set of machine-stable codes
// ("invalid-charset", "reserved-word-slug", ...; urn-lib-go errors.go: they are part of its fixture contract),
// never a fragment of the input, and TestURNValidatorsNeverEchoTheValue2902 checks every segment of a
// token-shaped value against the message, so a Reason that began to carry input would fail it.
func urnUsageError(flag string, err error) error {
	var pe *urn.ParseError
	if errors.As(err, &pe) {
		return exitcode.Newf(exitcode.Usage, "%s is not valid (the value given is not shown): %s", flag, pe.Reason)
	}
	return exitcode.Newf(exitcode.Usage, "%s is not valid (the value given is not shown)", flag)
}

// ValidateURNSlug checks a single URN slug atom supplied on a create/rename
// flag. The shared urn-lib owns the create-time slug policy.
func ValidateURNSlug(flag, slug string) error {
	if err := urn.ValidateUserSlug(slug); err != nil {
		return urnUsageError(flag, err)
	}
	return nil
}

// ValidateOrgSlug checks an organization slug supplied on a create/rename flag.
// An org root must be bare (no scheme prefix or colon), satisfy the shared slug
// rules, and be neither reserved (hrn/urn) nor ID-shaped; dotted and undotted
// names are both allowed (cli#839, hadron-server#1733). The shared urn-lib owns
// the policy, so the CLI fails fast with the same rule the server enforces
// rather than surfacing a less actionable server error.
func ValidateOrgSlug(flag, slug string) error {
	if err := urn.ValidateOrgSlug(slug); err != nil {
		return urnUsageError(flag, err)
	}
	return nil
}

// ValidateUserHandle checks a user handle supplied on a create/rename flag. A
// handle follows the same root policy as an org (not reserved, not ID-shaped)
// and must additionally be dot-free (#692). The shared urn-lib owns the policy.
func ValidateUserHandle(flag, handle string) error {
	if err := urn.ValidateUserHandle(handle); err != nil {
		return urnUsageError(flag, err)
	}
	return nil
}

// ValidateURNPath checks a colon-delimited node loc. A loc is a single
// hierarchy leaf whose atoms are separated by single colons.
func ValidateURNPath(flag, path string) error {
	if path == "" {
		return exitcode.Newf(exitcode.Usage, "%s must not be empty", flag)
	}
	for i, atom := range strings.Split(path, ":") {
		if err := urn.ValidateAtomShape(path, atom); err != nil {
			// The segment is named by position, never by text (cli#2902).
			return exitcode.Newf(exitcode.Usage,
				"%s is not valid (the value given is not shown): each colon-separated segment must be %s, and segment %d is not", flag, slugRule, i+1)
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
		return exitcode.Newf(exitcode.Usage, "%s is not valid (the value given is not shown): handle owner context requires a slug", flag)
	}
	if _, err := urn.FormatCanonicalUrn("agent", check); err != nil {
		return urnUsageError(flag, err)
	}
	return nil
}

// CanonicalizeURN validates a scheme-prefixed Hadron URN and returns its
// parser-canonical form. It is intentionally kept as a thin wrapper for the
// spec-047 golden tests, which pin CLI parser parity to urn-lib-go.
func CanonicalizeURN(flag, input string) (string, error) {
	if !urn.HasSchemePrefix(input) {
		return "", exitcode.Newf(exitcode.Usage, "%s is not a valid URN (the value given is not shown): malformed grammar", flag)
	}
	canon, err := urn.ToParserCanonical(input)
	if err != nil {
		return "", urnUsageError(flag, err)
	}
	if canon == "" {
		return "", exitcode.Newf(exitcode.Error, "canonicalizing %s: empty result (the value given is not shown)", flag)
	}
	return canon, nil
}
