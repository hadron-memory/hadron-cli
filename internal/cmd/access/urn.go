package access

import (
	"strings"

	urnlib "github.com/hadron-memory/urn-lib-go"
)

// canonicalResourceURN renders the URN the server resolved a resource to in the
// canonical grammar-v2 prefixed form (#423).
//
// effectiveAccess documents resourceUrn as "Canonical URN of the resolved
// resource", but for a memory, agent or app it returns the raw stored column —
// which is UNPREFIXED by construction (the server's chk_*_urn_not_prefixed
// guardrails forbid storing a rendered one). Only the node branch runs through
// an emitter. So `access check` printed a URN its own argument parser then
// refused, and scripting the command over a list meant re-prefixing by reading
// `kind` and mapping it. The fix belongs server-side as well — every
// effectiveAccess consumer sees the bare form — but the CLI is where CLAUDE.md
// puts the emission rule ("emit v2, accept everything"), and rendering here is
// IDEMPOTENT: an already-prefixed value (today's node, tomorrow's everything)
// passes through unchanged, so this does not become dead code when the server
// catches up.
//
// An unrecognized kind, an aiServiceConfig (which genuinely has no URN — the
// field carries its id), or a shape that will not compose is returned verbatim:
// echoing what the server said beats inventing a URN that resolves to nothing.
func canonicalResourceURN(kind, raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return raw
	}
	typeWord, ok := urnTypeWordForKind(kind)
	if !ok {
		return raw
	}
	// Already prefixed (with either scheme, and either the v1 or v2 type word):
	// normalize the scheme and leave the body alone.
	if normalized := urnlib.NormalizeScheme(value); strings.HasPrefix(normalized, "hrn:") {
		return normalized
	}
	atoms := strings.Split(value, ":")
	if len(atoms) < 2 {
		return raw
	}
	composed, err := urnlib.ComposeUrnV2(typeWord, atoms[0], atoms[1:]...)
	if err != nil {
		return raw
	}
	return composed
}

// urnTypeWordForKind maps an effectiveAccess resourceKind to its grammar-v2 URN
// type word. aiServiceConfig is deliberately absent: it has no URN, and the
// field carries a bare id that must not be dressed up as one.
func urnTypeWordForKind(kind string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "memory":
		return "mem", true
	case "node":
		return "node", true
	case "app":
		return "app", true
	case "agent":
		return "agent", true
	default:
		return "", false
	}
}
