//go:build linux

package hostpoller

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/hostrelaunch"
)

// cli#2172 (C5, from #2168/#2149): the phase a launch failure is observed in
// decides the closed reason it is reported with (cor:agt:070:01). Each
// boundary is pinned on the real launcher; the free text stays local.

func reasonOf(err error) string {
	var classified interface{ RelaunchFailureReason() string }
	if errors.As(err, &classified) {
		return classified.RelaunchFailureReason()
	}
	return ""
}

func launchSpec2172(t *testing.T) OrcaLaunchSpec {
	t.Helper()
	wt, tools := t.TempDir(), t.TempDir()
	return OrcaLaunchSpec{Argv: []string{fakeAgentScript(t, tools, filepath.Join(tools, "agent.out"), true), PromptArg}, Dir: wt, Prompt: "go"}
}

// A marker that names no usable process or no pts is the launched command's
// proof failing: VERIFY_FAILED, with the terminal closed.
func TestMalformedOrPtylessMarkerIsVerifyFailed2172(t *testing.T) {
	for name, arrange := range map[string]func(*fakeOrca){
		"no pts device":     func(f *fakeOrca) { f.noPTY = true },
		"no process in it":  func(f *fakeOrca) { f.markerPID = "abc" },
		"process 1 is init": func(f *fakeOrca) { f.markerPID = "1" },
	} {
		t.Run(name, func(t *testing.T) {
			spec := launchSpec2172(t)
			f := newFakeOrca(t)
			arrange(f)
			a, err := orcaLauncherFor(t, f).Launch(context.Background(), spec)
			if a != nil || err == nil || reasonOf(err) != hostrelaunch.FailureVerifyFailed || len(f.closed) != 1 {
				t.Fatalf("agent=%v err=%v reason=%q closed=%v", a, err, reasonOf(err), f.closed)
			}
		})
	}
}

// A cancelled marker wait is not a failed proof: no VERIFY_FAILED, and not a
// timeout either. The terminal is still closed.
func TestCancelledMarkerWaitIsNotVerifyFailed2172(t *testing.T) {
	spec := launchSpec2172(t)
	f := newFakeOrca(t)
	f.runNoCmd = true
	l := orcaLauncherFor(t, f)
	l.MarkerWait = 10 * time.Second
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	a, err := l.Launch(ctx, spec)
	if a != nil || err == nil || len(f.closed) != 1 {
		t.Fatalf("agent=%v err=%v closed=%v", a, err, f.closed)
	}
	if got := reasonOf(err); got == hostrelaunch.FailureVerifyFailed || got == hostrelaunch.FailureTimeout {
		t.Fatalf("a cancellation was reported as %q", got)
	}
}

// A process proven to be somewhere other than the worktree is a failed proof.
func TestWrongWorktreeCwdProofIsVerifyFailed2172(t *testing.T) {
	spec := launchSpec2172(t)
	elsewhere := t.TempDir()
	f := newFakeOrca(t)
	// Orca says the terminal is in the worktree, but the shell runs elsewhere.
	l := orcaLauncherFor(t, f)
	run := f.run
	l.Run = func(ctx context.Context, argv []string) (int, string, string) {
		argv = append([]string(nil), argv...)
		for i := range argv {
			if strings.HasPrefix(argv[i], "path:") {
				argv[i] = "path:" + elsewhere
			}
		}
		return run(ctx, argv)
	}
	a, err := l.Launch(context.Background(), spec)
	if a != nil || err == nil || reasonOf(err) != hostrelaunch.FailureVerifyFailed || len(f.closed) != 1 {
		t.Fatalf("agent=%v err=%v reason=%q closed=%v", a, err, reasonOf(err), f.closed)
	}
}

// A launch environment that cannot be written is a setup failure before any
// terminal exists.
func TestEnvFileWriteFailureIsSetupFailed2172(t *testing.T) {
	for name, mutate := range map[string]func(*OrcaLaunchSpec){
		"bad entry":     func(s *OrcaLaunchSpec) { s.Env = []string{"NOT_AN_ASSIGNMENT"} },
		"NUL in prompt": func(s *OrcaLaunchSpec) { s.Prompt = "a\x00b" },
	} {
		t.Run(name, func(t *testing.T) {
			spec := launchSpec2172(t)
			mutate(&spec)
			f := newFakeOrca(t)
			a, err := orcaLauncherFor(t, f).Launch(context.Background(), spec)
			if a != nil || err == nil || reasonOf(err) != hostrelaunch.FailureSetupFailed || len(f.typed) != 0 {
				t.Fatalf("agent=%v err=%v reason=%q typed=%v", a, err, reasonOf(err), f.typed)
			}
		})
	}
}

// causeProc answers one proof-read with a chosen error, standing in for a
// future context-aware backend (the native one is context-free).
type causeProc struct {
	LinuxProcessControl
	observeErr error
	nowErr     error
	mu         sync.Mutex
	armed      bool
}

type causeHandle struct {
	ProcessHandle
	p *causeProc
}

func (h causeHandle) SignalWithin(sig syscall.Signal, t0, lease time.Duration, now func() (time.Duration, error)) (time.Duration, error) {
	return h.ProcessHandle.SignalWithin(sig, t0, lease, now)
}

func (p *causeProc) Observe(pid int) (ProcessHandle, error) {
	if p.observeErr != nil {
		return nil, p.observeErr
	}
	h, err := p.LinuxProcessControl.Observe(pid)
	if err != nil {
		return nil, err
	}
	return causeHandle{h, p}, nil
}

func (p *causeProc) Now() (time.Duration, error) {
	p.mu.Lock()
	err := p.nowErr
	armed := p.armed
	p.mu.Unlock()
	if err != nil && armed {
		return 0, err
	}
	return p.LinuxProcessControl.Now()
}

// A proof read that fails with a context error keeps that cause through the
// terminal cleanup: a pure cancellation is not VERIFY_FAILED, a pure deadline
// is TIMEOUT, and the terminal is still closed with nothing left tracked.
func TestProofReadCauseSurvivesTerminalCleanup2172(t *testing.T) {
	type tc struct {
		name       string
		observeErr error
		nowErr     error
		wantReason string
		wantIs     error
	}
	for _, c := range []tc{
		{"pin cancelled", context.Canceled, nil, "", context.Canceled},
		{"pin deadline", context.DeadlineExceeded, nil, hostrelaunch.FailureTimeout, context.DeadlineExceeded},
		{"liveness cancelled", nil, context.Canceled, "", context.Canceled},
		{"liveness deadline", nil, context.DeadlineExceeded, hostrelaunch.FailureTimeout, context.DeadlineExceeded},
		{"pin plain failure", errors.New("pidfd unavailable"), nil, hostrelaunch.FailureVerifyFailed, nil},
		{"liveness plain failure", nil, errors.New("clock unavailable"), hostrelaunch.FailureVerifyFailed, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			spec := launchSpec2172(t)
			f := newFakeOrca(t)
			proc := &causeProc{observeErr: c.observeErr, nowErr: c.nowErr}
			l := orcaLauncherFor(t, f)
			l.Proc = proc
			l.afterReads = func(int) { proc.mu.Lock(); proc.armed = true; proc.mu.Unlock() }
			a, err := l.Launch(context.Background(), spec)
			if a != nil || err == nil || len(f.closed) != 1 {
				t.Fatalf("agent=%v err=%v closed=%v (the terminal must be closed and nothing tracked)", a, err, f.closed)
			}
			if got := reasonOf(err); got != c.wantReason {
				t.Fatalf("reason=%q want %q; err=%v", got, c.wantReason, err)
			}
			if c.wantIs != nil && !errors.Is(err, c.wantIs) {
				t.Fatalf("the structured cause %v was lost: %v", c.wantIs, err)
			}
		})
	}
}

// Jibril's BLIND P3 on #2276: when closing the terminal also fails, the
// original cause stays in the chain next to the unclosed-terminal error.
func TestProofCauseSurvivesAFailedTerminalClose2172(t *testing.T) {
	spec := launchSpec2172(t)
	f := newFakeOrca(t)
	proc := &causeProc{observeErr: context.Canceled}
	l := orcaLauncherFor(t, f)
	l.Proc = proc
	run := l.Run
	l.Run = func(ctx context.Context, argv []string) (int, string, string) {
		if len(argv) > 1 && argv[1] == "close" {
			return 1, "", "orca unreachable"
		}
		return run(ctx, argv)
	}
	a, err := l.Launch(context.Background(), spec)
	var unclosed *UnclosedLaunchError
	if a == nil || err == nil || !errors.As(err, &unclosed) {
		t.Fatalf("a failed close must hand the agent back with an UnclosedLaunchError: agent=%v err=%v", a, err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("the original cause was lost behind the unclosed terminal: %v", err)
	}
}
