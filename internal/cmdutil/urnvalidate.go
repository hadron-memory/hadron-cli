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

var (
	typeMarkers = map[string]struct{}{
		"app":    {},
		"agent":  {},
		"memory": {},
	}
	nodeURNTypes = map[string]struct{}{
		"node":         {},
		"abstract":     {},
		"partial":      {},
		"parent":       {},
		"plan":         {},
		"prompt":       {},
		"record":       {},
		"task":         {},
		"review":       {},
		"chat":         {},
		"chat-message": {},
		"config":       {},
		"conversation": {},
		"event":        {},
		"goal":         {},
		"stage":        {},
		"condition":    {},
		"data":         {},
	}
	urnTypes = map[string]struct{}{
		"agent":        {},
		"app":          {},
		"app-user":     {},
		"ai-config":    {},
		"asset":        {},
		"edge":         {},
		"license":      {},
		"memory":       {},
		"org":          {},
		"platform":     {},
		"reference":    {},
		"secret":       {},
		"session":      {},
		"subscription": {},
		"usage":        {},
		"user":         {},
	}
	ownerNamespacedTypes = map[string]struct{}{
		"app":    {},
		"agent":  {},
		"memory": {},
		"edge":   {},
	}
)

func init() {
	for typ := range nodeURNTypes {
		urnTypes[typ] = struct{}{}
		ownerNamespacedTypes[typ] = struct{}{}
	}
}

// ValidateAgentURNPath checks an agent slug path supplied as a partial URN
// component, accepting the spec-047 user-handle author context (`@handle:slug`)
// in the same position where the server accepts an org author context
// (`org.example:slug`). Plain node locs should keep using ValidateURNPath so
// `@` remains illegal inside node locations.
func ValidateAgentURNPath(flag, path string) error {
	if path == "" {
		return exitcode.Newf(exitcode.Usage, "%s must not be empty", flag)
	}
	if err := validateURNPathSegment(path, "agent", 1, 2); err != nil {
		return exitcode.Newf(exitcode.Usage,
			"%s %q is not valid — each colon-separated segment must be %s (%s)", flag, path, slugRule, err)
	}
	return nil
}

// CanonicalizeURN validates a scheme-prefixed Hadron URN and returns its
// parser-canonical form: the canonical hrn: scheme, optional app/agent/memory
// type markers stripped from positionally-known segments, and source/self
// installs collapsed. It mirrors the server parser cases the CLI needs for
// cross-repo URN grammar parity, including spec-047 `@<handle>` owner/author
// namespaces.
func CanonicalizeURN(flag, urn string) (string, error) {
	scheme, rest, ok := strings.Cut(urn, ":")
	if !ok || (scheme != "hrn" && scheme != "urn") {
		return "", invalidURN(flag, urn, "malformed grammar")
	}
	typ, path, ok := strings.Cut(rest, ":")
	if !ok || typ == "" || path == "" {
		return "", invalidURN(flag, urn, "malformed grammar")
	}
	if _, ok := urnTypes[typ]; !ok {
		return "", invalidURN(flag, urn, "unknown type "+typ)
	}
	if strings.HasSuffix(path, "::") {
		return "", invalidURN(flag, urn, "trailing double-colon")
	}
	segments := strings.Split(path, "::")
	for _, segment := range segments {
		if segment == "" {
			return "", invalidURN(flag, urn, "empty hierarchy segment")
		}
	}
	for i, segment := range segments {
		if err := validateURNPathSegment(segment, typ, i, len(segments)); err != nil {
			return "", invalidURN(flag, urn, err.Error())
		}
	}

	segments = stripURNTypeMarkers(segments, typ)
	if err := rejectInvalidURNSegmentShapes(typ, segments); err != nil {
		return "", invalidURN(flag, urn, err.Error())
	}
	segments = collapseSelfInstall(typ, segments)

	return "hrn:" + typ + ":" + strings.Join(segments, "::"), nil
}

func invalidURN(flag, urn, reason string) error {
	return exitcode.Newf(exitcode.Usage, "%s %q is not a valid URN: %s", flag, urn, reason)
}

func validateURNPathSegment(segment, typ string, index, totalSegments int) error {
	atoms := strings.Split(segment, ":")
	_, ownerNamespaced := ownerNamespacedTypes[typ]

	authorContextHere := false
	if index >= 1 {
		switch {
		case typ == "agent":
			authorContextHere = index <= totalSegments-1
		case typ == "memory":
			authorContextHere = index <= totalSegments-2
		case isNodeURNType(typ) || typ == "edge":
			authorContextHere = index <= totalSegments-3
		}
	}

	handleIdx := 0
	if index >= 1 && len(atoms) >= 2 && isTypeMarker(atoms[0]) {
		handleIdx = 1
	}

	for i, atom := range atoms {
		checked := atom
		isOwnerHandleAtom := ownerNamespaced &&
			i == handleIdx &&
			strings.HasPrefix(atom, "@") &&
			(index == 0 || (authorContextHere && len(atoms) >= handleIdx+2))
		if isOwnerHandleAtom {
			checked = strings.TrimPrefix(atom, "@")
			if checked == "" {
				return errInvalidSegment(segment)
			}
		}
		if !isSlugAtom(checked) {
			return errInvalidAtom(atom)
		}
	}
	return nil
}

func stripURNTypeMarkers(segments []string, typ string) []string {
	stripped := append([]string(nil), segments...)
	finalIdx := len(stripped) - 1
	for i, segment := range stripped {
		if i == 0 || (isNodeURNType(typ) && i == finalIdx) {
			continue
		}
		atoms := strings.Split(segment, ":")
		if len(atoms) < 2 || !isTypeMarker(atoms[0]) || isTypeMarker(atoms[1]) {
			continue
		}
		stripped[i] = strings.Join(atoms[1:], ":")
	}
	return stripped
}

func rejectInvalidURNSegmentShapes(typ string, segments []string) error {
	finalIdx := len(segments) - 1
	for i, segment := range segments {
		atomCount := len(strings.Split(segment, ":"))
		switch {
		case i == 0:
			if atomCount != 1 {
				return errInvalidSegment(segment)
			}
		case isNodeURNType(typ) && i == finalIdx:
			continue
		case atomCount > 3:
			return errInvalidSegment(segment)
		}
	}
	return nil
}

func collapseSelfInstall(typ string, segments []string) []string {
	if typ != "agent" || len(segments) < 2 {
		return segments
	}
	lastIdx := len(segments) - 1
	atoms := strings.Split(segments[lastIdx], ":")
	if len(atoms) < 2 || atoms[0] != segments[0] {
		return segments
	}
	collapsed := append([]string(nil), segments...)
	collapsed[lastIdx] = strings.Join(atoms[1:], ":")
	return collapsed
}

func isTypeMarker(atom string) bool {
	_, ok := typeMarkers[atom]
	return ok
}

func isNodeURNType(typ string) bool {
	_, ok := nodeURNTypes[typ]
	return ok
}

func errInvalidSegment(segment string) error {
	return exitcode.Newf(exitcode.Usage, "invalid segment shape %q", segment)
}

func errInvalidAtom(atom string) error {
	return exitcode.Newf(exitcode.Usage, "invalid atom %q", atom)
}
