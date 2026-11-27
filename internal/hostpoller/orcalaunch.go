package hostpoller

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/hadron-memory/hadron-cli/internal/delivery"
	"github.com/hadron-memory/hadron-cli/internal/hostrelaunch"
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
// pid and tty into the marker, and the pid it wrote must be that process (or
// a descendant of it), its stdin must be the tty it wrote, and its
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
	// MarkerWait bounds how long the launch waits for the marker (default 30s).
	MarkerWait time.Duration
	// StopWait bounds how long a stop waits for the process to go (default 10s).
	StopWait time.Duration
	// onMarker, for tests, runs when the marker is read, with the launch's
	// environment file path.
	onMarker func(envFile string)
	// afterReads, for tests, runs after the identity reads and before the
	// liveness check that makes them count.
	afterReads func(pid int)
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
// file paths in the poller's private directory and the quoted argv. The
// agent runs plainly in Orca's terminal scope (Holger, 2026-11-15: as
// Orca-conform as possible, no per-agent PID namespace), so the shell's $$
// in the marker is the agent's own pid after the exec.
//
// The go-gate (cli#2130): the command first waits, bounded by gateTries x
// 0.1s, for the gate file, and starts nothing without it. Launch creates the
// gate only once Orca's create reply has parsed with a handle, so a lost or
// malformed reply (Orca may well have started the shell) can never leave an
// agent running untracked: the shell gives up, and the launch file is gone.
func launchCommand(envFile, marker, gate string, argv []string) (string, error) {
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
	// After the loop the gate is a HARD condition: running out of the wait
	// never falls through to sourcing the launch file (Rook's §10 (1)).
	inner := "i=0; while [ ! -f " + shq(gate) + " ] && [ \"$i\" -lt " + strconv.Itoa(gateTries) + " ]; do sleep 0.1; i=$((i+1)); done; " +
		"[ -f " + shq(gate) + " ] && rm -f -- " + shq(gate) + " && " +
		". " + shq(envFile) + " && rm -f -- " + shq(envFile) +
		` && printf '%s %s\n' "$$" "$(tty)" > ` + shq(marker+".tmp") +
		" && mv -f -- " + shq(marker+".tmp") + " " + shq(marker) +
		" && exec " + strings.Join(words, " ")
	return inner, nil
}

// removeLaunchFile is os.Remove; a test makes it a no-op to model a poller
// that died before its cleanup, leaving the launch file behind. The gate,
// not the cleanup, is what keeps that shell from starting an agent.
var removeLaunchFile = os.Remove

// gateTries bounds the command's wait for the gate: 600 x 0.1s, the 60s
// bound on one Orca call, so a slow but successful create still starts. A
// create slower than that fails closed: the shell gives up, no marker
// appears and the launch closes the terminal. A test shortens it.
var gateTries = 600

// gateWindow is how long the command waits for the gate; Launch never
// writes it in the last tenth of that wait (6s of 60s).
func gateWindow() time.Duration { return time.Duration(gateTries) * 100 * time.Millisecond }

// writeGateFile is writeGate; a test counts its calls to show that a late or
// cancelled launch never writes a gate (cli#2136).
var writeGateFile = writeGate

// writeGate creates the gate atomically (a private temp file renamed into
// place), so the command never sees a partial one (Wolfgang's (1)).
func writeGate(gate string) error {
	tmp := gate + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, gate); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
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
	// The agent's own pid (cli#2161): sourced by the shell the command then
	// exec's into the agent, so "$$" there is the pinned pid. Written last, so
	// a value inherited from the poller's environment never wins.
	b.WriteString("export " + delivery.AgentPIDEnv + "=\"$$\"\n")
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

// closedLaunchError is a launch failure whose terminal was closed. It keeps
// the structured cause behind the message for errors.Is/As (cli#2172).
type closedLaunchError struct {
	msg   string
	cause error
}

func (e *closedLaunchError) Error() string { return e.msg }
func (e *closedLaunchError) Unwrap() error { return e.cause }

// Launch creates the terminal, waits for the marker, proves the process is
// the one the terminal runs, and pins it. Every failure after the terminal
// exists closes it: nothing this launch started is left running unrecorded.
func (l OrcaLauncher) Launch(ctx context.Context, spec OrcaLaunchSpec) (*OrcaAgent, error) {
	setupFailed := func(err error) (*OrcaAgent, error) {
		return nil, &hostrelaunch.LaunchError{Code: hostrelaunch.FailureSetupFailed, Err: err}
	}
	dir := filepath.Join(l.StateDir, "launch")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return setupFailed(fmt.Errorf("preparing the launch directory: %w", err))
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return setupFailed(err)
	}
	var rb [12]byte
	if _, err := rand.Read(rb[:]); err != nil {
		return setupFailed(err)
	}
	id := hex.EncodeToString(rb[:])
	envFile, marker, gate := filepath.Join(dir, id+".env"), filepath.Join(dir, id+".marker"), filepath.Join(dir, id+".go")
	defer func() {
		for _, f := range []string{envFile, marker, marker + ".tmp", gate, gate + ".tmp"} {
			_ = removeLaunchFile(f)
		}
	}()
	if err := writeLaunchEnv(envFile, spec.Env, spec.Prompt); err != nil {
		return setupFailed(fmt.Errorf("writing the launch environment: %w", err))
	}
	cmd, err := launchCommand(envFile, marker, gate, spec.Argv)
	if err != nil {
		return setupFailed(err)
	}
	argv := []string{"terminal", "create", "--worktree", "path:" + spec.Dir, "--command", cmd, "--json"}
	if spec.Title != "" {
		argv = append(argv[:len(argv)-1], "--title", spec.Title, "--json")
	}
	callStart := time.Now()
	callCtx, cancelCall := context.WithTimeout(ctx, time.Minute)
	code, out, errOut := l.Run(callCtx, argv)
	callErr := callCtx.Err()
	cancelCall()
	var created orcaCreated
	decodeErr := json.Unmarshal([]byte(out), &created)
	if decodeErr != nil || code != 0 || !created.OK || created.Result.Terminal.Handle == "" {
		why := strings.TrimSpace(errOut)
		if created.Error != nil && created.Error.Code != "" {
			why = created.Error.Code
		}
		cause := fmt.Errorf("orca did not create the terminal (%s)", firstNonEmpty(why, "no usable reply"))
		if errors.Is(callErr, context.DeadlineExceeded) {
			return nil, &hostrelaunch.LaunchError{Code: hostrelaunch.FailureTimeout, Err: cause}
		}
		if decodeErr == nil && created.Error != nil && (code != 0 || !created.OK) {
			return nil, &hostrelaunch.LaunchError{Code: hostrelaunch.FailureTerminalCreateRefused, Err: cause}
		}
		return nil, cause
	}
	handle, opid := created.Result.Terminal.Handle, created.Result.Terminal.ProcessID
	// failCause closes the terminal this launch created and returns the
	// failure. A structured cause (a context error from a proof read) rides
	// along through Unwrap, so classification never has to parse the text
	// (cli#2172).
	failCause := func(cause error, format string, a ...any) (*OrcaAgent, error) {
		// Ours to close: this launch created it moments ago.
		why := fmt.Sprintf(format, a...)
		if code, _, errOut := l.Run(context.WithoutCancel(ctx), []string{"terminal", "close", "--terminal", handle, "--json"}); code != 0 {
			// The terminal may still run something this launch started. It
			// is handed back as an agent that is NOT done, so the caller
			// keeps it recorded (one agent per worktree holds) and a later
			// stop retries the close (cli#2088 blind P2).
			u := &OrcaAgent{l: l, handle: handle, done: make(chan struct{}), unclosed: true}
			return u, &UnclosedLaunchError{Handle: handle, Why: why, CloseErr: firstNonEmpty(strings.TrimSpace(errOut), "no reply"), Cause: cause}
		}
		return nil, &closedLaunchError{msg: why + "; the terminal was closed", cause: cause}
	}
	fail := func(format string, a ...any) (*OrcaAgent, error) { return failCause(nil, format, a...) }
	// Only now, with the terminal's handle in hand, may the command start
	// the agent (cli#2130). Before this point a failure leaves no agent: the
	// shell waits, gives up, and stays an empty shell, which fail closes.
	// Not on a cancelled launch, and not once the command may have stopped
	// waiting (its window opened after callStart, so this is conservative).
	if err := ctx.Err(); err != nil {
		return fail("the launch was cancelled before its agent could start (%v); nothing was started", err)
	}
	if waited := time.Since(callStart); waited > gateWindow()-gateWindow()/10 {
		return fail("Orca's create reply came after %s, too late for the command's %s wait; nothing was started", waited.Round(time.Millisecond), gateWindow())
	}
	if err := writeGateFile(gate); err != nil {
		return fail("opening the launch gate: %v", err)
	}
	pid, tty, err := l.awaitMarker(ctx, marker)
	if err != nil {
		agent, finalErr := failCause(err, "%v", err)
		var classified interface{ RelaunchFailureReason() string }
		if errors.As(err, &classified) {
			return agent, &hostrelaunch.LaunchError{Code: classified.RelaunchFailureReason(), Err: finalErr}
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return agent, &hostrelaunch.LaunchError{Code: hostrelaunch.FailureTimeout, Err: finalErr}
		}
		if !errors.Is(err, context.Canceled) {
			return agent, &hostrelaunch.LaunchError{Code: hostrelaunch.FailureVerifyFailed, Err: finalErr}
		}
		return agent, finalErr
	}
	// proofFailCause is a failed proof of the launched process. The reason is
	// read from the structured cause: a pure cancellation is not a failed
	// proof (no reason), a pure deadline is a timeout, anything else cannot
	// prove the agent came up (VERIFY_FAILED).
	proofFailCause := func(cause error, format string, args ...any) (*OrcaAgent, error) {
		agent, err := failCause(cause, format, args...)
		switch {
		case errors.Is(cause, context.Canceled):
			return agent, err
		case errors.Is(cause, context.DeadlineExceeded):
			return agent, &hostrelaunch.LaunchError{Code: hostrelaunch.FailureTimeout, Err: err}
		}
		return agent, &hostrelaunch.LaunchError{Code: hostrelaunch.FailureVerifyFailed, Err: err}
	}
	proofFail := func(format string, args ...any) (*OrcaAgent, error) {
		return proofFailCause(nil, format, args...)
	}
	if l.onMarker != nil {
		l.onMarker(envFile)
	}
	if opid <= 1 {
		return proofFail("Orca named no process for the terminal")
	}
	// Pin first: every later read is then of this process, or it is known
	// gone. A launch whose process already exited (a short task, or a
	// launcher that failed at once) happened, and its agent is done;
	// worker.start decides from the bind what that means.
	//
	// Gone is a finished launch only when the marker's pid IS the terminal's
	// processId: two independent sources (the shell's $$ and Orca's reply)
	// naming the same number. Anything else cannot be proven the terminal's
	// once it is gone (its ancestry is unreadable), so it is refused and the
	// terminal closed, never taken as done (cli#2088 local delta P2: a dead
	// foreign marker pid must not read as a finished launch while the
	// terminal's own process still runs).
	exited := func() (*OrcaAgent, error) {
		if pid != opid {
			return proofFail("pid %d exited before it could be proven the process of Orca's terminal (pid %d)", pid, opid)
		}
		a := &OrcaAgent{l: l, handle: handle, pid: pid, tty: tty, done: make(chan struct{})}
		a.closeOnce.Do(func() { close(a.done) })
		return a, nil
	}
	h, err := l.Proc.Observe(pid)
	if errors.Is(err, syscall.ESRCH) {
		return exited()
	}
	if err != nil {
		return proofFailCause(err, "pid %d could not be pinned: %v", pid, err)
	}
	// alive is a positive answer or an error: only a successful liveness
	// check lets the reads count (cli#2088 blind delta P2). "Not seen gone"
	// is not "alive".
	alive := func() (bool, error) {
		t0, terr := l.Proc.Now()
		if terr != nil {
			return false, terr
		}
		_, serr := h.SignalWithin(syscall.Signal(0), t0, time.Hour, l.Proc.Now)
		if serr == nil {
			return true, nil
		}
		if errors.Is(serr, syscall.ESRCH) {
			return false, nil
		}
		return false, serr
	}
	// The identity reads go by number through /proc, which a pidfd does not
	// bind: if the pinned process exited during them, its pid could have been
	// reused and a read could be another process's (cli#2088 blind delta P2).
	// So all of them are made first, and only then is the pinned process
	// checked alive: a live pidfd holds its pid, so a process alive after the
	// reads was the process read. One gone by then is a launch whose agent is
	// done, never a proven one. Then: the parentage (the terminal's process or
	// its descendant), the terminal association (an Orca terminal always has a
	// pty, cli#2088 blind P2; stdin must be that pty), and the cwd.
	parent := isTerminalProcess(pid, opid)
	fd0, ferr := os.Readlink("/proc/" + strconv.Itoa(pid) + "/fd/0")
	cwd, cerr := os.Readlink("/proc/" + strconv.Itoa(pid) + "/cwd")
	if l.afterReads != nil {
		l.afterReads(pid)
	}
	live, lerr := alive()
	if lerr != nil {
		h.Close()
		return proofFailCause(lerr, "pid %d's liveness could not be established after its checks: %v", pid, lerr)
	}
	if !live {
		h.Close()
		return exited()
	}
	if !parent {
		h.Close()
		return proofFail("the process that started (pid %d) is not the one Orca's terminal runs (pid %d)", pid, opid)
	}
	if ferr != nil || fd0 != tty {
		h.Close()
		return proofFail("pid %d's terminal is not the tty it reported", pid)
	}
	root, rerr := filepath.EvalSymlinks(spec.Dir)
	if cerr != nil || rerr != nil || !within(cwd, root) {
		h.Close()
		return proofFail("pid %d is not working in the worktree", pid)
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
			return parseMarker(string(b))
		}
		if !time.Now().Before(deadline) {
			return 0, "", &hostrelaunch.LaunchError{Code: hostrelaunch.FailureTimeout, Err: fmt.Errorf("the launched command did not start within %s (no marker)", wait)}
		}
		select {
		case <-ctx.Done():
			return 0, "", ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// isTerminalProcess: the pid the command wrote is the terminal's own process,
// or a descendant of it (in case Orca's shell runs the typed text in a child;
// the sg wrapper that needed it is gone, cli#2121).
func isTerminalProcess(pid, terminalPID int) bool {
	return pid == terminalPID || descendsFrom(pid, terminalPID)
}

// markerTTYRE is the only tty a marker may name: a pts device.
var markerTTYRE = regexp.MustCompile(`^/dev/pts/[0-9]+$`)

// parseMarker reads exactly "<pid> <tty>\n" as the command wrote it; any
// other shape (no tty, "not a tty", extra fields) is refused.
func parseMarker(s string) (int, string, error) {
	if !strings.HasSuffix(s, "\n") || strings.Count(s, "\n") != 1 {
		return 0, "", errors.New("the launch marker is malformed")
	}
	pidText, tty, _ := strings.Cut(strings.TrimSuffix(s, "\n"), " ")
	pid, err := strconv.Atoi(pidText)
	if err != nil || pid <= 1 || strconv.Itoa(pid) != pidText {
		return 0, "", errors.New("the launch marker names no process")
	}
	if !markerTTYRE.MatchString(tty) {
		return 0, "", errors.New("the launched command has no terminal (its marker names no pts device)")
	}
	return pid, tty, nil
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

// UnclosedLaunchError is a launch that could not be proven AND whose terminal
// could not be closed: Launch returns it together with an agent that stays
// recorded until a stop closes the terminal.
type UnclosedLaunchError struct {
	Handle, Why, CloseErr string
	// Cause is the failure that made the launch close its terminal (a context
	// error from a proof read, for one), kept for errors.Is/As (cli#2172).
	Cause error
}

func (e *UnclosedLaunchError) Unwrap() error { return e.Cause }

func (e *UnclosedLaunchError) Error() string {
	return e.Why + "; closing Orca terminal " + e.Handle + " failed (" + e.CloseErr + "), so it stays recorded until a stop closes it"
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
	// unclosed: an unproven launch whose terminal could not be closed. No
	// process is pinned; Done stays open until a stop closes the terminal.
	unclosed bool
}

// watch closes done once the pinned process is gone: the terminal itself
// outlives its process in Orca, so its presence says nothing.
func (a *OrcaAgent) watch() {
	for {
		if a.gone() {
			// The pidfd is released once the process is gone: a poller that
			// launches for weeks must not leak one per run (cli#2088 blind P2).
			// watch is the handle's only user after Launch returns.
			a.h.Close()
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
	if a.unclosed {
		// Nothing was pinned: closing the terminal is the whole stop, and
		// its success is the confirmation.
		if authorize != nil {
			release, ok := authorize()
			if !ok {
				return false, "the stop of Orca terminal " + a.handle + " was declined before closing it; it stays recorded"
			}
			defer release()
		}
		if code, _, errOut := a.l.Run(context.Background(), []string{"terminal", "close", "--terminal", a.handle, "--json"}); code != 0 {
			return false, "closing Orca terminal " + a.handle + " failed again: " + firstNonEmpty(strings.TrimSpace(errOut), "no reply")
		}
		a.closeOnce.Do(func() { close(a.done) })
		return true, "closed Orca terminal " + a.handle
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
		if a == nil {
			return nil, err // never a typed-nil Agent
		}
		return a, err // an unclosed launch: the agent and its error
	}
}

// promptTaskRE: a task id interpolated into the fixed first prompt is a plain
// token, so the prompt stays a template (it is never typed either way).
var promptTaskRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

// FirstPrompt is the fixed first prompt of a fresh run (G9: always fresh,
// never a resumed conversation). A start with a launch grant binds through
// it first, then carries the task it names or, with none (a long-lived
// worker the platform starts, G2), recovers. A relaunch has no grant (C5:
// the worker's session is still open and is never bound again): it only
// recovers.
func FirstPrompt(workerRef, family, taskID string, hasGrant bool) string {
	return firstPrompt(workerRef, family, taskID, hasGrant, Bind{})
}

// firstPrompt is FirstPrompt with what the bind reports (#2199): the family,
// the model value actually passed and the effort applied, each only when the
// start settled it. The zero Bind is the prompt as it always was.
func firstPrompt(workerRef, family, taskID string, hasGrant bool, bind Bind) string {
	recover := "run hadron team session recover and act on what it reports; never resume an earlier conversation."
	if !hasGrant {
		return "Hadron relaunched worker " + workerRef + " in its own worktree. Its worker session is still open, so do not start a new one: " + recover
	}
	tool := "claude-code"
	if CanonicalFamily(family) == "codex" {
		tool = "codex"
	}
	next := "then " + recover
	if promptTaskRE.MatchString(taskID) {
		next = "then start and carry your task: hadron team task start " + taskID + " (and follow your briefing)."
	}
	report := ""
	for _, f := range []struct{ flag, v string }{{"family", bind.Family}, {"model", bind.Model}, {"effort", bind.Effort}} {
		if f.v != "" && profileTokenRE.MatchString(f.v) {
			report += " --" + f.flag + " " + f.v
		}
	}
	return "Hadron started this fresh run for worker " + workerRef + ". First bind your worker session: hadron team session start --tool " + tool + report +
		" (the launch grant is in HADRON_LAUNCH_GRANT, so no --as is needed); " + next
}

// RelaunchPromptWithCheckpoint adds a context reset's checkpoint to a
// relaunch prompt (cli#2161): `session recover` reads the state note, tasks
// and mentions but not the checkpoint, which holds where the old agent was.
// The URN is checked by the stamp reader (delivery.ValidCheckpointURN).
func RelaunchPromptWithCheckpoint(prompt, checkpoint string) string {
	return prompt + " This relaunch follows your own context reset: after recovering, read your checkpoint, hadron node get " + checkpoint + ", and continue its next action."
}

// resolveOrcaRun applies a start's run settings over the worker's launcher
// profile BEFORE any process, terminal or reset: a family this host cannot
// launch on the worker's account, or a malformed shape, fails the start with
// a retryable reason that names the family and the account.
func (p *Poller) resolveOrcaRun(ctx context.Context, op Operation, ref string, rs RunSettings) (Resolved, bool) {
	if p.orcaLaunch == nil || p.profiles == nil {
		p.finish(ctx, op, Failed, "", "launching the agent through Orca failed: this poller has no Orca launcher (orca_launch is not wired)")
		return Resolved{}, false
	}
	var (
		prof     LauncherProfile
		acct     AccountProfile
		ok       bool
		err      error
		resolved bool
	)
	if src, isResolving := p.profiles.(AccountResolving); isResolving {
		// A host that runs from the platform: the account is resolved now, from the start's name when it
		// names one (hadron-cli#3237), before any process or terminal exists.
		prof, acct, err = src.ResolveProfile(ref, rs)
		ok, resolved = err == nil, true
	} else {
		prof, acct, ok, err = p.profiles.Profile(ref)
	}
	if err == nil && !ok {
		err = errors.New("the worker has no launcher profile")
	}
	if err == nil && !familyRE.MatchString(CanonicalFamily(rs.Family)) {
		err = fmt.Errorf("start names no usable family (%q)", rs.Family)
	}
	var run Resolved
	if err == nil {
		run, err = ResolveRun(prof, acct, rs, prof.Account)
	}
	if err != nil {
		var lr *LaunchRefusal
		if errors.As(err, &lr) && lr.Local != "" {
			p.log("launch-refused", op.ID, op.Type, "", lr.Error()+" ("+lr.Local+")")
		}
		p.finish(ctx, op, Failed, "", err.Error())
		return Resolved{}, false
	}
	if resolved {
		a := acct
		run.Account = &a
	}
	if run.Note != "" {
		p.log("run-settings", op.ID, op.Type, "", run.Note)
	}
	return run, true
}

// orcaStart is worker.start's Orca path.
func (p *Poller) orcaStart(ctx context.Context, op Operation, ref string, w WorkerConfig, dir string, run Resolved) (Agent, error) {
	if p.orcaLaunch == nil || p.profiles == nil {
		return nil, errors.New("this poller has no Orca launcher (orca_launch is not wired)")
	}
	spec, err := p.orcaLaunchSpec(ref, w, dir, op.LaunchGrant, argString(op, "taskId"), run)
	if err != nil {
		return nil, err
	}
	return p.orcaLaunch(ctx, spec)
}

// orcaLaunchSpec is the ONE builder of an Orca launch: the profile's argv,
// the explicit environment (disk temp, Codex home, and the grant when there
// is one), and the fixed first prompt. worker.start passes its grant; a
// relaunch under a still-open session (C5, #2060) passes none, so the agent
// gets no grant and the recover-only prompt.
func (p *Poller) orcaLaunchSpec(ref string, w WorkerConfig, dir, grant, taskID string, run Resolved) (OrcaLaunchSpec, error) {
	if p.profiles == nil {
		return OrcaLaunchSpec{}, errors.New("this poller has no launcher profiles")
	}
	if run.Account != nil && run.Profile.Family != "" {
		// Resolved for this start (AccountResolving): launch exactly what was checked.
		return orcaProfileLaunchSpec(ref, w, dir, grant, taskID, run.Profile, *run.Account, run.Bind)
	}
	prof, acct, ok, err := p.profiles.Profile(ref)
	if err != nil {
		return OrcaLaunchSpec{}, err
	}
	if !ok {
		return OrcaLaunchSpec{}, errors.New("the worker has no launcher profile")
	}
	if run.Profile.Family != "" {
		prof = run.Profile // the start's settings, resolved against this very profile
	}
	return orcaProfileLaunchSpec(ref, w, dir, grant, taskID, prof, acct, run.Bind)
}

// orcaProfileLaunchSpec is the shared builder with an immutable typed snapshot.
// C5 prepares that snapshot without IO, then calls this only after claiming
// its attempt. Normal worker.start uses this through orcaLaunchSpec too.
func orcaProfileLaunchSpec(ref string, w WorkerConfig, dir, grant, taskID string, prof LauncherProfile, acct AccountProfile, bind Bind) (OrcaLaunchSpec, error) {
	argv, err := LaunchArgv(prof, acct)
	if err != nil {
		return OrcaLaunchSpec{}, err
	}
	if err := os.MkdirAll(w.DiskTmp, 0o700); err != nil {
		return OrcaLaunchSpec{}, &hostrelaunch.LaunchError{Code: hostrelaunch.FailureSetupFailed, Err: fmt.Errorf("preparing disk_tmp: %w", err)}
	}
	var env []string
	if grant != "" {
		env = append(env, launchGrantEnvVar+"="+grant)
	}
	env = append(env,
		"TMPDIR="+w.DiskTmp,
		"CLAUDE_CODE_TMPDIR="+w.DiskTmp,
		"NODE_COMPILE_CACHE="+filepath.Join(w.DiskTmp, "node-compile-cache"),
	)
	if acct.CodexHome != "" {
		env = append(env, "CODEX_HOME="+acct.CodexHome)
	}
	if acct.ClaudeConfigDir != "" {
		// hadron-cli#3237 (U1 as corrected): exported in the sourced launch file, inside the command, so it
		// replaces whatever Orca exported first; only an accounts-file entry sets it, never the host file.
		env = append(env, "CLAUDE_CONFIG_DIR="+acct.ClaudeConfigDir)
	}
	return OrcaLaunchSpec{
		Argv: argv, Dir: dir, Env: env, Title: ref,
		Prompt: firstPrompt(ref, prof.Family, taskID, grant != "", bind),
	}, nil
}
