package cmdutil

import (
	"strings"

	urn "github.com/hadron-memory/urn-lib-go"
)

const (
	memoryPrefixV1 = "hrn:memory:"
	memoryPrefixV2 = "hrn:mem:"
)

// stripMemoryPrefix removes an hrn:/urn: memory scheme prefix — v1 "memory" or
// the grammar-v2 "mem" type word — and reports whether one was present. urn:
// normalizes to hrn: first, so both legacy and canonical spellings are handled.
func stripMemoryPrefix(ref string) (body string, prefixed bool) {
	normalized := urn.NormalizeScheme(ref)
	for _, p := range []string{memoryPrefixV1, memoryPrefixV2} {
		if strings.HasPrefix(normalized, p) {
			return strings.TrimPrefix(normalized, p), true
		}
	}
	return ref, false
}

// MemoryParts decomposes a memory reference into its grammar-v2 (root, slug)
// atoms. It accepts every spelling the CLI advertises — a bare "org:slug" or
// "org::slug", or an hrn:memory:/urn:memory: (v1) / hrn:mem:/urn:mem: (v2) URN —
// and normalizes the separator either way. ok is false for a raw id (no
// separator), an unrelated scheme (hrn:node:…), or a malformed multi-part ref;
// callers pass those through for the server to resolve.
func MemoryParts(ref string) (root, slug string, ok bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", "", false
	}
	body, prefixed := stripMemoryPrefix(ref)
	if !prefixed && urn.HasSchemePrefix(ref) {
		return "", "", false // a non-memory URN (hrn:node:…): not ours to split
	}
	// The body is <root><sep><slug>; the sep is "::" (v1) or ":" (v2). Neither a
	// root nor a slug atom may contain a colon, so a collapsed body must split
	// into EXACTLY two atoms — this rejects "foo::bar::baz" and ":::".
	if strings.Contains(body, ":::") {
		return "", "", false
	}
	atoms := strings.Split(strings.ReplaceAll(body, "::", ":"), ":")
	if len(atoms) != 2 || atoms[0] == "" || atoms[1] == "" {
		return "", "", false
	}
	root, slug = atoms[0], atoms[1]
	if urn.ValidateAtomShape(root, root) != nil || urn.ValidateAtomShape(slug, slug) != nil {
		return "", "", false
	}
	return root, slug, true
}

// CanonicalMemoryRef normalizes a memory reference to the canonical grammar-v2
// flat URN hrn:mem:<root>:<slug> (#697 emission flip) for the server's
// memory(ref:) dispatch. A raw id (no separator) or an unrecognized shape passes
// through untouched — the server accepts every legacy spelling forever (#239),
// so the short forms the CLI advertises resolve consistently either way (#108).
func CanonicalMemoryRef(ref string) string {
	ref = strings.TrimSpace(ref)
	if root, slug, ok := MemoryParts(ref); ok {
		if u, err := urn.ComposeUrnV2("mem", root, slug); err == nil {
			return u
		}
	}
	return ref // raw id or an unrecognized shape — let the server decide
}

// NodeURN composes the canonical grammar-v2 flat node URN
// hrn:node:<root>:<slug>:<loc…> for a (memory, loc). The memory's separators are
// normalized (single-colon and legacy "::" both accepted). It returns "" when
// the memory isn't an <org>::<slug> pair (a raw id, or a malformed multi-part
// ref) or loc is empty — composing a URN from those would produce an invalid one
// that resolves to nothing, silently defeating the existence probe (#129
// review). The caller must probe otherwise.
func NodeURN(memory, loc string) string {
	loc = strings.TrimSpace(loc)
	if loc == "" {
		return ""
	}
	root, slug, ok := MemoryParts(memory)
	if !ok {
		return ""
	}
	segments := append([]string{slug}, strings.Split(loc, ":")...)
	nodeURN, err := urn.ComposeUrnV2("node", root, segments...)
	if err != nil {
		return ""
	}
	return nodeURN
}
