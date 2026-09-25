package api

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/Khan/genqlient/graphql"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// The KIND-SPECIFIC authoring doors (hadron-server #1201, shipped in #1203).
//
// #1203 replaced the single `authorProtectedNode` with one door per kind AND
// DELETED the generic one, so this is not a rename that could be taken at
// leisure: the old field stopped existing on the server and every call site in
// this repo broke at once. The published CLI was unaffected only because
// `766e0c5` landed after v0.13.0.
//
// # What changed underneath, and why it is a better shape
//
// `Memory.protectedLocs` is GONE. Protection is no longer by ADDRESS — a
// per-memory list of loc patterns — but by KIND, a property of the node itself.
// A write needs the door of every kind it touches: the kind the node will be,
// and for an update also the kind it is now (before ∪ after, cli#714):
//
//	role: "spec"     -> createSpecNode   / updateSpecNode
//	role: "review"   -> createReviewNode / updateReviewNode
//	isRunnable: true -> createTaskNode   / updateTaskNode
//
// The gain is containment. One generic door was exempt from EVERY declared
// entry in every memory, so holding it was a skeleton key; a kind-specific door
// is exempt from its own kind only and remains fully subject to the others and
// to Channel address protection, so it cannot forge a chat message.
//
// # The three are NOT equally strong, and the server says so in its own SDL
//
//   - SPEC is DILIGENCE. It attests that the spec tool authored the node — not
//     that a determined caller could not author one another way. The label is
//     free to omit and a citation resolves without it. This repo's older
//     "guardrail, not a boundary" note was about exactly this and still holds.
//   - REVIEW is SECURITY. A review may be a SAFETY CHECK, and any work can be
//     reviewed — so an attacker who can modify an assignment can also remove the
//     checks meant to catch that.
//   - TASK is CAPABILITY, and the only one whose gate cannot be evaded by
//     omission: it keys on `isRunnable` rather than on a label, because a label
//     is caller-set and freely dropped while omitting the capability means the
//     node does not run.
//
// Do not let anything built on the spec door imply the specs corpus is as
// tamper-evident as a chat message. That was true of the old door and is still
// true of this one.
//
// # No `upsert`, and the argument it settles
//
// The old door took `upsert` and every call site in this repo passed FALSE —
// deliberately, against cli#606's suggestion, because two commands PROMISE the
// refusal in user-visible text (`spec new` exits Conflict with "<citation>
// already exists"; `coding review create`'s help says a check that already
// exists "fails rather than overwriting it"), so true would have turned a
// documented refusal into a silent overwrite of a permanent citation.
//
// The new doors do not offer the argument at all, so that decision is now the
// server's and cannot be reopened per-call-site. A re-run against a live loc is
// a NodeLocConflictError, which cli#610 mapped to exit 5.
//
// # Deliberately not MCP tools
//
// An agent's node surface is `hadron_create_node`, which refuses a governed
// write. That ABSENCE is the mechanism rather than an oversight — the
// protection is a property of the door being off the agent surface, not of the
// caller's identity.

// AuthoredNode is the node an authoring write returns.
//
// A CONCRETE struct rather than an alias onto one door's generated type, which
// is what it used to be. Six doors plus the two generic surfaces return eight
// structurally similar but DISTINCT genqlient types, and Go will not convert
// between them — so aliasing one of them forced every dispatcher to pick a
// winner and cast, which does not compile and should not. Mapping once here
// keeps the call sites off genqlient shapes, the same reason the command
// packages marshal their own DTOs.
type AuthoredNode struct {
	Id         string
	MemoryId   string
	Loc        string
	Name       string
	NodeType   string
	Tags       []string
	Seq        *int
	IsRunnable *bool
	Role       *string
	UpdatedAt  string
}

// authoredNodeFrom maps any door's (or generic surface's) result into the shared
// shape. Generic over the eight structurally-identical generated types via an
// interface every one of them satisfies — genqlient emits the getters.
func authoredNodeFrom(n interface {
	GetId() string
	GetMemoryId() string
	GetLoc() string
	GetName() string
	GetNodeType() string
	GetTags() []string
	GetSeq() *int
	GetIsRunnable() *bool
	GetRole() *string
	GetUpdatedAt() string
}) *AuthoredNode {
	return &AuthoredNode{
		Id: n.GetId(), MemoryId: n.GetMemoryId(), Loc: n.GetLoc(), Name: n.GetName(),
		NodeType: n.GetNodeType(), Tags: n.GetTags(), Seq: n.GetSeq(),
		IsRunnable: n.GetIsRunnable(), Role: n.GetRole(), UpdatedAt: n.GetUpdatedAt(),
	}
}

// SpecNodeRole is the governed `Node.role` value that routes a write to the
// spec door. Setting it is what makes the node governed — the door is how the
// write gets through, and the role is what the gate reads.
const SpecNodeRole = "spec"

// ReviewNodeRole is the governed `Node.role` value for a review check. #1201
// put `review` in the register for a SECURITY reason rather than a tidiness
// one: a review may be a safety check, so being able to remove one silently is
// the attack.
const ReviewNodeRole = "review"

// CreateSpecNode writes a spec node through the spec door.
//
// The caller must set `input.Role` to SpecNodeRole — this wrapper does not do
// it, and that is on purpose: for a create the gate reads the state being
// written, so a node's kind is a property of the node being written, not of the function called to
// write it. Setting the role here would let a call site that forgot to think
// about the kind still produce a governed node, which is the wrong direction
// for a signal whose whole job is to be explicit.
func CreateSpecNode(ctx context.Context, client graphql.Client, input *gen.CreateNodeInput) (*AuthoredNode, error) {
	resp, err := gen.CreateSpecNode(ctx, client, input)
	if err != nil {
		return nil, err
	}
	// `createSpecNode` is `Node!`, so a nil here is a malformed response rather
	// than a legal "no node". The check lives once so no call site can forget it
	// and then dereference.
	if resp == nil || resp.CreateSpecNode == nil {
		return nil, errors.New("createSpecNode returned no node")
	}
	return authoredNodeFrom(resp.CreateSpecNode), nil
}

// CreateReviewNode writes a review check through the review door. As with
// CreateSpecNode, the caller sets `input.Role`.
func CreateReviewNode(ctx context.Context, client graphql.Client, input *gen.CreateNodeInput) (*AuthoredNode, error) {
	resp, err := gen.CreateReviewNode(ctx, client, input)
	if err != nil {
		return nil, err
	}
	if resp == nil || resp.CreateReviewNode == nil {
		return nil, errors.New("createReviewNode returned no node")
	}
	return authoredNodeFrom(resp.CreateReviewNode), nil
}

// UpdateSpecNode edits a spec node through the spec door — the counterpart the
// create half had to wait for (hadron-server #1192, then folded into #1201).
//
// It is needed even when an edit touches neither `role` nor `isRunnable`,
// because the gate reads the kind the node is now as well as the kind it will
// be: editing a node that already carries `role: "spec"` touches the spec kind,
// so the generic `updateNode` refuses it.
//
// It keeps `updateNode`'s selectors and, crucially, its OMIT-TO-PRESERVE
// semantics — which is why routing edits through the create door with
// `upsert: true` was never the workaround it looked like: `CreateNodeInput` has
// no omit-to-preserve, and `spec edit` depends on it.
func UpdateSpecNode(ctx context.Context, client graphql.Client, input *gen.UpdateNodeInput) (*AuthoredNode, error) {
	resp, err := gen.UpdateSpecNode(ctx, client, input)
	if err != nil {
		return nil, err
	}
	if resp == nil || resp.UpdateSpecNode == nil {
		return nil, errors.New("updateSpecNode returned no node")
	}
	return authoredNodeFrom(resp.UpdateSpecNode), nil
}

// TaskNodeCapability is not a `role` at all, and that asymmetry is the point.
// The task gate keys on `isRunnable` — a CAPABILITY, which a caller cannot drop
// to evade the gate without the node ceasing to run — while spec and review key
// on a label that is free to omit. There is no constant to compare against here;
// the predicate is the boolean itself.

// GovernedKindConflict reports the governed kinds a write would produce when
// there is MORE THAN ONE, and nil otherwise.
//
// No door can write such a node. Each is exempt from its OWN kind only and
// stays fully subject to the others, so `isRunnable: true` + `role: "spec"` is
// refused by `createTaskNode` (for the role) and by `createSpecNode` (for the
// capability) alike. The server's refusal names one of them, which makes it
// actively misleading: it sends the caller to a door that will also refuse.
//
// THIS IS ROUTING, NOT VALIDATION, and the distinction is the one @Ada drew on
// #615 — the CLI must not refuse a role VALUE, because the register is a
// server-side closed list that changes without this client knowing. It says
// nothing here about whether a value is governed in general; it reports that
// the mapping this client holds, which it needs anyway to pick a mutation,
// yields two doors and therefore none.
//
// The degradation is safe and loud: if the server later un-governs a kind, this
// refuses a write that would now succeed — a visible Usage error naming
// `hadron api` as the way through, rather than a silent wrong result.
func GovernedKindConflict(role *string, isRunnable bool) []string {
	if kinds := governedKinds(role, isRunnable); len(kinds) > 1 {
		return kinds
	}
	return nil
}

// The governed kinds, named as GovernedKindConflict names them so a refusal
// reads the same whichever check produced it.
const (
	kindTask   = "task (isRunnable)"
	kindSpec   = "role " + SpecNodeRole
	kindReview = "role " + ReviewNodeRole
)

// governedKinds lists the governed kinds a (role, isRunnable) state carries.
func governedKinds(role *string, isRunnable bool) []string {
	var kinds []string
	if isRunnable {
		kinds = append(kinds, kindTask)
	}
	if role != nil {
		switch *role {
		case SpecNodeRole:
			kinds = append(kinds, kindSpec)
		case ReviewNodeRole:
			kinds = append(kinds, kindReview)
		}
	}
	return kinds
}

// CreateNodeByKind writes a node through the door its KIND requires, or through
// the generic `createNode` when it is of no governed kind.
//
// It exists because the gate is a property of the NODE, not of the command: any
// caller that can set `role` or `isRunnable` can produce a governed node, and
// `hadron node add --runnable` does exactly that. Before this, that command sent
// a runnable node to the generic surface and was refused — a live break wider
// than the authoring commands, found by @codex on #614 and reproduced against
// the server before being believed.
func CreateNodeByKind(ctx context.Context, client graphql.Client, input *gen.CreateNodeInput) (*AuthoredNode, error) {
	switch {
	case input.IsRunnable != nil && *input.IsRunnable:
		resp, err := gen.CreateTaskNode(ctx, client, input)
		if err != nil {
			return nil, err
		}
		if resp == nil || resp.CreateTaskNode == nil {
			return nil, errors.New("createTaskNode returned no node")
		}
		return authoredNodeFrom(resp.CreateTaskNode), nil
	case input.Role != nil && *input.Role == SpecNodeRole:
		return CreateSpecNode(ctx, client, input)
	case input.Role != nil && *input.Role == ReviewNodeRole:
		return CreateReviewNode(ctx, client, input)
	}
	resp, err := gen.CreateNode(ctx, client, input)
	if err != nil {
		return nil, err
	}
	if resp == nil || resp.CreateNode == nil {
		return nil, errors.New("createNode returned no node")
	}
	return authoredNodeFrom(resp.CreateNode), nil
}

// NodeKindState is what a node ALREADY is, which an update needs because the
// gate reads the kind the node is now as well as the kind it will be, and an
// omitted field preserves the stored one.
type NodeKindState struct {
	Role       *string
	IsRunnable bool
}

// touchedKinds is every governed kind an update touches: the ones the node
// carries now and the ones it will carry, an omitted field preserving the
// stored value.
func touchedKinds(input *gen.UpdateNodeInput, cur NodeKindState) []string {
	runnable := cur.IsRunnable
	if input.IsRunnable != nil {
		runnable = *input.IsRunnable
	}
	role := cur.Role
	if input.Role != nil {
		role = input.Role
	}
	touched := governedKinds(cur.Role, cur.IsRunnable)
	for _, k := range governedKinds(role, runnable) {
		if !slices.Contains(touched, k) {
			touched = append(touched, k)
		}
	}
	return touched
}

func refuseTouched(touched []string) error {
	if len(touched) < 2 {
		return nil
	}
	return exitcode.Newf(exitcode.Usage,
		"this write touches two governed kinds — %s — and each door is exempt from its OWN kind only, so every one of them refuses it. Change one kind at a time, or write it with `hadron api` if the server's register has changed",
		strings.Join(touched, " AND "))
}

// CheckUpdateKinds is UpdateNodeByKind's refusal without the write, for a
// caller that must know before it reports a plan or prompts (node import's
// --dry-run and overwrite prompt): nil when some door can make the update.
func CheckUpdateKinds(input *gen.UpdateNodeInput, cur NodeKindState) error {
	return refuseTouched(touchedKinds(input, cur))
}

// UpdateNodeByKind edits a node through the door that owns every governed
// kind the write TOUCHES — the kind it is now and the kind it will be.
//
// THE SERVER GATE IS BEFORE ∪ AFTER, and that is the whole difficulty
// (hadron-server governedRole.ts, assertGovernedWrite). Two consequences:
//
//   - An update that touches neither `role` nor `isRunnable` still edits a
//     governed node when the stored one is governed, so a plain
//     `--description` edit of a review check or a task needs that kind's door.
//     Hence the node's current kind is an argument: an omitted field preserves
//     the stored value.
//   - REMOVING a kind needs that kind's door too. `isRunnable: false` on a task
//     or a role change away from `spec` leaves an ungoverned node, but the
//     generic surface refuses it ("removes"): the gate exists so the generic
//     surface cannot quietly un-govern a node. Routing on the resulting state
//     alone sent exactly those writes to updateNode (cli#714).
//
// A write that touches two governed kinds — a task given `role: spec`, or a
// spec turned into a task — has no door, because each door is exempt from its
// OWN kind only. It is refused here as a Usage error rather than sent to be
// refused. WHO may use a door is hadron-server#1202, not this client's call.
func UpdateNodeByKind(ctx context.Context, client graphql.Client, input *gen.UpdateNodeInput, cur NodeKindState) (*AuthoredNode, error) {
	touched := touchedKinds(input, cur)
	if err := refuseTouched(touched); err != nil {
		return nil, err
	}
	kind := ""
	if len(touched) == 1 {
		kind = touched[0]
	}
	switch kind {
	case kindTask:
		resp, err := gen.UpdateTaskNode(ctx, client, input)
		if err != nil {
			return nil, err
		}
		if resp == nil || resp.UpdateTaskNode == nil {
			return nil, errors.New("updateTaskNode returned no node")
		}
		return authoredNodeFrom(resp.UpdateTaskNode), nil
	case kindSpec:
		return UpdateSpecNode(ctx, client, input)
	case kindReview:
		resp, err := gen.UpdateReviewNode(ctx, client, input)
		if err != nil {
			return nil, err
		}
		if resp == nil || resp.UpdateReviewNode == nil {
			return nil, errors.New("updateReviewNode returned no node")
		}
		return authoredNodeFrom(resp.UpdateReviewNode), nil
	}
	resp, err := gen.UpdateNode(ctx, client, input)
	if err != nil {
		return nil, err
	}
	if resp == nil || resp.UpdateNode == nil {
		return nil, errors.New("updateNode returned no node")
	}
	return authoredNodeFrom(resp.UpdateNode), nil
}
