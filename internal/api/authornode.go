package api

import (
	"context"
	"errors"

	"github.com/Khan/genqlient/graphql"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
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
// per-memory list of loc patterns — but by KIND, a property of the node itself,
// read off the RESULTING state of the write:
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

// AuthoredNode is the node an authoring write returns. Aliased so call sites do
// not spell the deeply-nested genqlient name, matching the ListNode/batchNode
// pattern.
//
// The per-kind doors return the same selection set, so one alias still serves
// every caller; it is spelled against the spec door because that is the kind
// with the most call sites.
type AuthoredNode = gen.CreateSpecNodeCreateSpecNode

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
// it, and that is on purpose: the gate reads the RESULTING state, so a node's
// kind is a property of the node being written, not of the function called to
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
	return resp.CreateSpecNode, nil
}

// CreateReviewNode writes a review check through the review door. As with
// CreateSpecNode, the caller sets `input.Role`.
func CreateReviewNode(ctx context.Context, client graphql.Client, input *gen.CreateNodeInput) (*gen.CreateReviewNodeCreateReviewNode, error) {
	resp, err := gen.CreateReviewNode(ctx, client, input)
	if err != nil {
		return nil, err
	}
	if resp == nil || resp.CreateReviewNode == nil {
		return nil, errors.New("createReviewNode returned no node")
	}
	return resp.CreateReviewNode, nil
}

// UpdateSpecNode edits a spec node through the spec door — the counterpart the
// create half had to wait for (hadron-server #1192, then folded into #1201).
//
// It is needed even when an edit touches neither `role` nor `isRunnable`,
// because the gate reads the resulting state: editing a node that already
// carries `role: "spec"` still produces one, so the generic `updateNode`
// refuses it.
//
// It keeps `updateNode`'s selectors and, crucially, its OMIT-TO-PRESERVE
// semantics — which is why routing edits through the create door with
// `upsert: true` was never the workaround it looked like: `CreateNodeInput` has
// no omit-to-preserve, and `spec edit` depends on it.
func UpdateSpecNode(ctx context.Context, client graphql.Client, input *gen.UpdateNodeInput) (*gen.UpdateSpecNodeUpdateSpecNode, error) {
	resp, err := gen.UpdateSpecNode(ctx, client, input)
	if err != nil {
		return nil, err
	}
	if resp == nil || resp.UpdateSpecNode == nil {
		return nil, errors.New("updateSpecNode returned no node")
	}
	return resp.UpdateSpecNode, nil
}
