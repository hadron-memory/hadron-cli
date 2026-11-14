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
	DryRun  bool              `json:"dryRun"`
	Fixes   []health2027Fix   `json:"fixes"`
	Alerts  []health2027Alert `json:"alerts"`
	Skipped []health2027Skip  `json:"skipped"`
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
	// state is the poller state file's content (hostId or a resets map).
	state string
	// chatBodies records every CreateTeamChatMessage body that arrived.
	chatBodies []string
	chatRefs   []*string
	mu         sync.Mutex
	// unbound removes the worktree's session binding before the run.
	unbound bool

	orcaCloses string // file the fake orca appends closes to
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
	w.worktree = filepath.Join(dir, "wt-root", "wt")
	if err := os.MkdirAll(w.worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("git", "init", "-q", w.worktree).Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}

	// The fake orca: list reads a JSON file, read answers a per-handle screen
	// file, close appends the handle to a record.
	listPath := filepath.Join(dir, "orca-list.json")
	w.orcaCloses = filepath.Join(dir, "orca-closes.txt")
	screens := filepath.Join(dir, "screens")
	if err := os.MkdirAll(screens, 0o755); err != nil {
		t.Fatal(err)
	}
	orcaBin := filepath.Join(dir, "fake-orca")
	script := "#!/bin/sh\ncase \"$1 $2\" in\n" +
		"  \"terminal list\") cat \"" + listPath + "\";;\n" +
		"  \"terminal read\") printf '%s' '{\"ok\":true,\"result\":{\"terminal\":{\"source\":\"screen\",\"tail\":'; cat \"" + screens + "/$4.json\"; printf '%s' ',\"draft\":null}}}'};;\n" +
		"  \"terminal close\") echo \"$4\" >> \"" + w.orcaCloses + "\"; printf '%s' '{\"ok\":true,\"result\":{}}';;\n" +
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
			if w.pause == "" {
				_, _ = io.WriteString(rw, `{"data":{"workers":{"items":[{"id":"w1","name":"Iris","checkStatus":null}],"total":1}}}`)
				return
			}
			_, _ = io.WriteString(rw, w.pause)
		case "ContextReset":
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
	t.Cleanup(func() {
		for _, c := range w.agents {
			_ = c.Process.Kill()
			_, _ = c.Process.Wait()
		}
	})
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
	rows := make([]string, 0, len(terms))
	for _, tm := range terms {
		rows = append(rows, fmt.Sprintf(`{"handle":%q,"worktreePath":%q,"tabId":%q,"connected":true,"writable":true}`, tm.Handle, w.worktree, tm.Tab))
		tail, _ := json.Marshal(tm.Screen)
		if err := os.WriteFile(filepath.Join(w.dir, "screens", tm.Handle+".json"), tail, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	list := `{"ok":true,"result":{"terminals":[` + strings.Join(rows, ",") + `]}}`
	if err := os.WriteFile(filepath.Join(w.dir, "orca-list.json"), []byte(list), 0o600); err != nil {
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
	if err := c.Start(); err != nil {
		t.Fatalf("spawn %s: %v", name, err)
	}
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
		_ = json.Unmarshal([]byte(out.String()), &dto)
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
	if !strings.Contains(w.terms(), "term claude") || strings.Contains(w.terms(), "term codex") {
		t.Fatalf("wrong process signalled: %q", w.terms())
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
	if w.terms() != "" {
		t.Fatalf("something was signalled: %q", w.terms())
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
	if !w.alive(pid) || w.terms() != "" {
		t.Fatal("the only agent was touched")
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
	if w.terms() != "" || w.closes() != "" {
		t.Fatalf("an unbound worktree was touched: %q %q", w.terms(), w.closes())
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
	if strings.Contains(w.closes(), "term_agent") || !strings.Contains(w.closes(), "term_bare") {
		t.Fatalf("wrong terminal closed: %q", w.closes())
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
	if len(dto.Fixes) != 0 || w.closes() != "" {
		t.Fatalf("a mid-turn tab was closed: %+v %q", dto.Fixes, w.closes())
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
	skip := health2027Find(dto.Skipped, func(s health2027Skip) bool { return s.Code == "resetting" })
	if skip == nil || skip.Worker != "w1" {
		t.Fatalf("missing resetting skip: %+v", dto.Skipped)
	}
	if len(dto.Fixes) != 0 || w.terms() != "" {
		t.Fatalf("a resetting worker was acted on: %+v %q", dto.Fixes, w.terms())
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
	skip := health2027Find(dto.Skipped, func(s health2027Skip) bool { return s.Code == "team-paused" })
	if skip == nil {
		t.Fatalf("missing team-paused skip: %+v", dto.Skipped)
	}
	if w.terms() != "" {
		t.Fatalf("a paused sweep signalled a process: %q", w.terms())
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
	if w.terms() != "" {
		t.Fatalf("a dry run signalled a process: %q", w.terms())
	}
	if w.chatPosts() != 0 {
		t.Fatal("a dry run posted to the chat")
	}
}

// Acceptance: "A clean worktree produces no output and no action."
func TestHealth2027CleanWorktreeSilent(t *testing.T) {
	w := health2027Setup(t)
	dto, text, err := w.run(t, true)
	if err != nil {
		t.Fatalf("sweep failed: %v", err)
	}
	if text != "Iris  idle  -\n" {
		t.Fatalf("text output gained lines: %q", text)
	}
	if !dto.DryRun || len(dto.Fixes) != 0 || len(dto.Alerts) != 0 || len(dto.Skipped) != 0 {
		t.Fatalf("clean sweep reported something: %+v", dto)
	}
	if w.terms() != "" || w.closes() != "" || w.chatPosts() != 0 {
		t.Fatalf("clean sweep acted: %q %q posts=%d", w.terms(), w.closes(), w.chatPosts())
	}
	dto2, _, err := w.run(t, false)
	if err != nil {
		t.Fatal(err)
	}
	if dto2.Fixes == nil || dto2.Alerts == nil || dto2.Skipped == nil {
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
