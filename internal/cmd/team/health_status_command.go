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
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "orca", "terminal", "status", "--all", "--json", "--config", abs)
	cmd.Env = env
	out := healthStatusBuffer{onOverflow: cancel}
	cmd.Stdout = &out
	// stderr is deliberately discarded, including a child's credential, screen
	// detail or raw upstream diagnostic. The caller reports fixed classes only.
	if err := cmd.Run(); err != nil {
		var status *exec.ExitError
		if errors.As(err, &status) {
			return nil, status.ExitCode(), errors.New("status command failed")
		}
		return nil, 1, errors.New("status command unavailable")
	}
	return out.Bytes(), 0, nil
}

// Bound even a broken status command's stdout before decoding it.
type healthStatusBuffer struct {
	bytes.Buffer
	onOverflow func()
}

func (b *healthStatusBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 8<<20 {
		if b.onOverflow != nil {
			b.onOverflow()
		}
		return 0, errors.New("status output exceeds limit")
	}
	return b.Buffer.Write(p)
}
