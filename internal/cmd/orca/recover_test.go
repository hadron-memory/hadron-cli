package orca

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/flock"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
	"github.com/spf13/cobra"
)

// The fake world behind `terminal recover|recreate` (cli#2015): a fake `orca`
// whose create and close change what the next list and the process scan show,
// and a process table the scan reads. Nothing here touches a real Orca or /proc.

const claudeBin = "/home/orca/.local/bin/claude"

type recoverWorld struct {
	*hostRig
	wt          string
	procs       []Proc
	owner       map[string]int // terminal handle -> agent pid that dies with it
	transcripts []Transcript
	stuck       bool // a closed agent's process never exits
	keepWrapper bool // closing kills the agent but its supervising wrapper stays
	noAgent     bool // a created terminal shows a shell and starts no agent
	noScreen    bool // a created terminal's screen cannot be read
	oddScreen   bool // a created terminal shows text no rule recognizes, and no identity
	flipScreen  bool // the created terminal shows a shell on first read, an agent after; scans fail after the first
	reads       int
	shellScreen bool // a created terminal starts an agent but its screen shows a shell
	twoAgents   bool // a created terminal starts two agents
	staged      bool
	ancestors   []int
	slept       time.Duration
	nextPID     int
}

// recoverCfg writes a host configuration with the given workers block and
// returns its path and the worktrees root.
func recoverCfg(t *testing.T, workers string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	key := filepath.Join(dir, "host.key")
	if err := os.WriteFile(key, []byte(sentinelKey+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "trees")
	cfg := "app: hrn:app:acme.com:eng-team\nlabel: box1\npoll_interval_seconds: 30\nkey_file: " + key + "\nworktrees_root: " + root + "\nworkers:\n" + workers
	p := filepath.Join(dir, "host.yaml")
	if err := os.WriteFile(p, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return p, root
}

const jackWorker = `  Jack:
    family: claude
    launch: ["/home/orca/.local/bin/worker-supervise", "claude-holger", "--dangerously-skip-permissions", "--model", "sonnet"]
    worktrees: ["hadron-cli/jack"]
`

var died = clock

func newRecoverWorld(t *testing.T) (*recoverWorld, string) {
	t.Helper()
	h := newHostRig(t)
	cfg, root := recoverCfg(t, jackWorker)
	w := &recoverWorld{hostRig: h, wt: filepath.Join(root, "hadron-cli", "jack"), owner: map[string]int{}, nextPID: 5000}
	h.reads.workers = []Worker{{ID: "w_jack", Name: "Jack", URN: "hrn:worker:acme.com:eng-team:jack"}}
	h.env.BoundAt = func(_ context.Context, wt string) (Bound, bool) {
		if wt == w.wt {
			return Bound{WorkerID: "w_jack", WorkerName: "Jack", AppID: "app1"}, true
		}
		return Bound{}, false
	}
	h.orca.hook = w.hook
	h.env.Recover = RecoverEnv{
		Procs: func() ([]Proc, error) {
			if w.flipScreen && w.reads > 0 {
				return nil, errors.New("scan failed")
			}
			return append([]Proc(nil), w.procs...), nil
		},
		Transcripts:  func(string, []string) ([]Transcript, error) { return w.transcripts, nil },
		PromptStaged: func(string) bool { return w.staged },
		Ancestors:    func() []int { return w.ancestors },
		Sleep:        func(d time.Duration) { w.slept += d },
		GoneWait:     3 * time.Second,
		VerifyWait:   3 * time.Second,
	}
	return w, cfg
}

func (w *recoverWorld) newPID() int { w.nextPID++; return w.nextPID }

// shell adds a bare-shell terminal in the worktree.
func (w *recoverWorld) shell(handle string) {
	w.orca.terms = append(w.orca.terms, term(handle, w.wt, ""))
	w.orca.screens[handle] = []string{"orca@box:~/jack$"}
	w.procs = append(w.procs, Proc{PID: w.newPID(), Cwd: w.wt, Argv: []string{"/bin/bash", "--rcfile", "/x/rcfile"}})
}

// agent adds a live idle Claude terminal and its process in the worktree.
func (w *recoverWorld) agent() int {
	const handle = "term_live"
	w.orca.terms = append(w.orca.terms, term(handle, w.wt, "claude"))
	w.orca.screens[handle] = idleScreen
	pid := w.newPID()
	w.owner[handle] = pid
	w.procs = append(w.procs,
		Proc{PID: pid - 1000, Cwd: w.wt, Argv: []string{"/bin/bash", "/home/orca/.local/bin/worker-supervise", "claude-holger"}},
		Proc{PID: pid, Cwd: w.wt, Argv: []string{claudeBin, "--dangerously-skip-permissions"}})
	return pid
}

func flag(argv []string, name string) string {
	for i := range argv {
		if argv[i] == name && i+1 < len(argv) {
			return argv[i+1]
		}
	}
	return ""
}

func (w *recoverWorld) hook(argv []string) (orcaterm.Result, bool) {
	ok := orcaterm.Result{Stdout: `{"ok":true,"result":{}}`}
	switch argv[0] + " " + argv[1] {
	case "terminal close":
		h := flag(argv, "--terminal")
		kept := w.orca.terms[:0:0]
		for _, tm := range w.orca.terms {
			if tm["handle"] != h {
				kept = append(kept, tm)
			}
		}
		w.orca.terms = kept
		if pid, found := w.owner[h]; found && !w.stuck {
			var left []Proc
			for _, p := range w.procs {
				if p.PID != pid && (p.PID != pid-1000 || w.keepWrapper) {
					left = append(left, p)
				}
			}
			w.procs = left
		}
		return ok, true
	case "terminal read":
		if w.flipScreen && flag(argv, "--terminal") == "term_new" {
			w.reads++
			if w.reads == 1 {
				b, _ := json.Marshal(map[string]any{"ok": true, "result": map[string]any{"terminal": map[string]any{"source": "screen", "tail": []string{"orca@box:~/jack$"}}}})
				return orcaterm.Result{Stdout: string(b)}, true
			}
		}
		return orcaterm.Result{}, false
	case "terminal create":
		h := "term_new"
		if w.noAgent {
			w.orca.terms = append(w.orca.terms, term(h, w.wt, ""))
			w.orca.screens[h] = []string{"orca@box:~/jack$"}
			return orcaterm.Result{Stdout: `{"ok":true,"result":{"terminal":{"handle":"term_new"}}}`}, true
		}
		w.orca.terms = append(w.orca.terms, term(h, w.wt, "claude"))
		w.orca.screens[h] = idleScreen
		if w.shellScreen {
			w.orca.terms[len(w.orca.terms)-1] = term(h, w.wt, "")
			w.orca.screens[h] = []string{"orca@box:~/jack$"}
		}
		switch {
		case w.noScreen:
			delete(w.orca.screens, h)
		case w.flipScreen:
			w.orca.terms[len(w.orca.terms)-1] = term(h, w.wt, "")
		case w.oddScreen:
			w.orca.terms[len(w.orca.terms)-1] = term(h, w.wt, "")
			w.orca.screens[h] = []string{"something nothing recognizes"}
		}
		w.procs = append(w.procs, Proc{PID: w.newPID(), Cwd: w.wt, Argv: []string{claudeBin, "--resume", "x"}})
		if w.twoAgents {
			w.procs = append(w.procs, Proc{PID: w.newPID(), Cwd: w.wt, Argv: []string{claudeBin}})
		}
		return orcaterm.Result{Stdout: `{"ok":true,"result":{"terminal":{"handle":"term_new"}}}`}, true
	}
	return orcaterm.Result{}, false
}

func (w *recoverWorld) calls(verb string) []string {
	var out []string
	for _, c := range w.orca.commands {
		if strings.HasPrefix(c, verb) {
			out = append(out, c)
		}
	}
	return out
}

func (w *recoverWorld) ranOnlyTerminalVerbs(t *testing.T) {
	t.Helper()
	for _, c := range w.orca.commands {
		f := strings.Fields(c)
		if f[0] != "terminal" || (f[1] != "list" && f[1] != "read" && f[1] != "create" && f[1] != "close") {
			t.Fatalf("recover ran something beyond list/read/create/close: %q", c)
		}
	}
}

func (w *recoverWorld) exec(t *testing.T, build func(*recoverWorld) *cobra.Command, json bool, args ...string) (int, string) {
	t.Helper()
	w.f.JSON = json
	w.out.Reset()
	w.errs.Reset()
	cmd := build(w)
	cmd.SilenceErrors = true
	cmd.SetArgs(args)
	cmd.SetOut(w.out)
	cmd.SetErr(w.out)
	err := cmd.ExecuteContext(context.Background())
	return exitcode.FromError(err), w.out.String()
}

func recoverCmd(w *recoverWorld) *cobra.Command  { return newCmdRecover(w.f, w.env) }
func recreateCmd(w *recoverWorld) *cobra.Command { return newCmdRecreate(w.f, w.env) }

func tr(id string, ago time.Duration) Transcript { return Transcript{ID: id, ModTime: died.Add(-ago)} }

func TestRecoverBareShellCreatesOneResumedTerminal(t *testing.T) {
	w, cfg := newRecoverWorld(t)
	w.shell("term_old")
	// aaa is older, bbb the newest not newer than the death, ccc was written after.
	w.transcripts = []Transcript{tr("aaa", 2*time.Hour), tr("bbb", 30*time.Minute), {ID: "ccc", ModTime: died.Add(10 * time.Minute)}}
	code, out := w.exec(t, recoverCmd, true, "Jack", "--config", cfg, "--died-at", died.Format(time.RFC3339))
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	creates := w.calls("terminal create")
	if len(creates) != 1 {
		t.Fatalf("want exactly one create, got %v", creates)
	}
	c := creates[0]
	for _, want := range []string{"path:" + w.wt, "--title=Jack", "worker-supervise claude-holger --dangerously-skip-permissions --model sonnet --resume bbb"} {
		if !strings.Contains(c, want) {
			t.Fatalf("create lacks %q: %s", want, c)
		}
	}
	m := decode(t, out)
	tx, _ := m["transcript"].(map[string]any)
	if m["result"] != "recovered" || tx["id"] != "bbb" || tx["why"] == "" {
		t.Fatalf("json: %s", out)
	}
	if v, _ := m["verification"].(map[string]any); v["verified"] != true || v["agents"] != float64(1) {
		t.Fatalf("verification: %s", out)
	}
	w.ranOnlyTerminalVerbs(t)
}

func TestRecoverDryRunChangesNothing(t *testing.T) {
	w, cfg := newRecoverWorld(t)
	w.shell("term_old")
	w.transcripts = []Transcript{tr("bbb", 30*time.Minute)}
	code, out := w.exec(t, recoverCmd, true, "Jack", "--config", cfg, "--dry-run")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if n := len(w.calls("terminal create")) + len(w.calls("terminal close")); n != 0 {
		t.Fatalf("dry run changed Orca: %v", w.orca.commands)
	}
	m := decode(t, out)
	if m["dryRun"] != true || m["result"] != "planned" || m["plan"] == nil {
		t.Fatalf("json: %s", out)
	}
	_, text := w.exec(t, recoverCmd, false, "Jack", "--config", cfg, "--dry-run")
	if !strings.Contains(text, "--resume bbb") || !strings.Contains(strings.ToLower(text), "dry run") {
		t.Fatalf("text plan: %s", text)
	}
}

func TestRecoverLiveAgentWithoutForceRefuses(t *testing.T) {
	w, cfg := newRecoverWorld(t)
	w.agent()
	w.transcripts = []Transcript{tr("bbb", time.Hour)}
	code, out := w.exec(t, recoverCmd, false, "Jack", "--config", cfg)
	if code != exitcode.RecoverRefused {
		t.Fatalf("exit %d want %d\n%s", code, exitcode.RecoverRefused, out)
	}
	if !strings.Contains(out, "idle") {
		t.Fatalf("refusal must name the state: %s", out)
	}
	if n := len(w.calls("terminal create")) + len(w.calls("terminal close")); n != 0 {
		t.Fatalf("a refusal changed Orca: %v", w.orca.commands)
	}
}

func TestRecoverForceClosesTheAgentWaitsThenCreatesOne(t *testing.T) {
	w, cfg := newRecoverWorld(t)
	w.agent()
	w.shell("term_sh")
	w.transcripts = []Transcript{tr("bbb", time.Hour)}
	code, out := w.exec(t, recoverCmd, true, "Jack", "--config", cfg, "--force")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	closes := w.calls("terminal close")
	if len(closes) != 1 || !strings.Contains(closes[0], "term_live") {
		t.Fatalf("want the agent terminal closed: %v", closes)
	}
	if len(w.calls("terminal create")) != 1 {
		t.Fatalf("want one create: %v", w.orca.commands)
	}
	m := decode(t, out)
	if cl, _ := m["closed"].([]any); len(cl) != 1 {
		t.Fatalf("json closed: %s", out)
	}
	if in, _ := m["interrupted"].(map[string]any); in["status"] != "idle" {
		t.Fatalf("json must say what was interrupted: %s", out)
	}
}

func TestRecoverForceWithAProcessThatNeverExitsCreatesNothing(t *testing.T) {
	w, cfg := newRecoverWorld(t)
	w.agent()
	w.stuck = true
	w.transcripts = []Transcript{tr("bbb", time.Hour)}
	code, out := w.exec(t, recoverCmd, false, "Jack", "--config", cfg, "--force")
	if code != exitcode.RecoverRefused {
		t.Fatalf("exit %d want %d\n%s", code, exitcode.RecoverRefused, out)
	}
	if n := len(w.calls("terminal create")); n != 0 {
		t.Fatalf("created a second agent: %v", w.orca.commands)
	}
	if w.slept == 0 {
		t.Fatalf("it must wait before giving up")
	}
}

func TestRecoverRefusesAnAgentProcessNoTerminalShows(t *testing.T) {
	// The terminal is gone from the list but its agent still runs in the worktree.
	w, cfg := newRecoverWorld(t)
	w.shell("term_sh")
	w.procs = append(w.procs, Proc{PID: 7777, Cwd: w.wt, Argv: []string{claudeBin}})
	w.transcripts = []Transcript{tr("bbb", time.Hour)}
	code, out := w.exec(t, recoverCmd, false, "Jack", "--config", cfg)
	if code != exitcode.RecoverRefused || len(w.calls("terminal create")) != 0 {
		t.Fatalf("exit %d, calls %v\n%s", code, w.orca.commands, out)
	}
	if w.slept != 0 {
		t.Fatalf("an agent no terminal shows is refused up front, not after a wait")
	}
}

func TestRecoverWrapperAloneIsNotAnAgentButIsNotLeftBehind(t *testing.T) {
	// "agent binary first in argv, never a contains-match on the wrapper": a
	// process whose argv merely MENTIONS claude is not an agent.
	w, cfg := newRecoverWorld(t)
	w.shell("term_sh")
	w.procs = append(w.procs, Proc{PID: 7778, Cwd: w.wt, Argv: []string{"/usr/bin/vim", "notes-about-claude.md"}})
	w.transcripts = []Transcript{tr("bbb", time.Hour)}
	code, out := w.exec(t, recoverCmd, false, "Jack", "--config", cfg)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

func TestRecoverNeverRunsInsideTheTargetAgent(t *testing.T) {
	w, cfg := newRecoverWorld(t)
	pid := w.agent()
	w.ancestors = []int{pid}
	w.transcripts = []Transcript{tr("bbb", time.Hour)}
	code, out := w.exec(t, recoverCmd, false, "Jack", "--config", cfg, "--force")
	if code != exitcode.RecoverRefused || len(w.calls("terminal close")) != 0 {
		t.Fatalf("exit %d calls %v\n%s", code, w.orca.commands, out)
	}
}

func TestRecoverUnverifiedWhenNoAgentAppears(t *testing.T) {
	w, cfg := newRecoverWorld(t)
	w.shell("term_old")
	w.noAgent = true
	w.transcripts = []Transcript{tr("bbb", time.Hour)}
	code, out := w.exec(t, recoverCmd, true, "Jack", "--config", cfg)
	if code != exitcode.RecoverUnverified {
		t.Fatalf("exit %d want %d\n%s", code, exitcode.RecoverUnverified, out)
	}
	if v, _ := decode(t, out)["verification"].(map[string]any); v["verified"] != false {
		t.Fatalf("json: %s", out)
	}
}

func TestRecoverUnverifiedWhenTwoAgentsAppear(t *testing.T) {
	w, cfg := newRecoverWorld(t)
	w.shell("term_old")
	w.twoAgents = true
	w.transcripts = []Transcript{tr("bbb", time.Hour)}
	code, _ := w.exec(t, recoverCmd, true, "Jack", "--config", cfg)
	if code != exitcode.RecoverUnverified {
		t.Fatalf("exit %d want %d", code, exitcode.RecoverUnverified)
	}
}

func TestRecoverOwnCodesCreateNothing(t *testing.T) {
	cases := []struct {
		name    string
		workers string
		trs     []Transcript
		args    []string
		want    int
	}{
		{"no host.yaml entry", "  Someone:\n    family: claude\n    launch: [x]\n    worktrees: [a/b]\n", []Transcript{tr("bbb", time.Hour)}, nil, exitcode.RecoverNoLaunch},
		{"codex worker", "  Jack:\n    family: codex\n    launch: [codex]\n    worktrees: [hadron-cli/jack]\n", []Transcript{tr("bbb", time.Hour)}, nil, exitcode.RecoverUnsupported},
		{"no transcript", jackWorker, nil, nil, exitcode.RecoverNoTranscript},
		{"only a later transcript", jackWorker, []Transcript{{ID: "ccc", ModTime: died.Add(time.Minute)}}, []string{"--died-at", died.Format(time.RFC3339)}, exitcode.RecoverNoTranscript},
	}
	seen := map[int]bool{}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w, _ := newRecoverWorld(t)
			cfg, root := recoverCfg(t, c.workers)
			w.wt = filepath.Join(root, "hadron-cli", "jack")
			w.shell("term_old")
			w.transcripts = c.trs
			code, out := w.exec(t, recoverCmd, false, append([]string{"Jack", "--config", cfg}, c.args...)...)
			if code != c.want {
				t.Fatalf("exit %d want %d\n%s", code, c.want, out)
			}
			if n := len(w.calls("terminal create")) + len(w.calls("terminal close")); n != 0 {
				t.Fatalf("created or closed: %v", w.orca.commands)
			}
			seen[code] = true
		})
	}
	if len(seen) != 3 { // the two no-transcript cases share a code
		t.Fatalf("each refusal needs its own code, got %v", seen)
	}
}

func TestRecoverFreshLaunchesWithoutResumeOnAStagedPrompt(t *testing.T) {
	w, cfg := newRecoverWorld(t)
	w.shell("term_old")
	w.staged = true
	code, out := w.exec(t, recoverCmd, true, "Jack", "--config", cfg, "--fresh")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	c := w.calls("terminal create")
	if len(c) != 1 || strings.Contains(c[0], "--resume") {
		t.Fatalf("fresh launch must not resume: %v", c)
	}
	// --fresh with nothing staged is no safe launch.
	w2, cfg2 := newRecoverWorld(t)
	w2.shell("term_old")
	if code, _ := w2.exec(t, recoverCmd, false, "Jack", "--config", cfg2, "--fresh"); code != exitcode.RecoverNoTranscript {
		t.Fatalf("exit %d", code)
	}
}

func TestRecreateClosesEveryTerminalAndLeavesOne(t *testing.T) {
	w, cfg := newRecoverWorld(t)
	w.agent()
	w.shell("term_sh")
	w.transcripts = []Transcript{tr("bbb", time.Hour)}
	code, out := w.exec(t, recreateCmd, true, "Jack", "--config", cfg)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if n := len(w.calls("terminal close")); n != 2 {
		t.Fatalf("want both terminals closed, got %v", w.calls("terminal close"))
	}
	if len(w.calls("terminal create")) != 1 {
		t.Fatalf("want one create: %v", w.orca.commands)
	}
	var live int
	for _, tm := range w.orca.terms {
		if tm["worktreePath"] == w.wt {
			live++
		}
	}
	if live != 1 {
		t.Fatalf("want exactly one terminal left, got %d", live)
	}
	if decode(t, out)["action"] != "recreate" {
		t.Fatalf("json: %s", out)
	}
	w.ranOnlyTerminalVerbs(t)
}

func TestRecreateDryRunAndStuckAgent(t *testing.T) {
	w, cfg := newRecoverWorld(t)
	w.agent()
	w.transcripts = []Transcript{tr("bbb", time.Hour)}
	if code, out := w.exec(t, recreateCmd, false, "Jack", "--config", cfg, "--dry-run"); code != 0 || len(w.calls("terminal close"))+len(w.calls("terminal create")) != 0 {
		t.Fatalf("dry run: exit %d, %v\n%s", code, w.orca.commands, out)
	}
	w.stuck = true
	if code, _ := w.exec(t, recreateCmd, false, "Jack", "--config", cfg); code != exitcode.RecoverRefused || len(w.calls("terminal create")) != 0 {
		t.Fatalf("a stuck agent must refuse: exit %d, %v", code, w.orca.commands)
	}
}

func TestRecoverAcceptsNameUrnAndId(t *testing.T) {
	for _, arg := range []string{"Jack", "jack", "w_jack", "hrn:worker:acme.com:eng-team:jack"} {
		w, cfg := newRecoverWorld(t)
		w.shell("term_old")
		w.transcripts = []Transcript{tr("bbb", time.Hour)}
		if code, out := w.exec(t, recoverCmd, false, arg, "--config", cfg, "--dry-run"); code != 0 {
			t.Fatalf("%s: exit %d\n%s", arg, code, out)
		}
	}
}

func TestRecoverNeedsOrcaAndAConfig(t *testing.T) {
	w, cfg := newRecoverWorld(t)
	w.orca.down = true
	if code, _ := w.exec(t, recoverCmd, false, "Jack", "--config", cfg); code != exitcode.OrcaUnreachable {
		t.Fatalf("exit %d want 10", code)
	}
	if code, _ := w.exec(t, recoverCmd, false, "Jack"); code != exitcode.Usage {
		t.Fatalf("exit %d want usage", code)
	}
}

func TestGroupHelpNoLongerClaimsReadOnly(t *testing.T) {
	long := newCmdTerminal(newRig(t).f).Long
	if strings.Contains(strings.ToLower(long), "this group reads only") {
		t.Fatalf("group help still claims read-only:\n%s", long)
	}
	for _, want := range []string{"recover", "recreate"} {
		if !strings.Contains(long, want) {
			t.Fatalf("group help lacks %q", want)
		}
	}
	if !strings.Contains(strings.ToLower(newCmdRecover(newRig(t).f, Env{}).Long), "never") {
		t.Fatalf("recover help must state the never-two-agents guarantee")
	}
}

func TestRecoverJSONShapeIsStable(t *testing.T) {
	w, cfg := newRecoverWorld(t)
	w.shell("term_old")
	w.transcripts = []Transcript{tr("bbb", time.Hour)}
	_, out := w.exec(t, recoverCmd, true, "Jack", "--config", cfg)
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"contract", "action", "worker", "worktree", "dryRun", "result", "found", "plan", "transcript", "closed", "created", "verification"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("missing key %q in %s", k, out)
		}
	}
}

func TestRecoverForceWithTheSupervisorStillAliveCreatesNothing(t *testing.T) {
	// The wrapper would relaunch the agent beside the new one: it must be gone too.
	w, cfg := newRecoverWorld(t)
	w.agent()
	w.keepWrapper = true
	w.transcripts = []Transcript{tr("bbb", time.Hour)}
	code, out := w.exec(t, recoverCmd, false, "Jack", "--config", cfg, "--force")
	if code != exitcode.RecoverRefused || len(w.calls("terminal create")) != 0 {
		t.Fatalf("exit %d calls %v\n%s", code, w.orca.commands, out)
	}
}

func TestRecoverUnverifiedWhenTheNewScreenShowsAShell(t *testing.T) {
	w, cfg := newRecoverWorld(t)
	w.shell("term_old")
	w.shellScreen = true
	w.transcripts = []Transcript{tr("bbb", time.Hour)}
	if code, _ := w.exec(t, recoverCmd, false, "Jack", "--config", cfg); code != exitcode.RecoverUnverified {
		t.Fatalf("exit %d want %d", code, exitcode.RecoverUnverified)
	}
}

func TestRecoverTranscriptsDirFlagOverridesTheDerivedOne(t *testing.T) {
	w, cfg := newRecoverWorld(t)
	w.shell("term_old")
	dir := t.TempDir()
	const id = "0a1b2c3d-0000-4000-8000-000000000001"
	f := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(f, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(f, died.Add(-time.Hour), died.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	w.transcripts = []Transcript{tr("not-this-one", time.Minute)}
	if code, out := w.exec(t, recoverCmd, false, "Jack", "--config", cfg, "--transcripts-dir", dir, "--dry-run"); code != 0 || !strings.Contains(out, "--resume "+id) {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

func TestRecoverEmptyFlagsAreUsageNotDefaults(t *testing.T) {
	for _, fl := range []string{"--died-at=", "--worktree=", "--transcripts-dir="} {
		w, cfg := newRecoverWorld(t)
		w.shell("term_old")
		w.transcripts = []Transcript{tr("bbb", time.Hour)}
		if code, out := w.exec(t, recoverCmd, false, "Jack", "--config", cfg, fl); code != exitcode.Usage || len(w.orca.commands) != 0 {
			t.Fatalf("%s: exit %d, orca calls %v\n%s", fl, code, w.orca.commands, out)
		}
	}
}

func TestRecoverUnverifiedWithoutPositiveScreenEvidence(t *testing.T) {
	for name, set := range map[string]func(*recoverWorld){
		"unreadable screen":     func(w *recoverWorld) { w.noScreen = true },
		"unrecognized screen":   func(w *recoverWorld) { w.oddScreen = true },
		"stale evidence reused": func(w *recoverWorld) { w.flipScreen = true },
	} {
		t.Run(name, func(t *testing.T) {
			w, cfg := newRecoverWorld(t)
			w.shell("term_old")
			w.transcripts = []Transcript{tr("bbb", time.Hour)}
			set(w)
			code, out := w.exec(t, recoverCmd, true, "Jack", "--config", cfg)
			if code != exitcode.RecoverUnverified {
				t.Fatalf("exit %d want %d\n%s", code, exitcode.RecoverUnverified, out)
			}
		})
	}
}

func TestRecoverOneRunPerWorktree(t *testing.T) {
	w, cfg := newRecoverWorld(t)
	w.shell("term_old")
	w.transcripts = []Transcript{tr("bbb", time.Hour)}
	held := flock.New(recoverLockPath(filepath.Join(filepath.Dir(cfg), "hadron-host-state.json"), w.wt))
	if ok, err := held.TryLock(); err != nil || !ok {
		t.Fatal("test could not take the lock", err)
	}
	defer func() { _ = held.Unlock() }()
	for _, build := range []func(*recoverWorld) *cobra.Command{recoverCmd, recreateCmd} {
		code, out := w.exec(t, build, false, "Jack", "--config", cfg)
		if code != exitcode.RecoverRefused || len(w.calls("terminal create"))+len(w.calls("terminal close")) != 0 {
			t.Fatalf("a competing run must refuse and change nothing: exit %d, %v\n%s", code, w.orca.commands, out)
		}
	}
}

func TestRecoverSeesAgentsThroughASymlinkedWorktreesRoot(t *testing.T) {
	// /proc reports the canonical cwd; the configured path goes through a symlink.
	w, _ := newRecoverWorld(t)
	cfg, root := recoverCfg(t, jackWorker)
	real := root + "-real"
	realWT := filepath.Join(real, "hadron-cli", "jack")
	if err := os.MkdirAll(realWT, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, root); err != nil {
		t.Fatal(err)
	}
	w.wt = filepath.Join(root, "hadron-cli", "jack")
	w.shell("term_old")
	w.procs = append(w.procs, Proc{PID: 7001, Cwd: realWT, Argv: []string{claudeBin}})
	w.transcripts = []Transcript{tr("bbb", time.Hour)}
	code, out := w.exec(t, recoverCmd, false, "Jack", "--config", cfg)
	if code != exitcode.RecoverRefused || len(w.calls("terminal create")) != 0 {
		t.Fatalf("an agent behind a symlinked root must be seen: exit %d, %v\n%s", code, w.orca.commands, out)
	}
}

func TestSupervisorShapes(t *testing.T) {
	launch := []string{"/home/orca/.local/bin/worker-supervise", "claude-holger"}
	yes := [][]string{
		{"/bin/bash", "/home/orca/.local/bin/worker-supervise", "claude-holger"},
		{"/bin/sh", "-c", "exec /home/orca/.local/bin/worker-supervise claude-holger --model sonnet"},
		{"/home/orca/.local/bin/worker-supervise", "claude-holger"},
		{"/usr/bin/fish", "/home/orca/.local/bin/worker-supervise"},
	}
	no := [][]string{
		{"/usr/bin/vim", "worker-supervise"},
		{"/bin/bash", "-c", "echo not-worker-supervise-really"},
		{"/bin/bash", "--rcfile", "/x/rcfile"},
	}
	for _, a := range yes {
		if !isSupervisor(Proc{Argv: a}, launch) {
			t.Errorf("%v must be a supervisor", a)
		}
	}
	for _, a := range no {
		if isSupervisor(Proc{Argv: a}, launch) {
			t.Errorf("%v must not be a supervisor", a)
		}
	}
}

func TestShellJoinQuotesWhatOrcaWillExecute(t *testing.T) {
	got := shellJoin([]string{"/bin/x", "--model", "so net", "it's", "a;b", "--resume", "id-1"})
	want := `/bin/x --model 'so net' 'it'\''s' 'a;b' --resume id-1`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestClaudeTranscriptsDerivedFromTheLauncherAndWorktree(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, ".claude-acct")
	wt := "/srv/work trees/jack.1"
	proj := filepath.Join(cfgDir, "projects", "-srv-work-trees-jack-1")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	const id = "0a1b2c3d-0000-4000-8000-0000000000aa"
	for name, body := range map[string]string{id + ".jsonl": "{}\n", "notes.jsonl": "{}\n", id + "-empty.jsonl": ""} {
		if err := os.WriteFile(filepath.Join(proj, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	launcher := filepath.Join(home, "claude-acct")
	if err := os.WriteFile(launcher, []byte("#!/bin/bash\nexport CLAUDE_CONFIG_DIR=\"$HOME/.claude-acct\"\nexec claude \"$@\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := claudeTranscripts(wt, []string{"/bin/worker-supervise", launcher, "--model", "sonnet"})
	if err != nil || len(got) != 1 || got[0].ID != id {
		t.Fatalf("got %v, %v", got, err)
	}
}
