package cmdutil

import (
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

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
		// A full node URN has at least org:memory:loc.
		if strings.Count(urn, ":") < 2 {
			return "", exitcode.Newf(exitcode.Usage,
				"%q is not a fully-qualified node URN — expected <org>:<memory>:<loc> (e.g. hadronmemory.com:dev:start-here)", ref)
		}
		urn = "hrn:node:" + urn
	}
	resp, err := gen.ResolveUrn(cmd.Context(), client, urn)
	if err != nil {
		return "", api.MapError(err)
	}
	if resp.ResolveUrn == nil {
		return "", exitcode.Newf(exitcode.NotFound, "node %q not found", ref)
	}
	if resp.ResolveUrn.Kind != "node" {
		return "", exitcode.Newf(exitcode.Usage, "%q resolves to a %s, not a node", ref, resp.ResolveUrn.Kind)
	}
	return resp.ResolveUrn.Id, nil
}
