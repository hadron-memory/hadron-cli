package cmd

import (
	"strings"
	"testing"
)

// cli#1054 slice D round 1 (Celine, AT0.8604.1): the 14 failing pages. Each
// page must say the thing the evaluator found missing, and must not bring back
// the wording she could not parse.
func TestSliceDRound1Fails1054(t *testing.T) {
	const flow = "1. pr schedule the owner lists the slots the PR needs"
	for _, tc := range []struct {
		page []string
		want []string
		ban  []string
	}{
		{[]string{"team", "pr"}, []string{
			flow, "6. pr ready the build task's assignee records the ready call",
			"a slot is one required review", "a seat is a child task of it that does one review",
			"After a push, freeze the new head", "hadron team pr schedule AT0.7850 --slot local --slot blind --slot qa",
		}, []string{"event-derived", "hadron-server#3448"}},
		{[]string{"team", "pr", "schedule"}, []string{"Each call REPLACES the whole list", "REVIEW_SEAT_NOT_SUPERSEDABLE", "hadron team pr schedule AT0.7850 --slot local"}, []string{"Record the server's PR review protocol"}},
		{[]string{"team", "pr", "freeze"}, []string{"PR_REFERENCE_MISSING", "so after a push freeze the new one", "Run it after \"pr schedule\" and before \"pr seat\""}, []string{"hadron-server#3448"}},
		{[]string{"team", "pr", "seat"}, []string{"The seat task must already exist, as a direct child of the build task", "REVIEW_SEAT_SUPERSEDES_REQUIRED", "REVIEW_SLOT_UNKNOWN", "--supersedes AT0.7850.1"}, nil},
		{[]string{"team", "pr", "verdict"}, []string{
			"CARRY says your earlier verdict still holds at this new head", "APPROVE, PASS and CARRY count toward readiness",
			"REVIEW_SEAT_UNASSIGNED", "a seat counts toward readiness only once finished", "--node names the Hadron node holding your saved review",
		}, nil},
		{[]string{"team", "pr", "readiness"}, []string{"A slot counts when its current seat is finished", "NOT READY is a successful read (exit 0)", "the build task's assignee runs \"pr ready\""}, []string{"event-derived"}},
		{[]string{"team", "pr", "ready"}, []string{"it does not merge the PR or check CI", "PR_NOT_READY (exit 5)", "Run \"pr readiness\" first"}, nil},
		{[]string{"team", "alarm"}, []string{"An alarm is a warning the platform raises when work stalls", "hadron team alarms", "hadron team events --alarms", "hadron team alarm ack <eventId>"}, nil},
		{[]string{"team", "alarm", "ack"}, []string{
			"<eventId> is the alarm's event id: the ALARM column of \"hadron team alarms\"", "ALARM_ALREADY_ACKNOWLEDGED",
			"✓ acknowledged alarm", "hadron team alarm ack 01a1f0c2d3e4f5a6b7c8d9e0f1a2b3c4", "The full rules are in \"hadron agentic-usage\"",
		}, []string{"The CLI splits on the first colon only", "held-worker path"}},
		{[]string{"team", "task", "create"}, []string{
			"put it with someone", "--match tries once, right away", "It does nothing for a task that already has an assignee or is blocked",
			"--model and --tier exclude each other", "The full rules are in \"hadron agentic-usage\"",
		}, []string{"without an explicit assignee, or a task that is blocked", "matching metadata above needs its provider", "hadron-server#4295", "hadron-server#2983", "The CLI splits on the first colon only"}},
		{[]string{"team", "task", "dispose-finding"}, []string{
			"Record what became of one finding on a finished task", "hadron team task findings <task>", "hadron team task finding-dispositions <task>",
			"A finding is an issue noted when a task finished", "The full rules are in \"hadron agentic-usage\"",
		}, []string{"Read or replace", "The CLI splits on the first colon only"}},
		{[]string{"team", "task", "finding-dispositions"}, []string{
			"List what became of a finished task's findings", "hadron team task findings <task>", "A disposition is what became of one",
		}, []string{"Read or replace", "findings client slice"}},
		{[]string{"team", "task", "findings"}, []string{
			"hadron team task finish <task> --findings-file <path|->", "the issues its holder noted when it finished", "a severity (P1, P2, P3 or note)",
			"hadron team task finding-dispositions",
		}, []string{"exits4", "GraphQL Int", "opt-in finish"}},
		{[]string{"team", "task", "find-untracked"}, []string{
			"Which source? Name exactly one", "SERVER INTEGRATION below is the one for you", "hadron team task find-untracked (--adapter <exe> | --integration <name>) [flags]",
			"# with your team's intake adapter", "# with the App's task integration",
		}, []string{"[--ready only|exclude|any] [--repo <owner/name>] [--skip N] [--limit N] [--assign-to-me [--assignee <party>] [--owner <party>]])\n"}},
	} {
		help := flatHelp1053S3(t, tc.page...)
		for _, w := range tc.want {
			if !strings.Contains(help, w) {
				t.Errorf("%v help lacks %q", tc.page, w)
			}
		}
		for _, b := range tc.ban {
			if strings.Contains(help, b) {
				t.Errorf("%v help still has %q", tc.page, b)
			}
		}
	}
}
