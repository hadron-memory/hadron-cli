package access

import (
	"strings"

	urnlib "github.com/hadron-memory/urn-lib-go"
)

// canonicalResourceURN renders the URN the server resolved a resource to in the
// canonical grammar-v2 prefixed form.
//
// THIS IS A BACKWARD-COMPATIBILITY SHIM, and since hadron-server#966 that is
// ALL it is (#426). Read the two halves separately, because the original
// rationale no longer holds and the remaining one is easy to delete by mistake.
//
// What it was for (#423): effectiveAccess documents resourceUrn as "Canonical
// URN of the resolved resource", but for a memory, agent or app it returned the
// raw stored column — UNPREFIXED by construction, since the server's
// chk_*_urn_not_prefixed guardrails forbid storing a rendered one. Only the
// node branch ran through an emitter. So `access check` printed a URN its own
// argument parser then refused.
//
// What it is for NOW: hadron-server#966 (PR #969, 955afd9, 2026-08-14) made the
// server emit the canonical form for memory / agent / app / organization, so
// against a CURRENT server this function receives an already-prefixed value and
// returns it unchanged. It survives only because the CLI talks to self-hosted
// and older deployments, where a pre-#966 server still returns the bare column
// and deleting this would silently reintroduce #423 for those users.
//
// WHEN TO DELETE IT: when this repo declares a minimum supported server version
// at or past #966. It has none today — nothing in the CLI gates on
// serverInfo.version, and there is no documented floor — which is the whole
// reason the shim stays rather than a judgement that it is still doing work.
// Re-checking that one fact is the entire decision.
//
// It is NOT a thin-client violation: it renders a value, and rendering is the
// client's half of conventions:logic-lives-in-the-server-unless-it-must-run-
// without-one. The judgement — what a resource resolves to — is the server's
// and always was.
//
// Rendering is IDEMPOTENT, which is what makes the compat posture cheap: an
// already-prefixed value passes through untouched, so a current server pays
// nothing for an older one's benefit.
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
	// A bare STORED value may be v1-separated (acme.com::kb) — the stored
	// column keeps whatever grammar it was written in, and the server hands it
	// back untouched. Splitting that on every single colon yields an empty
	// atom, ComposeUrnV2 refuses it, and the value would pass through
	// unprefixed — leaving the round-trip this function exists to restore
	// broken for exactly the historical rows (PR #424 review). Collapsing "::"
	// to ":" IS the v1→v2 conversion: in v1 the doubled colon is the only
	// separator, so no legitimate atom spans one.
	atoms := strings.Split(strings.ReplaceAll(value, "::", ":"), ":")
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
//
// organization and user are absent too, and that gap is no longer worth closing
// (#426). Be precise about WHY, because the tempting description is wrong:
// they do NOT reach the prefixed-value branch. Returning false here exits
// canonicalResourceURN at its !ok check, so the value comes back VERBATIM,
// never normalized (@codex, #601).
//
// That is the correct outcome rather than a lucky one: a current server emits
// hrn:org:<slug> and hrn:user:<handle> already canonical, so there is nothing
// to render and verbatim IS the right answer. The consequence to know is that
// these kinds get no scheme normalization — a hypothetical urn:org:… would
// pass through unchanged where urn:mem:… would not. No server emits that, so
// the gap stays open deliberately rather than being closed for symmetry.
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
