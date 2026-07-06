package cmdutil

import (
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

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
		// Normalize the memory to canonical org::memory so a single-colon
		// `-m acme.com:kb` composes a valid <org>::<memory>::<loc> URN, not the
		// 3-colon `acme.com:kb::loc` the strict grammar rejects (#38/#138).
		ref = canonicalOrgMemory(memory) + "::" + strings.TrimSpace(ref)
	}
	return ResolveNodeURN(cmd, client, ref)
}

// ResolveNodeURN turns a fully-qualified node URN into a node ID via
// Query.resolveUrn. Bare locs are rejected client-side with a usage
// error: node references always name the memory (same-loc collisions
// across memories made anything less ambiguous). A URN that resolves
// to a different entity kind is a usage error too.
func ResolveNodeURN(cmd *cobra.Command, client graphql.Client, ref string) (string, error) {
	urn := ref
	// Accept either scheme prefix: hrn: is canonical (issue #239), urn: is
	// legacy-but-accepted-forever. A prefixed URN passes through verbatim
	// (the server accepts both); a bare ref gets the canonical hrn:node:.
	if !strings.HasPrefix(urn, "hrn:") && !strings.HasPrefix(urn, "urn:") {
		// A full node URN is <org>::<memory>::<loc> — TWO double-colon separators
		// (org::memory, memory::loc). Counting single colons wouldn't enforce the
		// grammar: a single-colon ref whose loc has its own colons
		// (acme.com:kb:services:secureid:x) has ≥4 colons yet zero `::`, so it must
		// be rejected as the ambiguous form it is, not passed to the server.
		if strings.Count(urn, "::") < 2 {
			return "", exitcode.Newf(exitcode.Usage,
				"%q is not a fully-qualified node URN — expected <org>::<memory>::<loc> (e.g. hadronmemory.com::dev::start-here), or pass -m <org::memory> with a bare loc", ref)
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
			"node %q not found — verify it exists and the URN form is hrn:node:<org>::<memory>::<loc>", ref)
	}
	if resp.ResolveUrn.Kind != "node" {
		return "", exitcode.Newf(exitcode.Usage, "%q resolves to a %s, not a node", ref, resp.ResolveUrn.Kind)
	}
	return resp.ResolveUrn.Id, nil
}
