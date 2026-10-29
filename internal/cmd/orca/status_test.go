package orca

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/config"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
	"github.com/hadron-memory/hadron-cli/internal/output"
	ts "github.com/hadron-memory/hadron-cli/internal/terminalstatus"
)

var clock = time.Date(2026, 10, 29, 16, 31, 40, 0, time.UTC)

const rule = "────────────────────────────────────────────────────────────"

var (
	claudeHint = "  ⏵⏵ bypass permissions on (shift+tab to cycle)"
	idleScreen = []string{"⏺ Done.", "✻ Worked for 1m 10s · done 3:54 PM", rule, "❯ ", rule, claudeHint}
	busyScreen = []string{"✽ Bunning… (5m 55s · ↓ 15.6k tokens)", rule, "❯ ", rule, claudeHint}
	menuScreen = []string{"Do you want to proceed?", "❯ 1. Yes", "  2. No, and tell Claude what to do differently (esc)"}
)

// fakeOrca answers `orca terminal list` and `terminal read` from fixtures.
type fakeOrca struct {
	down     bool
	terms    []map[string]any
	screens  map[string][]string
	commands []string
}

func (o *fakeOrca) run(_ context.Context, argv []string) orcaterm.Result {
	o.commands = append(o.commands, strings.Join(argv, " "))
	if o.down {
		return orcaterm.Result{Code: 1, Stderr: "call failed: executable file not found"}
	}
	switch argv[0] + " " + argv[1] {
	case "terminal list":
		b, _ := json.Marshal(map[string]any{"ok": true, "result": map[string]any{"terminals": o.terms}})
		return orcaterm.Result{Stdout: string(b)}
	case "terminal read":
		tail, ok := o.screens[argv[3]]
		if !ok {
			return orcaterm.Result{Code: 1}
		}
		b, _ := json.Marshal(map[string]any{"ok": true, "result": map[string]any{"terminal": map[string]any{"source": "screen", "tail": tail}}})
		return orcaterm.Result{Stdout: string(b)}
	}
	return orcaterm.Result{Code: 1}
}

func term(handle, worktree, family string) map[string]any {
	m := map[string]any{"handle": handle, "worktreePath": worktree, "connected": true, "writable": true}
	if family != "" {
		m["agentIdentity"] = family
	}
	return m
}

type fakeReads struct {
	workers []Worker
	subs    map[string][]ts.Ticker
	due     map[string][]ts.DueTick
	dueErr  error
	calls   map[string]int
}

func (r *fakeReads) count(k string) {
	if r.calls == nil {
		r.calls = map[string]int{}
	}
	r.calls[k]++
}
func (r *fakeReads) Workers(context.Context) ([]Worker, error) {
	r.count("workers")
	return r.workers, nil
}
func (r *fakeReads) Subscriptions(context.Context) (map[string][]ts.Ticker, error) {
	r.count("subs")
	return r.subs, nil
}
func (r *fakeReads) DueTicks(context.Context, []string) (map[string][]ts.DueTick, error) {
	r.count("due")
	return r.due, r.dueErr
}

type rig struct {
	orca  *fakeOrca
	reads *fakeReads
	out   *strings.Builder
	errs  *strings.Builder
	env   Env
	f     *cmdutil.Factory
}

func newRig(t *testing.T) *rig {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HADRON_APP", "")
	io, _, _ := output.Test()
	out, errs := &strings.Builder{}, &strings.Builder{}
	io.Out = output.Tracked(out)
	io.ErrOut = errs
	r := &rig{
		orca: &fakeOrca{screens: map[string][]string{}},
		reads: &fakeReads{
			workers: []Worker{{ID: "w_j", Name: "Joshua"}, {ID: "w_a", Name: "Ada"}},
			subs:    map[string][]ts.Ticker{"w_j": {{ID: "tk5", Name: "base", Interval: 5 * time.Minute}}, "w_a": {{ID: "tk5", Name: "base", Interval: 5 * time.Minute}}},
			due:     map[string][]ts.DueTick{},
		},
		out:  out,
		errs: errs,
		f: &cmdutil.Factory{IOStreams: io, AppFlag: "hrn:app:x:team",
			ConfigFn: config.Load},
	}
	r.env = Env{
		Orca: r.orca.run,
		Now:  func() time.Time { return clock },
		Reads: func(context.Context, string) (Reads, error) {
			return r.reads, nil
		},
		BoundAt: func(_ context.Context, wt string) (Bound, bool) {
			switch wt {
			case "/w/joshua":
				return Bound{WorkerID: "w_j", WorkerName: "Joshua", AppID: "app1"}, true
			case "/w/ada":
				return Bound{WorkerID: "w_a", WorkerName: "Ada", AppID: "app1"}, true
			}
			return Bound{}, false
		},
	}
	return r
}

func (r *rig) run(t *testing.T, json bool, args ...string) (int, string) {
	t.Helper()
	r.f.JSON = json
	r.out.Reset()
	r.errs.Reset()
	cmd := newCmdStatus(r.f, r.env)
	cmd.SetArgs(args)
	cmd.SetOut(r.out)
	cmd.SetErr(r.out)
	err := cmd.ExecuteContext(context.Background())
	return exitcode.FromError(err), r.out.String()
}

func (r *rig) joshua(screen []string) {
	r.orca.terms = append(r.orca.terms, term("term_j", "/w/joshua", "claude"))
	r.orca.screens["term_j"] = screen
}

func decode(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("not one JSON object: %v\n%s", err, s)
	}
	return m
}

func TestStatusIdleJSONAndText(t *testing.T) {
	r := newRig(t)
	r.joshua(idleScreen)
	code, out := r.run(t, true, "Joshua")
	m := decode(t, out)
	if code != 0 || m["contract"] != "terminal-status@1" || m["status"] != "idle" || m["prompt"] != "empty" || m["worker"] != "Joshua" || m["terminal"] != "term_j" || m["reason"] != nil {
		t.Fatalf("exit %d: %v", code, m)
	}
	if m["lastTurnAt"] != "2026-10-29T15:54:00Z" || m["checkedAt"] != "2026-10-29T16:31:40Z" {
		t.Fatalf("times: %v", m)
	}
	if code, out := r.run(t, false, "Joshua"); code != 0 || strings.TrimSpace(out) != "Joshua idle" {
		t.Fatalf("text mode %d %q", code, out)
	}
}

func TestStatusDarkRule(t *testing.T) {
	due := func(ago time.Duration) map[string][]ts.DueTick {
		return map[string][]ts.DueTick{"w_j": {{TickerID: "tk5", TickerName: "base", DueAt: clock.Add(-ago)}}}
	}
	for name, c := range map[string]struct {
		screen []string
		due    map[string][]ts.DueTick
		status string
		reason any
	}{
		"idle screen, tick due and unanswered": {idleScreen, due(6 * time.Minute), "dark", "no_turn_for_due_ticker"},
		"same screen, nothing due":             {idleScreen, nil, "idle", nil},
		"due but within the interval":          {idleScreen, due(2 * time.Minute), "idle", nil},
		"busy outranks dark":                   {busyScreen, due(time.Hour), "busy", nil},
		"locked outranks dark":                 {menuScreen, due(time.Hour), "locked", "menu_or_question"},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			r.joshua(c.screen)
			r.reads.due = c.due
			code, out := r.run(t, true, "Joshua")
			m := decode(t, out)
			if code != 0 || m["status"] != c.status || m["reason"] != c.reason {
				t.Fatalf("exit %d: %v", code, m)
			}
			// A dark worker at an empty prompt is the one to push.
			if c.status == "dark" && m["prompt"] != "empty" {
				t.Fatalf("dark prompt %v", m["prompt"])
			}
		})
	}
}

func TestStatusDetailIsLocalOnly(t *testing.T) {
	r := newRig(t)
	r.joshua(idleScreen)
	r.reads.due = map[string][]ts.DueTick{"w_j": {{TickerID: "tk5", TickerName: "base", DueAt: clock.Add(-6 * time.Minute)}}}
	_, out := r.run(t, true, "Joshua")
	if strings.Contains(out, "detail") || strings.Contains(out, "tk5") {
		t.Fatalf("detail reached the JSON: %s", out)
	}
	if _, text := r.run(t, false, "Joshua"); !strings.Contains(text, "tk5") || !strings.HasPrefix(text, "Joshua dark (no_turn_for_due_ticker)") {
		t.Fatalf("text mode lost the detail: %q", text)
	}
}

func TestStatusNoTerminalIsDarkNotAnError(t *testing.T) {
	r := newRig(t)
	r.orca.terms = []map[string]any{term("term_o", "/w/other", "claude")}
	code, out := r.run(t, true, "Joshua")
	m := decode(t, out)
	if code != 0 || m["status"] != "dark" || m["reason"] != "no_terminal" || m["terminal"] != nil {
		t.Fatalf("exit %d: %v", code, m)
	}
}

func TestStatusOffTheOrcaHostExits10(t *testing.T) {
	r := newRig(t)
	r.orca.down = true
	code, out := r.run(t, true, "Joshua")
	if code != 10 || exitcode.OrcaUnreachable != 10 {
		t.Fatalf("exit %d, want 10", code)
	}
	if strings.Contains(out, "terminal-status@1") {
		t.Fatalf("a status was printed off-host: %s", out)
	}
}

func TestStatusRefusedWakeReadFailsClosedForIdleOnly(t *testing.T) {
	r := newRig(t)
	r.reads.dueErr = exitcode.Newf(exitcode.Forbidden, "not permitted to read another worker's wakes")
	r.joshua(idleScreen)
	r.orca.terms = append(r.orca.terms, term("term_a", "/w/ada", "claude"))
	r.orca.screens["term_a"] = busyScreen
	_, out := r.run(t, true, "--all")
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil || len(rows) != 2 {
		t.Fatalf("want one array of 2: %v\n%s", err, out)
	}
	by := map[string]map[string]any{}
	for _, m := range rows {
		by[m["worker"].(string)] = m
	}
	if by["Joshua"]["status"] != "locked" || by["Joshua"]["reason"] != "unrecognized" {
		t.Fatalf("idle with a refused wake read must fail closed: %v", by["Joshua"])
	}
	if by["Ada"]["status"] != "busy" {
		t.Fatalf("busy keeps its status: %v", by["Ada"])
	}
	if strings.Contains(out, "not permitted") {
		t.Fatalf("refusal text reached the JSON: %s", out)
	}
}

func TestStatusAllIsOneArrayOneEntryPerWorker(t *testing.T) {
	r := newRig(t)
	r.joshua(idleScreen)
	_, out := r.run(t, true, "--all")
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("not an array: %v\n%s", err, out)
	}
	if len(rows) != 2 || rows[0]["worker"] != "Joshua" || rows[1]["worker"] != "Ada" || rows[1]["reason"] != "no_terminal" {
		t.Fatalf("rows: %v", rows)
	}
	// One call for the whole team, not one per worker.
	if r.reads.calls["due"] != 1 || r.reads.calls["subs"] != 1 {
		t.Fatalf("reads: %v", r.reads.calls)
	}
}

func TestStatusByTerminalIDNeedsNoServerWhenUnbound(t *testing.T) {
	r := newRig(t)
	r.f.AppFlag = ""
	r.orca.terms = []map[string]any{term("term_x", "/w/unbound", "claude")}
	r.orca.screens["term_x"] = idleScreen
	code, out := r.run(t, true, "term_x")
	m := decode(t, out)
	if code != 0 || m["status"] != "idle" || m["worker"] != nil || m["terminal"] != "term_x" {
		t.Fatalf("exit %d: %v", code, m)
	}
	if len(r.reads.calls) != 0 {
		t.Fatalf("server read for an unbound terminal: %v", r.reads.calls)
	}
}

func TestStatusUsage(t *testing.T) {
	r := newRig(t)
	r.joshua(idleScreen)
	for name, args := range map[string][]string{"neither": {}, "both": {"Joshua", "--all"}} {
		if code, _ := r.run(t, true, args...); code != exitcode.Usage {
			t.Errorf("%s: exit %d, want %d", name, code, exitcode.Usage)
		}
	}
	if code, _ := r.run(t, true, "Nobody"); code != exitcode.NotFound {
		t.Errorf("unknown worker: exit %d, want %d", code, exitcode.NotFound)
	}
}

func TestStatusAgentOutranksShellInTheSameWorktree(t *testing.T) {
	r := newRig(t)
	r.orca.terms = []map[string]any{term("term_sh", "/w/joshua", ""), term("term_j", "/w/joshua", "claude")}
	r.orca.screens["term_sh"] = []string{"user@host:~$"}
	r.orca.screens["term_j"] = idleScreen
	_, out := r.run(t, true, "Joshua")
	if m := decode(t, out); m["status"] != "idle" || m["terminal"] != "term_j" {
		t.Fatalf("%v", m)
	}
}

func TestStatusTwoAgentsFailClosed(t *testing.T) {
	r := newRig(t)
	r.orca.terms = []map[string]any{term("term_1", "/w/joshua", "claude"), term("term_2", "/w/joshua", "claude")}
	r.orca.screens["term_1"], r.orca.screens["term_2"] = idleScreen, idleScreen
	_, out := r.run(t, true, "Joshua")
	if m := decode(t, out); m["status"] != "locked" || m["reason"] != "unrecognized" {
		t.Fatalf("%v", m)
	}
}

// The command only reads: it never sends, and a guard on the runner would
// refuse a send anyway.
func TestStatusOnlyListsAndReads(t *testing.T) {
	r := newRig(t)
	r.joshua(idleScreen)
	r.run(t, true, "Joshua")
	for _, c := range r.orca.commands {
		if !strings.HasPrefix(c, "terminal list") && !strings.HasPrefix(c, "terminal read") {
			t.Fatalf("unexpected orca call %q", c)
		}
	}
}

// The App is resolved through a fallback chain (--app, the App context, a
// worker-session binding). Text mode says which branch answered, on stderr so
// the one-line stdout contract and the JSON stay untouched.
func TestStatusTextNamesTheAmbientApp(t *testing.T) {
	r := newRig(t)
	r.joshua(idleScreen)
	r.run(t, false, "Joshua")
	if got := r.errs.String(); !strings.Contains(got, "hrn:app:x:team") || !strings.Contains(got, "--app") {
		t.Fatalf("scope line missing: %q", got)
	}
	r.f.AppFlag = ""
	r.run(t, false, "Joshua")
	if got := r.errs.String(); !strings.Contains(got, "app1") || !strings.Contains(got, "worker-session binding") {
		t.Fatalf("binding fallback not named: %q", got)
	}
	r.run(t, true, "Joshua")
	if r.errs.String() != "" {
		t.Fatalf("JSON mode wrote to stderr: %q", r.errs.String())
	}
}
