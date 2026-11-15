//go:build linux

package hostpoller

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// cli#2058 C3: a launch through Orca. The fake Orca below does what Orca's
// terminal does with --command: it types the text into a login shell (here,
// runs it with bash in the worktree) and reports that shell's pid as the
// terminal's processId.

type fakeOrca struct {
	t        *testing.T
	mu       sync.Mutex
	typed    []string // every --command text
	closed   []string // every closed handle
	procs    map[string]*exec.Cmd
	pidFor   func(real int) int // what create reports as processId (default: the real pid)
	runNoCmd bool               // create starts nothing (no marker will appear)
}

func newFakeOrca(t *testing.T) *fakeOrca {
	f := &fakeOrca{t: t, procs: map[string]*exec.Cmd{}}
	t.Cleanup(func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, c := range f.procs {
			if c.Process != nil {
				_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
				_, _ = c.Process.Wait()
			}
		}
	})
	return f
}

func (f *fakeOrca) run(_ context.Context, argv []string) (int, string, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(argv) < 2 {
		return 2, "", "bad call"
	}
	opt := func(name string) string {
		for i := 0; i+1 < len(argv); i++ {
			if argv[i] == name {
				return argv[i+1]
			}
		}
		return ""
	}
	switch argv[0] + " " + argv[1] {
	case "terminal create":
		cmdText, dir := opt("--command"), strings.TrimPrefix(opt("--worktree"), "path:")
		f.typed = append(f.typed, cmdText)
		script := cmdText
		if f.runNoCmd {
			script = "exec sleep 60"
		}
		c := exec.Command("bash", "--norc", "--noprofile", "-c", script)
		c.Dir = dir
		c.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME")}
		c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := c.Start(); err != nil {
			return 1, "", err.Error()
		}
		go func() { _ = c.Wait() }()
		pid := c.Process.Pid
		if f.pidFor != nil {
			pid = f.pidFor(pid)
		}
		handle := fmt.Sprintf("term_%d", c.Process.Pid)
		f.procs[handle] = c
		b, _ := json.Marshal(map[string]any{"ok": true, "result": map[string]any{"terminal": map[string]any{"handle": handle, "processId": pid}}})
		return 0, string(b), ""
	case "terminal close":
		h := opt("--terminal")
		f.closed = append(f.closed, h)
		if c, ok := f.procs[h]; ok && c.Process != nil {
			_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
		}
		return 0, `{"ok":true}`, ""
	}
	return 1, "", "refused"
}

// fakeAgent writes its argv (one per line) and selected env to out, then
// sleeps or exits.
func fakeAgentScript(t *testing.T, dir, out string, sleep bool) string {
	t.Helper()
	tail := "exit 0"
	if sleep {
		tail = "exec sleep 120"
	}
	s := "#!/bin/bash\n{ for a in \"$@\"; do printf 'ARG<%s>\\n' \"$a\"; done; printf 'GRANT<%s>\\nTMPDIR<%s>\\nCODEX_HOME<%s>\\n' \"$HADRON_LAUNCH_GRANT\" \"$TMPDIR\" \"$CODEX_HOME\"; } > " + shq(out) + "\n" + tail + "\n"
	p := filepath.Join(dir, "fake-agent")
	if err := os.WriteFile(p, []byte(s), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func orcaLauncherFor(t *testing.T, f *fakeOrca) OrcaLauncher {
	// No namespace by default here: CI runners may forbid unprivileged ones.
	// TestOrcaLaunchNamespaceIsolatesAgents2058 runs with it.
	return OrcaLauncher{Run: f.run, StateDir: t.TempDir(), Proc: LinuxProcessControl{}, MarkerWait: 3 * time.Second, StopWait: 3 * time.Second, noNamespace: true}
}

func waitFile(t *testing.T, path string) string {
	t.Helper()
	for i := 0; i < 100; i++ {
		if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
			return string(b)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s never appeared", path)
	return ""
}

// The typed command holds neither the grant nor the prompt; the prompt, with
// quotes, a newline and a command substitution, reaches the agent as one
// argument, intact and never executed; the grant reaches its environment; the
// environment file and marker are gone; the agent is the terminal's process.
func TestOrcaLaunchTypesNoSecretAndPassesThePromptIntact2058(t *testing.T) {
	wt, tools := t.TempDir(), t.TempDir()
	out := filepath.Join(tools, "agent.out")
	pwned := filepath.Join(tools, "PWNED")
	agentBin := fakeAgentScript(t, tools, out, true)
	f := newFakeOrca(t)
	l := orcaLauncherFor(t, f)
	envGoneAtMarker := false
	l.onMarker = func(envFile string) {
		_, err := os.Stat(envFile)
		envGoneAtMarker = os.IsNotExist(err)
	}
	prompt := "it's \"quoted\" $(touch " + pwned + ") `touch " + pwned + "`\nsecond line"
	a, err := l.Launch(context.Background(), OrcaLaunchSpec{
		Argv: []string{agentBin, "--dangerously-skip-permissions", PromptArg}, Dir: wt,
		Env:    []string{"HADRON_LAUNCH_GRANT=grant-SECRET-123", "TMPDIR=/disk/tmp", "CODEX_HOME=/acct/home"},
		Prompt: prompt, Title: "iris",
	})
	if err != nil {
		t.Fatal(err)
	}
	got := waitFile(t, out)
	if !strings.Contains(got, "ARG<--dangerously-skip-permissions>\nARG<"+prompt+">\n") {
		t.Fatalf("the agent's argv is not the profile's with the prompt last:\n%s", got)
	}
	for _, want := range []string{"GRANT<grant-SECRET-123>", "TMPDIR</disk/tmp>", "CODEX_HOME</acct/home>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("agent env lacks %s:\n%s", want, got)
		}
	}
	if _, err := os.Stat(pwned); err == nil {
		t.Fatal("the prompt was executed by the shell")
	}
	typed := strings.Join(f.typed, "\n")
	for _, secret := range []string{"grant-SECRET-123", "quoted", "second line", "/disk/tmp", "/acct/home"} {
		if strings.Contains(typed, secret) {
			t.Fatalf("the typed command carries %q:\n%s", secret, typed)
		}
	}
	// The command itself removed the grant's file before it wrote the marker,
	// not only the launcher's cleanup afterwards (Wolfgang (c), #108152).
	if !envGoneAtMarker {
		t.Fatal("the environment file still existed when the launched command wrote its marker")
	}
	if ents, _ := os.ReadDir(filepath.Join(l.StateDir, "launch")); len(ents) != 0 {
		t.Fatalf("launch files left behind: %v", ents)
	}
	handle, pid, _ := a.Terminal()
	if pid != a.PID() || handle != fmt.Sprintf("term_%d", pid) {
		t.Fatalf("agent pid %d / handle %s is not the terminal's process", a.PID(), handle)
	}
	select {
	case <-a.Done():
		t.Fatal("Done closed while the agent runs")
	case <-time.After(700 * time.Millisecond):
	}
}

// A stop is authorized first, closes the terminal, and is confirmed only when
// the pinned process is gone; a declined authorization closes nothing.
func TestOrcaAgentStopIsAuthorizedAndConfirmed2058(t *testing.T) {
	wt, tools := t.TempDir(), t.TempDir()
	agentBin := fakeAgentScript(t, tools, filepath.Join(tools, "agent.out"), true)
	f := newFakeOrca(t)
	l := orcaLauncherFor(t, f)
	a, err := l.Launch(context.Background(), OrcaLaunchSpec{Argv: []string{agentBin, PromptArg}, Dir: wt, Prompt: "go"})
	if err != nil {
		t.Fatal(err)
	}
	stopped, detail := a.TerminateWith(func() (func(), bool) { return nil, false })
	if stopped || len(f.closed) != 0 || !strings.Contains(detail, "declined") {
		t.Fatalf("a declined stop: stopped=%v closed=%v %q", stopped, f.closed, detail)
	}
	released := false
	stopped, detail = a.TerminateWith(func() (func(), bool) { return func() { released = true }, true })
	if !stopped || len(f.closed) != 1 || !released {
		t.Fatalf("an authorized stop: stopped=%v closed=%v released=%v %q", stopped, f.closed, released, detail)
	}
	select {
	case <-a.Done():
	default:
		t.Fatal("Done is not closed after a confirmed stop")
	}
}

// An agent that finishes on its own (a short-lived run) closes Done.
func TestOrcaAgentDoneWhenItExits2058(t *testing.T) {
	wt, tools := t.TempDir(), t.TempDir()
	agentBin := fakeAgentScript(t, tools, filepath.Join(tools, "agent.out"), false)
	f := newFakeOrca(t)
	a, err := orcaLauncherFor(t, f).Launch(context.Background(), OrcaLaunchSpec{Argv: []string{agentBin, PromptArg}, Dir: wt, Prompt: "go"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-a.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Done never closed for an agent that exited")
	}
}

// A process that is not the terminal's, or no marker at all, is refused, and
// the terminal this launch created is closed: nothing is left unrecorded.
func TestOrcaLaunchRefusesAnUnprovenProcess2058(t *testing.T) {
	wt, tools := t.TempDir(), t.TempDir()
	agentBin := fakeAgentScript(t, tools, filepath.Join(tools, "agent.out"), true)
	other := exec.Command("sleep", "60")
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Process.Kill(); _, _ = other.Process.Wait() })

	f := newFakeOrca(t)
	f.pidFor = func(int) int { return other.Process.Pid }
	if _, err := orcaLauncherFor(t, f).Launch(context.Background(), OrcaLaunchSpec{Argv: []string{agentBin, PromptArg}, Dir: wt, Prompt: "go"}); err == nil || !strings.Contains(err.Error(), "not the one Orca's terminal runs") || len(f.closed) != 1 {
		t.Fatalf("a foreign processId: err=%v closed=%v", err, f.closed)
	}

	f = newFakeOrca(t)
	f.runNoCmd = true
	l := orcaLauncherFor(t, f)
	l.MarkerWait = 500 * time.Millisecond
	if _, err := l.Launch(context.Background(), OrcaLaunchSpec{Argv: []string{agentBin, PromptArg}, Dir: wt, Prompt: "go"}); err == nil || !strings.Contains(err.Error(), "no marker") || len(f.closed) != 1 {
		t.Fatalf("no marker: err=%v closed=%v", err, f.closed)
	}
}

// A process outside the worktree is refused.
func TestOrcaLaunchRefusesAProcessOutsideTheWorktree2058(t *testing.T) {
	wt, elsewhere, tools := t.TempDir(), t.TempDir(), t.TempDir()
	agentBin := fakeAgentScript(t, tools, filepath.Join(tools, "agent.out"), true)
	f := newFakeOrca(t)
	// Orca says the terminal is in wt, but the shell runs elsewhere.
	run := f.run
	f2 := func(ctx context.Context, argv []string) (int, string, string) {
		for i := range argv {
			if strings.HasPrefix(argv[i], "path:") {
				argv = append([]string(nil), argv...)
				argv[i] = "path:" + elsewhere
			}
		}
		return run(ctx, argv)
	}
	l := orcaLauncherFor(t, f)
	l.Run = f2
	if _, err := l.Launch(context.Background(), OrcaLaunchSpec{Argv: []string{agentBin, PromptArg}, Dir: wt, Prompt: "go"}); err == nil || !strings.Contains(err.Error(), "not working in the worktree") {
		t.Fatalf("err=%v", err)
	}
}

func TestLaunchCommandAndEnvGuards2058(t *testing.T) {
	cmd, err := launchCommand("/s/a.env", "/s/a.marker", []string{"codex", "exec", PromptArg}, "incus", nil)
	if err != nil || !strings.HasPrefix(cmd, "exec sg 'incus' -c '") || strings.Contains(cmd, "\n") {
		t.Fatalf("group command %q %v", cmd, err)
	}
	if _, err := launchCommand("/s/a.env", "/s/a.marker", []string{"codex", "a\nb"}, "", nil); err == nil {
		t.Fatal("an argv word with a newline was accepted")
	}
	dir := t.TempDir()
	for name, env := range map[string][]string{
		"no equals":       {"HADRON_LAUNCH_GRANT"},
		"bad name":        {"BAD-NAME=x"},
		"prompt override": {firstPromptVar + "=x"},
	} {
		if err := writeLaunchEnv(filepath.Join(dir, name+".env"), env, "p"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	p := filepath.Join(dir, "ok.env")
	if err := writeLaunchEnv(p, []string{"A=1"}, "x"); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
		t.Fatalf("env file mode %v", st.Mode().Perm())
	}
	if err := writeLaunchEnv(p, []string{"A=1"}, "x"); err == nil {
		t.Fatal("an existing env file was overwritten")
	}
}

// namespacesAvailable: unprivileged user and PID namespaces work here (CI
// runners may forbid them; the tests below then say so and skip).
func namespacesAvailable(t *testing.T) {
	t.Helper()
	if err := exec.Command(AgentNamespace[0], append(AgentNamespace[1:], "true")...).Run(); err != nil {
		t.Skipf("unprivileged user/PID namespaces are not available here: %v", err)
	}
}

// The namespace prefix sits after the group wrapper and before the argv.
func TestLaunchCommandRunsTheAgentInItsNamespace2058(t *testing.T) {
	cmd, err := launchCommand("/s/a.env", "/s/a.marker", []string{"claude-worker", PromptArg}, "", AgentNamespace)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cmd, "&& exec 'unshare' '--user' '--map-current-user' '--pid' '--fork' '--mount-proc' '--kill-child' 'claude-worker' \"$HADRON_FIRST_PROMPT\"") {
		t.Fatalf("command %q", cmd)
	}
	// The marker is written by the shell BEFORE the exec, so it records the
	// host-side pid, never the namespace's pid 1.
	if strings.Index(cmd, "printf") > strings.Index(cmd, "unshare") {
		t.Fatal("the marker is written inside the namespace")
	}
}

// Inside the namespace the agent is pid 1 and sees only its own processes;
// the launcher pins the HOST pid (the terminal's process); a pkill by one
// agent cannot reach another; closing the terminal ends the agent.
func TestOrcaLaunchNamespaceIsolatesAgents2058(t *testing.T) {
	namespacesAvailable(t)
	wt, tools := t.TempDir(), t.TempDir()
	f := newFakeOrca(t)
	l := orcaLauncherFor(t, f)
	l.noNamespace = false // the production default: AgentNamespace
	victimOut := filepath.Join(tools, "victim.out")
	victim := filepath.Join(tools, "victim")
	if err := os.WriteFile(victim, []byte("#!/bin/bash\necho \"ns-pid=$$ procs=$(ls /proc | grep -c '^[0-9]')\" > "+shq(victimOut)+"\nexec -a \"c3victim$(printf %s 9q)\" sleep 120\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	killer := filepath.Join(tools, "killer")
	killerOut := filepath.Join(tools, "killer.out")
	if err := os.WriteFile(killer, []byte("#!/bin/bash\npkill -f \"c3victim$(printf %s 9q)\"; echo \"pkill-exit=$?\" > "+shq(killerOut)+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	a, err := l.Launch(context.Background(), OrcaLaunchSpec{Argv: []string{victim, PromptArg}, Dir: wt, Prompt: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if got := waitFile(t, victimOut); !strings.HasPrefix(got, "ns-pid=1 ") {
		t.Fatalf("the agent is not pid 1 of its own namespace: %q", got)
	}
	if _, pid, _ := a.Terminal(); pid <= 1 || pid != a.PID() {
		t.Fatalf("the pinned pid %d is not a host pid", pid)
	}
	if _, err := l.Launch(context.Background(), OrcaLaunchSpec{Argv: []string{killer, PromptArg}, Dir: wt, Prompt: "go"}); err != nil {
		t.Fatal(err)
	}
	if got := waitFile(t, killerOut); !strings.Contains(got, "pkill-exit=1") {
		t.Fatalf("the other agent's pkill found something: %q", got)
	}
	select {
	case <-a.Done():
		t.Fatal("a pkill from another agent's namespace ended this agent")
	case <-time.After(700 * time.Millisecond):
	}
	if stopped, detail := a.TerminateWith(func() (func(), bool) { return func() {}, true }); !stopped {
		t.Fatalf("closing the terminal did not end the namespaced agent: %s", detail)
	}
}

// Every production launch is namespaced: the default, with no field set,
// types the AgentNamespace prefix.
func TestOrcaLaunchIsNamespacedByDefault2058(t *testing.T) {
	f := newFakeOrca(t)
	f.runNoCmd = true // nothing needs to start; the typed text is the subject
	l := OrcaLauncher{Run: f.run, StateDir: t.TempDir(), Proc: LinuxProcessControl{}, MarkerWait: 200 * time.Millisecond}
	_, _ = l.Launch(context.Background(), OrcaLaunchSpec{Argv: []string{"claude-worker", PromptArg}, Dir: t.TempDir(), Prompt: "go"})
	if len(f.typed) != 1 || !strings.Contains(f.typed[0], "exec 'unshare' '--user'") || !strings.Contains(f.typed[0], "'--kill-child'") {
		t.Fatalf("the default launch is not namespaced: %q", f.typed)
	}
}
