package cmdutil

import (
	"strings"

	urn "github.com/hadron-memory/urn-lib-go"
)

const memoryPrefix = "hrn:memory:"

func stripMemoryPrefix(ref string) (string, bool) {
	normalized := urn.NormalizeScheme(ref)
	if strings.HasPrefix(normalized, memoryPrefix) {
		return strings.TrimPrefix(normalized, memoryPrefix), true
	}
	return ref, false
}

func canonicalMemoryPath(ref string) (string, bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", false
	}
	bare, prefixed := stripMemoryPrefix(ref)
	if !prefixed && urn.HasSchemePrefix(ref) {
		return "", false
	}
	if !prefixed {
		switch {
		case strings.Contains(bare, ":::"):
			return "", false
		case strings.Contains(bare, "::"):
			if strings.Count(bare, "::") != 1 {
				return "", false
			}
		case strings.Count(bare, ":") != 1:
			return "", false
		}
	}
	canon, err := urn.FormatCanonicalUrn("memory", bare)
	if err != nil {
		return "", false
	}
	return strings.TrimPrefix(canon, memoryPrefix), true
}

// canonicalOrgMemory normalizes a memory identifier — "org:slug" or "org::slug",
// optionally hrn:memory:/urn:memory:-prefixed — to the canonical "org::slug" URN
// segment used when composing a node URN. Unrecognized shapes pass through
// unchanged. This is what lets `-m acme.com:kb` (single colon, as the docs once
// wrote it) and `-m acme.com::kb` both address the same memory.
func canonicalOrgMemory(memory string) string {
	if path, ok := canonicalMemoryPath(memory); ok {
		return path
	}
	return memory
}

// NodeURN composes the canonical hrn:node URN for a (memory, loc). The memory's
// org::slug separators are normalized (single-colon accepted), so it emits the
// double-colon <org>::<memory>::<loc> the server resolves. It returns "" when
// the memory isn't EXACTLY an org::slug pair (a raw id, or a malformed
// multi-part ref like foo::bar::baz) or loc is empty — composing a URN from
// those would produce an invalid one that resolves to nothing, silently
// defeating the existence probe (#129 review). The caller must probe otherwise.
func NodeURN(memory, loc string) string {
	if loc == "" {
		return ""
	}
	path, ok := canonicalMemoryPath(memory)
	if !ok {
		return ""
	}
	nodeURN, err := urn.ComposeNodeUrn(path, loc)
	if err != nil {
		return ""
	}
	return nodeURN
}

// CanonicalMemoryRef normalizes a memory reference for the server's memory(ref:)
// dispatch: a raw id (no colon) or an already hrn:/urn:-prefixed URN passes
// through; a bare "org:slug" / "org::slug" becomes the canonical
// hrn:memory:org::slug, so the short forms the CLI advertises resolve
// consistently instead of failing as "not found" (#108).
func CanonicalMemoryRef(ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ref
	}
	if path, ok := canonicalMemoryPath(ref); ok {
		return memoryPrefix + path
	}
	return ref // raw id or an unrecognized shape — let the server decide
}
