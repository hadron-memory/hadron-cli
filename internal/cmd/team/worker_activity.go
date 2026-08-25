package team

import (
	"fmt"
	"time"
)

// Rendering for hadron-server#1086's two Worker fields (hadron-cli#487):
// whether a session is OPEN on a name right now, and when the name was last
// DRIVEN.
//
// The issue this closes is not a missing column, it is a WORD. "Taken" named
// two unrelated facts — a name allocated forever, and a session open this
// minute — and a coordinator reading the roster met the second meaning first.
// So every helper here keeps the two apart on purpose:
//
//   - HELD is whose name it is (cor:agt:020:09). It is what decides whether
//     you may bind, it is freed only by an explicit release, and a held name
//     is not yours to take however idle it looks.
//   - LIVE says only that a worker session is open. A worker session outlives
//     the chat session that started it, so "live" NEVER means somebody is at
//     the keyboard, and liveness is only ever a question about a name that is
//     already yours.
//
// Anything that lets the second read as the first re-files the issue it closes.

// unknownCell is what every one of these renders when the caller was not
// permitted to read the worker's working state.
//
// NOT the em-dash the rest of this table uses, and that is the whole point.
// The adjacent RETIRED column renders "—" for a definite answer — this worker
// is not retired — so a masked row spelled with dashes reads as three settled
// facts in a row: unheld, never driven, not retired. Two of those three would
// be claims the server refused to make, rendered in the vocabulary the table
// uses for facts.
//
// That is this issue's own defect one level in: an unknown that reads as a
// settled fact. It was invisible to every structured assertion here — the
// tests named the cells and each one was correct — and visible immediately in
// the rendered table.
const unknownCell = "?"

// workingStateVisible reports whether this row's working-state fields carry
// real answers — i.e. whether the caller passed the worker read gate.
//
// hasLiveSession is the signal. Its resolver masks to null on deny and
// otherwise coalesces to false (a worker with no sessions answers false, never
// null), so the ONLY null path is the gate — and it is the same
// maySeeWorkerInternals(appId) predicate that masks heldByUserId, heldAt,
// memoryId and promptOverride, memoized per request per App. So on a row where
// this returns true, a null in any of those is a genuine absence rather than a
// mask.
//
// That is what makes "never driven" and "nobody" renderable at all: before
// #1086 the CLI had no visibility signal on Worker, and every null was
// irreducibly two things at once.
//
// STATED AS AN INFERENCE, because that is what it is. Both fields document
// their own masking, but "these two mask together" is read from
// resolvers.worker.ts (hadron-server main @ e5c6ff2) rather than from any
// contract. Asked of hadron-server rather than assumed permanent; if it is
// ever untrue, this is the one place that has to change.
func workingStateVisible(hasLiveSession *bool) bool { return hasLiveSession != nil }

// renderHeldBy is the HELD BY cell: whose name this is.
//
// selfID is currentUserID's id, and ONLY its id. That helper reports three
// states — an id, a caller definitively without one, and a lookup that failed
// — and the distinction is load-bearing where an act must be CLASSIFIED
// (`worker release` must not reclassify a self-release as a force-release on a
// failed self-read). Here it collapses safely, and the collapse is checked
// rather than assumed: both non-id states return "", "" matches no holder id,
// and the row falls through to the label — which names the holder without
// claiming it is or is not the caller. Passing `known` as well would add a
// condition with NO constructible failing input, since selfID != "" already
// implies it. A guard nothing can fail is a line of setup (this repo's
// review:a-mutation-check-can-itself-be-a-no-op), and it was one here until a
// mutation run said so.
//
// label resolves a holder id to something a human reads. It is called ONLY for
// a hold that is not the caller's own, so the common roster — everyone holding
// their own names — costs nothing, and it is called once per DISTINCT holder
// rather than once per row.
func renderHeldBy(heldByUserID *string, hasLiveSession *bool, selfID string, label func(string) string) string {
	if !workingStateVisible(hasLiveSession) {
		return unknownCell
	}
	if heldByUserID == nil || *heldByUserID == "" {
		// Sayable only because the read was permitted. A bare null still means
		// "unheld OR not visible to you", and this branch has established
		// which — see workingStateVisible.
		return "nobody"
	}
	// COUPLED to the branch above: an empty holder has already returned, so
	// this compares a non-empty holder id against selfID, and an empty selfID
	// (the caller has no id, or its lookup failed) simply matches nothing. A
	// `selfID != ""` conjunct here would read as a guard while having no input
	// that reaches it — the same unreachable-branch shape a mutation run caught
	// in the `known` flag this function used to take. If the "nobody" return
	// above is ever removed, this comparison has to be revisited in the same
	// commit.
	if *heldByUserID == selfID {
		return "you"
	}
	return label(*heldByUserID)
}

// holderLabeller resolves holder ids to display labels, once per distinct id.
//
// The roster would otherwise print an opaque uuid for a fact `worker get`
// renders as a name — the same entity answering two surfaces two ways, which
// is the asymmetry PR #520 was reviewed for one command over. It is also what
// the MCP roster prints, and the two rosters answering one question in two
// vocabularies is the thing #487 is about.
//
// DECORATION, never a gate, exactly like describeHolder itself: a caller
// entitled to see a hold may not be entitled to read that person's user
// record, and the fallback is the raw id rather than a failure. Memoized
// because a team's distinct holders are few while its rows are many, and
// because a repeated miss would otherwise re-pay a failed lookup per row.
func holderLabeller(resolve func(string) string) func(string) string {
	seen := map[string]string{}
	return func(id string) string {
		if label, ok := seen[id]; ok {
			return label
		}
		label := resolve(id)
		seen[id] = label
		return label
	}
}

// renderLastDriven is the LAST DRIVEN cell, and it carries liveness too.
//
// ONE column rather than two, and no age beside a live session — both
// deliberate, and both are the server engineer's call on #1086 rather than
// mine. During a live session "live" IS the answer, and an age printed next to
// it invites reading the age as presence: the reader asks "driven 4h ago, so
// is anyone there?" when the honest answer is that a session is open and
// nobody may be at the keyboard at all. A separate ACTIVE/DRIVING column has
// the same failure one step earlier — it reads as availability, which is the
// HOLD, which is the conflation this issue exists to remove.
//
// "never" is the value the whole issue was filed for: a casting nobody has
// picked up and one worked yesterday render identically on every other
// surface, so a coordinator dispatches into a channel no one reads and gets no
// signal at all.
func renderLastDriven(hasLiveSession *bool, lastActiveAt *string, now time.Time) string {
	if !workingStateVisible(hasLiveSession) {
		return unknownCell
	}
	if *hasLiveSession {
		return "live"
	}
	if lastActiveAt == nil || *lastActiveAt == "" {
		return "never"
	}
	at, err := time.Parse(time.RFC3339, *lastActiveAt)
	if err != nil {
		// The raw value rather than a dash or a guess. A DateTime is
		// guaranteed parseable, not guaranteed canonical ISO UTC (see the
		// scalar binding in genqlient.yaml), so a shape this does not know is
		// possible — and showing it lets a reader see WHAT the server said,
		// where "—" would claim the caller is not permitted to know.
		return *lastActiveAt
	}
	return workerActivityAge(at, now)
}

// workerActivityAge is a compact relative age — "3d ago".
//
// A deliberate mirror of the MCP roster's ageOf (hadron-server src/mcp/
// server.ts), thresholds and wording included, so the two rosters answering
// the same question do not answer it in two vocabularies. Coarse on purpose: a
// coordinator is asking "has anyone touched this in a while?", and
// minute-precision on a two-week-old session invites treating it as presence.
//
// Rounds DOWN, so it never overstates recency. A future timestamp — clock skew
// between the server and this host — falls out as "just now" rather than a
// negative age.
func workerActivityAge(at, now time.Time) string {
	mins := int(now.Sub(at) / time.Minute)
	if mins < 1 {
		return "just now"
	}
	if mins < 60 {
		return fmt.Sprintf("%dm ago", mins)
	}
	hours := mins / 60
	if hours < 24 {
		return fmt.Sprintf("%dh ago", hours)
	}
	return fmt.Sprintf("%dd ago", hours/24)
}
