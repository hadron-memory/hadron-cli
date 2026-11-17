//go:build !windows

package team

// hadron-cli#2027 — the health sweep's duplicate-detection and fix phase.
// These are the acceptance tests from the issue and Xabat's CHECKED gate,
// driven end to end through the sweep command with config-level fakes only:
// the orca binary is a script named by delivery.orca_bin, the duplicate
// agents are real sleeping processes whose argv[0] basename is claude/codex
// and whose cwd is a real git worktree with a session binding, and the
// supervised ones run under a real worker-supervise wrapper. The stop is a
// real SIGTERM the agent's trap records, so "nothing was stopped" is
// observable, not inferred.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

const health2027Status = `[{"contract":"terminal-status@1","worker":"Iris","status":"idle","prompt":"empty","reason":null,"lastTurnAt":null,"checkedAt":"2026-11-14T00:00:00Z"}]`

const health2027SetTerminals = `{"data":{"setHostWorkerTerminals":{"stored":[{"id":"w1","urn":"hrn:worker:test:team:iris"}],"refused":[]}}}`

type health2027Fix struct {
	Worker  string `json:"worker"`
	Action  string `json:"action"`
	Subject string `json:"subject"`
	Applied bool   `json:"applied"`
}
type health2027Alert struct {
	Worker string `json:"worker"`
	Code   string `json:"code"`
	Count  int    `json:"count"`
}
type health2027Skip struct {
	Worker string `json:"worker"`
	Code   string `json:"code"`
}
type health2027DTO struct {
	DryRun bool              `json:"dryRun"`
	Fixes  []health2027Fix   `json:"fixes"`
	Alerts []health2027Alert `json:"alerts"`
	Skips  []health2027Skip  `json:"skips"`
}

// health2027World is one sweep run's surroundings.
type health2027World struct {
	dir      string
	worktree string // absolute, a real git repo with a binding for w1/Iris
	// pause is the HealthSweepPause answer body (workers page); empty means
	// the op answers no pending reasons.
	pause string
	// reset is the ContextReset answer body; empty means contextReset null.
	reset string
	// resetsByID, when set, answers ContextReset per resetId (a JSON body
	// with the {"data":… wrapper); a resetId missing from it reads as null.
	resetsByID map[string]string
	// state is the poller state file's content (hostId or a resets map).
	state string
	// midSweepReset, when set, is written to the state file by the pause
	// handler — strictly after the sweep loaded its state snapshot and
	// before its fix loop reads the file back (#2037 N2 note: the gate is
	// not the snapshot).
	midSweepReset string
	// chatBodies records every CreateTeamChatMessage body that arrived.
	chatBodies []string
	chatRefs   []*string
	mu         sync.Mutex
	// unbound removes the worktree's session binding before the run.
	unbound bool

	orcaCloses string // file the fake orca appends closes to
	// list2Path, when it exists, replaces the listing from the SECOND
	// `terminal list` call on — the shape the N3 recheck reads.
	list2Path string
	// failList2, when it exists, fails the SECOND `terminal list` call.
	failList2  string
	agentTerms string // file the fake agents append SIGTERM traps to
	agents     []*exec.Cmd

	srv     *httptest.Server
	cfgPath string
}

// health2027Setup builds the host config, worktree, fake orca and fake server.
func health2027Setup(t *testing.T) *health2027World {
	t.Helper()
	dir := t.TempDir()
	w := &health2027World{dir: dir, state: `{"hostId":"own"}`}
	// The sweep reads its notification binding from the process's ambient
	// worktree: run from a plain directory, not this repo (whose own live
	// binding would otherwise leak into every test) and not the fixture
	// worktree (only the posting test binds there).
	ambient := filepath.Join(dir, "ambient")
	if err := os.MkdirAll(ambient, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(ambient)
	w.worktree = filepath.Join(dir, "wt-root", "wt")
	if err := os.MkdirAll(w.worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("git", "init", "-q", w.worktree).Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}

	// The fake orca: list reads a JSON file, read answers a per-handle screen
	// file, close appends the handle to a record, rename answers ok. The N3
	// recheck variants — a later listing, a second screen per handle, a
	// failing recheck list — are opt-in per test through the files the
	// counters branch on. One sweep Lists three times (the #2017 title pass,
	// the sweep's own first listing, then the recheck), so the variants arm
	// from the THIRD list; the title pass reads no screens, so the recheck
	// stays the SECOND read of a handle.
	listPath := filepath.Join(dir, "orca-list.json")
	w.list2Path = filepath.Join(dir, "orca-list-2.json")
	w.failList2 = filepath.Join(dir, "orca-list-2.fail")
	counts := filepath.Join(dir, "call-counts")
	w.orcaCloses = filepath.Join(dir, "orca-closes.txt")
	screens := filepath.Join(dir, "screens")
	for _, d := range []string{counts, screens} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	orcaBin := filepath.Join(dir, "fake-orca")
	incr := filepath.Join(dir, "incr")
	if err := os.WriteFile(incr, []byte("#!/bin/sh\nf=\"$1\"; n=0; [ -f \"$f\" ] && n=$(cat \"$f\"); n=$((n+1)); echo $n > \"$f\"; echo $n\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\ncase \"$1 $2\" in\n" +
		"  \"terminal list\")\n" +
		"    n=$(\"" + incr + "\" \"" + counts + "/list\")\n" +
		"    if [ \"$n\" -ge 3 ] && [ -f \"" + w.failList2 + "\" ]; then exit 1; fi\n" +
		"    if [ \"$n\" -ge 3 ] && [ -f \"" + w.list2Path + "\" ]; then cat \"" + w.list2Path + "\"; else cat \"" + listPath + "\"; fi;;\n" +
		"  \"terminal read\")\n" +
		"    n=$(\"" + incr + "\" \"" + counts + "/read-$4\")\n" +
		"    f=\"" + screens + "/$4.json\"\n" +
		"    if [ \"$n\" -ge 2 ] && [ -f \"" + screens + "/$4.json.2\" ]; then f=\"" + screens + "/$4.json.2\"; fi\n" +
		"    printf '%s' '{\"ok\":true,\"result\":{\"terminal\":{\"source\":\"screen\",\"tail\":'; cat \"$f\"; printf '%s' ',\"draft\":null}}}';;\n" +
		"  \"terminal close\") echo \"$4\" >> \"" + w.orcaCloses + "\"; printf '%s' '{\"ok\":true,\"result\":{}}';;\n" +
		"  \"terminal rename\") printf '%s' '{\"ok\":true,\"result\":{}}';;\n" +
		"esac\n"
	if err := os.WriteFile(orcaBin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	// A single agent tab with chrome on it: the default, "clean but normal".
	w.setTerminals(t, []health2027Term{{Handle: "term_agent", Tab: "tab-agent", Screen: []string{"✻ Compiling…", "  go build ./..."}}})

	key := "SYNTHETIC_HOST_KEY_2027"
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		var q struct {
			OperationName string         `json:"operationName"`
			Variables     map[string]any `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&q)
		rw.Header().Set("Content-Type", "application/json")
		switch q.OperationName {
		case "HostRegistrations":
			_, _ = io.WriteString(rw, `{"data":{"hostRegistrations":[{"hostId":"own","live":true,"workers":[{"workerId":"w1","worker":{"id":"w1","name":"Iris","urn":"hrn:worker:test:team:iris"},"worktrees":["wt"]}]}]}}`)
		case "HealthSweepSetTerminals":
			_, _ = io.WriteString(rw, health2027SetTerminals)
		case "HealthSweepPause":
			if w.midSweepReset != "" {
				if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(w.midSweepReset), 0o600); err != nil {
					t.Errorf("mid-sweep reset write: %v", err)
				}
			}
			if w.pause == "" {
				_, _ = io.WriteString(rw, `{"data":{"workers":{"items":[{"id":"w1","name":"Iris","checkStatus":null}],"total":1}}}`)
				return
			}
			_, _ = io.WriteString(rw, w.pause)
		case "ContextReset":
			if w.resetsByID != nil {
				body, ok := w.resetsByID[fmt.Sprint(q.Variables["resetId"])]
				if !ok {
					_, _ = io.WriteString(rw, `{"data":{"contextReset":null}}`)
					return
				}
				_, _ = io.WriteString(rw, body)
				return
			}
			if w.reset == "" {
				_, _ = io.WriteString(rw, `{"data":{"contextReset":null}}`)
				return
			}
			_, _ = io.WriteString(rw, w.reset)
		case "CreateTeamChatMessage":
			w.mu.Lock()
			w.chatBodies = append(w.chatBodies, fmt.Sprint(q.Variables["body"]))
			if ref, ok := q.Variables["sessionRef"].(string); ok {
				v := ref
				w.chatRefs = append(w.chatRefs, &v)
			} else {
				w.chatRefs = append(w.chatRefs, nil)
			}
			w.mu.Unlock()
			_, _ = io.WriteString(rw, `{"data":{"createTeamChatMessage":{"seq":7}}}`)
		default:
			t.Errorf("unexpected operation %s", q.OperationName)
		}
	}))
	t.Cleanup(srv.Close)
	w.srv = srv
	w.writeBinding(t, "sess-1")

	w.agentTerms = filepath.Join(dir, "agent-terms.txt")
	if err := os.WriteFile(w.agentTerms, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	w.stateFileWrite(t, w.state)

	cfg := fmt.Sprintf("server: %q\napp: hrn:app:test:team\nkey_file: key\nstate_file: state.json\nworktrees_root: %q\nworkers:\n  w1:\n    family: codex\n    launch: [fake]\n    worktrees: [wt]\ndelivery:\n  orca_bin: %q\n", srv.URL, filepath.Join(dir, "wt-root"), orcaBin)
	path := filepath.Join(dir, "host.yaml")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "key"), []byte(key), 0o600); err != nil {
		t.Fatal(err)
	}
	w.cfgPath = path
	return w
}

type health2027Term struct {
	Handle string
	Tab    string
	Screen []string
}

// setTerminals rewrites the fake orca's terminal list and per-handle screens.
func (w *health2027World) setTerminals(t *testing.T, terms []health2027Term) {
	t.Helper()
	w.writeList(t, filepath.Join(w.dir, "orca-list.json"), terms)
	for _, tm := range terms {
		tail, _ := json.Marshal(tm.Screen)
		if err := os.WriteFile(filepath.Join(w.dir, "screens", tm.Handle+".json"), tail, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// writeList renders terms as one `terminal list --json` answer body at path.
// Every row's title is the worker's name ("Iris"): the merged #2017 title
// pass then finds nothing to rename, so its lines stay out of these tests'
// receipts (the title pass has its own tests).
func (w *health2027World) writeList(t *testing.T, path string, terms []health2027Term) {
	t.Helper()
	rows := make([]string, 0, len(terms))
	for _, tm := range terms {
		rows = append(rows, fmt.Sprintf(`{"handle":%q,"worktreePath":%q,"tabId":%q,"title":"Iris","connected":true,"writable":true}`, tm.Handle, w.worktree, tm.Tab))
	}
	list := `{"ok":true,"result":{"terminals":[` + strings.Join(rows, ",") + `]}}`
	if err := os.WriteFile(path, []byte(list), 0o600); err != nil {
		t.Fatal(err)
	}
}

// setRecheckList makes every `terminal list` call from the RECHECK on answer
// terms instead (#2037 N3): the sweep's pre-close recheck re-lists, and the
// world it then sees is this one. A handle with no screen file yet gets one,
// so the recheck can read it.
func (w *health2027World) setRecheckList(t *testing.T, terms []health2027Term) {
	t.Helper()
	w.writeList(t, w.list2Path, terms)
	for _, tm := range terms {
		if _, err := os.Stat(filepath.Join(w.dir, "screens", tm.Handle+".json")); err != nil {
			tail, _ := json.Marshal(tm.Screen)
			if err := os.WriteFile(filepath.Join(w.dir, "screens", tm.Handle+".json"), tail, 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// failRecheckList makes the recheck's `terminal list` call fail (#2037 N3):
// the recheck cannot verify the tab and must decline.
func (w *health2027World) failRecheckList(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(w.failList2, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
}

// flipRecheckScreen makes the SECOND `terminal read` of handle answer screen
// instead (#2037 N3): the tab classified bare on the first read and turns
// before the close.
func (w *health2027World) flipRecheckScreen(t *testing.T, handle string, screen []string) {
	t.Helper()
	tail, _ := json.Marshal(screen)
	if err := os.WriteFile(filepath.Join(w.dir, "screens", handle+".json.2"), tail, 0o600); err != nil {
		t.Fatal(err)
	}
}

func (w *health2027World) writeBinding(t *testing.T, session string) {
	t.Helper()
	b := fmt.Sprintf(`{"sessionId":%q,"workerId":"w1","workerName":"Iris","appId":"hrn:app:test:team","server":%q}`, session, w.serverURL())
	if err := os.WriteFile(filepath.Join(w.worktree, ".git", "hadron-team-session.json"), []byte(b), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (w *health2027World) stateFileWrite(t *testing.T, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(w.dir, "state.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// spawnAgent starts a sleeping process named claude or codex with its cwd in
// the worktree; supervised runs it under a worker-supervise wrapper.
func (w *health2027World) spawnAgent(t *testing.T, name string, supervised bool) int {
	t.Helper()
	bin := filepath.Join(w.dir, "bin", name)
	script := "#!/bin/sh\ntrap 'echo \"term " + name + "\" >> \"" + w.agentTerms + "\"; exit 0' TERM INT\nwhile :; do sleep 1; done\n"
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	var c *exec.Cmd
	if supervised {
		wrap := filepath.Join(w.dir, "bin", "worker-supervise")
		if _, err := os.Stat(wrap); err != nil {
			if err := os.WriteFile(wrap, []byte("#!/bin/sh\n\"$1\" &\nwait\n"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		c = exec.Command(wrap, bin)
	} else {
		c = exec.Command(bin)
	}
	c.Dir = w.worktree
	startFixture2078(t, c)
	w.agents = append(w.agents, c)
	time.Sleep(50 * time.Millisecond) // let /proc settle
	return c.Process.Pid
}

// run executes the sweep with the given extra args and returns the parsed
// --json receipt (json mode unless text is asked for).
func (w *health2027World) run(t *testing.T, text bool, args ...string) (health2027DTO, string, error) {
	t.Helper()
	if w.unbound {
		if err := os.Remove(filepath.Join(w.worktree, ".git", "hadron-team-session.json")); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HADRON_TOKEN", "AMBIENT_TOKEN_2027")
	t.Setenv("HADRON_SERVER", w.serverURL())
	f := cmdutil.NewFactory()
	streams, out, errOut := output.Test()
	f.IOStreams = streams
	f.JSON = !text
	c := newCmdTeamHealthWithReader(f, func(context.Context, string, *hostpoller.Config) ([]byte, int, error) {
		return []byte(health2027Status), 0, nil
	})
	c.SetArgs(append([]string{"sweep", "--config", w.cfgPath}, args...))
	err := c.Execute()
	var dto health2027DTO
	if !text {
		_ = json.Unmarshal(out.Bytes(), &dto)
	}
	all := out.String() + errOut.String()
	if strings.Contains(all, "SYNTHETIC_HOST_KEY_2027") {
		t.Fatalf("key in sink %q", all)
	}
	return dto, out.String(), err
}

func (w *health2027World) serverURL() string { return w.srv.URL }

func (w *health2027World) terms() string {
	b, _ := os.ReadFile(w.agentTerms)
	return string(b)
}

// settle polls cond for up to 3s: a SIGTERM the sweep has sent is still
// travelling when the command returns, so a positive assertion on its record
// waits for it instead of racing it.
func (w *health2027World) settle(t *testing.T, cond func() bool) bool {
	t.Helper()
	for i := 0; i < 150; i++ {
		if cond() {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return cond()
}

// records reads the signal and close records after a quiesce, so a negative
// assertion ("nothing was touched") cannot pass by reading before a signal
// lands.
func (w *health2027World) records() (string, string) {
	time.Sleep(200 * time.Millisecond)
	return w.terms(), w.closes()
}

func (w *health2027World) closes() string {
	b, _ := os.ReadFile(w.orcaCloses)
	return string(b)
}

func (w *health2027World) alive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

func (w *health2027World) chatPosts() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.chatBodies)
}

func health2027Find[T any](xs []T, f func(T) bool) *T {
	for i := range xs {
		if f(xs[i]) {
			return &xs[i]
		}
	}
	return nil
}

// Acceptance: "Two agents in one worktree: after one applying sweep, exactly
// one (the supervised one) remains, and its session binding is unchanged."
func TestHealth2027SupervisedDuplicateIsStopped(t *testing.T) {
	w := health2027Setup(t)
	before, _ := os.ReadFile(filepath.Join(w.worktree, ".git", "hadron-team-session.json"))
	w.spawnAgent(t, "codex", true)
	w.spawnAgent(t, "claude", false)
	dto, _, err := w.run(t, false, "--dry-run=false")
	if err != nil {
		t.Fatalf("sweep failed: %v", err)
	}
	fix := health2027Find(dto.Fixes, func(f health2027Fix) bool { return f.Action == "stop-agent" })
	if fix == nil || !fix.Applied || !strings.Contains(fix.Subject, "claude") {
		t.Fatalf("no applied stop-agent fix for the claude duplicate: %+v", dto.Fixes)
	}
	if !w.settle(t, func() bool { return strings.Contains(w.terms(), "term claude") }) {
		t.Fatalf("the claude duplicate was not signalled: %q", w.terms())
	}
	if strings.Contains(w.terms(), "term codex") {
		t.Fatalf("the supervised agent was signalled: %q", w.terms())
	}
	for _, c := range w.agents {
		if filepath.Base(c.Path) == "worker-supervise" {
			if !w.alive(c.Process.Pid) {
				t.Fatal("the wrapper is gone; the stop reached the supervised tree")
			}
		}
	}
	after, _ := os.ReadFile(filepath.Join(w.worktree, ".git", "hadron-team-session.json"))
	if string(before) != string(after) {
		t.Fatalf("session binding changed:\n%s\n%s", before, after)
	}
}

// spawnChain starts the layered launch of one logical agent (#2027 review
// P1): a worker-supervise wrapper whose shim runs the real (trapped) agent as
// a CHILD — the shape that used to count twice, dooming the real agent. The
// shim and the child both record their names when signalled, so "the chain
// was left alone" is observable member by member.
func (w *health2027World) spawnChain(t *testing.T) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(w.dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(w.dir, "bin", "claude-child")
	if err := os.WriteFile(child, []byte("#!/bin/sh\ntrap 'echo \"term chain-child\" >> \""+w.agentTerms+"\"; exit 0' TERM INT\nwhile :; do sleep 1; done\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(w.dir, "bin", "claude")
	if err := os.WriteFile(shim, []byte("#!/bin/sh\ntrap 'echo \"term chain-shim\" >> \""+w.agentTerms+"\"; exit 0' TERM INT\n\""+child+"\" &\nwait\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	wrap := filepath.Join(w.dir, "bin", "worker-supervise")
	if _, err := os.Stat(wrap); err != nil {
		if err := os.WriteFile(wrap, []byte("#!/bin/sh\n\"$1\" &\nwait\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	c := exec.Command(wrap, shim)
	c.Dir = w.worktree
	startFixture2078(t, c)
	w.agents = append(w.agents, c)
	time.Sleep(50 * time.Millisecond) // let /proc settle
}

// #2027 review P1: one logical agent running as a CHAIN (wrapper → shim →
// child) beside a typed stray is ONE supervised agent plus one duplicate —
// the sweep stops the stray and lays a finger on no member of the chain.
// The pre-fix census read this as two agents with exactly one supervised and
// stopped the chain's real agent.
func TestHealth2027LayeredChainIsOneAgentAndTheStrayIsStopped(t *testing.T) {
	w := health2027Setup(t)
	w.spawnChain(t)
	w.spawnAgent(t, "codex", false)
	dto, _, err := w.run(t, false, "--dry-run=false")
	if err != nil {
		t.Fatalf("sweep failed: %v", err)
	}
	fix := health2027Find(dto.Fixes, func(f health2027Fix) bool { return f.Action == "stop-agent" })
	if fix == nil || !fix.Applied || !strings.Contains(fix.Subject, "codex") {
		t.Fatalf("the stray, and only the stray, must be the stop subject: %+v", dto.Fixes)
	}
	if !w.settle(t, func() bool { return strings.Contains(w.terms(), "term codex") }) {
		t.Fatalf("the stray was not signalled: %q", w.terms())
	}
	terms, _ := w.records()
	if strings.Contains(terms, "chain-shim") || strings.Contains(terms, "chain-child") {
		t.Fatalf("a member of the supervised chain was signalled: %q", terms)
	}
}

// #2027 review: the pidfd identity re-verification. A census decision is a
// (pid, start-time) pair; a stop whose process no longer matches the pair is
// refused, so a reused pid is skipped, not killed.
func TestHealth2027StopRefusedWhenIdentityDoesNotMatch(t *testing.T) {
	w := health2027Setup(t)
	sleeper := exec.Command("sh", "-c", "while :; do sleep 1; done")
	sleeper.Dir = w.worktree
	startFixture2078(t, sleeper)
	time.Sleep(50 * time.Millisecond)
	if err := stopAgent(sleeper.Process.Pid, 1); err == nil {
		t.Fatal("a start-time mismatch must refuse the stop")
	}
	if !w.alive(sleeper.Process.Pid) {
		t.Fatal("a refused stop must leave the process alive")
	}
}

// Acceptance: "Two unsupervised agents: nothing stopped, one alert."
func TestHealth2027TwoUnsupervisedAgentsAlertOnly(t *testing.T) {
	w := health2027Setup(t)
	w.spawnAgent(t, "codex", false)
	w.spawnAgent(t, "claude", false)
	dto, _, err := w.run(t, false, "--dry-run=false")
	if err != nil {
		t.Fatalf("sweep failed: %v", err)
	}
	if len(dto.Fixes) != 0 {
		t.Fatalf("unsupervised duplicates were acted on: %+v", dto.Fixes)
	}
	alert := health2027Find(dto.Alerts, func(a health2027Alert) bool { return a.Code == "unsupervised-agents" })
	if alert == nil || alert.Count != 2 || alert.Worker != "w1" {
		t.Fatalf("missing one unsupervised-agents alert: %+v", dto.Alerts)
	}
	if terms, _ := w.records(); terms != "" {
		t.Fatalf("something was signalled: %q", terms)
	}
	if dto.DryRun {
		t.Fatal("an applying sweep reported dryRun")
	}
}

// Guard: never stop the only agent.
func TestHealth2027OnlyAgentNeverStopped(t *testing.T) {
	w := health2027Setup(t)
	pid := w.spawnAgent(t, "codex", false)
	dto, _, err := w.run(t, false, "--dry-run=false")
	if err != nil {
		t.Fatalf("sweep failed: %v", err)
	}
	if len(dto.Fixes) != 0 || len(dto.Alerts) != 0 {
		t.Fatalf("a lone agent produced output: %+v %+v", dto.Fixes, dto.Alerts)
	}
	if terms, _ := w.records(); !w.alive(pid) || terms != "" {
		t.Fatalf("the only agent was touched: %q", terms)
	}
}

// Guard: never touch a worktree whose worker has no binding.
func TestHealth2027UnboundWorktreeUntouched(t *testing.T) {
	w := health2027Setup(t)
	w.spawnAgent(t, "codex", true)
	w.spawnAgent(t, "claude", false)
	w.unbound = true
	dto, _, err := w.run(t, false, "--dry-run=false")
	if err != nil {
		t.Fatalf("sweep failed: %v", err)
	}
	if len(dto.Fixes) != 0 || len(dto.Alerts) != 0 {
		t.Fatalf("an unbound worktree was acted on: %+v %+v", dto.Fixes, dto.Alerts)
	}
	if terms, closes := w.records(); terms != "" || closes != "" {
		t.Fatalf("an unbound worktree was touched: %q %q", terms, closes)
	}
}

// Acceptance: "Extra bare-shell tabs are closed; a tab running an agent is
// never closed."
func TestHealth2027BareShellTabClosedAgentTabKept(t *testing.T) {
	w := health2027Setup(t)
	w.setTerminals(t, []health2027Term{
		{Handle: "term_agent", Tab: "tab-agent", Screen: []string{"✻ Compiling…", "  go build ./..."}},
		{Handle: "term_bare", Tab: "tab-bare", Screen: []string{"orca@orca1:~$"}},
	})
	dto, _, err := w.run(t, false, "--dry-run=false")
	if err != nil {
		t.Fatalf("sweep failed: %v", err)
	}
	fix := health2027Find(dto.Fixes, func(f health2027Fix) bool { return f.Action == "close-tab" })
	if fix == nil || !fix.Applied || !strings.Contains(fix.Subject, "tab-bare") {
		t.Fatalf("no applied close-tab fix for the bare shell: %+v", dto.Fixes)
	}
	if !w.settle(t, func() bool { return strings.Contains(w.closes(), "term_bare") }) {
		t.Fatalf("the bare tab was not closed: %q", w.closes())
	}
	if strings.Contains(w.closes(), "term_agent") {
		t.Fatalf("the agent tab was closed: %q", w.closes())
	}
}

// Guard: never run on a tab that is mid-turn.
func TestHealth2027MidTurnTabLeftForNextSweep(t *testing.T) {
	w := health2027Setup(t)
	w.setTerminals(t, []health2027Term{
		{Handle: "term_agent", Tab: "tab-agent", Screen: []string{"✻ Compiling…", "  go build ./..."}},
		{Handle: "term_busy", Tab: "tab-busy", Screen: []string{"running tests…", "  ok  internal/cmd/team"}},
	})
	dto, _, err := w.run(t, false, "--dry-run=false")
	if err != nil {
		t.Fatalf("sweep failed: %v", err)
	}
	if _, closes := w.records(); len(dto.Fixes) != 0 || closes != "" {
		t.Fatalf("a mid-turn tab was closed: %+v %q", dto.Fixes, closes)
	}
}

// #2037 N3: a tab that classified bare on the sweep's first read and turns
// BEFORE the close must be declined by the read-adjacent recheck — and the
// decline must leave its skip row in the receipt, not a silent continue.
func TestHealth2027FlippedScreenDeclinesCloseAndReportsIt(t *testing.T) {
	w := health2027Setup(t)
	w.setTerminals(t, []health2027Term{
		{Handle: "term_agent", Tab: "tab-agent", Screen: []string{"✻ Compiling…", "  go build ./..."}},
		{Handle: "term_bare", Tab: "tab-bare", Screen: []string{"orca@orca1:~$"}},
	})
	w.flipRecheckScreen(t, "term_bare", []string{"running tests…", "  ok  internal/cmd/team"})
	dto, _, err := w.run(t, false, "--dry-run=false")
	if err != nil {
		t.Fatalf("sweep failed: %v", err)
	}
	if fix := health2027Find(dto.Fixes, func(f health2027Fix) bool { return f.Action == "close-tab" }); fix != nil {
		t.Fatalf("a tab that turned before the close was acted on: %+v", dto.Fixes)
	}
	skip := health2027Find(dto.Skips, func(s health2027Skip) bool { return s.Code == "close-not-bare" })
	if skip == nil || skip.Worker != "w1" {
		t.Fatalf("the declined close left no trace: %+v", dto.Skips)
	}
	if _, closes := w.records(); closes != "" {
		t.Fatalf("a turned tab was closed: %q", closes)
	}
}

// #2037 N3: a pane SPLIT into the tab after the sweep's first listing must
// be read before any close — the recheck re-lists, so the added pane is part
// of the tab it judges, and a live pane declines the close visibly.
func TestHealth2027AddedPaneDeclinesCloseAndReportsIt(t *testing.T) {
	w := health2027Setup(t)
	w.setTerminals(t, []health2027Term{
		{Handle: "term_agent", Tab: "tab-agent", Screen: []string{"✻ Compiling…", "  go build ./..."}},
		{Handle: "term_bare", Tab: "tab-bare", Screen: []string{"orca@orca1:~$"}},
	})
	// The recheck's fresh listing carries a second pane in tab-bare.
	w.setRecheckList(t, []health2027Term{
		{Handle: "term_agent", Tab: "tab-agent", Screen: []string{"✻ Compiling…", "  go build ./..."}},
		{Handle: "term_bare", Tab: "tab-bare", Screen: []string{"orca@orca1:~$"}},
		{Handle: "term_split", Tab: "tab-bare", Screen: []string{"✻ Thinking…", "  reviewing the delta"}},
	})
	dto, _, err := w.run(t, false, "--dry-run=false")
	if err != nil {
		t.Fatalf("sweep failed: %v", err)
	}
	if fix := health2027Find(dto.Fixes, func(f health2027Fix) bool { return f.Action == "close-tab" }); fix != nil {
		t.Fatalf("a tab that grew a live pane was acted on: %+v", dto.Fixes)
	}
	skip := health2027Find(dto.Skips, func(s health2027Skip) bool { return s.Code == "close-not-bare" })
	if skip == nil || skip.Worker != "w1" {
		t.Fatalf("the declined close left no trace: %+v", dto.Skips)
	}
	if _, closes := w.records(); closes != "" {
		t.Fatalf("a tab with an added pane was closed: %q", closes)
	}
}

// #2037 N3: a recheck whose re-LIST fails can prove nothing — the close
// declines fail-closed, and the receipt says so instead of falling silent.
func TestHealth2027FailingRecheckListDeclinesAndReportsIt(t *testing.T) {
	w := health2027Setup(t)
	w.setTerminals(t, []health2027Term{
		{Handle: "term_agent", Tab: "tab-agent", Screen: []string{"✻ Compiling…", "  go build ./..."}},
		{Handle: "term_bare", Tab: "tab-bare", Screen: []string{"orca@orca1:~$"}},
	})
	w.failRecheckList(t)
	dto, _, err := w.run(t, false, "--dry-run=false")
	if err != nil {
		t.Fatalf("sweep failed: %v", err)
	}
	if fix := health2027Find(dto.Fixes, func(f health2027Fix) bool { return f.Action == "close-tab" }); fix != nil {
		t.Fatalf("an unverifiable tab was acted on: %+v", dto.Fixes)
	}
	skip := health2027Find(dto.Skips, func(s health2027Skip) bool { return s.Code == "close-recheck-failed" })
	if skip == nil || skip.Worker != "w1" {
		t.Fatalf("the unverifiable close left no trace: %+v", dto.Skips)
	}
	if _, closes := w.records(); closes != "" {
		t.Fatalf("an unverifiable tab was closed: %q", closes)
	}
}

// #2037 N3: a tab already gone from the recheck's fresh listing has nothing
// to close — the decline lands as a close-gone skip row, never silently.
func TestHealth2027GoneTabDeclinesAndReportsIt(t *testing.T) {
	w := health2027Setup(t)
	w.setTerminals(t, []health2027Term{
		{Handle: "term_agent", Tab: "tab-agent", Screen: []string{"✻ Compiling…", "  go build ./..."}},
		{Handle: "term_bare", Tab: "tab-bare", Screen: []string{"orca@orca1:~$"}},
	})
	w.setRecheckList(t, []health2027Term{
		{Handle: "term_agent", Tab: "tab-agent", Screen: []string{"✻ Compiling…", "  go build ./..."}},
	})
	dto, _, err := w.run(t, false, "--dry-run=false")
	if err != nil {
		t.Fatalf("sweep failed: %v", err)
	}
	if fix := health2027Find(dto.Fixes, func(f health2027Fix) bool { return f.Action == "close-tab" }); fix != nil {
		t.Fatalf("a gone tab was acted on: %+v", dto.Fixes)
	}
	skip := health2027Find(dto.Skips, func(s health2027Skip) bool { return s.Code == "close-gone" })
	if skip == nil || skip.Worker != "w1" {
		t.Fatalf("the gone tab left no trace: %+v", dto.Skips)
	}
	if _, closes := w.records(); closes != "" {
		t.Fatalf("a close was attempted on a gone tab: %q", closes)
	}
}

// Gate edge 3: resetting workers are skipped and rechecked next sweep.
func TestHealth2027ResettingWorkerSkipped(t *testing.T) {
	w := health2027Setup(t)
	w.state = `{"hostId":"own","resets":{"r1":{"resetId":"r1","workerId":"w1"}}}`
	w.stateFileWrite(t, w.state)
	w.reset = `{"data":{"contextReset":{"id":"r1","endedAt":null,"phase":"claimed"}}}`
	w.spawnAgent(t, "codex", true)
	w.spawnAgent(t, "claude", false)
	dto, _, err := w.run(t, false, "--dry-run=false")
	if err != nil {
		t.Fatalf("sweep failed: %v", err)
	}
	skip := health2027Find(dto.Skips, func(s health2027Skip) bool { return s.Code == "resetting" })
	if skip == nil || skip.Worker != "w1" {
		t.Fatalf("missing resetting skip: %+v", dto.Skips)
	}
	if terms, _ := w.records(); len(dto.Fixes) != 0 || terms != "" {
		t.Fatalf("a resetting worker was acted on: %+v %q", dto.Fixes, terms)
	}
}

// Gate edge 3, #2037 N2: an in-flight reset whose id sorts BELOW an older,
// ended one must still skip the worker — resetIds carry no ordering contract,
// so "newest by string max" could read the ended one and act mid-reset.
func TestHealth2027InFlightResetBelowEndedMaxStillSkips(t *testing.T) {
	w := health2027Setup(t)
	w.state = `{"hostId":"own","resets":{"a1":{"resetId":"a1","workerId":"w1"},"zz9":{"resetId":"zz9","workerId":"w1"}}}`
	w.stateFileWrite(t, w.state)
	w.resetsByID = map[string]string{
		"a1":  `{"data":{"contextReset":{"id":"a1","endedAt":null,"phase":"claimed"}}}`,
		"zz9": `{"data":{"contextReset":{"id":"zz9","endedAt":"2026-11-14T00:00:00Z","phase":"succeeded"}}}`,
	}
	w.spawnAgent(t, "codex", true)
	w.spawnAgent(t, "claude", false)
	dto, _, err := w.run(t, false, "--dry-run=false")
	if err != nil {
		t.Fatalf("sweep failed: %v", err)
	}
	skip := health2027Find(dto.Skips, func(s health2027Skip) bool { return s.Code == "resetting" })
	if skip == nil || skip.Worker != "w1" {
		t.Fatalf("missing resetting skip: %+v", dto.Skips)
	}
	if terms, _ := w.records(); len(dto.Fixes) != 0 || terms != "" {
		t.Fatalf("acted inside a reset window: %+v %q", dto.Fixes, terms)
	}
}

// Gate edge 3, #2037 N2: a worker whose recorded resets all read ended is
// not resetting — ended resets must not wedge the skip on.
func TestHealth2027AllEndedResetsLeaveWorkerActionable(t *testing.T) {
	w := health2027Setup(t)
	w.state = `{"hostId":"own","resets":{"a1":{"resetId":"a1","workerId":"w1"},"zz9":{"resetId":"zz9","workerId":"w1"}}}`
	w.stateFileWrite(t, w.state)
	w.resetsByID = map[string]string{
		"a1":  `{"data":{"contextReset":{"id":"a1","endedAt":"2026-11-14T00:00:00Z","phase":"succeeded"}}}`,
		"zz9": `{"data":{"contextReset":{"id":"zz9","endedAt":"2026-11-14T01:00:00Z","phase":"succeeded"}}}`,
	}
	w.spawnAgent(t, "codex", true)
	w.spawnAgent(t, "claude", false)
	dto, _, err := w.run(t, false, "--dry-run=false")
	if err != nil {
		t.Fatalf("sweep failed: %v", err)
	}
	if skip := health2027Find(dto.Skips, func(s health2027Skip) bool { return s.Code == "resetting" }); skip != nil {
		t.Fatalf("ended resets still skipped the worker: %+v", dto.Skips)
	}
	if len(dto.Fixes) == 0 {
		t.Fatalf("ended resets left the duplicate unhandled: %+v", dto.Fixes)
	}
}

// Gate edge 3, #2037 N2 note: a reset recorded AFTER the sweep's initial
// state read must still skip the worker — the pause handler below rewrites
// the state file mid-sweep, and the fixing loop re-reads it, so the reset
// gate is not frozen at the sweep's start-of-run snapshot.
func TestHealth2027MidSweepResetRecordStillSkips(t *testing.T) {
	w := health2027Setup(t)
	w.spawnAgent(t, "codex", true)
	w.spawnAgent(t, "claude", false)
	w.midSweepReset = `{"hostId":"own","resets":{"r2":{"resetId":"r2","workerId":"w1"}}}`
	w.resetsByID = map[string]string{
		"r2": `{"data":{"contextReset":{"id":"r2","endedAt":null,"phase":"claimed"}}}`,
	}
	dto, _, err := w.run(t, false, "--dry-run=false")
	if err != nil {
		t.Fatalf("sweep failed: %v", err)
	}
	skip := health2027Find(dto.Skips, func(s health2027Skip) bool { return s.Code == "resetting" })
	if skip == nil || skip.Worker != "w1" {
		t.Fatalf("a mid-sweep reset record went unseen: %+v", dto.Skips)
	}
	if terms, _ := w.records(); len(dto.Fixes) != 0 || terms != "" {
		t.Fatalf("acted on a reset recorded mid-sweep: %+v %q", dto.Fixes, terms)
	}
}

// Gate edge 4: under a team pause, detect and report only.
func TestHealth2027TeamPauseReportsOnly(t *testing.T) {
	w := health2027Setup(t)
	w.pause = `{"data":{"workers":{"items":[{"id":"w1","name":"Iris","checkStatus":{"status":"PENDING","pendingReason":"TEAM_PAUSED"}}],"total":1}}}`
	w.spawnAgent(t, "codex", true)
	w.spawnAgent(t, "claude", false)
	dto, _, err := w.run(t, false, "--dry-run=false")
	if err != nil {
		t.Fatalf("sweep failed: %v", err)
	}
	fix := health2027Find(dto.Fixes, func(f health2027Fix) bool { return f.Action == "stop-agent" })
	if fix == nil || fix.Applied {
		t.Fatalf("a paused sweep must report the fix without applying it: %+v", dto.Fixes)
	}
	skip := health2027Find(dto.Skips, func(s health2027Skip) bool { return s.Code == "team-paused" })
	if skip == nil {
		t.Fatalf("missing team-paused skip: %+v", dto.Skips)
	}
	if terms, _ := w.records(); terms != "" {
		t.Fatalf("a paused sweep signalled a process: %q", terms)
	}
}

// Ask 4: manual runs change nothing without --dry-run=false.
func TestHealth2027DryRunIsTheDefault(t *testing.T) {
	w := health2027Setup(t)
	w.spawnAgent(t, "codex", true)
	w.spawnAgent(t, "claude", false)
	dto, _, err := w.run(t, false)
	if err != nil {
		t.Fatalf("sweep failed: %v", err)
	}
	if !dto.DryRun {
		t.Fatal("a manual run without --dry-run=false must report dryRun")
	}
	fix := health2027Find(dto.Fixes, func(f health2027Fix) bool { return f.Action == "stop-agent" })
	if fix == nil || fix.Applied {
		t.Fatalf("a dry run must list the fix unapplied: %+v", dto.Fixes)
	}
	if terms, _ := w.records(); terms != "" {
		t.Fatalf("a dry run signalled a process: %q", terms)
	}
	if w.chatPosts() != 0 {
		t.Fatal("a dry run posted to the chat")
	}
}

// Acceptance: "A clean worktree produces no output and no action."
func TestHealth2027CleanWorktreeSilent(t *testing.T) {
	w := health2027Setup(t)
	_, text, err := w.run(t, true)
	if err != nil {
		t.Fatalf("sweep failed: %v", err)
	}
	if text != "Iris  idle  -\n" {
		t.Fatalf("text output gained lines: %q", text)
	}
	if terms, closes := w.records(); terms != "" || closes != "" || w.chatPosts() != 0 {
		t.Fatalf("clean sweep acted: %q %q posts=%d", terms, closes, w.chatPosts())
	}
	dto2, _, err := w.run(t, false)
	if err != nil {
		t.Fatal(err)
	}
	if !dto2.DryRun || len(dto2.Fixes) != 0 || len(dto2.Alerts) != 0 || len(dto2.Skips) != 0 {
		t.Fatalf("clean sweep reported something: %+v", dto2)
	}
	if dto2.Fixes == nil || dto2.Alerts == nil || dto2.Skips == nil {
		t.Fatalf("JSON contract must carry empty arrays, not null: %+v", dto2)
	}
}

// Gate edge 2: one chat post per sweep from a bound worker session; an
// unbound scheduled sweep posts nothing.
func TestHealth2027BoundApplyingSweepPostsOneLine(t *testing.T) {
	w := health2027Setup(t)
	w.spawnAgent(t, "codex", true)
	w.spawnAgent(t, "claude", false)
	wd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(wd) })
	if err := os.Chdir(w.worktree); err != nil {
		t.Fatal(err)
	}
	dto, _, err := w.run(t, false, "--dry-run=false")
	if err != nil {
		t.Fatalf("sweep failed: %v", err)
	}
	if w.chatPosts() != 1 {
		t.Fatalf("want exactly one chat post, got %d", w.chatPosts())
	}
	w.mu.Lock()
	body, ref := "", (*string)(nil)
	if len(w.chatBodies) == 1 {
		body = w.chatBodies[0]
	}
	if len(w.chatRefs) == 1 {
		ref = w.chatRefs[0]
	}
	w.mu.Unlock()
	if ref == nil || *ref != "sess-1" {
		t.Fatalf("post not attributed to the bound session: %v", ref)
	}
	if !strings.Contains(body, "Iris") || !strings.Contains(body, "stop") {
		t.Fatalf("post does not list the fix: %q", body)
	}
	_ = dto
}

// The scheduled entry applies: the installed command carries --dry-run=false.
func TestHealth2027InstallEntryApplies(t *testing.T) {
	orcaOnPath(t)
	_, run, home := healthHarness(t, "systemctl")
	cfg := hostConfig(t, "")
	if _, _, err := run("install", "--config", cfg, "--interval", "5m", "--bin", "/usr/local/bin/hadron"); err != nil {
		t.Fatal(err)
	}
	unit, err := os.ReadFile(filepath.Join(home, ".config/systemd/user/hadron-team-health.service"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(unit), `"--dry-run=false"`) {
		t.Fatalf("timer entry does not apply: %s", unit)
	}
}
