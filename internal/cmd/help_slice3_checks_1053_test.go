package cmd

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/pflag"
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
		{"chat", "served-prompts"}, {"team", "session", "served-prompts"},
	} {
		help := flatHelp1053S3(t, page...)
		for _, b := range banned {
			if strings.Contains(help, b) {
				t.Errorf("%v help carries %q", page, b)
			}
		}
	}
}

// cli#2559 — the #1053 slice-3 deferred P3s: the wording the seat receipts on
// #2554 chose, pinned literally. The bans keep the replaced jargon ("platform
// token", "trigger budgets", the API names) from coming back.
func TestSlice3DeferredP3Wording2559(t *testing.T) {
	for _, tc := range []struct {
		page []string
		want []string
	}{
		// 1: one name for the webhook credential — the secret is the pair (URL
		// path + token), the token is the hpt part.
		{[]string{"webhook"}, []string{"the secret (the URL path and the token)"}},
		{[]string{"webhook", "create"}, []string{"the secret (the URL path and the token)"}},
		// 2: --policy reads the same on every page that takes it.
		{[]string{"schedule", "create"}, []string{`adds no restriction of its own`, `"hadron policy"`, `{"allow":[]} allows nothing`, `"hadron policy explain"`}},
		{[]string{"schedule", "update"}, []string{`adds no restriction of its own`, `"hadron policy"`}},
		{[]string{"webhook", "create"}, []string{`adds no restriction of its own`, `"hadron policy"`, `{"allow":[]} allows nothing`, `"hadron policy explain"`}},
		// 3: the jargon phrases, replaced.
		{[]string{"schedule", "create"}, []string{"a caller using an App key (hadron app key create) gets UNAUTHENTICATED", "Needs a server version that supports budgets"}},
		{[]string{"schedule", "update"}, []string{"Needs a server version that supports budgets"}},
		{[]string{"webhook", "create"}, []string{"Needs a server version that supports budgets"}},
		// 4: the legacy URN clause, restored.
		{[]string{"schedule", "preview"}, []string{"--entry is a fully-qualified node URN (the legacy org::memory::loc form is also accepted)"}},
		// 5: the cadence floor, defined.
		{[]string{"ticker", "create"}, []string{"the fastest any ticker in the App may tick", "120 seconds", `"hadron team ticker settings get"`}},
		{[]string{"ticker", "update"}, []string{"the fastest any ticker in the App may tick", "120 seconds", `"hadron team ticker settings get"`}},
		// 6: task run names the read-only path without API names.
		{[]string{"task", "run"}, []string{"To only read the prompt, use 'hadron task render'.", "the arguments a task node declares"}},
	} {
		help := flatHelp1053S3(t, tc.page...)
		for _, w := range tc.want {
			if !strings.Contains(help, w) {
				t.Errorf("%v help lacks %q", tc.page, w)
			}
		}
	}
	for _, tc := range []struct {
		page []string
		ban  []string
	}{
		{[]string{"webhook"}, []string{"platform token"}},
		{[]string{"webhook", "create"}, []string{"platform token"}},
		{[]string{"webhook", "rotate"}, []string{"platform token"}},
		{[]string{"webhook", "list"}, []string{"platform token"}},
		{[]string{"schedule", "create"}, []string{"trigger budgets"}},
		{[]string{"schedule", "update"}, []string{"trigger budgets"}},
		{[]string{"webhook", "create"}, []string{"trigger budgets"}},
		{[]string{"task", "run"}, []string{"renderRunNode", "Node.data.args"}},
	} {
		help := flatHelp1053S3(t, tc.page...)
		for _, b := range tc.ban {
			if strings.Contains(help, b) {
				t.Errorf("%v help still carries %q", tc.page, b)
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
		if err := json.Unmarshal(captured["UpdateAgentSchedule"], &vars); err != nil {
			t.Fatalf("%v: %v", tc.args, err)
		}
		if vars.Input == nil {
			t.Fatalf("%v: no input was sent", tc.args)
		}
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
		{"chat", "served-prompts"}, {"team", "session", "served-prompts"},
	}
	// N2: no leading-dash words, so a quoted reference carrying flags ("hadron
	// task run --app ..." style) is not half-consumed by Find.
	quoted := regexp.MustCompile(`"hadron ((?:[a-z][a-z-]*)(?: [a-z][a-z-]*)*)[" ]`)
	// Flags a page names in prose without owning them: schedule preview says
	// --name/--scope/--policy "are not preview inputs and are not accepted
	// here" (naming them is the point), and the ticker group page shows
	// create's and subscribe's flags inside its own examples.
	mentionedNotOwned := map[string]map[string]bool{
		"schedule/preview": {"--name": true, "--scope": true, "--policy": true},
		"ticker":           {"--name": true, "--cadence-seconds": true, "--worker": true},
	}
	flagName := regexp.MustCompile(`--[a-z][a-z0-9-]*`)
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
		// N3: every flag the page names in its Long or a flag's usage exists on
		// the command — a rename that leaves the prose behind reddens here.
		have := map[string]bool{}
		usage := cmd.Long + "\n"
		for _, set := range []*pflag.FlagSet{cmd.Flags(), cmd.InheritedFlags()} {
			set.VisitAll(func(fl *pflag.Flag) {
				have["--"+fl.Name] = true
				usage += fl.Usage + "\n"
			})
		}
		pageKey := strings.Join(page, "/")
		for _, name := range flagName.FindAllString(usage, -1) {
			if !have[name] && !mentionedNotOwned[pageKey][name] {
				t.Errorf("%v names %q, which is not one of its flags", page, name)
			}
		}
		for _, line := range strings.Split(cmd.Example, "\n") {
			line = strings.TrimSpace(line)
			line = strings.TrimPrefix(line, "$ ") // N3: shell-prompt examples parse too
			if strings.HasPrefix(line, "#") || !strings.HasPrefix(line, "hadron ") {
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
