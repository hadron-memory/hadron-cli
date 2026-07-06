package cmdutil

import "strings"

// splitOrgSlug parses an "org:slug" or "org::slug" reference into its parts.
// ok is false when the ref isn't confidently that shape — empty, or the slug
// still carries a colon (e.g. a full node URN or a raw id) — so callers can
// leave it untouched for the server to judge.
func splitOrgSlug(ref string) (org, slug string, ok bool) {
	i := strings.Index(ref, ":")
	if i <= 0 {
		return "", "", false
	}
	org = ref[:i]
	slug = strings.TrimLeft(ref[i+1:], ":") // collapse ':' or '::'
	if slug == "" || strings.Contains(slug, ":") {
		return "", "", false
	}
	return org, slug, true
}

// canonicalOrgMemory normalizes a memory identifier — "org:slug" or "org::slug",
// optionally hrn:memory:/urn:memory:-prefixed — to the canonical "org::slug" URN
// segment used when composing a node URN. Unrecognized shapes pass through
// unchanged. This is what lets `-m acme.com:kb` (single colon, as the docs once
// wrote it) and `-m acme.com::kb` both address the same memory.
func canonicalOrgMemory(memory string) string {
	for _, p := range []string{"hrn:memory:", "urn:memory:"} {
		memory = strings.TrimPrefix(memory, p)
	}
	if org, slug, ok := splitOrgSlug(memory); ok {
		return org + "::" + slug
	}
	return memory
}

// CanonicalMemoryRef normalizes a memory reference for the server's memory(ref:)
// dispatch: a raw id (no colon) or an already hrn:/urn:-prefixed URN passes
// through; a bare "org:slug" / "org::slug" becomes the canonical
// hrn:memory:org::slug, so the short forms the CLI advertises resolve
// consistently instead of failing as "not found" (#108).
func CanonicalMemoryRef(ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" || strings.HasPrefix(ref, "hrn:") || strings.HasPrefix(ref, "urn:") {
		return ref
	}
	if org, slug, ok := splitOrgSlug(ref); ok {
		return "hrn:memory:" + org + "::" + slug
	}
	return ref // raw id or an unrecognized shape — let the server decide
}
