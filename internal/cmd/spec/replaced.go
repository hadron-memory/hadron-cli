package spec

import (
	"context"
	"strings"

	"github.com/Khan/genqlient/graphql"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// replacement is the display state of a spec `spec supersede` retired: the
// persisted convention is the `superseded` tag plus a `superseded-by` edge,
// unchanged by cli#1145. The word shown is "replaced" because SUPERSEDED is
// already the approval state "approved, then edited" and "retired" is the
// repealed-number overlay in allocation; the three are independent.
type replacement struct {
	Replaced bool
	// By is the replacement's loc, nil when there is no edge, its target is not
	// readable, it lies in another memory, or the edges disagree. An identity is
	// never guessed (cli#1145).
	By *string
	// Kind is the spec's own statement of how it was retired when it carries a
	// rescind prefix on its abstract: retirementWithdrawn or retirementRescinded,
	// "" otherwise (cli#1906). The abstract prefix is the spec-fixed signal
	// (cor:spc:010:01); the absence of a readable edge is not, because the
	// caller may simply not be able to read a successor.
	Kind string
}

const (
	retirementWithdrawn = "withdrawn"
	retirementRescinded = "rescinded"
)

// rescindKinds is the one list of how a spec can say it was retired: the kind
// each --json `retirement` word carries and the abstract prefix `spec rescind`
// writes for it. rescindKind (abstract to kind) and kindOfWord (word to kind)
// both read it, so a new kind cannot be added to one and dropped from the
// other (cli#3220).
var rescindKinds = []struct{ kind, prefixWord string }{
	{retirementWithdrawn, withdrawnWord},
	{retirementRescinded, rescindedWord},
}

// rescindKind reads the rescind prefix (`spec rescind` writes "WITHDRAWN (<date>);"
// or "RESCINDED (<date>);") off an abstract.
func rescindKind(abstract string) string {
	a := strings.TrimSpace(abstract)
	for _, k := range rescindKinds {
		if strings.HasPrefix(a, k.prefixWord+" (") {
			return k.kind
		}
	}
	return ""
}

// kindOfWord maps the --json retirement word back to a rescind kind, so the
// human register label reads the same state the DTO carries.
func kindOfWord(w string) string {
	for _, k := range rescindKinds {
		if w == k.kind {
			return k.kind
		}
	}
	return ""
}

// word is the one-word state for the additive --json `retirement` key: a
// readable successor is "replaced", the spec's own rescind prefix is
// "withdrawn" or "rescinded", and where neither settles it the neutral
// "retired" says no more than is known. A spec that was not replaced has no
// retirement word at all: "" (cli#3220; the callers only ask for a retired one).
// A readable successor wins over a rescind prefix.
func (r replacement) word() string {
	switch {
	case !r.Replaced:
		return ""
	case r.By != nil:
		return "replaced"
	case r.Kind != "":
		return r.Kind
	}
	return "retired"
}

// replacedCandidate is what a listing knows about a node before any edge read.
type replacedCandidate struct {
	ID       string
	Loc      string
	MemoryID string
	Tags     []string
}

// loadReplacements returns the replacement state per node id for the
// candidates carrying the superseded tag. Only those are read again (one bulk
// nodeBatch for their outgoing edges), so a corpus with no retired spec costs
// no extra call. A tagged node whose edges cannot be read is still replaced,
// with no identity.
func loadReplacements(ctx context.Context, client graphql.Client, cands []replacedCandidate) (map[string]replacement, error) {
	out := map[string]replacement{}
	var ids []string
	for _, c := range cands {
		if hasTag(c.Tags, supersededTag) {
			out[c.ID] = replacement{Replaced: true}
			ids = append(ids, c.ID)
		}
	}
	if len(ids) == 0 {
		return out, nil
	}
	nodes, _, err := api.CollectNodeBatch(ids, func(chunk []string) (*gen.NodeBatchNodeBatchNodeBatchResult, error) {
		resp, ferr := api.ReadNodeBatch(ctx, client, chunk, nil, nil)
		if ferr != nil {
			return nil, api.MapError(ferr)
		}
		if resp == nil {
			return nil, nil
		}
		return resp.NodeBatch, nil
	})
	if err != nil {
		return nil, err
	}
	for _, n := range nodes {
		if n == nil {
			continue
		}
		r := out[n.Id]
		if n.Abstract != nil {
			r.Kind = rescindKind(*n.Abstract)
		}
		if by, ok := replacedByLoc(n.MemoryId, n.OutgoingEdges); ok {
			r.By = &by
		}
		out[n.Id] = r
	}
	return out, nil
}

// replacedByLoc names the replacement only when it is unambiguous: exactly one
// distinct superseded-by target, readable (it carries a loc) and in the
// node's own memory. It reads the raw edges, not the lint projection: that one
// drops an edge whose target the caller cannot read, which would let a
// readable edge name itself the sole replacement beside a concealed one.
func replacedByLoc(memoryID string, edges []*gen.NodeBatchNodeBatchNodeBatchResultNodesNodeOutgoingEdgesEdge) (string, bool) {
	var found string
	for _, e := range edges {
		if e == nil || edgeNameStr(e.Name) != supersededByLabel {
			continue
		}
		if e.Target == nil || e.Target.Loc == "" || e.Target.MemoryId != memoryID {
			return "", false
		}
		if found != "" && found != e.Target.Loc {
			return "", false
		}
		found = e.Target.Loc
	}
	return found, found != ""
}

// replacedMarker is the human suffix: "(replaced by <loc>)" for a readable
// successor, "(withdrawn)" / "(rescinded)" from the spec's own rescind prefix,
// and the neutral "(retired)" where neither settles it (cli#1906). It never
// says "replaced" without a successor to name, and never guesses one.
func replacedMarker(r replacement, shortenTo string) string {
	if !r.Replaced {
		return ""
	}
	if r.By == nil {
		if r.Kind != "" {
			return " (" + r.Kind + ")"
		}
		return " (retired)"
	}
	by := *r.By
	if shortenTo != "" && strings.HasPrefix(by, shortenTo+":") {
		by = strings.TrimPrefix(by, shortenTo+":")
	}
	return " (replaced by " + by + ")"
}
