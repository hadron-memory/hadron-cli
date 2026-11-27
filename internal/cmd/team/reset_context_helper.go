package team

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/config"
)

// hadron-cli#3299: on the exit-now branch of `team session reset-context` (the checkpoint and the relaunch stamp
// are written, the agent's own pid could not be pinned) the command finishes the reset through a HOST-CONFIGURED
// exit helper, so one command works on every host.
//
// The helper is named in user-level host configuration: a file `session-exit-helper` in the Hadron config
// directory holding ONE absolute path. A file, not an environment variable, so an agent that is already running
// gets it without a wrapper restart; never read from a repository, a binding or the server. The helper is run
// without a shell as `<helper> --checkpoint <node URN>`, with a bounded timeout, its output discarded (so --json
// stdout stays one receipt and nothing the helper prints reaches the receipt or a reason).
//
// The helper ends the AGENT only, never the session (cor:agt:050:27: a reset is a shed, the binding and session
// stay and the host relaunches the agent). Its contract: it returns promptly, and ends the agent only after this
// command has returned and the agent is idle.

const (
	// exitHelperFile is the config file naming the helper.
	exitHelperFile = "session-exit-helper"
	// agentHelperPending: the helper is validated and is run after this receipt. NOT success: the agent is ended
	// later, by the helper, and the outcome is not in this receipt.
	agentHelperPending = "helper-pending"

	exitHelperReadLimit = 4096
)

// exitHelperTimeout bounds one helper run. It is a variable so a test can shorten it.
var exitHelperTimeout = 10 * time.Second

// helperPendingReason is the receipt's reason on helper-pending. It must not say or imply that the session or the
// binding ends (cor:agt:050:27).
const helperPendingReason = "your exit helper is asked to end this agent once it is idle: end your turn now and run nothing more; the session and binding stay and the host relaunches the agent"

// exitHelperRefusal is the receipt's reason when a configured helper is refused. why is one of a fixed set of
// phrases, never a path or the helper's output.
func exitHelperRefusal(why string) string {
	return "the exit helper named in " + exitHelperFile + " was not run: " + why + "; exit your agent yourself"
}

// lookupExitHelper finds the configured helper. configured is false when there is none (the behaviour as before
// #3299); refusal is non-empty when one is named and refused (fixed phrases only).
func lookupExitHelper() (path, refusal string, configured bool) {
	if runtime.GOOS == "windows" {
		return "", "", false
	}
	dir, err := config.Dir()
	if err != nil {
		return "", "", false
	}
	cfg := filepath.Join(dir, exitHelperFile)
	fi, err := os.Lstat(cfg)
	if errors.Is(err, os.ErrNotExist) {
		return "", "", false
	}
	if err != nil {
		return "", "the configuration file cannot be read", true
	}
	if why := unsafeFile(fi); why != "" {
		return "", "the configuration file " + why, true
	}
	named, why := readOnePath(cfg)
	if why != "" {
		return "", why, true
	}
	if !filepath.IsAbs(named) {
		return "", "the helper path is not absolute", true
	}
	hfi, err := os.Lstat(named)
	if errors.Is(err, os.ErrNotExist) {
		return "", "the helper is missing", true
	}
	if err != nil {
		return "", "the helper cannot be read", true
	}
	if why := unsafeFile(hfi); why != "" {
		return "", "the helper " + why, true
	}
	if hfi.Mode().Perm()&0o100 == 0 {
		return "", "the helper is not executable by you", true
	}
	return named, "", true
}

// readOnePath reads the config file (without following a symlink, so the checks above hold for what is read) and
// returns the one path it holds.
func readOnePath(cfg string) (string, string) {
	f, err := openNoFollow(cfg)
	if err != nil {
		return "", "the configuration file cannot be read"
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, exitHelperReadLimit+1))
	if err != nil || len(raw) > exitHelperReadLimit {
		return "", "the configuration file cannot be read"
	}
	var paths []string
	for _, l := range strings.Split(string(raw), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			paths = append(paths, l)
		}
	}
	if len(paths) != 1 || strings.ContainsRune(paths[0], 0) {
		return "", "the configuration file must name exactly one path"
	}
	return paths[0], ""
}

// runExitHelper runs the helper without a shell, bounded, with no standard streams (so nothing it prints reaches
// this command's receipt) and without the API token in its environment.
func runExitHelper(path, checkpoint string) error {
	ctx, cancel := context.WithTimeout(context.Background(), exitHelperTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--checkpoint", checkpoint)
	cmd.Env = envWithout(os.Environ(), "HADRON_TOKEN")
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("timed out")
		}
		return fmt.Errorf("failed")
	}
	return nil
}

func envWithout(env []string, name string) []string {
	out := make([]string, 0, len(env))
	for _, e := range env {
		if !strings.HasPrefix(e, name+"=") {
			out = append(out, e)
		}
	}
	return out
}

// exitHelper is a validated helper as the "agent" finishResetContext ends: signal runs it, after the receipt.
type exitHelper struct {
	path, checkpoint string
}

func (h exitHelper) signal() error { return runExitHelper(h.path, h.checkpoint) }
func (h exitHelper) close()        {}

func (exitHelper) receiptFailed() string {
	return "hadron: team session reset-context: the receipt could not be written, so the exit helper was not run: exit your agent yourself; the host relaunches the worker."
}

func (exitHelper) signalFailed() string {
	return "hadron: team session reset-context: the exit helper failed or timed out after the receipt: exit your agent yourself; the host relaunches the worker."
}
