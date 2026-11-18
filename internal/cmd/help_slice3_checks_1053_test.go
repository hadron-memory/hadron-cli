package cmd

import (
	"strings"
	"testing"
)

// cli#1053 slice 3 (automation: run, schedule, task, ticker, webhook): facts the
// evaluator's round 1 found missing or wrong, pinned so a later edit cannot drop
// them.

func flatHelp1053S3(t *testing.T, path ...string) string {
	t.Helper()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{}, path...), "--help"))
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(strings.Fields(out.String()), " ")
}

func TestSlice3PagesSayWhatRound1FoundMissing1053(t *testing.T) {
	for _, tc := range []struct {
		page []string
		want []string
	}{
		// F1: what it is for, and where the run id comes from
		{[]string{"run", "served-prompt"}, []string{`"what was this run told?"`, `"hadron run list"`}},
		// F2: what omitting --policy means, and what it names
		{[]string{"schedule", "create"}, []string{"Left out, the schedule adds no restriction of its own", "comm.outbound"}},
		{[]string{"webhook", "create"}, []string{"Left out, the webhook adds no restriction of its own", "comm.outbound"}},
		{[]string{"schedule", "update"}, []string{`--policy "" clears it`}},
		// F3: a flag left out is unchanged, not reset to a default
		{[]string{"schedule", "update"}, []string{"Leave a flag out to keep the schedule's current value", "--enabled=false turns it off (left out: unchanged)"}},
		// F4: what ADOPTABLE means and what to do next
		{[]string{"schedule", "preview"}, []string{"See what a schedule would be allowed to read and write", `"hadron schedule create" with the same arguments`}},
		// F5: how to call the webhook
		{[]string{"webhook", "create"}, []string{"CALLING IT:", "hpt query parameter", "answers 202 with its run id"}},
		// F6: task run prints unless --app
		{[]string{"task"}, []string{"Nothing executes unless you pass --app", "STARTS a real run"}},
		// F7: what a ticker is
		{[]string{"ticker"}, []string{"A ticker wakes the workers subscribed to it", "cadence"}},
		// F8: the values, and the limits
		{[]string{"ticker", "create"}, []string{"every-tick always", "at most 86400", "--relevance is all or mentions", "hadron ticker create --name check-in"}},
		{[]string{"ticker", "update"}, []string{"either every change you pass is applied or none is", "hadron ticker update hrn:ticker:"}},
		// F9: whose tickers and what is shown
		{[]string{"ticker", "list"}, []string{"tickers of the current App that you can read", "hadron ticker get"}},
		// F10: each page says its own thing
		{[]string{"ticker", "subscribe"}, []string{"Subscribe a worker to a ticker", "TICKER_ON_SHORT_LIVED"}},
		{[]string{"ticker", "unsubscribe"}, []string{"its ticks stop waking the worker", "hrn:worker:"}},
	} {
		help := flatHelp1053S3(t, tc.page...)
		for _, w := range tc.want {
			if !strings.Contains(help, w) {
				t.Errorf("%v help lacks %q", tc.page, w)
			}
		}
	}
}

// Spec citations, issue numbers, commit hashes and "may change" boilerplate are
// for the maintainers, not the reader of a page.
func TestSlice3HelpHasNoMaintainerText1053(t *testing.T) {
	banned := []string{"cor:agt:", "cor:acl:", "hadron-server#3463", "b7cf14b0", "spec-036", "D-2026-05-02", "may change", "enforcement is follow-on"}
	for _, page := range [][]string{
		{"run", "served-prompt"}, {"schedule", "create"}, {"schedule", "update"}, {"schedule", "preview"},
		{"webhook", "create"}, {"task"}, {"task", "run"}, {"ticker"}, {"ticker", "create"}, {"ticker", "update"},
		{"ticker", "list"}, {"ticker", "subscribe"}, {"ticker", "unsubscribe"},
	} {
		help := flatHelp1053S3(t, page...)
		for _, b := range banned {
			if strings.Contains(help, b) {
				t.Errorf("%v help carries %q", page, b)
			}
		}
	}
}
