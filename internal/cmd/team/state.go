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

// binding is the per-worktree worker↔session binding that `session start`
// writes and `whoami` reads back after a context compaction. It lives under
// the worktree's RESOLVED git dir — `git rev-parse --git-dir`, never a
// literal .git/ path: a linked worktree's .git is a file pointing at
// <main>/.git/worktrees/<name>, and the binding must be per-worktree.
type binding struct {
	SessionID string `json:"sessionId"`
	// WorkerID is the bound Worker's id (#428). A
	// binding written by a pre-Worker CLI carries agentId/personaName instead
	// and unmarshals with WorkerID empty: a DEGRADED read, not an error —
	// `session end` still works (SessionID is all it needs, and it is the
	// recovery path), while commands that need the worker (--mentions-me)
	// say to start a new session.
	WorkerID   string `json:"workerId"`
	WorkerName string `json:"workerName"`
	WorkerRole string `json:"workerRole"`
	// AppID is the bound worker's App (#399) — the worklog home. It is what
	// lets `session log` and the provenance query run with no -m at all: the
	// worklog surface is App-addressed, and the team memory was only ever an
	// indirection back to this value. Absent on bindings written before #399
	// (those fall back to TeamMemory, then to the degraded diagnostics).
	AppID string `json:"appId,omitempty"`
	// AgentID is the role-agent behind the casting — informational (the
	// server stamps Session.agentId itself from the worker).
	AgentID   string `json:"agentId"`
	Server    string `json:"server"`
	StartedAt string `json:"startedAt"`
	// AppBound records whether startSession was given an appRef — from -m, or
	// from an ambient --app context. It decides which remedy a worklog
	// diagnostic can honestly offer: `session log -m <mem>` can only work on a
	// session that IS App-bound (recordTeamWork refuses others with
	// SESSION_NOT_IN_APP, and no mutation can bind a session after the fact).
	// Absent on bindings written by older CLIs, where false means "unknown" —
	// so the diagnostics phrase the unbound branch as a possibility, not a
	// certainty.
	AppBound bool `json:"appBound,omitempty"`
	// TeamMemory is the team App memory holding the worklog collection
	// (`session start -m`); log/list read it back so worklog writes and the
	// provenance query need no per-call flag.
	TeamMemory string `json:"teamMemory"`
	// Tool, Repo, and Model mirror the startSession provenance inputs: Tool
	// flows into every worklog row (a flat queried field, D13/D14), Repo
	// qualifies bare `--pr 371`-style refs, and Model becomes the chat
	// identity of worker-posted messages.
	Tool  string `json:"tool"`
	Repo  string `json:"repo"`
	Model string `json:"model"`
	// ChatSeenSeq is the team-chat watermark this worktree has actually READ
	// (#474): `chat read` records the seq it returned, `session log` compares
	// against it to say how much landed while you were heads-down.
	//
	// NIL means never read through THIS binding, which is a distinct and
	// louder signal than "nothing new" — it is the state Gil was in when a
	// ratified commit-trailer change reached the chat four hours before he
	// merged with the retired form. So callers must branch on it rather than
	// treating it as seq 0.
	//
	// A POINTER for exactly that reason (PR #493 review): on a bare int, 0 had
	// to mean both "never read" and "read a chat that was empty", so reading an
	// empty team chat could never be recorded and every later `session log`
	// nagged forever. Only an unfiltered read of the binding's OWN App records
	// here — see the write site in chat.go for why both qualifiers are load-
	// bearing.
	ChatSeenSeq *int `json:"chatSeenSeq,omitempty"`
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

// recordChatWatermark stores the team-chat watermark WITHOUT rewriting the
// rest of the binding from a stale snapshot (PR #493 review).
//
// Every other binding write follows a user action that just read the file.
// This one does not: it lands after a paginated fetch and a full render, so
// the snapshot it started from can be seconds old — and a worktree with two
// agents in it is the normal case here, not a hypothetical
// (dev:findings:concurrent-agent-sessions-share-one-worktree). Writing the
// whole snapshot back would silently undo whatever landed in between: a
// `session log --pr` that appended a PR number, or worse, a `session end` that
// removed the file, which a wholesale write would RESURRECT — leaving a
// binding for a session the server has already closed.
//
// So: re-read, confirm it is still the same session, set the one field, write.
// This narrows the race to the gap between this read and this write rather
// than closing it — there is no lock on the binding file, and giving it one is
// a change for EVERY writer including clearBinding (a lock three of four
// writers respect is not a lock), with stale-lock and timeout policy of its
// own. Tracked as #499; the CAS sketch there is probably the better fit.
// Best-effort throughout: the caller
// has already delivered the messages, and a failed bookkeeping write must not
// turn that into an error.
func recordChatWatermark(ctx context.Context, sessionID string, seq int) {
	fresh, _, err := readBinding(ctx)
	if err != nil || fresh == nil {
		return // gone (a concurrent `session end`), or unreadable — do not recreate it.
	}
	if fresh.SessionID != sessionID {
		return // a different session owns this worktree now.
	}
	if fresh.ChatSeenSeq != nil && seq <= *fresh.ChatSeenSeq {
		return // someone read further while we were rendering.
	}
	fresh.ChatSeenSeq = &seq
	_, _ = writeBinding(ctx, fresh)
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
