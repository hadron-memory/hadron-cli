package team

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/config"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// binding is the per-worktree persona↔session binding that `session start`
// writes and `whoami` reads back after a context compaction. It lives under
// the worktree's RESOLVED git dir — `git rev-parse --git-dir`, never a
// literal .git/ path: a linked worktree's .git is a file pointing at
// <main>/.git/worktrees/<name>, and the binding must be per-worktree.
type binding struct {
	SessionID   string `json:"sessionId"`
	AgentID     string `json:"agentId"`
	AgentURN    string `json:"agentUrn"`
	PersonaName string `json:"personaName"`
	PersonaRole string `json:"personaRole"`
	Server      string `json:"server"`
	StartedAt   string `json:"startedAt"`
	// TeamMemory is the team App memory holding the worklog collection
	// (`session start -m`); log/list read it back so worklog writes and the
	// provenance query need no per-call flag.
	TeamMemory string `json:"teamMemory"`
	// Tool and Repo mirror the startSession provenance inputs: Tool flows
	// into every worklog row (a flat queried field, D13/D14) and Repo
	// qualifies bare `--pr 371`-style refs.
	Tool string `json:"tool"`
	Repo string `json:"repo"`
	// PRNumbers is `session log --pr`'s local history — the server's
	// Session.prNumber holds only the latest (#932), so whoami keeps the
	// full list. TODO(#369 slice 3): the worklog collection becomes the
	// durable multi-ref record.
	PRNumbers []int `json:"prNumbers"`
}

const bindingFileName = "hadron-team-session.json"

// gitDir resolves the current worktree's git dir. HADRON_TEAM_GIT_DIR
// overrides the git call (tests, and environments without a git binary).
func gitDir(ctx context.Context) (string, error) {
	if d := os.Getenv("HADRON_TEAM_GIT_DIR"); d != "" {
		return d, nil
	}
	out, err := exec.CommandContext(ctx, "git", "rev-parse", "--git-dir").Output()
	if err != nil {
		detail := err.Error()
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			detail = strings.TrimSpace(string(exitErr.Stderr))
		}
		return "", exitcode.Newf(exitcode.Usage, "not inside a git worktree — a team session binds to one (%s)", detail)
	}
	return filepath.Abs(strings.TrimSpace(string(out)))
}

func bindingPath(ctx context.Context) (string, error) {
	dir, err := gitDir(ctx)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, bindingFileName), nil
}

// readBinding returns the worktree's binding and its path, or a nil binding
// when none is recorded.
func readBinding(ctx context.Context) (*binding, string, error) {
	path, err := bindingPath(ctx)
	if err != nil {
		return nil, "", err
	}
	data, err := os.ReadFile(path) // #nosec G304 — path is derived from the worktree's own git dir
	if errors.Is(err, os.ErrNotExist) {
		return nil, path, nil
	}
	if err != nil {
		return nil, path, fmt.Errorf("reading session binding %s: %w", path, err)
	}
	var b binding
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, path, fmt.Errorf("session binding %s is corrupt (%v) — remove it and start a new session", path, err)
	}
	// Keep the --json contract's slice non-null even for a file written
	// without the key.
	if b.PRNumbers == nil {
		b.PRNumbers = []int{}
	}
	return &b, path, nil
}

func writeBinding(ctx context.Context, b *binding) (string, error) {
	path, err := bindingPath(ctx)
	if err != nil {
		return "", err
	}
	if b.PRNumbers == nil {
		b.PRNumbers = []int{}
	}
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return "", err
	}
	// Atomic (same-dir temp + rename): this is the durable recovery record —
	// a crash mid-write must never leave whoami/end with truncated JSON.
	if err := config.WriteFileAtomic(path, append(data, '\n'), 0o600); err != nil {
		return "", fmt.Errorf("writing session binding %s: %w", path, err)
	}
	return path, nil
}

func clearBinding(ctx context.Context) error {
	path, err := bindingPath(ctx)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing session binding %s: %w", path, err)
	}
	return nil
}
