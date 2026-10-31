package teamrouter

// Ported 1:1 from router.test.ts describe('nudgeText read anchor (#1805
// P2s, #1811 large-gap branch)') — the wake text's read instruction.

import (
	"strings"
	"testing"
)

func TestNudgeAnchorPresentNamesFirstUnreadCLIFormBounded(t *testing.T) {
	text := NudgeText(worker("w-eli", "Eli", workerOpts{mentions: ip(2), unread: ip(5), first: ip(100), relevant: ip(110), head: ip(120)}))
	for _, want := range []string{"START AT #100 (your first unread message of any kind)", "--since 99 --limit 200", "nextSince"} {
		if !strings.Contains(text, want) {
			t.Errorf("text missing %q", want)
		}
	}
	if strings.Contains(text, "--all") {
		t.Errorf("text must not suggest --all")
	}
	assertNoMCP(t, text)
	if strings.Contains(text, "\n") {
		t.Errorf("text must be one line")
	}
}

func assertNoMCP(t *testing.T, text string) {
	t.Helper()
	for _, not := range []string{"MCP", "hadron_team_chat_read", "hadron_team_chat_mark_read", "sinceSeq", "beforeSeq"} {
		if strings.Contains(text, not) {
			t.Errorf("text must not name the disabled MCP surface: contains %q", not)
		}
	}
}

func TestNudgeAnchorNullGenericTextNoMentionsWalk(t *testing.T) {
	text := NudgeText(AttentionItem{Worker: "w-eli", WorkerName: "Eli", Channel: channel,
		Unread: 3, UnreadMentions: 1, FirstUnreadSeq: nil, LastRelevantSeq: 110, LastSeq: 120})
	if strings.Contains(text, "START AT") {
		t.Errorf("null anchor must carry no START clause")
	}
	if !strings.Contains(text, "Read team chat unfiltered") {
		t.Errorf("text missing the generic unfiltered instruction")
	}
	if strings.Contains(text, "--mentions-me") {
		t.Errorf("null anchor must not trigger the mentions walk")
	}
}

func TestNudgeFirstUnreadOneSmallSpanClampsToZero(t *testing.T) {
	text := NudgeText(worker("w-eli", "Eli", workerOpts{mentions: ip(2), unread: ip(349), first: ip(1), relevant: ip(110), head: ip(350)}))
	for _, want := range []string{"--since 0 --limit 200"} {
		if !strings.Contains(text, want) {
			t.Errorf("text missing %q", want)
		}
	}
	if strings.Contains(text, "--all") || strings.Contains(text, "--mentions-me") {
		t.Errorf("small span keeps the unfiltered anchor")
	}
}

func TestNudgeCursor0LargeGapWalkStartsAtZero(t *testing.T) {
	text := NudgeText(worker("w-eli", "Eli", workerOpts{mentions: ip(2), unread: ip(9000), first: ip(1), relevant: ip(110), head: ip(9001)}))
	for _, want := range []string{"--mentions-me --since 0 --before 9002 --limit 200"} {
		if !strings.Contains(text, want) {
			t.Errorf("text missing %q", want)
		}
	}
	if strings.Contains(text, "--all") || strings.Contains(text, "START AT") {
		t.Errorf("large gap switches to the mentions anchor")
	}
}

func TestNudgeLargeGapMentionsOnlyAnchorFullDiscipline(t *testing.T) {
	text := NudgeText(worker("w-eli", "Eli", workerOpts{mentions: ip(2), unread: ip(9841), first: ip(61), relevant: ip(9750), head: ip(9901)}))
	for _, want := range []string{
		"LARGE GAP", "unread #61–#9901, latest mention #9750",
		"--mentions-me --since 60 --before 9902 --limit 200",
		"mark-read --through", "marks NOTHING", "your OWN session", "prevBefore",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("text missing %q", want)
		}
	}
	for _, not := range []string{"START AT", "Read team chat unfiltered", "--all"} {
		if strings.Contains(text, not) {
			t.Errorf("text must not contain %q", not)
		}
	}
	assertNoMCP(t, text)
	if strings.Contains(text, "\n") {
		t.Errorf("text must be one line")
	}
}

func TestNudgeFrozenSampleBoundsAtNudgeHead(t *testing.T) {
	// Anne's counterexample: page 1 samples head 9901; a mention posted at
	// 9902 during the walk must not enter any later page, and the mark must
	// never reach it. The walk's upper bound is the nudge's OWN head value.
	text := NudgeText(worker("w-eli", "Eli", workerOpts{mentions: ip(2), unread: ip(9841), first: ip(61), relevant: ip(9750), head: ip(9901)}))
	for _, want := range []string{"--before 9902", "never above #9901", "actually delivered"} {
		if !strings.Contains(text, want) {
			t.Errorf("text missing %q", want)
		}
	}
	if strings.Contains(text, "--before 9903") {
		t.Errorf("the bound must not extend past the nudge's head")
	}
}

func TestNudgeMultiPageWalkRunsBackwardViaPrevBefore(t *testing.T) {
	// Anne's trace + Colin's live control (#9687/#9689): --before forces
	// NEWEST-page selection, so a forward nextSince walk with 201 unread
	// mentions loses the oldest #1. The instruction must walk BACKWARD
	// (prevBefore, decreasing --before) down to the fixed since floor.
	text := NudgeText(worker("w-eli", "Eli", workerOpts{mentions: ip(201), unread: ip(9841), first: ip(61), relevant: ip(9750), head: ip(9901)}))
	for _, want := range []string{
		"prevBefore", "BACKWARD",
		"--mentions-me --since 60 --before 9902 --limit 200", "never above #9901", "limit 200",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("text missing %q", want)
		}
	}
	assertNoMCP(t, text)
	if strings.Contains(text, "nextSince") {
		t.Errorf("the forward cursor is what lost the oldest mention — must not appear")
	}
	if strings.Contains(text, "--before 9903") {
		t.Errorf("the bound must not extend past the nudge's head")
	}
}

func TestNudgeBoundaryDeterministic(t *testing.T) {
	for _, head := range []int{499, 500} {
		text := NudgeText(worker("w-eli", "Eli", workerOpts{first: ip(100), head: ip(head)}))
		if !strings.Contains(text, "START AT #100") || strings.Contains(text, "--mentions-me") {
			t.Errorf("span %d keeps the unfiltered anchor", head-100)
		}
	}
	if text := NudgeText(worker("w-eli", "Eli", workerOpts{first: ip(100), head: ip(501)})); !strings.Contains(text, "--mentions-me") {
		t.Errorf("span 401 switches to the mentions anchor")
	}
}

// Mirrors the merged TS #2094 pins: a prompted turn can end with a short
// assistant line while still avoiding a no-action post to the team chat.
func TestNudgeEndingsSatisfiable(t *testing.T) {
	variants := []struct {
		name string
		text string
	}{
		{"small-gap", NudgeText(worker("w-eli", "Eli", workerOpts{mentions: ip(2), unread: ip(5), first: ip(100), relevant: ip(110), head: ip(120)}))},
		{"large-gap", NudgeText(worker("w-eli", "Eli", workerOpts{mentions: ip(2), unread: ip(9841), first: ip(61), relevant: ip(9750), head: ip(9901)}))},
		{"generic-anchor", NudgeText(AttentionItem{Worker: "w-eli", WorkerName: "Eli", Channel: channel, Unread: 3, UnreadMentions: 1, LastRelevantSeq: 110, LastSeq: 120})},
		{"liveness", LivenessNudgeText(100, 120)},
	}
	for _, v := range variants {
		t.Run(v.name, func(t *testing.T) {
			if strings.Contains(strings.ToLower(v.text), "finish silently") || strings.Contains(v.text, "'read/no-action' comment") {
				t.Error("ending contains an unsatisfiable silence instruction")
			}
			for _, want := range []string{"end with one short line", "do not post to team chat"} {
				if !strings.Contains(v.text, want) {
					t.Errorf("ending missing %q", want)
				}
			}
		})
	}
}

func TestLivenessEndingMarksReadOnce(t *testing.T) {
	if n := strings.Count(strings.ToLower(LivenessNudgeText(100, 120)), "mark read"); n != 1 {
		t.Errorf("mark read appears %d times, want 1", n)
	}
}

func TestLivenessNudgeNamesOnlyTheCLI(t *testing.T) {
	text := LivenessNudgeText(100, 120)
	assertNoMCP(t, text)
	if !strings.Contains(text, "hadron team chat read --since 100 --limit 200") {
		t.Errorf("liveness text lost its CLI read command: %q", text)
	}
}
