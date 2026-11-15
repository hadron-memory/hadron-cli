package hostpoller

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Launching through Orca (hadron-cli#2058 C3; decision G3 as ruled by Holger,
// 2026-11-15: agents are children of the Orca daemon, in Orca's own
// per-terminal scopes, which carry the resource caps).
//
// `orca terminal create --command` TYPES its text into the terminal's login
// shell, so the text is on screen and possibly in shell history, and a
// newline would run early. The typed command is therefore short and fixed,
// and holds no secret and no untrusted text: it sources a 0600 environment
// file the poller wrote (the launch grant, the first prompt, the disk temp,
// CODEX_HOME), deletes it, records the shell's own pid and tty in a marker,
// and execs the shell-quoted argv, whose prompt slot expands to
// "$HADRON_FIRST_PROMPT".
//
// The agent is not the poller's child, so it is identified, not owned:
// Orca's reply names the terminal's process id, the process writes its own
// pid and tty into the marker, and the pid it wrote must be that process (or,
// under `sg`, a descendant of it), its stdin must be the tty it wrote, and its
// working directory must be the worktree. The pid is then pinned by pidfd, so
// no later check can land on a reused number. Nothing is inferred from Orca's
// ptyId (#2074, Rodolfo F1).

// OrcaRun runs one `orca` CLI call (argv from the verb on) and returns its
// exit code and output; the binary and the call allowlist are the caller's.
type OrcaRun func(ctx context.Context, argv []string) (code int, stdout, stderr string)

// OrcaLauncher launches agents through Orca terminals.
type OrcaLauncher struct {
	Run OrcaRun
	// StateDir is a private directory for the per-launch environment and
	// marker files (created 0700).
	StateDir string
	// Proc pins the launched process (LinuxProcessControl in production).
	Proc ProcessControl
	// Group, when set, runs the command under `sg <group>`.
	Group string
	// MarkerWait bounds how long the launch waits for the marker (default 30s).
	MarkerWait time.Duration
	// StopWait bounds how long a stop waits for the process to go (default 10s).
	StopWait time.Duration
	// onMarker, for tests, runs when the marker is read, with the launch's
	// environment file path.
	onMarker func(envFile string)
}

// OrcaLaunchSpec is one launch.
type OrcaLaunchSpec struct {
	// Argv is the profile's argv; PromptArg marks the first prompt's slot.
	Argv []string
	// Dir is the worktree; Orca must know it as a worktree (path:).
	Dir string
	// Env are KEY=VALUE pairs set for the agent (never typed).
	Env []string
	// Prompt is the first prompt (never typed).
	Prompt string
	// Title is the terminal tab's title.
	Title string
}

const firstPromptVar = "HADRON_FIRST_PROMPT"

// shq quotes s for a POSIX shell, as one word.
func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

var envNameOK = func(k string) bool {
	if k == "" {
		return false
	}
	for i, r := range k {
		if !envNameRune(r, i > 0) {
			return false
		}
	}
	return true
}

// envNameRune: a letter or underscore, or after the first, a digit.
func envNameRune(r rune, notFirst bool) bool {
	return r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || notFirst && r >= '0' && r <= '9'
}

// launchCommand is the text typed into the terminal's shell. It names only
// file paths in the poller's private directory and the quoted argv.
func launchCommand(envFile, marker string, argv []string, group string) (string, error) {
	words := make([]string, 0, len(argv))
	for _, a := range argv {
		if a == PromptArg {
			words = append(words, `"$`+firstPromptVar+`"`)
			continue
		}
		if strings.ContainsAny(a, "\x00\n\r") {
			return "", fmt.Errorf("an argv word holds a control character")
		}
		words = append(words, shq(a))
	}
	inner := ". " + shq(envFile) + " && rm -f -- " + shq(envFile) +
		` && printf '%s %s\n' "$$" "$(tty)" > ` + shq(marker+".tmp") +
		" && mv -f -- " + shq(marker+".tmp") + " " + shq(marker) +
		" && exec " + strings.Join(words, " ")
	if group == "" {
		return inner, nil
	}
	return "exec sg " + shq(group) + " -c " + shq(inner), nil
}

// writeLaunchEnv writes the environment the command sources: one `export`
// per variable, each value single-quoted. Created exclusively, 0600.
func writeLaunchEnv(path string, env []string, prompt string) error {
	var b strings.Builder
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || !envNameOK(k) || k == firstPromptVar {
			return fmt.Errorf("launch environment entry %q is not NAME=value", k)
		}
		if strings.ContainsRune(v, 0) {
			return fmt.Errorf("launch environment %s holds a NUL", k)
		}
		b.WriteString("export " + k + "=" + shq(v) + "\n")
	}
	if strings.ContainsRune(prompt, 0) {
		return errors.New("the first prompt holds a NUL")
	}
	b.WriteString("export " + firstPromptVar + "=" + shq(prompt) + "\n")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(b.String()); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// orcaCreated is the part of `orca terminal create --json` a launch reads.
type orcaCreated struct {
	OK     bool `json:"ok"`
	Result struct {
		Terminal struct {
			Handle    string `json:"handle"`
			ProcessID int    `json:"processId"`
		} `json:"terminal"`
	} `json:"result"`
	Error *struct {
		Code string `json:"code"`
	} `json:"error"`
}

// Launch creates the terminal, waits for the marker, proves the process is
// the one the terminal runs, and pins it. Every failure after the terminal
// exists closes it: nothing this launch started is left running unrecorded.
func (l OrcaLauncher) Launch(ctx context.Context, spec OrcaLaunchSpec) (*OrcaAgent, error) {
	dir := filepath.Join(l.StateDir, "launch")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("preparing the launch directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, err
	}
	var rb [12]byte
	if _, err := rand.Read(rb[:]); err != nil {
		return nil, err
	}
	id := hex.EncodeToString(rb[:])
	envFile, marker := filepath.Join(dir, id+".env"), filepath.Join(dir, id+".marker")
	defer func() { _ = os.Remove(envFile); _ = os.Remove(marker); _ = os.Remove(marker + ".tmp") }()
	if err := writeLaunchEnv(envFile, spec.Env, spec.Prompt); err != nil {
		return nil, fmt.Errorf("writing the launch environment: %w", err)
	}
	cmd, err := launchCommand(envFile, marker, spec.Argv, l.Group)
	if err != nil {
		return nil, err
	}
	argv := []string{"terminal", "create", "--worktree", "path:" + spec.Dir, "--command", cmd, "--json"}
	if spec.Title != "" {
		argv = append(argv[:len(argv)-1], "--title", spec.Title, "--json")
	}
	code, out, errOut := l.Run(ctx, argv)
	var created orcaCreated
	if json.Unmarshal([]byte(out), &created) != nil || code != 0 || !created.OK || created.Result.Terminal.Handle == "" {
		why := strings.TrimSpace(errOut)
		if created.Error != nil && created.Error.Code != "" {
			why = created.Error.Code
		}
		return nil, fmt.Errorf("orca did not create the terminal (%s)", firstNonEmpty(why, "no usable reply"))
	}
	handle, opid := created.Result.Terminal.Handle, created.Result.Terminal.ProcessID
	fail := func(format string, a ...any) (*OrcaAgent, error) {
		// Ours to close: this launch created it moments ago.
		_, _, _ = l.Run(context.WithoutCancel(ctx), []string{"terminal", "close", "--terminal", handle, "--json"})
		return nil, fmt.Errorf(format+"; the terminal was closed", a...)
	}
	pid, tty, err := l.awaitMarker(ctx, marker)
	if err != nil {
		return fail("%v", err)
	}
	if l.onMarker != nil {
		l.onMarker(envFile)
	}
	if opid <= 1 || !isTerminalProcess(pid, opid) {
		return fail("the process that started (pid %d) is not the one Orca's terminal runs (pid %d)", pid, opid)
	}
	if tty != "" {
		if fd0, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/fd/0"); err != nil || fd0 != tty {
			return fail("pid %d's terminal is not the tty it reported", pid)
		}
	}
	h, err := l.Proc.Observe(pid)
	if errors.Is(err, syscall.ESRCH) {
		// It already ran and exited (a short task, or a launcher that failed
		// at once): the launch happened, and its agent is done. worker.start
		// decides from the bind what that means; nothing is left to stop.
		a := &OrcaAgent{l: l, handle: handle, pid: pid, tty: tty, done: make(chan struct{})}
		a.closeOnce.Do(func() { close(a.done) })
		return a, nil
	}
	if err != nil {
		return fail("pid %d could not be pinned: %v", pid, err)
	}
	// After the pin, the number is this process: its cwd is the worktree.
	cwd, cerr := os.Readlink("/proc/" + strconv.Itoa(pid) + "/cwd")
	root, rerr := filepath.EvalSymlinks(spec.Dir)
	if cerr != nil || rerr != nil || !within(cwd, root) {
		h.Close()
		return fail("pid %d is not working in the worktree", pid)
	}
	a := &OrcaAgent{l: l, handle: handle, pid: pid, tty: tty, h: h, done: make(chan struct{})}
	go a.watch()
	return a, nil
}

func within(path, root string) bool {
	path = filepath.Clean(path)
	return path == root || strings.HasPrefix(path, root+string(os.PathSeparator))
}

// awaitMarker waits for the marker the command writes atomically (tmp then
// rename) and reads "<pid> <tty>"; "not a tty" is an empty tty.
func (l OrcaLauncher) awaitMarker(ctx context.Context, marker string) (int, string, error) {
	wait := l.MarkerWait
	if wait <= 0 {
		wait = 30 * time.Second
	}
	deadline := time.Now().Add(wait)
	for {
		b, err := os.ReadFile(marker)
		if err == nil {
			f := strings.Fields(strings.TrimSpace(string(b)))
			if len(f) < 1 {
				return 0, "", errors.New("the launch marker is empty")
			}
			pid, perr := strconv.Atoi(f[0])
			if perr != nil || pid <= 1 {
				return 0, "", errors.New("the launch marker names no process")
			}
			tty := ""
			if len(f) == 2 && strings.HasPrefix(f[1], "/dev/") {
				tty = f[1]
			}
			return pid, tty, nil
		}
		if !time.Now().Before(deadline) {
			return 0, "", fmt.Errorf("the launched command did not start within %s (no marker)", wait)
		}
		select {
		case <-ctx.Done():
			return 0, "", ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// isTerminalProcess: the pid the command wrote is the terminal's own process,
// or (under sg) a descendant of it.
func isTerminalProcess(pid, terminalPID int) bool {
	return pid == terminalPID || descendsFrom(pid, terminalPID)
}

// descendsFrom reports whether pid's parent chain reaches ancestor (bounded).
func descendsFrom(pid, ancestor int) bool {
	for i := 0; i < 16 && pid > 1; i++ {
		b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
		if err != nil {
			return false
		}
		s := string(b)
		j := strings.LastIndexByte(s, ')')
		if j < 0 {
			return false
		}
		f := strings.Fields(s[j+1:])
		if len(f) < 2 {
			return false
		}
		ppid, err := strconv.Atoi(f[1])
		if err != nil {
			return false
		}
		if ppid == ancestor {
			return true
		}
		pid = ppid
	}
	return false
}

// OrcaAgent is an agent running in an Orca terminal, identified by the
// terminal's handle and its pinned process.
type OrcaAgent struct {
	l      OrcaLauncher
	handle string
	pid    int
	tty    string
	h      ProcessHandle

	mu        sync.Mutex
	done      chan struct{}
	closeOnce sync.Once
}

// watch closes done once the pinned process is gone: the terminal itself
// outlives its process in Orca, so its presence says nothing.
func (a *OrcaAgent) watch() {
	for {
		if a.gone() {
			a.closeOnce.Do(func() { close(a.done) })
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func (a *OrcaAgent) gone() bool {
	if a.h == nil {
		return true
	}
	t0, err := a.l.Proc.Now()
	if err != nil {
		return false
	}
	_, err = a.h.SignalWithin(syscall.Signal(0), t0, time.Hour, a.l.Proc.Now)
	return errors.Is(err, syscall.ESRCH)
}

func (a *OrcaAgent) Done() <-chan struct{} { return a.done }
func (a *OrcaAgent) PID() int              { return a.pid }

// ExitDetail: Orca does not hand the poller the exit status.
func (a *OrcaAgent) ExitDetail() string {
	return "the agent in Orca terminal " + a.handle + " exited (its exit status is not visible through Orca)"
}

// Terminal is the verified association C2c's idle proof needs: the Orca
// terminal handle, the kernel pid it runs, and that process's tty.
func (a *OrcaAgent) Terminal() (handle string, pid int, tty string) { return a.handle, a.pid, a.tty }

func (a *OrcaAgent) Terminate() (bool, string) { return a.TerminateWith(nil) }

// TerminateWith closes the agent's Orca terminal, which ends its whole
// per-terminal scope, after authorize (held through the close, released
// before the wait), and confirms only when the pinned process is gone.
func (a *OrcaAgent) TerminateWith(authorize Authorize) (bool, string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	select {
	case <-a.done:
		return true, ""
	default:
	}
	if authorize != nil {
		release, ok := authorize()
		if !ok {
			return false, "the stop of the agent in Orca terminal " + a.handle + " was declined before closing it; its records are kept"
		}
		code, _, errOut := a.l.Run(context.Background(), []string{"terminal", "close", "--terminal", a.handle, "--json"})
		release()
		if code != 0 {
			return false, "closing Orca terminal " + a.handle + " failed: " + firstNonEmpty(strings.TrimSpace(errOut), "no reply")
		}
	} else if code, _, errOut := a.l.Run(context.Background(), []string{"terminal", "close", "--terminal", a.handle, "--json"}); code != 0 {
		return false, "closing Orca terminal " + a.handle + " failed: " + firstNonEmpty(strings.TrimSpace(errOut), "no reply")
	}
	wait := a.l.StopWait
	if wait <= 0 {
		wait = 10 * time.Second
	}
	select {
	case <-a.done:
		return true, "closed Orca terminal " + a.handle
	case <-time.After(wait):
		return false, fmt.Sprintf("closed Orca terminal %s but pid %d was not seen gone within %s; its records are kept", a.handle, a.pid, wait)
	}
}

// OrcaLaunchFunc launches one agent through Orca (OrcaLauncher.Launch in
// production; tests replace it).
type OrcaLaunchFunc func(ctx context.Context, spec OrcaLaunchSpec) (Agent, error)

// EnableOrcaLaunch lets worker.start launch a launch_via: orca worker through
// Orca, with profiles from src (ConfigProfiles until S1b).
func (p *Poller) EnableOrcaLaunch(launch OrcaLaunchFunc, src ProfileSource) {
	p.orcaLaunch, p.profiles = launch, src
}

// LaunchFunc adapts the launcher to the poller's seam.
func (l OrcaLauncher) LaunchFunc() OrcaLaunchFunc {
	return func(ctx context.Context, spec OrcaLaunchSpec) (Agent, error) {
		a, err := l.Launch(ctx, spec)
		if err != nil {
			return nil, err
		}
		return a, nil
	}
}

// promptTaskRE: a task id interpolated into the fixed first prompt is a plain
// token, so the prompt stays a template (it is never typed either way).
var promptTaskRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

// FirstPrompt is the fixed first prompt of a fresh run (G9: always fresh,
// never a resumed conversation): bind through the launch grant, then the task
// the start names, or, with none (a relaunch), the recovery routine.
func FirstPrompt(workerRef, family, taskID string) string {
	tool := "claude-code"
	if CanonicalFamily(family) == "codex" {
		tool = "codex"
	}
	// A start with no task is a long-lived worker's relaunch (G2, C5): it
	// recovers from its own records, never a resumed conversation (G9).
	next := "then run hadron team session recover and act on what it reports; never resume an earlier conversation."
	if promptTaskRE.MatchString(taskID) {
		next = "then start and carry your task: hadron team task start " + taskID + " (and follow your briefing)."
	}
	return "Hadron started this fresh run for worker " + workerRef + ". First bind your worker session: hadron team session start --tool " + tool +
		" (the launch grant is in HADRON_LAUNCH_GRANT, so no --as is needed); " + next
}

// orcaStart is worker.start's Orca path: the profile's argv, the explicit
// environment (grant, disk temp, Codex home), the fixed first prompt.
func (p *Poller) orcaStart(ctx context.Context, op Operation, ref string, w WorkerConfig, dir string) (Agent, error) {
	if p.orcaLaunch == nil || p.profiles == nil {
		return nil, errors.New("this poller has no Orca launcher (orca_launch is not wired)")
	}
	prof, acct, ok, err := p.profiles.Profile(ref)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("the worker has no launcher profile")
	}
	argv, err := LaunchArgv(prof, acct)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(w.DiskTmp, 0o700); err != nil {
		return nil, fmt.Errorf("preparing disk_tmp: %w", err)
	}
	env := []string{
		launchGrantEnvVar + "=" + op.LaunchGrant,
		"TMPDIR=" + w.DiskTmp,
		"CLAUDE_CODE_TMPDIR=" + w.DiskTmp,
		"NODE_COMPILE_CACHE=" + filepath.Join(w.DiskTmp, "node-compile-cache"),
	}
	if acct.CodexHome != "" {
		env = append(env, "CODEX_HOME="+acct.CodexHome)
	}
	return p.orcaLaunch(ctx, OrcaLaunchSpec{
		Argv: argv, Dir: dir, Env: env, Title: ref,
		Prompt: FirstPrompt(ref, prof.Family, argString(op, "taskId")),
	})
}
