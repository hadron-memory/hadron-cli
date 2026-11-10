package team

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
)

const (
	// healthStatusFloor is the least time the status read is given: the
	// pre-#1838 fixed deadline, so a small host is judged exactly as before.
	healthStatusFloor = 2 * time.Minute
	// healthStatusPerWorker is the allowance added per registered worker: the
	// read classifies each worker's terminal one after another, so its time
	// grows with the roster (#1838: a full roster outran the fixed 2 minutes).
	healthStatusPerWorker = 5 * time.Second
)

type healthStatusTimeoutKey struct{}

// healthStatusDeadline sizes the status read from the registered worker count,
// never below the floor.
func healthStatusDeadline(workers int) time.Duration {
	if d := time.Duration(workers) * healthStatusPerWorker; d > healthStatusFloor {
		return d
	}
	return healthStatusFloor
}

// withHealthStatusTimeout carries the sweep's chosen deadline to the status
// read without widening the reader boundary.
func withHealthStatusTimeout(ctx context.Context, d time.Duration) context.Context {
	return context.WithValue(ctx, healthStatusTimeoutKey{}, d)
}

func healthStatusTimeout(ctx context.Context) time.Duration {
	if d, ok := ctx.Value(healthStatusTimeoutKey{}).(time.Duration); ok && d > 0 {
		return d
	}
	return healthStatusFloor
}

// errHealthStatusTimeout is the deadline class: the status read ran out of time
// (distinct from the child failing or Orca being unreachable).
type errHealthStatusTimeout struct{ after time.Duration }

func (e errHealthStatusTimeout) Error() string {
	return "terminal status timed out after " + e.after.String()
}

// The status command's --config boundary is a separately gated dependency
// of #1181. Only its JSON leaves this subprocess; never inspect its screen or
// diagnostics, or pass a key value through argv/environment.
func readHealthStatusCommand(ctx context.Context, path string, cfg *hostpoller.Config) ([]byte, int, error) {
	bin, err := os.Executable()
	if err != nil {
		return nil, 1, errors.New("status executable unavailable")
	}
	key, err := cfg.ReadHostKey()
	if err != nil {
		return nil, 3, errors.New("host key unavailable")
	}
	return runHealthStatusCommandEnv(ctx, bin, path, hostpoller.ScrubbedEnv(os.Environ(), key))
}

func runHealthStatusCommandEnv(ctx context.Context, bin, path string, env []string) ([]byte, int, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, 1, errors.New("status config unavailable")
	}
	limit := healthStatusTimeout(ctx)
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "orca", "terminal", "status", "--all", "--json", "--config", abs)
	cmd.Env = env
	out := healthStatusBuffer{onOverflow: cancel}
	cmd.Stdout = &out
	// stderr is deliberately discarded, including a child's credential, screen
	// detail or raw upstream diagnostic. The caller reports fixed classes only.
	if err := cmd.Run(); err != nil {
		// Only our own deadline (not the caller's cancellation, not an output
		// overflow) is the timeout class.
		if isHealthStatusTimeout(ctx.Err(), parent.Err(), out.overflowed) {
			return nil, 1, errHealthStatusTimeout{limit}
		}
		var status *exec.ExitError
		if errors.As(err, &status) {
			return nil, status.ExitCode(), errors.New("status command failed")
		}
		return nil, 1, errors.New("status command unavailable")
	}
	return out.Bytes(), 0, nil
}

// isHealthStatusTimeout reports whether a failed status read ran out of OUR
// deadline: the sized one expired, the caller had not cancelled or hit its own
// deadline, and the read was not already killed for oversized output (an
// overflow that races the deadline stays an ordinary failure).
func isHealthStatusTimeout(childErr, parentErr error, overflowed bool) bool {
	return errors.Is(childErr, context.DeadlineExceeded) && parentErr == nil && !overflowed
}

// Bound even a broken status command's stdout before decoding it.
type healthStatusBuffer struct {
	bytes.Buffer
	onOverflow func()
	overflowed bool
}

func (b *healthStatusBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 8<<20 {
		b.overflowed = true
		if b.onOverflow != nil {
			b.onOverflow()
		}
		return 0, errors.New("status output exceeds limit")
	}
	return b.Buffer.Write(p)
}
