package coding

import "strings"

// Membership — which nodes hanging off the review parent are actually checklist
// items.
//
// This is the load-bearing predicate of `coding review lint`. The review parent
// legitimately has non-checklist neighbours: the preflight router, the tasks
// that consume the tree, a meta backlog, pattern nodes, and findings. Linting
// every inbound edge (the obvious reading) produced 9 findings on
// micromentor.org::mmdata of which 5 were false positives — a linter that cries
// wolf on more than half its output does not get used.
//
// The rule is the child-loc prefix of the tree being linted, minus two
// disqualifiers:
//
//	review:thin-resolver-field   tags=[review …]      isRunnable=false  → check
//	review:format-sources        tags=[]              isRunnable=false  → check
//	review:backlog               tags=[review meta …] isRunnable=false  → meta, excluded
//	tasks:review-changes         tags=[]              isRunnable=true   → task, excluded
//	patterns:function-signatures tags=[conventions …] isRunnable=false  → pattern, excluded
//	findings:nightly-…-race      tags=[… review …]    isRunnable=false  → finding, excluded
//
// Two signals were considered and rejected on evidence:
//
//   - A `review` TAG requirement excludes 17 of mmdata's 48 checks, which carry
//     no tags at all — including `review:format-sources`, the misfiled-toolchain
//     example that motivated this command. A tag-only rule silently ignored 35%
//     of the checklist: a false negative that counting false positives alone
//     would never have surfaced.
//   - Accepting the `review` tag as an ALTERNATIVE to the prefix admits
//     findings nodes, which carry the tag because they are review-relevant.
//     Across micromentor.org::mmdata and hadronmemory.com::hadron-portal, every
//     node tagged `review` outside the prefix is a finding and every real check
//     is inside it, so the tag contributes nothing but noise.
//
// Note that `review:backlog` shares the prefix, so the `meta` disqualifier is
// what keeps it out.
//
// See docs/plans/coding-command-group.md, Decision 1.
const metaTag = "meta"

// childPrefix is the loc prefix a lint root's children carry: `review` → `review:`.
func childPrefix(rootLoc string) string { return rootLoc + ":" }

// isChecklistItem reports whether n is a check under the given root.
func isChecklistItem(rootLoc string, n checkNode) bool {
	return isChecklistItemListing(rootLoc, n.Loc, n.Tags, &n.IsRunnable)
}

// isChecklistItemListing is the same predicate over the cheaper listing
// projection, so the memory sweep can filter before the bulk read. A nil
// isRunnable means unset, which most nodes leave that way and which does not
// disqualify.
func isChecklistItemListing(rootLoc, loc string, tags []string, isRunnable *bool) bool {
	if isRunnable != nil && *isRunnable {
		return false
	}
	if hasTag(tags, metaTag) {
		return false
	}
	return strings.HasPrefix(loc, childPrefix(rootLoc))
}
