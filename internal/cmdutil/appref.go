package cmdutil

import (
	"regexp"
	"strings"

	urn "github.com/hadron-memory/urn-lib-go"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

const appPrefix = "hrn:app:"

// The two PK shapes hadron-server mints, as its entity-ref dispatcher tells
// them apart (spec 007, src/lib/entityRef/shape.ts `isId`): a Prisma cuid —
// 25 chars, c-led lowercase alphanumeric — or a 32-char lowercase hex UUIDv7.
var (
	reAppIDCuid = regexp.MustCompile(`^c[a-z0-9]{24}$`)
	reAppIDHex  = regexp.MustCompile(`^[0-9a-f]{32}$`)
)

// IsAppID reports whether ref is an App PK rather than a URN. The server
// dispatches on exactly these two shapes and treats EVERYTHING else as a URN
// that must be fully qualified — so a colon-less slug ("dev-team") is not an
// id here either, and forwarding it would only buy the server's refusal, in
// the server's words (#540).
//
// Unlike IsNodeID this admits the cuid shape. noderef.go refuses it because a
// cuid is indistinguishable from an ordinary loc; nothing loc-shaped is ever
// an App ref, so that hazard has no counterpart here.
func IsAppID(ref string) bool {
	ref = strings.TrimSpace(ref)
	return reAppIDCuid.MatchString(ref) || reAppIDHex.MatchString(ref)
}

// AppParts decomposes an App reference into its grammar-v2 (root, slug)
// atoms. It accepts every spelling the CLI advertises — a bare "root:slug" or
// legacy "root::slug", or an hrn:app:/urn:app: URN in either separator — and
// normalizes the separator either way. ok is false for an id (no separator),
// a URN of another type (hrn:worker:…), or a malformed multi-part ref.
func AppParts(ref string) (root, slug string, ok bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", "", false
	}
	body := ref
	if normalized := urn.NormalizeScheme(ref); strings.HasPrefix(normalized, appPrefix) {
		body = strings.TrimPrefix(normalized, appPrefix)
	} else if urn.HasSchemePrefix(ref) {
		return "", "", false // a non-App URN: not ours to split
	}
	return pairAtoms(body)
}

// appRefForms is the one place the accepted App spellings are spelled out for
// a reader, so the two refusals below cannot drift apart — and it names ONLY
// grammar v2. The legacy `::` separator stays accepted (#239) but is never
// advertised: an error message is read at the moment someone decides what to
// type, and it is the most-copied text in a CLI (#540).
const appRefForms = "hrn:app:<root>:<slug> (canonical, e.g. hrn:app:acme.com:dev-app), the <root>:<slug> short form, or an App id"

// CanonicalAppRef validates an App reference client-side and normalizes it to
// the canonical grammar-v2 URN hrn:app:<root>:<slug> — the App counterpart of
// CanonicalMemoryRef, with one difference: an unrecognized shape is REFUSED as
// a usage error rather than passed through for the server to reject.
//
// The server's refusal is the wrong one to relay (#540): it names the GraphQL
// field the ref landed in rather than the flag the caller typed, carries a
// genqlient `input:3:` location prefix, and its hint teaches the retired `::`
// grammar. Checking the shape here costs no round trip and lets the message
// say what the caller can act on. `what` names the source for that message —
// "--app", "--install-into", "<app-ref>".
//
// An id passes through verbatim (the server dispatches PKs); an empty ref
// returns "" with no error, since whether an App is REQUIRED is the caller's
// question, not this function's.
func CanonicalAppRef(what, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", nil
	}
	if IsAppID(ref) {
		return ref, nil
	}
	if root, slug, ok := AppParts(ref); ok {
		if u, err := urn.ComposeUrnV2("app", root, slug); err == nil {
			return u, nil
		}
	}
	if !strings.Contains(ref, ":") {
		// The #540 shape: a bare slug, which reads as "I forgot the org" —
		// say so, rather than only listing forms it does not match.
		return "", exitcode.Newf(exitcode.Usage,
			"%s %q is not org-qualified — name the App as %s", what, ref, appRefForms)
	}
	return "", exitcode.Newf(exitcode.Usage,
		"%s %q does not name an App — expected %s", what, ref, appRefForms)
}
