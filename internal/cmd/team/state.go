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
	// Rev counts the writes that have landed (#499). It is NOT load-bearing:
	// serialization comes from the lock in withBindingLock, and this started
	// life as the version field of a compare-and-swap scheme that could not be
	// built on a plain file (see docs/plans/binding-write-serialization.md).
	//
	// Kept because it is free and useful: it makes concurrent behaviour
	// observable in tests and tells anyone reading a binding by hand how much
	// has happened to it. Absent (0) on a binding written before #499, and no
	// migration is needed — the first update reads 0 and writes 1.
	Rev int `json:"rev,omitempty"`
	// PRNumbers is `session log --pr`'s local history — the server's
	// Session.prNumber holds only the latest (#932), so whoami keeps the
	// full list. TODO(#369 slice 3): the worklog collection becomes the
	// durable multi-ref record.
	PRNumbers []int `json:"prNumbers"`
}

const bindingFileName = "hadron-team-session.json"

// GitDirEnv overrides the git call that locates the worktree binding — for
// tests, and for environments with no git binary. Exported so the test
// sandbox names the same string this reads rather than a copy of it: two
// spellings of one well-known key is how a sandbox silently stops sandboxing
// (hadron-cli#498).
const GitDirEnv = "HADRON_TEAM_GIT_DIR"

// gitDir resolves the current worktree's git dir. GitDirEnv overrides the git
// call (tests, and environments without a git binary).
func gitDir(ctx context.Context) (string, error) {
	if d := os.Getenv(GitDirEnv); d != "" {
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
	data, err := marshalBinding(b)
	if err != nil {
		return "", err
	}
	// Under the lock like every other writer (#499), so a bind cannot land in
	// the middle of another command's read-modify-write.
	// Atomic (same-dir temp + rename): this is the durable recovery record —
	// a crash mid-write must never leave whoami/end with truncated JSON.
	//
	// Unconditional, and only `session start` may call it: establishing a
	// binding REPLACES whatever was there, which is the documented behaviour
	// of a fresh bind. Every other writer goes through updateBinding (#499).
	if err := withBindingLock(ctx, func() error {
		return config.WriteFileAtomic(path, data, 0o600)
	}); err != nil {
		return "", fmt.Errorf("writing session binding %s: %w", path, err)
	}
	return path, nil
}

// errNoBinding is returned by updateBinding when the worktree has no binding.
// A distinct value because the ONE thing an update must never do is create
// one: `session end` removes the file, and a concurrent read-modify-write that
// recreated it would leave the worktree bound to a session the server has
// already closed, which whoami would then report as live.
var errNoBinding = errors.New("no session binding for this worktree")

const bindingLockFileName = "hadron-team-session.lock"

// withBindingLock runs fn while holding an exclusive lock on the worktree's
// binding, serializing every writer against every other (#499).
//
// WHY A LOCK, having set out to do compare-and-swap. A plain file offers no
// atomic compare-and-swap: WriteFileAtomic is temp-plus-rename, an
// UNCONDITIONAL replace. The best an optimistic scheme can do is write and
// then read back — and reading back only proves nobody had clobbered you by
// the moment you looked, not that your write survived. Measured: six
// concurrent writers, all six reporting success, two edits landing. The
// stale-snapshot case it did fix is real and is the one the issue describes,
// but it is not the general case.
//
// The objection to locking was stale locks. It does not apply to flock(2) /
// LockFileEx, which the kernel releases when the descriptor closes, INCLUDING
// on process death. No age heuristic, no timeout policy, nothing to clean up.
//
// The lock lives on its OWN file, never on the binding. WriteFileAtomic
// renames a fresh inode over the binding, and a lock is held on an inode — so
// a lock taken on the binding itself would be silently replaced mid-critical-
// section by the very write it was meant to serialize. The lock file is
// created once and never removed or renamed; an empty leftover is inert,
// which is precisely why this has no recovery path.
//
// EVERY writer goes through here, clearBinding included: a lock three of four
// writers respect is not a lock.
func withBindingLock(ctx context.Context, fn func() error) error {
	dir, err := gitDir(ctx)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, bindingLockFileName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) // #nosec G304 — derived from the worktree's own git dir
	if err != nil {
		return fmt.Errorf("opening session binding lock %s: %w", path, err)
	}
	defer func() { _ = f.Close() }() // closing releases the lock even if unlock below is skipped
	if err := lockFile(f); err != nil {
		return fmt.Errorf("locking session binding %s: %w", path, err)
	}
	defer func() { _ = unlockFile(f) }()
	return fn()
}

// updateBinding applies mutate to the binding under an exclusive lock, and is
// the only way any command may modify an existing one (#499).
//
// Before this, two read-modify-write sequences could interleave and the later
// writer silently discarded the earlier one's edit — `session log --pr`
// appended a PR number, and a watermark write that began from an older
// snapshot wrote its own copy back with the PR number simply gone. No error,
// no torn file.
//
// Two properties, and only one of them comes from the lock:
//
//   - SERIALIZED. The read, the mutation and the write happen with no other
//     writer in between, so the mutation always sees current state. This is
//     why the mutation itself must do any membership or precondition checks —
//     the caller's snapshot from before the lock is stale by definition.
//   - NEVER CREATES. An update refuses a binding that is not there, which is
//     structural rather than a consequence of locking. `session end` removes
//     the file; a read-modify-write that recreated it would leave the worktree
//     bound to a session the server has already closed, and whoami would
//     report it as live.
func updateBinding(ctx context.Context, mutate func(*binding) error) error {
	return withBindingLock(ctx, func() error {
		current, path, err := readBinding(ctx)
		if err != nil {
			return err
		}
		if current == nil {
			return errNoBinding
		}
		next := *current
		// The slice too: a struct copy shares the backing array, so a mutation
		// that appends could otherwise reach the caller's snapshot.
		next.PRNumbers = append([]int(nil), current.PRNumbers...)
		if err := mutate(&next); err != nil {
			return err
		}
		next.Rev = current.Rev + 1

		data, err := marshalBinding(&next)
		if err != nil {
			return err
		}
		if err := config.WriteFileAtomic(path, data, 0o600); err != nil {
			return fmt.Errorf("writing session binding %s: %w", path, err)
		}
		return nil
	})
}

// marshalBinding is the ONE encoder both the writer and its verification read
// go through. Two spellings would make the comparison fail on formatting and
// retry forever.
func marshalBinding(b *binding) ([]byte, error) {
	if b.PRNumbers == nil {
		b.PRNumbers = []int{}
	}
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// recordChatWatermark stores the team-chat watermark (#474).
//
// This is the write that motivated #499: every other binding write follows a
// user action that just read the file, while this one lands after a paginated
// fetch and a full render, so the snapshot it began from can be SECONDS old —
// and two agents in one worktree is the normal case here, not a hypothetical
// (dev:findings:concurrent-agent-sessions-share-one-worktree).
//
// updateBinding now supplies what the hand-rolled re-read approximated: it
// refuses a write it did not produce and reapplies to the winner's state, and
// it never recreates a binding a concurrent `session end` removed.
//
// The session check stays INSIDE the mutation, where it is re-evaluated on
// every attempt: a retry re-reads, and by then the worktree may belong to a
// different session, in which case this watermark is not ours to record.
//
// Best-effort throughout: the caller has already delivered the messages, and a
// failed bookkeeping write must not turn that into an error.
func recordChatWatermark(ctx context.Context, sessionID string, seq int) {
	_ = updateBinding(ctx, func(b *binding) error {
		if b.SessionID != sessionID {
			return errWatermarkNotOurs // a different session owns this worktree now
		}
		if b.ChatSeenSeq != nil && seq <= *b.ChatSeenSeq {
			return errWatermarkNotOurs // someone read further while we were rendering
		}
		b.ChatSeenSeq = &seq
		return nil
	})
}

// errWatermarkNotOurs aborts the watermark update without writing. A sentinel
// rather than a nil-return-no-op, because updateBinding must be able to tell
// "nothing to do" from "mutation succeeded" — the latter writes.
var errWatermarkNotOurs = errors.New("watermark not applicable to the current binding")

// errPRAlreadyKnown aborts the PR append when the number is already on the
// binding — checked inside the mutation, against what is on disk now.
var errPRAlreadyKnown = errors.New("pr already recorded on this binding")

func clearBinding(ctx context.Context) error {
	path, err := bindingPath(ctx)
	if err != nil {
		return err
	}
	// Under the lock too. This is the writer the issue singles out: a lock
	// three of four writers respect is not a lock, and removal is exactly the
	// operation a concurrent read-modify-write must not straddle.
	if err := withBindingLock(ctx, func() error {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}); err != nil {
		return fmt.Errorf("removing session binding %s: %w", path, err)
	}
	return nil
}
