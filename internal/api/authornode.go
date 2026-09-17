package api

import (
	"context"
	"errors"

	"github.com/Khan/genqlient/graphql"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// AuthoredNode is the node an authoring write returns. Aliased so call sites do
// not spell the deeply-nested genqlient name, matching the ListNode/batchNode
// pattern.
type AuthoredNode = gen.AuthorProtectedNodeAuthorProtectedNode

// AuthorProtectedNode writes a node through the protected-loc DOOR
// (hadron-server #1180, cli #606) instead of the generic `createNode`.
//
// It is what `hadron spec` and `hadron coding` must use, and the reason is
// specific: once a memory declares `Memory.protectedLocs` — `['*']` on the
// specs corpus, `['review','tasks']` on a repo memory — the generic node write
// is REFUSED at those locs. The authoring commands would then be refused by the
// very guard meant to protect the corpus they own. This door carries the
// bypass; nothing else changes.
//
// It is PURE ROUTING. The server validates nothing here that `createNode` does
// not, and knows nothing about specs, reviews or tasks — the numbering, rubric
// and templates stay in this repo, which cor:api:240:02 permits because both
// command groups target an audience that can be required to install a CLI.
//
// **A guardrail, not a security boundary.** What it prevents is the ACCIDENTAL
// generic write, refused and told which tool to use. A determined caller can
// still author a malformed node THROUGH the door — `hadron spec` and a
// hand-rolled `hadron_create_node` authenticate identically, which is exactly
// why the exemption had to be a distinct server entry point and could not be a
// flag this CLI sets. The Channel source of the same gate IS a boundary
// (#1047, where authorship was forgeable and not tamper-evident): same
// mechanism, same error code, materially different strengths. Do not let
// anything built on this imply the specs corpus is as tamper-evident as a chat
// message.
//
// # On upsert
//
// Every call site in this repo passes FALSE, and that is a decision rather than
// an oversight — cli#606 suggested `upsert: true` for "the re-run case".
//
// No authoring command upserts. BEFORE this move they wrote through
// `createNode`, which refuses a live `(memoryId, loc)`; passing `upsert: true`
// here would have quietly changed that behaviour while moving them, which is
// not what a routing change is for. Two of them promise the refusal in
// user-visible text — `spec new` exits Conflict with "<citation> already
// exists", and `coding review create`'s help says a check that already exists
// "fails rather than overwriting it" — so true would have turned a documented
// refusal into a silent overwrite.
//
// The door preserves it: `authorProtectedNode` with upsert false rejects a live
// loc with the same NodeLocConflictError `createNode` does. Verified on the
// real server, not inferred: re-running `coding review create` against a loc it
// had just minted refuses, and the original node is unchanged.
//
// It would also undo work this team had just finished: hadron-server #1182 and
// #1184 closed exactly this hole on the generic surfaces, after a racing
// `createNode` was measured silently replacing a live node 40 times out of 40.
// A permanent citation whose node can be overwritten by a re-run is the hazard
// `findings:persona-name-allocation-two-uniques` calls a reclaim convenience
// that silently frees a permanent name.
//
// So the argument is carried — the wrapper mirrors the mutation rather than
// hard-coding half its contract — and every caller says false. Changing that
// for a command is a behaviour change to that command, and wants its own issue
// and its own ruling, not a default.
func AuthorProtectedNode(
	ctx context.Context,
	client graphql.Client,
	input *gen.CreateNodeInput,
	upsert bool,
) (*AuthoredNode, error) {
	resp, err := gen.AuthorProtectedNode(ctx, client, input, &upsert)
	if err != nil {
		return nil, err
	}
	// `authorProtectedNode` is `Node!`, so a nil here is a malformed response
	// rather than a legal "no node". Callers previously repeated this check
	// against createNode; it lives once, here, so no call site can forget it
	// and then dereference.
	if resp == nil || resp.AuthorProtectedNode == nil {
		return nil, errors.New("authorProtectedNode returned no node")
	}
	return resp.AuthorProtectedNode, nil
}
