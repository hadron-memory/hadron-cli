// Package orcaterm is the terminal half of chat delivery (cli#907, C1): the
// bounded `orca` runner, the four terminal calls delivery may make, and the
// screen checks that decide whether a composer may be typed into.
//
// It started as a copy of internal/teamrouter's orca.go and screen.go,
// corrected for plan r2's rulings rather than moved: the router keeps its
// own files, frozen, until phase 3 retires it (C6). The corrections are the
// #906 guards (hadron-server#1940's bounded, fail-closed call; #1976 and
// #1985's final recap and notice rules) and R2 (any draft that is not our
// own is held, including the Claude "stale draft" the router typed over).
//
// Nothing here ever returns, logs or reports screen or draft text: verdicts
// carry a reason code, never the line that decided them.
package orcaterm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"
)

// Result is one `orca` invocation's outcome.
type Result struct {
	Code   int
	Stdout string
	Stderr string
}

// Runner execs the `orca` CLI. argv starts at the verb ("terminal", "list",
// …): the binary is the runner's business, so a fake never sees it.
type Runner func(ctx context.Context, argv []string) Result

// CallTimeout bounds one exec (hadron-server#1940: router.ts CALL_TIMEOUT_MS).
// A hung Orca costs one call, not the loop. It must stay at least
// MinCallTimeout: the longest legitimate call is a `terminal send
// --wait-submit`, whose wait stays under it (see SubmitWait).
const (
	CallTimeout    = 90 * time.Second
	MinCallTimeout = 75 * time.Second
	// SubmitWait is the --wait-submit observation window, in seconds: long
	// enough for a turn to start, short enough to leave the call its margin.
	SubmitWait = 30
)

// Exec is the production runner. A call is killed with SIGKILL at the bound
// (exec.CommandContext's default Cancel is Process.Kill), and ANY failure
// other than an ordinary non-zero exit — the bound, a missing binary, a pipe
// error — reads as code 1 with EMPTY stdout: output a failure invalidated
// must never reach a caller that acts on it (#1940 cycles 1 and 2).
func Exec(bin string, timeout time.Duration) Runner { return ExecEnv(bin, timeout, nil) }

// ExecEnv is Exec with an explicit child environment (name=value pairs); nil
// inherits the process's.
func ExecEnv(bin string, timeout time.Duration, env []string) Runner {
	return func(ctx context.Context, argv []string) Result {
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, argv...)
		cmd.Env = env
		// A grandchild holding the pipes open must not outlive the bound.
		cmd.WaitDelay = time.Second
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		if ctx.Err() != nil {
			return Result{Code: 1, Stderr: fmt.Sprintf("call exceeded %s and was killed", timeout)}
		}
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) && exitErr.Exited() {
				return Result{Code: exitErr.ExitCode(), Stdout: stdout.String(), Stderr: stderr.String()}
			}
			return Result{Code: 1, Stderr: "call failed: " + err.Error()}
		}
		return Result{Code: 0, Stdout: stdout.String(), Stderr: stderr.String()}
	}
}

// allowed is every `orca` call delivery may make. `orchestration send` is a
// mailbox no agent reads (hadron-server:findings:
// codex-workers-default-to-orca-orchestration-mailbox), and nothing else is
// needed: there is no clear, close or create here.
var allowed = map[string]bool{
	"terminal list": true, "terminal read": true, "terminal wait": true, "terminal send": true,
}

// Guard refuses, without running it, any call outside the four delivery
// makes. The refusal is the fail-closed shape, so every caller already
// handles it.
func Guard(run Runner) Runner {
	return func(ctx context.Context, argv []string) Result {
		if len(argv) < 2 || !allowed[argv[0]+" "+argv[1]] {
			return Result{Code: 1, Stderr: "refused: not a delivery call"}
		}
		return run(ctx, argv)
	}
}
