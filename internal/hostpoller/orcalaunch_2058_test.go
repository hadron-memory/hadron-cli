//go:build linux

package hostpoller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// openPTY opens a pseudo-terminal pair: the master end, and the slave end a
// shell takes as its controlling terminal.
func openPTY() (*os.File, *os.File, error) {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, err
	}
	fd := int(master.Fd())
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		_ = master.Close()
		return nil, nil, err
	}
	n, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
	if err != nil {
		_ = master.Close()
		return nil, nil, err
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		_ = master.Close()
		return nil, nil, err
	}
	return master, slave, nil
}

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
	noPTY    bool               // the shell gets no terminal (Orca always gives one)
	ttyLie   bool               // the command reports a pts it is not on
	// markerPID, when set, is the pid the command writes instead of its own.
	markerPID string
	// badReply, when set, is how create answers AFTER it has really started
	// the command (cli#2130): "lost" (exit 1, no reply) or "malformed"
	// (exit 0, unparseable stdout). The shell has had replyDelay to run.
	badReply   string
	replyDelay time.Duration
	// beforeReply, when set, runs (unlocked) after the command has started
	// and before a good reply: a slow Orca, or a cancel landing then.
	beforeReply func()
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
		if f.markerPID != "" {
			script = strings.Replace(script, `"$$"`, f.markerPID, 1)
		}
		if f.ttyLie {
			script = strings.Replace(script, `"$(tty)"`, `/dev/pts/999999`, 1)
		}
		c := exec.Command("bash", "--norc", "--noprofile", "-c", script)
		c.Dir = dir
		c.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME")}
		// Like Orca: the shell runs on a pty of its own, as its controlling
		// terminal (cli#2088 blind P2: the launch requires the association).
		if f.noPTY {
			c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			if err := c.Start(); err != nil {
				return 1, "", err.Error()
			}
		} else {
			master, slave, err := openPTY()
			if err != nil {
				return 1, "", err.Error()
			}
			c.Stdin, c.Stdout, c.Stderr = slave, slave, slave
			c.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
			if err := c.Start(); err != nil {
				_ = master.Close()
				_ = slave.Close()
				return 1, "", err.Error()
			}
			_ = slave.Close()
			go func() { _, _ = io.Copy(io.Discard, master); _ = master.Close() }()
		}
		go func() { _ = c.Wait() }()
		pid := c.Process.Pid
		if f.pidFor != nil {
			pid = f.pidFor(pid)
		}
		handle := fmt.Sprintf("term_%d", c.Process.Pid)
		f.procs[handle] = c
		if f.badReply != "" {
			f.mu.Unlock()
			time.Sleep(f.replyDelay)
			f.mu.Lock()
			if f.badReply == "lost" {
				return 1, "", "post-create reply lost"
			}
			return 0, "{malformed create reply", ""
		}
		if f.beforeReply != nil {
			f.mu.Unlock()
			f.beforeReply()
			f.mu.Lock()
		}
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
	return OrcaLauncher{Run: f.run, StateDir: t.TempDir(), Proc: LinuxProcessControl{}, MarkerWait: 3 * time.Second, StopWait: 3 * time.Second}
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
	cmd, err := launchCommand("/s/a.env", "/s/a.marker", "/s/a.go", []string{"codex", "exec", PromptArg}, "incus")
	if err != nil || !strings.HasPrefix(cmd, "exec sg 'incus' -c '") || strings.Contains(cmd, "\n") {
		t.Fatalf("group command %q %v", cmd, err)
	}
	if _, err := launchCommand("/s/a.env", "/s/a.marker", "/s/a.go", []string{"codex", "a\nb"}, ""); err == nil {
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

// cli#2088 blind P2: a launch that could not be proven AND whose terminal
// would not close is handed back recorded and not done; a later stop retries
// the close and only then is it done.
func TestOrcaLaunchUnclosedTerminalStaysRecorded2088(t *testing.T) {
	wt, tools := t.TempDir(), t.TempDir()
	agentBin := fakeAgentScript(t, tools, filepath.Join(tools, "agent.out"), true)
	f := newFakeOrca(t)
	f.runNoCmd = true // no marker: the launch cannot be proven
	closeFails := true
	l := orcaLauncherFor(t, f)
	l.MarkerWait = 300 * time.Millisecond
	l.Run = func(ctx context.Context, argv []string) (int, string, string) {
		if len(argv) > 1 && argv[1] == "close" && closeFails {
			return 1, "", "orca timed out"
		}
		return f.run(ctx, argv)
	}
	a, err := l.Launch(context.Background(), OrcaLaunchSpec{Argv: []string{agentBin, PromptArg}, Dir: wt, Prompt: "go"})
	var unclosed *UnclosedLaunchError
	if !errors.As(err, &unclosed) || a == nil {
		t.Fatalf("a failed close must return the agent and an UnclosedLaunchError: agent=%v err=%v", a, err)
	}
	if strings.Contains(err.Error(), "the terminal was closed") || !strings.Contains(err.Error(), "stays recorded") {
		t.Fatalf("the error claims a close that failed: %v", err)
	}
	select {
	case <-a.Done():
		t.Fatal("an unclosed launch reads as done")
	default:
	}
	if stopped, _ := a.TerminateWith(nil); stopped {
		t.Fatal("a stop reported confirmed while the close still fails")
	}
	closeFails = false
	if stopped, detail := a.TerminateWith(nil); !stopped {
		t.Fatalf("the retried close did not confirm: %s", detail)
	}
	select {
	case <-a.Done():
	default:
		t.Fatal("Done is not closed after the retried close")
	}
}

// The poller keeps an unclosed launch recorded (the one-agent-per-worktree
// guard holds) while it reports the start failed.
func TestWorkerStartRecordsAnUnclosedOrcaLaunch2088(t *testing.T) {
	p, srv, host := testPoller(t)
	orcaWorker(p, filepath.Join(t.TempDir(), "disk"), LauncherProfile{Family: "claude", Mode: ModeLong, Account: "a"}, AccountProfile{Bin: "claude-holger"})
	stuck := &OrcaAgent{handle: "term_x", done: make(chan struct{}), unclosed: true}
	p.EnableOrcaLaunch(func(context.Context, OrcaLaunchSpec) (Agent, error) {
		return stuck, &UnclosedLaunchError{Handle: "term_x", Why: "no marker", CloseErr: "timeout"}
	}, ConfigProfiles{Cfg: p.cfg})
	srv.notDone["s1"] = 1
	srv.batches = [][]Operation{{startOp()}}
	step(t, p)
	if r := only(t, srv); r.outcome != Failed || !strings.Contains(r.reason, "stays recorded") {
		t.Fatalf("report %+v", r)
	}
	if !p.agentRunningIn("repo/iris") || len(host.launches) != 0 {
		t.Fatal("the unclosed launch is not recorded against its worktree")
	}
}

// cli#2088 blind P2: the pinned pidfd is released once the agent is gone.
type countingProc struct {
	LinuxProcessControl
	mu     sync.Mutex
	closes int
}

type countingHandle struct {
	ProcessHandle
	c *countingProc
}

func (h countingHandle) Close() {
	h.c.mu.Lock()
	h.c.closes++
	h.c.mu.Unlock()
	h.ProcessHandle.Close()
}

func (c *countingProc) Observe(pid int) (ProcessHandle, error) {
	h, err := c.LinuxProcessControl.Observe(pid)
	if err != nil {
		return nil, err
	}
	return countingHandle{h, c}, nil
}

func TestOrcaAgentReleasesItsPidfd2088(t *testing.T) {
	wt, tools := t.TempDir(), t.TempDir()
	out := filepath.Join(tools, "agent.out")
	bin := filepath.Join(tools, "agent")
	if err := os.WriteFile(bin, []byte("#!/bin/bash\necho started > "+shq(out)+"\nexec sleep 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	f := newFakeOrca(t)
	proc := &countingProc{}
	l := orcaLauncherFor(t, f)
	l.Proc = proc
	a, err := l.Launch(context.Background(), OrcaLaunchSpec{Argv: []string{bin, PromptArg}, Dir: wt, Prompt: "go"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-a.Done():
	case <-time.After(6 * time.Second):
		t.Fatal("the agent never finished")
	}
	proc.mu.Lock()
	defer proc.mu.Unlock()
	if proc.closes != 1 {
		t.Fatalf("pidfd closed %d times after the agent exited, want 1", proc.closes)
	}
}

// cli#2088 blind P2: the marker must name a process AND a pts terminal,
// exactly; a launch on no terminal is refused.
func TestParseMarker2088(t *testing.T) {
	if pid, tty, err := parseMarker("123 /dev/pts/4\n"); err != nil || pid != 123 || tty != "/dev/pts/4" {
		t.Fatalf("a good marker: %d %q %v", pid, tty, err)
	}
	for _, bad := range []string{"123\n", "123 not a tty\n", "123 /dev/pts/4 x\n", "123 /dev/pts/4", "0 /dev/pts/1\n", "1 /dev/pts/1\n",
		"+12 /dev/pts/1\n", "012 /dev/pts/1\n", "123 /dev/tty1\n", "123 /dev/pts/4\n456 /dev/pts/5\n", "123  /dev/pts/4\n", ""} {
		if _, _, err := parseMarker(bad); err == nil {
			t.Errorf("marker %q accepted", bad)
		}
	}
}

func TestOrcaLaunchRefusesALaunchWithNoTerminal2088(t *testing.T) {
	wt, tools := t.TempDir(), t.TempDir()
	agentBin := fakeAgentScript(t, tools, filepath.Join(tools, "agent.out"), true)
	f := newFakeOrca(t)
	f.noPTY = true
	_, err := orcaLauncherFor(t, f).Launch(context.Background(), OrcaLaunchSpec{Argv: []string{agentBin, PromptArg}, Dir: wt, Prompt: "go"})
	if err == nil || !strings.Contains(err.Error(), "no terminal") || len(f.closed) != 1 {
		t.Fatalf("a launch with no pty: err=%v closed=%v", err, f.closed)
	}
}

// The tty the marker names must be the process's own stdin: a well-formed pts
// that is not its terminal is refused (cli#2088 blind P2).
func TestOrcaLaunchRefusesATTYThatIsNotTheProcesses2088(t *testing.T) {
	wt, tools := t.TempDir(), t.TempDir()
	agentBin := fakeAgentScript(t, tools, filepath.Join(tools, "agent.out"), true)
	f := newFakeOrca(t)
	f.ttyLie = true
	_, err := orcaLauncherFor(t, f).Launch(context.Background(), OrcaLaunchSpec{Argv: []string{agentBin, PromptArg}, Dir: wt, Prompt: "go"})
	if err == nil || !strings.Contains(err.Error(), "not the tty it reported") || len(f.closed) != 1 {
		t.Fatalf("a lying tty: err=%v closed=%v", err, f.closed)
	}
}

// The authorization is held THROUGH the close, released only after it
// (Rook F1 on cli#2088): a writer must not slip in between.
func TestOrcaAgentHoldsTheAuthorizationThroughTheClose2088(t *testing.T) {
	wt, tools := t.TempDir(), t.TempDir()
	agentBin := fakeAgentScript(t, tools, filepath.Join(tools, "agent.out"), true)
	f := newFakeOrca(t)
	l := orcaLauncherFor(t, f)
	var order []string
	run := l.Run
	l.Run = func(ctx context.Context, argv []string) (int, string, string) {
		if len(argv) > 1 && argv[1] == "close" {
			order = append(order, "close")
		}
		return run(ctx, argv)
	}
	a, err := l.Launch(context.Background(), OrcaLaunchSpec{Argv: []string{agentBin, PromptArg}, Dir: wt, Prompt: "go"})
	if err != nil {
		t.Fatal(err)
	}
	stopped, _ := a.TerminateWith(func() (func(), bool) {
		order = append(order, "authorize")
		return func() { order = append(order, "release") }, true
	})
	if !stopped || strings.Join(order, ",") != "authorize,close,release" {
		t.Fatalf("stop order %v (stopped=%v), want authorize,close,release", order, stopped)
	}
}

// cli#2088 blind delta P2: the identity reads go by number through /proc, so a
// process that exits during them may hand its pid to another. A launch whose
// pinned process is gone after the reads is never returned as a proven, running
// agent: it is a finished one. (The exit lands between the reads and the
// liveness check that makes them count.)
func TestOrcaLaunchDoesNotTrustReadsOfAProcessThatExitedDuringThem2088(t *testing.T) {
	wt, tools := t.TempDir(), t.TempDir()
	bin := filepath.Join(tools, "agent")
	if err := os.WriteFile(bin, []byte("#!/bin/bash\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	f := newFakeOrca(t)
	l := orcaLauncherFor(t, f)
	l.afterReads = func(pid int) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		for i := 0; i < 200 && syscall.Kill(pid, 0) == nil; i++ {
			time.Sleep(10 * time.Millisecond)
		}
	}
	a, err := l.Launch(context.Background(), OrcaLaunchSpec{Argv: []string{bin, PromptArg}, Dir: wt, Prompt: "go"})
	if err != nil {
		t.Fatalf("a launch whose agent exited is done, not refused: %v", err)
	}
	// Deterministic (Jamir's delta P3): a proven launch hands back a pinned
	// agent whose watcher closes Done later, which a Done check alone could
	// not tell from this result; a finished launch pins nothing.
	if a.h != nil {
		t.Fatal("reads of a process that exited during them were trusted: the agent came back pinned and running")
	}
	select {
	case <-a.Done():
	default:
		t.Fatal("a finished launch's agent is not done")
	}
}

// cli#2088 local delta P2 (Jamir): a well-formed marker naming a dead pid that
// is not the terminal's is refused and the terminal closed; it is never a
// finished launch while the terminal's own process runs on.
func TestOrcaLaunchRefusesADeadForeignMarkerPID2088(t *testing.T) {
	wt, tools := t.TempDir(), t.TempDir()
	agentBin := fakeAgentScript(t, tools, filepath.Join(tools, "agent.out"), true)
	f := newFakeOrca(t)
	f.markerPID = "2147483647"
	a, err := orcaLauncherFor(t, f).Launch(context.Background(), OrcaLaunchSpec{Argv: []string{agentBin, PromptArg}, Dir: wt, Prompt: "go"})
	if err == nil || a != nil || !strings.Contains(err.Error(), "before it could be proven") || len(f.closed) != 1 {
		t.Fatalf("a dead foreign marker pid: agent=%v err=%v closed=%v", a, err, f.closed)
	}
}

// Rook's delta P3: on the unclosed-terminal stop, the authorization is held
// through the retried close and released after it.
func TestOrcaUnclosedStopHoldsTheAuthorizationThroughTheClose2088(t *testing.T) {
	var order []string
	l := OrcaLauncher{Run: func(_ context.Context, argv []string) (int, string, string) {
		if len(argv) > 1 && argv[1] == "close" {
			order = append(order, "close")
		}
		return 0, `{"ok":true}`, ""
	}}
	a := &OrcaAgent{l: l, handle: "term_x", done: make(chan struct{}), unclosed: true}
	stopped, _ := a.TerminateWith(func() (func(), bool) {
		order = append(order, "authorize")
		return func() { order = append(order, "release") }, true
	})
	if !stopped || strings.Join(order, ",") != "authorize,close,release" {
		t.Fatalf("stop order %v (stopped=%v), want authorize,close,release", order, stopped)
	}
}

// failingProc observes for real, then answers liveness with an error once
// armed: the reads were made, but whether the process was still the one read
// is unknown.
type failingProc struct {
	LinuxProcessControl
	mu       sync.Mutex
	armed    bool
	clockErr bool
}

type failingHandle struct {
	ProcessHandle
	p *failingProc
}

func (h failingHandle) SignalWithin(sig syscall.Signal, t0, lease time.Duration, now func() (time.Duration, error)) (time.Duration, error) {
	h.p.mu.Lock()
	armed := h.p.armed && !h.p.clockErr
	h.p.mu.Unlock()
	if armed {
		return 0, syscall.EPERM
	}
	return h.ProcessHandle.SignalWithin(sig, t0, lease, now)
}

func (p *failingProc) Now() (time.Duration, error) {
	p.mu.Lock()
	armed := p.armed && p.clockErr
	p.mu.Unlock()
	if armed {
		return 0, ErrClock
	}
	return p.LinuxProcessControl.Now()
}

func (p *failingProc) Observe(pid int) (ProcessHandle, error) {
	h, err := p.LinuxProcessControl.Observe(pid)
	if err != nil {
		return nil, err
	}
	return failingHandle{h, p}, nil
}

// cli#2088 blind delta P2 (Julius, 6e2c5dad): a liveness check that errs after
// the identity reads proves nothing; the launch is refused and the terminal
// closed, for a clock error and for a signal-check error alike.
func TestOrcaLaunchRefusesWhenLivenessCannotBeEstablished2088(t *testing.T) {
	for name, clockErr := range map[string]bool{"clock": true, "signal check": false} {
		t.Run(name, func(t *testing.T) {
			wt, tools := t.TempDir(), t.TempDir()
			agentBin := fakeAgentScript(t, tools, filepath.Join(tools, "agent.out"), true)
			f := newFakeOrca(t)
			proc := &failingProc{clockErr: clockErr}
			l := orcaLauncherFor(t, f)
			l.Proc = proc
			l.afterReads = func(int) { proc.mu.Lock(); proc.armed = true; proc.mu.Unlock() }
			a, err := l.Launch(context.Background(), OrcaLaunchSpec{Argv: []string{agentBin, PromptArg}, Dir: wt, Prompt: "go"})
			if err == nil || a != nil || !strings.Contains(err.Error(), "liveness could not be established") || len(f.closed) != 1 {
				t.Fatalf("agent=%v err=%v closed=%v", a, err, f.closed)
			}
		})
	}
}

// Julius's delta P3: a marker number that another process holds by the pin
// (the agent replaced before it was pinned) pins that other process, whose
// parentage is then read after the pin: not the terminal's, so refused and
// closed, never a running agent.
func TestOrcaLaunchRefusesAReplacementPinnedInsteadOfTheAgent2088(t *testing.T) {
	wt, tools := t.TempDir(), t.TempDir()
	agentBin := fakeAgentScript(t, tools, filepath.Join(tools, "agent.out"), true)
	other := exec.Command("sleep", "60")
	other.Dir = wt // even in the worktree, and alive
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Process.Kill(); _, _ = other.Process.Wait() })
	f := newFakeOrca(t)
	f.markerPID = strconv.Itoa(other.Process.Pid)
	a, err := orcaLauncherFor(t, f).Launch(context.Background(), OrcaLaunchSpec{Argv: []string{agentBin, PromptArg}, Dir: wt, Prompt: "go"})
	if err == nil || a != nil || !strings.Contains(err.Error(), "not the one Orca's terminal runs") || len(f.closed) != 1 {
		t.Fatalf("a replacement pinned in the agent's place: agent=%v err=%v closed=%v", a, err, f.closed)
	}
}

// Holger's ruling (2026-11-15, relayed by Wolfgang #108664): stay as
// Orca-conform as possible, so no per-agent PID namespace. A default launcher
// types the agent's argv plainly after the marker, and the pinned pid is the
// agent's own pid, the one it sees as $$.
func TestOrcaLaunchRunsTheAgentPlainlyInOrcasScope2088(t *testing.T) {
	wt, tools := t.TempDir(), t.TempDir()
	out := filepath.Join(tools, "agent.out")
	bin := filepath.Join(tools, "agent")
	if err := os.WriteFile(bin, []byte("#!/bin/bash\necho \"$$\" > "+shq(out)+"\nexec sleep 60\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	f := newFakeOrca(t)
	l := OrcaLauncher{Run: f.run, StateDir: t.TempDir(), Proc: LinuxProcessControl{}, MarkerWait: 3 * time.Second, StopWait: 3 * time.Second}
	a, err := l.Launch(context.Background(), OrcaLaunchSpec{Argv: []string{bin, PromptArg}, Dir: wt, Prompt: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.typed) != 1 || strings.Contains(f.typed[0], "unshare") || !strings.Contains(f.typed[0], "&& exec "+shq(bin)+` "$HADRON_FIRST_PROMPT"`) {
		t.Fatalf("the agent is not run plainly in the terminal: %q", f.typed)
	}
	if got := strings.TrimSpace(waitFile(t, out)); got != strconv.Itoa(a.PID()) {
		t.Fatalf("the agent sees itself as pid %s, the launcher pinned %d", got, a.PID())
	}
	if stopped, detail := a.TerminateWith(nil); !stopped {
		t.Fatalf("closing the terminal did not end the agent: %s", detail)
	}
}
