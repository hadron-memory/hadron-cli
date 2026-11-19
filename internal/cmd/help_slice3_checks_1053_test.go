package cmd

import (
	"encoding/json"
	"regexp"
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

// A flag left out of "schedule update" is omitted, not reset: the help says so,
// and the --enabled default change (no "(default true)" on the page) must not
// change what is sent. BLIND P2-1/P2-3 on #2554.
func TestScheduleUpdateEnabledAndAsSelfSendOnlyWhenPassed1053(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want map[string]any // key -> value that must be sent
		omit []string
	}{
		{[]string{"--name", "n"}, nil, []string{"enabled", "runAsSelf"}},
		{[]string{"--enabled"}, map[string]any{"enabled": true}, []string{"runAsSelf"}},
		{[]string{"--enabled=false"}, map[string]any{"enabled": false}, []string{"runAsSelf"}},
		{[]string{"--as-self"}, map[string]any{"runAsSelf": true}, []string{"enabled"}},
		{[]string{"--as-self=false"}, map[string]any{"runAsSelf": false}, []string{"enabled"}},
	} {
		gql, captured := captureGraphQL(t, map[string]string{
			"UpdateAgentSchedule": `{"data":{"updateAgentSchedule":` + scheduleJSON + `}}`,
		})
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append([]string{"schedule", "update", "sch1"}, append(tc.args, "--server", gql.URL)...))
		if err := root.Execute(); err != nil {
			t.Fatalf("%v: %v", tc.args, err)
		}
		var vars struct {
			Input map[string]any `json:"input"`
		}
		_ = json.Unmarshal(captured["UpdateAgentSchedule"], &vars)
		for k, want := range tc.want {
			if got, ok := vars.Input[k]; !ok || got != want {
				t.Errorf("%v: %s = %v (present=%v), want %v", tc.args, k, got, ok, want)
			}
		}
		for _, k := range tc.omit {
			if v, ok := vars.Input[k]; ok {
				t.Errorf("%v: %s must be omitted, got %v", tc.args, k, v)
			}
		}
	}
}

// Every command a revised page points at, and every flag its examples use,
// exists: the help can't send a reader to a command that isn't there.
// (BLIND P2-2 on #2554: the diff alone could not show this.)
func TestSlice3HelpPointersAndExamplesResolve1053(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	pages := [][]string{
		{"run", "served-prompt"}, {"schedule", "create"}, {"schedule", "update"}, {"schedule", "preview"},
		{"webhook", "create"}, {"task"}, {"task", "run"}, {"ticker"}, {"ticker", "create"}, {"ticker", "update"},
		{"ticker", "list"}, {"ticker", "get"}, {"ticker", "subscribe"}, {"ticker", "unsubscribe"},
	}
	quoted := regexp.MustCompile(`"hadron ([a-z][a-z -]*[a-z])[" ]`)
	resolves := func(words []string) bool {
		c, rest, err := root.Find(words)
		return err == nil && c != root && len(rest) == 0
	}
	for _, page := range pages {
		help := flatHelp1053S3(t, page...)
		for _, m := range quoted.FindAllStringSubmatch(help, -1) {
			if !resolves(strings.Fields(m[1])) {
				t.Errorf("%v help points at %q, which is not a command", page, "hadron "+m[1])
			}
		}
		cmd, _, err := root.Find(page)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(cmd.Example, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "hadron ") {
				continue
			}
			args := strings.Fields(strings.TrimPrefix(line, "hadron "))
			target, rest, err := root.Find(args)
			if err != nil || target == root {
				t.Errorf("%v example %q: no such command", page, line)
				continue
			}
			if err := target.ParseFlags(rest); err != nil {
				t.Errorf("%v example %q: %v", page, line, err)
			}
		}
	}
}
