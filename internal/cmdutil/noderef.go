package cmdutil

import (
	"regexp"
	"strings"

	"github.com/Khan/genqlient/graphql"
	urnlib "github.com/hadron-memory/urn-lib-go"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// reNodeID matches an opaque node id as the server mints them: 32 lowercase
// hex characters.
var reNodeID = regexp.MustCompile(`^[0-9a-f]{32}$`)

// IsNodeID reports whether ref is a bare node id rather than a URN or loc.
//
// Every --json surface prints these (`id`, and `otherNodeId` on each edge) and
// both node(ref:) and nodeBatch(refs:) accept them, so a ref the CLI just
// emitted has to be feedable straight back (#336). It is matched by SHAPE, not
// merely by "contains no colon": a bare loc typed without -m (`start-here`) is
// also colon-free, and treating that as an id would swap a usage error naming
// -m for a bare "not found".
//
// Note resolveUrn does NOT accept an id — it returns null for one — so callers
// short-circuit rather than round-tripping.
func IsNodeID(ref string) bool {
	return reNodeID.MatchString(strings.TrimSpace(ref))
}

// EdgeDisplay is the human handle for an edge: its name, or its loc when the
// name is empty (spec 037 — an edge's name is optional, its loc is the
// identity, so a nameless edge still prints something addressable).
func EdgeDisplay(name *string, loc string) string {
	if name != nil && *name != "" {
		return *name
	}
	return loc
}

// ResolveNodeRef resolves a node reference into a node ID. With an empty
// memory it requires a fully-qualified URN (ResolveNodeURN). With a memory
// (the `org::memory` form, optionally hrn:/urn:-prefixed) the ref is a bare loc
// within that memory: a node URN is just <org>::<memory>::<loc>, so the two are
// joined and resolved. The memory form is the additive convenience; without it
// the strict-URN behavior is unchanged.
func ResolveNodeRef(cmd *cobra.Command, client graphql.Client, memory, ref string) (string, error) {
	if memory = strings.TrimSpace(memory); memory != "" {
		loc := strings.TrimSpace(ref)
		if loc == "" {
			return "", exitcode.Newf(exitcode.Usage, "a bare node loc is required with -m/--memory <org::memory>")
		}
		// A simple <org>::<slug> memory + a bare loc composes a canonical
		// grammar-v2 flat node URN (hrn:node:<root>:<slug>:<loc…>) in one step. A
		// single-colon `-m acme.com:kb` and the legacy `acme.com::kb` both
		// normalize here.
		if _, _, ok := MemoryParts(memory); ok {
			nodeURN := NodeURN(memory, loc)
			if nodeURN == "" {
				return "", exitcode.Newf(exitcode.Usage,
					"%q is not a valid node loc — each colon-separated segment must be %s", loc, slugRule)
			}
			return ResolveNodeURN(cmd, client, nodeURN)
		}
		// A COMPOUND app-mem memory (<org>::<agent>:app-mem:<slug>) carries its own
		// colons, so it can't be expressed as a fixed-arity flat v2 node URN. Join
		// it into the legacy <memory>::<loc> form — already "::"-separated — which
		// ResolveNodeURN prefixes and the server still resolves (accepted forever,
		// #239). This preserves `-m <compound-memory> <bare-loc>` for those.
		ref = memory + "::" + loc
	}
	return ResolveNodeURN(cmd, client, ref)
}

// BatchNodeRef canonicalizes a node reference for a server op that resolves a
// PK-or-URN itself, WITHOUT the resolveUrn round trip ResolveNodeRef costs —
// today nodeBatch(refs:), which accepts both since hadron-server#813. It takes
// the same inputs ResolveNodeRef does (a fully-qualified URN, or a bare loc
// with -m/--memory) and applies the same composition, but returns the ref to
// send instead of an id. That is what makes a batch of URNs ONE call rather
// than N resolves plus a batch.
//
// It rejects locally what the server would reject loudly: the server errors on
// a malformed / unqualified ref rather than listing it as unavailable, and one
// mistyped ref must not cost the other nineteen their read. Callers map the
// returned Usage error onto their own unavailable list.
func BatchNodeRef(memory, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if memory = strings.TrimSpace(memory); memory != "" {
		if ref == "" {
			return "", exitcode.Newf(exitcode.Usage, "a bare node loc is required with -m/--memory <org::memory>")
		}
		// Mirrors ResolveNodeRef: a simple <org>::<slug> memory + a bare loc
		// composes a canonical flat v2 node URN; a COMPOUND app-mem memory
		// carries its own colons and joins into the legacy <memory>::<loc>
		// form instead (still accepted forever, #239).
		if _, _, ok := MemoryParts(memory); ok {
			nodeURN := NodeURN(memory, ref)
			if nodeURN == "" {
				return "", exitcode.Newf(exitcode.Usage,
					"%q is not a valid node loc — each colon-separated segment must be %s", ref, slugRule)
			}
			return nodeURN, nil
		}
		ref = memory + "::" + ref
	}
	if urnlib.HasSchemePrefix(ref) {
		// Validate rather than pass through. A prefixed ref used to go out
		// unchecked, so a wrong-kind URN like "hrn:mem:acme.com:kb" reached the
		// server and failed the WHOLE nodeBatch(refs:) call with a generic
		// BAD_USER_INPUT — taking every valid ref in the batch down with it.
		// AssertFullyQualifiedUrn catches it here, precisely and locally
		// (review on #305).
		//
		// It validates only; the ref is NOT rewritten. Collapsing spellings to
		// one canonical form would flatten a COMPOUND app-mem memory
		// (<org>::<agent>:app-mem:<slug>), whose "::" is the only thing marking
		// where the memory ends and the loc begins.
		if urnlib.AssertFullyQualifiedUrn(ref, "node") != nil {
			return "", exitcode.Newf(exitcode.Usage,
				"%q is not a fully-qualified node URN — expected <org>::<memory>::<loc>, optionally hrn:node:/urn:node:-prefixed", ref)
		}
		return ref, nil
	}
	// Same client-side grammar gate as ResolveNodeURN, so `node get <ref>` and
	// `node get <ref> <ref>` accept exactly the same refs — including a bare
	// node id, which nodeBatch(refs:) takes verbatim.
	if IsNodeID(ref) {
		return ref, nil
	}
	if strings.Count(ref, "::") < 2 || urnlib.AssertFullyQualifiedUrn(ref, "node") != nil {
		return "", exitcode.Newf(exitcode.Usage,
			"%q is not a fully-qualified node URN — expected <org>::<memory>::<loc> (e.g. hadronmemory.com::dev::start-here), or pass -m <org::memory> (single-colon <org:memory> also accepted) with a bare loc", ref)
	}
	return "hrn:node:" + ref, nil
}

// CanonicalNodeRef canonicalizes a node reference for a server op that itself
// accepts an ID or a URN (spec 007 dispatch) — e.g. the object store's
// object(ref:)/updateObject/deleteObject, which forward ref to node(ref:). A
// scheme-prefixed URN passes through, a bare/legacy fully-qualified node URN
// (<org>::<memory>::<loc>) gets the canonical hrn:node: prefix, and a raw id (or
// any unrecognized shape) is left for the server to resolve. Unlike
// ResolveNodeURN it does NOT round-trip through resolveUrn, so a raw object id
// works without a lookup and without being rejected as "not a URN".
func CanonicalNodeRef(ref string) string {
	ref = strings.TrimSpace(ref)
	if urnlib.HasSchemePrefix(ref) {
		return ref
	}
	if strings.Count(ref, "::") >= 2 && urnlib.AssertFullyQualifiedUrn(ref, "node") == nil {
		return "hrn:node:" + ref
	}
	return ref // raw id or an unrecognized shape — let the server decide
}

// ResolveNodeURN turns a fully-qualified node URN into a node ID via
// Query.resolveUrn. Bare locs are rejected client-side with a usage
// error: node references always name the memory (same-loc collisions
// across memories made anything less ambiguous). A URN that resolves
// to a different entity kind is a usage error too.
func ResolveNodeURN(cmd *cobra.Command, client graphql.Client, ref string) (string, error) {
	urn := strings.TrimSpace(ref)
	// A bare node id addresses the node directly. resolveUrn would return null
	// for it, so return it as the id rather than round-tripping — the same
	// shape as resolveMemoryID's raw-id fast path. Kind isn't verified (that
	// would need the round-trip resolveUrn can't serve); a wrong-kind id fails
	// cleanly at the caller's own read.
	if IsNodeID(urn) {
		return urn, nil
	}
	// Accept either scheme prefix: hrn: is canonical (issue #239), urn: is
	// legacy-but-accepted-forever. A prefixed URN passes through verbatim
	// (the server accepts both); a bare ref gets the canonical hrn:node:.
	if !urnlib.HasSchemePrefix(urn) {
		// A full node URN is <org>::<memory>::<loc> — TWO double-colon separators
		// (org::memory, memory::loc). Counting single colons wouldn't enforce the
		// grammar: a single-colon ref whose loc has its own colons
		// (acme.com:kb:services:secureid:x) has ≥4 colons yet zero `::`, so it must
		// be rejected as the ambiguous form it is, not passed to the server.
		if strings.Count(urn, "::") < 2 || urnlib.AssertFullyQualifiedUrn(urn, "node") != nil {
			return "", exitcode.Newf(exitcode.Usage,
				"%q is not a fully-qualified node URN — expected <org>::<memory>::<loc> (e.g. hadronmemory.com::dev::start-here), or pass -m <org::memory> (single-colon <org:memory> also accepted) with a bare loc", ref)
		}
		urn = "hrn:node:" + urn
	}
	resp, err := gen.ResolveUrn(cmd.Context(), client, urn)
	if err != nil {
		return "", api.MapError(err)
	}
	if resp.ResolveUrn == nil {
		// resolveUrn returns null for a well-formed URN that names nothing —
		// point at the canonical grammar so a spelling slip isn't read as a
		// genuinely-absent node (#138).
		return "", exitcode.Newf(exitcode.NotFound,
			"node %q not found — verify it exists; the URN form is <org>::<memory>::<loc>, optionally hrn:node:/urn:node:-prefixed", ref)
	}
	if resp.ResolveUrn.Kind != "node" {
		return "", exitcode.Newf(exitcode.Usage, "%q resolves to a %s, not a node", ref, resp.ResolveUrn.Kind)
	}
	return resp.ResolveUrn.Id, nil
}
