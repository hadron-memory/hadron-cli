package teamrouter

// The Go port of the TS suite's FakeCLIs (scripts/orca-attention-router/
// router.test.ts, at server main f0ab31a0): no network, no Orca, no GraphQL.
// One fake plays the typed Server seam and the exec'd `orca` CLI, preserving
// the TS phase semantics exactly — including sawWait (a satisfied wait or an
// untracked idle gate read precedes the tokenless fresh re-read, #1616) and
// the recheck screen's read index (2 tracked, 3+ untracked, #1700).

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Rendered screens as Orca returned them on 2026-10-01 (idle, nobody typing).
var rule = strings.Repeat("─", 60)
var claudeHint = "  ⏵⏵ bypass permissions on (shift+tab to cycle) · ← for agents"

var claudeIdle = []string{"⏺ Done.", "❯ an earlier submitted turn", "✻ Cogitated for 1m 3s", rule, "❯ ", rule, claudeHint}
var codexIdle = []string{"  Worked for 33s • 16:32", "› Ask Codex to do anything", "  GPT-6-Sol high · ~/orca/workspaces/hadron-server/andi · Start worker session as Andi", "  ? for shortcuts"}

// A WORKING agent renders an EMPTY composer — the running-turn line above the
// prompt is the only separator (@codex P2, #1700; live screens 2026-10-09/10).
var claudeBusyEmpty = []string{"  ⎿ Tip: Use /btw to ask a quick side question", "  (ctrl+b to run in background)", "✽ Bunning… (5m 55s · ↓ 15.6k tokens)", rule, "❯ ", rule, claudeHint}
var codexBusyEmpty = []string{"  • Working (6s • esc to interrupt)", "› Ask Codex to do anything", "  GPT-6-Sol high · ~/orca/workspaces/hadron-server/andi · Start worker session as Andi", "  ? for shortcuts"}

// Fresh launches (never worked): captured live 2026-10-10 — GLM worker Cody,
// Codex worker Dara. Positive idle chrome, no done-marker exists yet (#1700 P2 r2).
var claudeFresh = []string{" ▐▛███▛█   Claude Code v2.1.296", "▝▜██████▀  glm-5.3 · API Usage Billing", " ▝▝   ▝▝   ~/orca/workspaces/hadron-server/cody", rule, "❯ ", rule, claudeHint}
var codexFresh = []string{"│ ✨ Update available! 0.162.0 -> 0.162.1         │", "  >_ OpenAI Codex (v0.162.0)", "     ~/orca/workspaces/hadron-server/dara", "  permissions: YOLO mode", "› Ask Codex to do anything", "  GPT-6.1-Sol default · ~/orca/workspaces/hadron-server/dara", "  ? for shortcuts"}

// Empty prompt, no busy marker, and nothing positively idle either — must HOLD.
var claudeUnknownQuiet = []string{"  some status output nobody classified", "  another unrecognized line", rule, "❯ ", rule, claudeHint}

// REAL idle screens captured live 2026-10-11 (Yuri's first-night production
// report, #8165): ordinary assistant output text above the done-marker.
var claudeIdleRealXan = []string{"  Queued for my gate: Bindi's remaining attach-steps, Dan's 12-follow-up batch,", "  Session open.", "✻ Brewed for 2m 36s · done 3:20 AM", rule, "❯ ", rule, claudeHint}
var claudeIdleRealLiam = []string{"  both types.", "  file\". I've marked team chat read through #8177, the worktree is clean, and I'm standing by.", "✻ Cooked for 9m 42s · done 3:26 AM", "  ✔ Update installed · Restart to update", rule, "❯ ", rule, claudeHint}
var claudeIdleCompacted = []string{"✻ Compacted while idle", rule, "❯ ", rule, claudeHint}

func withPrompt(screen []string, marker, text string) []string {
	last := -1
	for i, l := range screen {
		if strings.HasPrefix(strings.TrimLeft(l, " "), marker) {
			last = i
		}
	}
	out := append([]string{}, screen[:last]...)
	out = append(out, marker+" "+text)
	return append(out, screen[last+1:]...)
}

const (
	token1   = "tok-one-SECRET"
	token2   = "tok-two-SECRET"
	channel  = "chan-team"
	freshTok = "tok-fresh-NEVER-ADOPT"
)

// workerOpts mirrors the TS helper's `?? defaults`: a nil pointer takes the
// default, an explicit value — including 0 — is kept as written.
type workerOpts struct {
	mentions *int
	unread   *int
	first    *int
	relevant *int
	head     *int
}

func ip(v int) *int { return &v }

func worker(id, name string, o workerOpts) AttentionItem {
	def := func(p *int, d int) int {
		if p == nil {
			return d
		}
		return *p
	}
	return AttentionItem{Worker: id, WorkerName: name, Channel: channel,
		Unread: def(o.unread, 3), UnreadMentions: def(o.mentions, 1), FirstUnreadSeq: def(o.first, 100),
		LastRelevantSeq: def(o.relevant, 110), LastSeq: def(o.head, 120)}
}

type screenSpec struct {
	tail   []string
	draft  *string
	source string
	ok     bool // false default = readable
}

type fakeCLIs struct {
	t *testing.T
	// attention
	attention struct {
		token   string
		workers []AttentionItem
	}
	attentionOk bool
	// fresh re-read rows; nil = same as the poll
	fresh   []AttentionItem
	freshOk bool
	freshN  int
	pages   int
	dropTok bool
	incompl bool

	terminals []Terminal
	busy      map[string]bool
	// busy on a handle's second wait — the post-recompute idle recheck
	busyRecheck map[string]bool
	// terminals with NO output tracking — tui-idle can never satisfy (#7398)
	untracked map[string]bool
	// served as TRACKED on every list AFTER the first — tracking healed between gate and recheck
	healTracking map[string]bool
	listCalls    int
	waits        map[string]int
	reject       map[string]bool
	screens      map[string]*screenSpec
	recheck      map[string]*screenSpec
	reads        map[string]int
	sent         []struct{ handle, text string }
	// workers with a mention-only row; nil = every queried worker has one
	mentionOnly    map[string]bool
	registerFails  map[string]bool
	registerRowFor func(worker string) *RegisterRow

	head      int
	headOk    bool
	headRead  int
	freshHead *int

	cursors       map[string]int
	cursorQueries map[string]int
	freshCursors  map[string]int
	cursorNulls   map[string]bool
	cursorFails   map[string]bool
	cursorErrors  map[string]bool

	renameFromList int
	hideFromList   int

	polls   []AttentionRequest
	calls   []string
	sawWait bool
	root    string
	logs    []string
}

func newFake(t *testing.T, root string) *fakeCLIs {
	t.Helper()
	f := &fakeCLIs{t: t, root: root,
		attentionOk: true, freshOk: true, pages: 1,
		busy: map[string]bool{}, busyRecheck: map[string]bool{}, untracked: map[string]bool{},
		healTracking: map[string]bool{}, waits: map[string]int{}, reject: map[string]bool{},
		screens: map[string]*screenSpec{}, recheck: map[string]*screenSpec{}, reads: map[string]int{},
		registerFails: map[string]bool{}, cursors: map[string]int{}, cursorQueries: map[string]int{},
		freshCursors: map[string]int{}, cursorNulls: map[string]bool{}, cursorFails: map[string]bool{},
		cursorErrors: map[string]bool{},
	}
	f.attention.token = token1
	return f
}

//nolint:unparam // later describe blocks add terminals under other names
func (f *fakeCLIs) addTerminal(name string, agent string, handle string, tracked bool) Terminal {
	f.t.Helper()
	wt := filepath.Join(f.root, "workspaces", name)
	if err := os.MkdirAll(wt, 0o755); err != nil {
		f.t.Fatal(err)
	}
	if handle == "" {
		handle = "term_" + name
	}
	var ident *string
	if agent != "" {
		ident = &agent
	}
	var out *int64
	if tracked {
		now := int64(1700000000)
		out = &now
	} else {
		f.untracked[handle] = true
	}
	row := Terminal{Handle: handle, WorktreePath: wt, AgentIdentity: ident, Connected: true, Writable: true, LastOutputAt: out}
	f.terminals = append(f.terminals, row)
	tail := claudeIdle
	if agent == "codex" {
		tail = codexIdle
	}
	f.screens[handle] = &screenSpec{tail: tail}
	return row
}

// The handle param stays: later describe blocks set screens on other handles.
func (f *fakeCLIs) setScreen(handle string, s screenSpec) { //nolint:unparam
	f.screens[handle] = &s
}

func (f *fakeCLIs) setRecheck(handle string, s screenSpec) { //nolint:unparam
	f.recheck[handle] = &s
}

// Server seam.

func (f *fakeCLIs) AttentionPage(ctx context.Context, req AttentionRequest) (AttentionPage, error) {
	f.t.Helper()
	f.polls = append(f.polls, req)
	bare := req.Since == "" && req.Page == ""
	out := func(data any, code int) (AttentionPage, error) {
		if code != 0 {
			return AttentionPage{}, fmt.Errorf("exit %d", code)
		}
		return data.(AttentionPage), nil
	}
	if bare && f.sawWait {
		f.freshN++
		if !f.freshOk {
			return out(nil, 1)
		}
		items := f.attention.workers
		if f.fresh != nil {
			items = f.fresh
		}
		return out(AttentionPage{ScanComplete: true, AdoptableSince: freshTok, Items: items}, 0)
	}
	if !f.attentionOk {
		return out(nil, 1)
	}
	n := 1
	if req.Page != "" {
		if parsed, err := strconv.Atoi(req.Page); err == nil {
			n = parsed
		}
	}
	last := n >= f.pages
	tok := ""
	if last && !f.dropTok {
		tok = f.attention.token
	}
	return out(AttentionPage{ScanComplete: last && !f.incompl, NextPage: map[bool]string{true: "", false: fmt.Sprint(n + 1)}[last],
		AdoptableSince: tok, Items: map[bool][]AttentionItem{true: f.attention.workers, false: nil}[last]}, 0)
}

func (f *fakeCLIs) RegisterRow(ctx context.Context, workerID string) (RegisterRow, error) {
	f.t.Helper()
	if f.registerFails[workerID] {
		return RegisterRow{}, fmt.Errorf("register down")
	}
	if f.registerRowFor != nil {
		if row := f.registerRowFor(workerID); row != nil {
			return *row, nil
		}
	}
	ids := f.mentionOnly
	if ids == nil {
		ids = map[string]bool{"w-eli": true, "w-baloo": true, "w-ada": true}
	}
	return RegisterRow{NamedBy: "WORKER", ChannelID: channel, MentionOnly: ids[workerID]}, nil
}

func (f *fakeCLIs) ReadCursor(ctx context.Context, workerID string) (CursorResult, error) {
	f.t.Helper()
	n := f.cursorQueries[workerID] + 1
	f.cursorQueries[workerID] = n
	if f.cursorFails[workerID] {
		return CursorResult{}, fmt.Errorf("read-state down")
	}
	if f.cursorErrors[workerID] {
		v := f.cursors[workerID]
		if v == 0 {
			v = 100
		}
		return CursorResult{LastSeen: &v, FieldErrs: []string{"partial"}}, nil
	}
	if n >= 2 {
		if v, ok := f.freshCursors[workerID]; ok {
			return CursorResult{LastSeen: &v}, nil
		}
	}
	if f.cursorNulls[workerID] {
		return CursorResult{LastSeen: nil}, nil
	}
	v := f.cursors[workerID]
	return CursorResult{LastSeen: &v}, nil
}

func (f *fakeCLIs) ChannelHead(ctx context.Context, channelRef string) (int, error) {
	f.t.Helper()
	f.headRead++
	if !f.headOk {
		return 0, fmt.Errorf("channel query down")
	}
	if f.headRead >= 2 && f.freshHead != nil {
		return *f.freshHead, nil
	}
	if f.head == 0 {
		return 120, nil
	}
	return f.head, nil
}

func (f *fakeCLIs) MarkRead(ctx context.Context, through int) error { return nil }

// Orca seam: one runner playing terminal list/wait/read/send.

type orcaOut struct {
	Ok    bool           `json:"ok"`
	Error *orcaErr       `json:"error,omitempty"`
	Raw   map[string]any `json:"result,omitempty"`
}

type orcaErr struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (f *fakeCLIs) orca(ctx context.Context, argv []string) RunResult {
	f.t.Helper()
	f.calls = append(f.calls, strings.Join(argv, " "))
	verb := strings.Join(argv[0:2], " ")
	out := func(o any, code int) RunResult {
		b, _ := json.Marshal(o)
		return RunResult{Code: code, Stdout: string(b)}
	}
	ok := func(res map[string]any) RunResult { return out(orcaOut{Ok: true, Raw: res}, 0) }
	fail := func(code string, exit int) RunResult {
		return out(orcaOut{Ok: false, Error: &orcaErr{Code: code, Message: code}}, exit)
	}
	switch verb {
	case "terminal list":
		f.listCalls++
		if f.hideFromList != 0 && f.listCalls >= f.hideFromList {
			return ok(map[string]any{"terminals": []any{}})
		}
		rows := f.terminals
		if f.renameFromList != 0 && f.listCalls >= f.renameFromList {
			rows = append([]Terminal{}, rows...)
			for i := range rows {
				rows[i].Handle += "_r"
			}
		} else if f.listCalls > 1 && len(f.healTracking) > 0 {
			rows = append([]Terminal{}, rows...)
			now := int64(1700000000)
			for i := range rows {
				if f.healTracking[rows[i].Handle] {
					rows[i].LastOutputAt = &now
				}
			}
		}
		return ok(map[string]any{"terminals": rows})
	case "terminal wait":
		handle := argv[flagIdx(argv, "--terminal")+1]
		n := f.waits[handle] + 1
		f.waits[handle] = n
		busyNow := f.busy[handle] || (n != 1 && f.busyRecheck[handle])
		satisfiable := !busyNow && !f.untracked[handle]
		if satisfiable {
			f.sawWait = true
		}
		if satisfiable {
			return ok(map[string]any{"wait": map[string]any{"satisfied": true}})
		}
		return fail("timeout", 0)
	case "terminal read":
		handle := argv[flagIdx(argv, "--terminal")+1]
		n := f.reads[handle] + 1
		f.reads[handle] = n
		rcIdx := 2
		if f.untracked[handle] {
			rcIdx = 3
		}
		sc := f.screens[handle]
		if n >= rcIdx {
			if r := f.recheck[handle]; r != nil {
				sc = r
			}
		}
		if sc == nil || !sc.ok {
			return fail("terminal_handle_stale", 1)
		}
		if n == 1 && f.untracked[handle] {
			for _, fx := range [][]string{claudeIdle, codexIdle, claudeFresh, codexFresh} {
				if fmt.Sprint(sc.tail) == fmt.Sprint(fx) {
					f.sawWait = true
				}
			}
		}
		term := map[string]any{"handle": handle, "source": sc.source, "tail": sc.tail}
		if sc.source == "" {
			term["source"] = "screen"
		}
		if sc.draft != nil {
			term["draft"] = *sc.draft
		}
		return ok(map[string]any{"terminal": term})
	case "terminal send":
		handle := argv[flagIdx(argv, "--terminal")+1]
		if f.reject[handle] {
			return fail("terminal_handle_stale", 1)
		}
		text := argv[flagIdx(argv, "--text")+1]
		f.sent = append(f.sent, struct{ handle, text string }{handle, text})
		return ok(map[string]any{"send": map[string]any{"accepted": true, "prompt": map[string]any{"requestId": "req-1", "stages": []string{"input_accepted", "turn_started"}}}})
	}
	f.t.Fatalf("unexpected call %v", argv)
	return RunResult{}
}

func flagIdx(argv []string, flag string) int {
	for i, a := range argv {
		if a == flag {
			return i
		}
	}
	return -1
}

// Harness helpers mirroring the TS cfg()/poll()/adopted().

func cfgEntries(root string, entries ...[2]string) *Config {
	c := &Config{App: "hrn:app:x:team", Channel: channel, OrcaCli: "orca",
		StateDir: filepath.Join(root, "state"), BusyWaitMS: 10, SubmitWaitS: 1, CoordinatorWatchMin: 20}
	for _, e := range entries {
		c.Roster = append(c.Roster, RosterEntry{Worker: e[0], Name: e[1], Worktree: filepath.Join(root, "workspaces", strings.ToLower(e[1]))})
	}
	return c
}

func (f *fakeCLIs) poll(t *testing.T, c *Config) *Outcome {
	t.Helper()
	out, err := PollOnce(context.Background(), c, f, f.orca, func(l string) { f.logs = append(f.logs, l) }, nil)
	if err != nil {
		t.Fatalf("pollOnce: %v", err)
	}
	return out
}

func adoptedToken(t *testing.T, c *Config) string {
	t.Helper()
	return NewState(c.StateDir).Token()
}
