// Package node implements `hadron node ...`.
package node

import (
	"encoding/json"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
)

// nodeDTO is the stable --json shape for a node in list output.
//
// URN is the fully-qualified node URN the server composes. It's omitempty
// because only the surfaces that select `urn` populate it (move/clone, and
// since #515 `node get`); others leave the key out rather than emit "".
type nodeDTO struct {
	ID  string `json:"id"`
	URN string `json:"urn,omitempty"`
	// PortalURL is the server-built link that opens this node
	// (hadron-server#881, cor:api:230:01) — the one to COPY when citing a node
	// to a human, rather than assembling <origin>/app/u/<urn> yourself, which
	// is the composition the field exists to remove.
	//
	// Nullable for ONE reason on a node: the deployment has no usable web
	// origin. It renders as nothing — never a locally-composed fallback, since
	// a link to a guessed origin fails silently for whoever clicks it.
	//
	// NOT the worker's second reason. `Worker.portalUrl` is additionally null
	// when there is no URN to build one from, because `Worker.urn` is nullable
	// (an App URN predating the arity a worker URN needs). `Node.urn` is
	// `String!`, so that arm cannot occur here. The two stories sat in adjacent
	// DTOs with the worker's copied onto the node, and a hadron-docs reader took
	// it as authoritative and inherited a nullability that does not exist
	// (PR #526, and hadron-docs#266's review before it).
	//
	// omitempty for the same reason as URN: a surface that does not select it
	// leaves the key out rather than asserting null. That differs from
	// `workerDTO.PortalURL`, which is a pointer and stays PRESENT as null —
	// do not reuse one null-check across the two.
	PortalURL  string   `json:"portalUrl,omitempty"`
	MemoryID   string   `json:"memoryId"`
	Loc        string   `json:"loc"`
	Name       string   `json:"name"`
	NodeType   string   `json:"nodeType"`
	Tags       []string `json:"tags"`
	Seq        *int     `json:"seq"`
	IsRunnable bool     `json:"isRunnable"`
	// Role is `Node.role` (#1201) — what the node is FOR, an open string whose
	// GOVERNED values ("spec", "review") decide which door may write it.
	//
	// A POINTER with NO omitempty: null means the node is UNGOVERNED, which is a
	// real answer and must render rather than vanish.
	//
	// That only reads cleanly because EVERY surface feeding this DTO selects
	// `role` — otherwise "not selected" and "ungoverned" would both print null
	// and a reader could not tell them apart, which is the absent-vs-null
	// collapse #602 was filed for. `updateNodeData` was the one that did not,
	// and now does.
	//
	// Projected at all because `--role` can set it, and a field you can set and
	// cannot read back is #602 again (@Ada on #615).
	//
	// NOT `nodeType` (the platform kind, above) and NOT a membership role.
	Role      *string `json:"role"`
	UpdatedAt string  `json:"updatedAt"`
}

// nodeDetailDTO extends the list shape for single-node output.
type nodeDetailDTO struct {
	nodeDTO
	// Revision is the node's live revision (#1323, hadron-server#1339):
	// creation is 1, and each committed authoring change advances it.
	//
	// A POINTER with NO omitempty: null is a real answer — the server
	// predates revisions — and must render rather than vanish. It is never
	// guessed: not 0, and not "current".
	Revision    *int    `json:"revision"`
	ObjectType  *string `json:"objectType"`
	Description *string `json:"description"`
	Abstract    *string `json:"abstract"`
	// AbstractOriginHash is spec 032's staleness fingerprint — the content hash
	// as of when the abstract was written (#306). `GetNode` has always selected
	// it; this DTO dropped it, so it never reached anyone.
	//
	// That absence is why #306 was filed, and the shape is worth keeping in
	// mind: the issue's reproduction was `node get --json | jq
	// .abstractOriginHash`, which printed `null` — because **jq returns null
	// for a key that is not there**, indistinguishable from a key whose value
	// is null. The field read as permanently null on every node, which looked
	// exactly like a detector that had been disarmed. It had not been; the
	// server had been stamping it correctly the whole time.
	//
	// A pointer WITHOUT omitempty, deliberately: null is a meaningful value
	// here (no abstract, or an abstract never fingerprinted — #1128's
	// unverified state), so the key must always be present. Making it vanish
	// would reintroduce the exact ambiguity above.
	//
	// The raw fingerprint only, and no derived "is it stale" boolean — but NOT
	// because computing one here would be illegitimate. This repo already does
	// compute it client-side, deliberately: `staleAbstract` in
	// internal/cmd/spec/citations.go compares this value against
	// `sha256(content)[:8]`, which is the server's own definition, and its
	// comment records why it does not delegate to the server audit instead —
	// `validateMemory` CAPS its findings, so a large memory returns an
	// incomplete stale set and a cited spec reads as fresh (#355).
	//
	// The reason to stay out of it here is narrower: `node get` reports what a
	// node IS, and a verdict belongs to the command asking the question.
	// Exposing this field is what lets any caller reach the same exact
	// comparison — which is the point, since before #306 none could.
	// (An earlier draft of this comment said recomputing client-side would be a
	// second implementation free to disagree with the first, and named `memory
	// validate` the one authoritative answer. Both halves were wrong, and
	// @copilot caught it on PR #582: the computation is defined by the schema
	// rather than owned by the server, and the capped audit is the LESS
	// complete source of the two.)
	AbstractOriginHash *string          `json:"abstractOriginHash"`
	Content            *string          `json:"content"`
	Data               *json.RawMessage `json:"data,omitempty"`
	Properties         *json.RawMessage `json:"properties,omitempty"`
	Seq                *int             `json:"seq"`
	CreatedAt          string           `json:"createdAt"`
	OutgoingEdges      []edgeRefDTO     `json:"outgoingEdges"`
	IncomingEdges      []edgeRefDTO     `json:"incomingEdges"`
}

func NewCmdNode(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "node <command>",
		Aliases: []string{"nodes"},
		Short:   "Work with nodes in a memory",
	}
	cmd.AddCommand(newCmdLs(f))
	cmd.AddCommand(newCmdGet(f))
	cmd.AddCommand(newCmdAdd(f))
	cmd.AddCommand(newCmdUpdate(f))
	cmd.AddCommand(newCmdMove(f))
	cmd.AddCommand(newCmdClone(f))
	cmd.AddCommand(newCmdMerge(f))
	cmd.AddCommand(newCmdRm(f))
	cmd.AddCommand(newCmdExport(f))
	cmd.AddCommand(newCmdImport(f))
	cmd.AddCommand(newCmdRevision(f))
	return cmd
}

// createDTO and updateDTO now take the shared api.AuthoredNode, because the
// write they render may have gone through any of six kind doors or the generic
// surface (#1201) — eight distinct genqlient types that Go will not convert
// between. The --json shape is unchanged.
func createDTO(n *api.AuthoredNode) nodeDTO {
	return nodeDTO{
		ID:         n.Id,
		MemoryID:   n.MemoryId,
		Loc:        n.Loc,
		Name:       n.Name,
		NodeType:   n.NodeType,
		Tags:       n.Tags,
		Seq:        nil,
		IsRunnable: boolVal(n.IsRunnable),
		Role:       n.Role,
		UpdatedAt:  n.UpdatedAt,
	}
}

func updateDTO(n *api.AuthoredNode) nodeDTO {
	return nodeDTO{
		ID:         n.Id,
		MemoryID:   n.MemoryId,
		Loc:        n.Loc,
		Name:       n.Name,
		NodeType:   n.NodeType,
		Tags:       n.Tags,
		Seq:        nil,
		IsRunnable: boolVal(n.IsRunnable),
		Role:       n.Role,
		UpdatedAt:  n.UpdatedAt,
	}
}

func mergeDTO(n *gen.UpdateNodeDataUpdateNodeDataNode) nodeDTO {
	return nodeDTO{
		ID:         n.Id,
		MemoryID:   n.MemoryId,
		Loc:        n.Loc,
		Name:       n.Name,
		NodeType:   n.NodeType,
		Tags:       n.Tags,
		Seq:        nil,
		IsRunnable: boolVal(n.IsRunnable),
		Role:       n.Role,
		UpdatedAt:  n.UpdatedAt,
	}
}

// boolVal dereferences a nullable Boolean, treating an absent value as false
// (the server treats a null isRunnable as "not runnable").
func boolVal(b *bool) bool {
	return b != nil && *b
}

// strVal flattens a nullable server string. Absent becomes "", which the
// omitempty DTO fields then leave out of --json entirely rather than emitting
// an empty string that reads like a value.
func strVal(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
