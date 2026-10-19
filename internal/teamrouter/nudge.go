package teamrouter

import (
	"fmt"
	"strings"
)

// largeGapSpan: above this seq span between the first unread and the head,
// the unfiltered walk switches to the mentions-only anchor (#1811). The SPAN,
// not the unread count, is what the unfiltered instruction would page
// through. The mentions walk still covers EVERY unread mention.
const largeGapSpan = 400

// NudgeText is the one line a worker receives: fresh counts with the seq
// range they cover — never a message body, never a token. The seqs let the
// recipient check the nudge against its own read cursor (#1616).
//
// Two anchors (#1811): at or below the large-gap span, the #1805 watermark
// anchor (start at the first unread, walk forward); above it, a BACKWARD walk
// of the mentions filter from the head THIS NUDGE NAMED (frozen as every
// page's --before ceiling, #1827) down to the first unread, then a mark at the
// highest seq actually delivered — never above the named head.
func NudgeText(a AttentionItem) string {
	var where []string
	if a.FirstUnreadSeq != nil {
		where = append(where, fmt.Sprintf("unread #%d–#%d", *a.FirstUnreadSeq, a.LastSeq))
	}
	if a.LastRelevantSeq != 0 {
		where = append(where, fmt.Sprintf("latest mention #%d", a.LastRelevantSeq))
	}
	plural := "s"
	if a.UnreadMentions == 1 {
		plural = ""
	}
	whereText := ""
	if len(where) > 0 {
		whereText = "; " + strings.Join(where, ", ")
	}
	head := fmt.Sprintf("Hadron team attention: %d relevant unread (%d mention%s%s).", a.Unread, a.UnreadMentions, plural, whereText)

	if a.FirstUnreadSeq != nil && a.LastSeq-*a.FirstUnreadSeq > largeGapSpan {
		since := max(0, *a.FirstUnreadSeq-1)
		// Bounded both ways by values this nudge named: the ceiling is the
		// head (an H+1 mention posted during the walk can neither enter a page
		// nor be acknowledged), the floor the first unread. BACKWARD, because
		// beforeSeq selects the NEWEST page under the bound (#1827).
		bound := a.LastSeq + 1
		return head +
			fmt.Sprintf(" LARGE GAP (unread span %d > %d): walk MENTIONS ONLY from your first unread to the head named here — every unread mention is on the path; the rest is history.", a.LastSeq-*a.FirstUnreadSeq, largeGapSpan) +
			fmt.Sprintf(" CLI: `hadron team chat read --mentions-me --since %d --before %d --limit 200`, then follow each page's prevBefore BACKWARD to your first unread — beforeSeq selects the NEWEST page under the bound, so page down to the floor and stop at an empty or floor page; MCP: `hadron_team_chat_read` with your session, `sinceSeq=%d`, `beforeSeq=%d`, its mentions filter and limit 200, then re-call with `beforeSeq` = the smallest seq on your last page until the floor (MCP pages carry no prevBefore field).", since, bound, since, bound) +
			fmt.Sprintf(" Then mark read through your OWN session at the highest seq actually delivered to you, never above #%d (CLI: `hadron team chat mark-read --through <that seq>`; MCP: `hadron_team_chat_mark_read` — a mentions-filtered read never auto-marks).", a.LastSeq) +
			" An incomplete walk marks NOTHING; anything outside the walked bound — messages arriving now included — stays unread. If no mention needs action, mark read and end with one short line (for example \"read through #N, nothing in my lane\"); do not post to team chat." +
			" (Orca attention router; counts as of send.)"
	}
	// Watermark-anchored (Eden #9017/#9035): workers with a stale read mark
	// read the NEWEST page, found nothing for them and stopped — while fresh
	// mentions sat at their watermark. Name the mark; say where to start.
	from := ""
	if a.FirstUnreadSeq != nil {
		since := max(0, *a.FirstUnreadSeq-1)
		from = fmt.Sprintf(" START AT #%d (your first unread message of any kind) — CLI: `hadron team chat read --since %d --limit 200` then follow nextSince; MCP: `hadron_team_chat_read` with your session and `sinceSeq=%d` then the returned cursor — NOT the newest page alone;", *a.FirstUnreadSeq, since, since)
	}
	return head + from + " " +
		"Read team chat unfiltered with your own live session; if nothing needs action, " +
		"mark read and end with one short line (for example \"read through #N, nothing in my lane\"); do not post to team chat. (Orca attention router; counts as of send.)"
}

// LivenessNudgeText is the one line a FLAGGED worker receives from the
// liveness rule (#1826). Its own literal, never NudgeText's: it makes NO
// mention-count claim, names its rule (Taylor #9748), names the two positions
// it is true for — both read fresh immediately before the send — and anchors
// an unfiltered catch-up from the watermark. "Mark read" appears exactly once.
func LivenessNudgeText(cursor, head int) string {
	return "Hadron team liveness check — the idle-watch rule for your roster flag, not a mention nudge: " +
		fmt.Sprintf("the team chat has moved past your read watermark (your cursor #%d < head #%d). ", cursor, head) +
		fmt.Sprintf("Catch up: read team chat unfiltered with your own live session — CLI: `hadron team chat read --since %d --limit 200` then follow nextSince; MCP: `hadron_team_chat_read` with your session and `sinceSeq=%d` then the returned cursor — ", cursor, cursor) +
		"then mark read through your OWN session. " +
		"If nothing needs action, end with one short line (for example \"read through #N, nothing in my lane\"); do not post to team chat. " +
		"(Orca attention router; liveness nudge; cursor and head as of send.)"
}
