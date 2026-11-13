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

// gitDirOrPlain resolves the current worktree's git dir (GitDirEnv overrides
// the git call: tests, and environments without a git binary), and says
// whether a failure was "this is not a git checkout" (cli#1391). Only that
// answer — git's own "not a git repository", or no git binary at all — makes
// a directory PLAIN. Any other
// git failure (a dubious-ownership refusal, a corrupt repository) stays the
// hard errNoWorktree it always was: reading a real worktree as a plain
// directory would put its binding in the wrong place.
func gitDirOrPlain(ctx context.Context) (dir string, plain bool, err error) {
	if d := os.Getenv(GitDirEnv); d != "" {
		return d, false, nil
	}
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--git-dir")
	// git localizes "not a git repository" (gettext): the C locale keeps the
	// one message this classifies on in English (Gloria's BLIND F1, #1968).
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANGUAGE=C")
	out, err := cmd.Output()
	if err != nil {
		detail := err.Error()
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			detail = strings.TrimSpace(string(exitErr.Stderr))
		}
		// The DISCOVERY form only: git also says "not a git repository:
		// <path>" for a worktree whose admin dir is gone or a bad GIT_DIR —
		// a real checkout, which must stay the hard error (Rico's §10 P3,
		// #1968). The C locale above keeps this wording stable.
		plain = errors.Is(err, exec.ErrNotFound) || strings.Contains(strings.ToLower(detail), "not a git repository (or any of the parent directories)")
		return "", plain, exitcode.Newf(exitcode.Usage, "%w — a team session binds to one (%s)", errNoWorktree, detail)
	}
	d, err := filepath.Abs(strings.TrimSpace(string(out)))
	return d, false, err
}

// plainDirCacheName is the binding cache's home in a directory that is not a
// git checkout (cli#1391, ruling 0230 = B): a hidden directory in the
// directory the command runs from, holding the same binding and lock files a
// worktree keeps in its git dir. The CURRENT directory only — never a parent —
// so a command can never pick up the binding of a directory above it.
const plainDirCacheName = ".hadron-team"

// bindingLoc is where this context's binding cache lives.
type bindingLoc struct {
	dir   string // the directory holding the binding and lock files
	plain bool   // a plain directory's cache, not a git dir (cli#1391)
}

// locateBinding finds the binding cache's directory.
//
// In a git worktree it is the git dir, exactly as before. In a plain
// directory it is plainDirCacheName in the current directory, and the two
// modes differ on purpose:
//
//   - a READ (writable false) sees a plain directory's cache only when the
//     binding file is already there; otherwise it gets errNoWorktree, as
//     every reader always has, so the server fallbacks (whoami, signing,
//     the unbound-write refusal) behave exactly as they did;
//   - a BINDING WRITE (writable true: start, resume, shed's stamp) gets the
//     cache directory, which withBindingLock creates.
func locateBinding(ctx context.Context, writable bool) (bindingLoc, error) {
	dir, plain, err := gitDirOrPlain(ctx)
	if err == nil {
		return bindingLoc{dir: dir}, nil
	}
	if !plain {
		return bindingLoc{}, err
	}
	cwd, werr := os.Getwd()
	if werr != nil {
		return bindingLoc{}, err
	}
	loc := bindingLoc{dir: filepath.Join(cwd, plainDirCacheName), plain: true}
	if writable {
		return loc, nil
	}
	if _, serr := os.Stat(filepath.Join(loc.dir, bindingFileName)); serr != nil {
		return bindingLoc{}, err // no cache here: the reader's errNoWorktree, unchanged
	}
	return loc, nil
}

// errNoWorktree marks "there is no git worktree here" as a DISTINCT condition
// rather than one more failure string.
//
// For the commands that write a binding it is fatal — there is nowhere to put
// one. For `session whoami` it is not: since #623 the server answers, and the
// callers who most need that answer are exactly the ones with no worktree —
// a non-coding role, or Cowork. Treating this as a hard error is what made a
// worktree a precondition for asking "what am I driving?", which is the
// coupling #623 exists to remove.
var errNoWorktree = errors.New("not inside a git worktree")

func bindingPath(ctx context.Context) (string, error) {
	loc, err := locateBinding(ctx, false)
	if err != nil {
		return "", err
	}
	return filepath.Join(loc.dir, bindingFileName), nil
}

// readBinding returns the worktree's binding and its path, or a nil binding
// when none is recorded.
// readBinding is how a command consumes the worktree binding, so it is also
// where a call through a binding learns that its session ended (#914): the
// first read of a session binding per command runs that check.
func readBinding(ctx context.Context) (*binding, string, error) {
	b, path, err := readBindingFile(ctx)
	if err == nil && b != nil {
		noteEndedBinding(ctx, b)
	}
	return b, path, err
}

// readBindingFile reads the binding with no server check. For paths that read
// it without consuming it, such as AuthoredWriteContext's per-request look,
// which checks only when it actually attaches the session.
func readBindingFile(ctx context.Context) (*binding, string, error) {
	path, err := bindingPath(ctx)
	if err != nil {
		return nil, "", err
	}
	b, err := readBindingAt(path)
	return b, path, err
}

// readBindingAt reads a binding file, or nil when there is none.
func readBindingAt(path string) (*binding, error) {
	data, err := os.ReadFile(path) // #nosec G304 — path is derived from the worktree's git dir or the plain directory's cache
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading session binding %s: %w", path, err)
	}
	var b binding
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("session binding %s is corrupt (%v) — remove it and start a new session", path, err)
	}
	// Keep the --json contract's slice non-null even for a file written
	// without the key.
	if b.PRNumbers == nil {
		b.PRNumbers = []int{}
	}
	return &b, nil
}

// readBindingForBind reads the binding a bind is about to replace, from where
// that bind would write it: in a plain directory with no cache yet that is
// "no binding" (nil), not errNoWorktree, so start and resume can run there
// (cli#1391). Plain reports which kind of directory it is.
func readBindingForBind(ctx context.Context) (b *binding, plain bool, err error) {
	loc, err := locateBinding(ctx, true)
	if err != nil {
		return nil, false, err
	}
	if !loc.plain {
		b, _, err = readBinding(ctx)
		return b, false, err
	}
	b, err = readBindingAt(filepath.Join(loc.dir, bindingFileName))
	if err == nil && b != nil {
		noteEndedBinding(ctx, b)
	}
	return b, true, err
}

// errDirectoryHeld refuses a second worker's bind in a plain directory that
// already caches another worker's binding (ruling 0230): a directory holds ONE
// worker's binding, refused rather than shared, so two workers can never read
// each other's binding there. A worktree keeps its documented replace.
func errDirectoryHeld(path string, held *binding) error {
	return exitcode.Newf(exitcode.Conflict,
		"this directory already holds worker %s's session binding (%s, session %s): a plain directory binds one worker, and another worker's binding is never shared or replaced here. Start this worker from its own directory; or, if %s's session is over, run `hadron team session end` here (or remove %s)",
		held.WorkerName, path, held.SessionID, held.WorkerName, path)
}

// heldByOtherWorker reports whether a plain directory's cached binding may
// name a different worker than the one being bound. A binding with no worker
// id cannot show it is the same worker, so it counts as held — the safe
// direction, as the start's pre-check reads it (Gloria's BLIND F2, #1968).
func heldByOtherWorker(held *binding, workerID string) bool {
	return held != nil && (strings.TrimSpace(held.WorkerID) == "" || held.WorkerID != workerID)
}

func writeBinding(ctx context.Context, b *binding) (string, error) {
	loc, err := locateBinding(ctx, true)
	if err != nil {
		return "", err
	}
	path := filepath.Join(loc.dir, bindingFileName)
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
	var held *binding
	if err := withBindingLockAt(loc, func() error {
		if loc.plain {
			// Under the lock, against a fresh read: the backstop for a bind
			// whose worker was only known after the server answered (a launch
			// grant, a resume), and for two starts racing in one directory.
			cur, rerr := readBindingAt(path)
			if rerr != nil {
				return rerr
			}
			if heldByOtherWorker(cur, b.WorkerID) {
				held = cur
				return errBindingHeld
			}
		}
		return config.WriteFileAtomic(path, data, 0o600)
	}); err != nil {
		if held != nil {
			return "", errDirectoryHeld(path, held)
		}
		return "", fmt.Errorf("writing session binding %s: %w", path, err)
	}
	return path, nil
}

var errBindingHeld = errors.New("directory holds another worker's binding")

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
	// Read mode: an existing cache only. Only a bind creates a plain
	// directory's cache, so an update, a clear or a check in a plain
	// directory with none gets errNoWorktree, as it always has.
	loc, err := locateBinding(ctx, false)
	if err != nil {
		return err
	}
	return withBindingLockAt(loc, fn)
}

// withBindingLockAt is withBindingLock at a located cache. A plain
// directory's cache directory is created here, owner-only, and only for a
// bind (locateBinding's writable mode is the only way to reach a missing one).
func withBindingLockAt(loc bindingLoc, fn func() error) error {
	if loc.plain {
		if err := os.MkdirAll(loc.dir, 0o700); err != nil {
			return fmt.Errorf("creating the session binding cache %s: %w", loc.dir, err)
		}
	}
	path := filepath.Join(loc.dir, bindingLockFileName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) // #nosec G304 — derived from the worktree's git dir or the plain directory's cache
	if err != nil {
		return fmt.Errorf("opening session binding lock %s: %w", path, err)
	}
	defer func() { _ = f.Close() }() // closing releases the lock even if unlock below is skipped
	if err := lockFile(f); err != nil {
		return fmt.Errorf("acquiring the session binding lock %s: %w", path, err)
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
		// The raw read: never a server round trip under the lock (#914).
		current, path, err := readBindingFile(ctx)
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

// marshalBinding is the one encoder every binding write goes through: stable
// JSON formatting, and a non-nil PRNumbers so the --json contract renders []
// rather than null.
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
// The session check stays INSIDE the mutation, which runs under the lock
// against a fresh read: by then the worktree may belong to a different session
// — `session start` may have replaced the binding while this command was
// rendering — in which case this watermark is not ours to record.
//
// Best-effort throughout: the caller has already delivered the messages, and a
// failed bookkeeping write must not turn that into an error.
//
// It REPORTS whether the worktree is still bound to sessionID — checked under
// the lock, against a fresh read — because `team chat read`'s server-side mark
// (#1353) must not go out for a session that `session end` or a rebind retired
// while the messages were rendering (PR #732, @copilot). "Someone already read
// further" is still ours; a removed binding, another session, or a failed
// write is not, and the mark is then skipped — the loss-safe direction, a
// duplicate nudge at worst.
func recordChatWatermark(ctx context.Context, sessionID string, seq int) (stillOurs bool) {
	err := updateBinding(ctx, func(b *binding) error {
		if b.SessionID != sessionID {
			return errBindingChangedSession // a different session owns this worktree now
		}
		if b.ChatSeenSeq != nil && seq <= *b.ChatSeenSeq {
			return errWatermarkNotOurs // someone read further while we were rendering
		}
		b.ChatSeenSeq = &seq
		return nil
	})
	return err == nil || errors.Is(err, errWatermarkNotOurs)
}

// bindingIsSession reports, under the binding lock against a fresh read,
// whether the worktree is still bound to sessionID. A command that read the
// binding earlier asks this immediately before acting for that session on the
// server, so a concurrent `session start --force` or `session end` is not
// acted through (PR #732, @copilot). The window between this check and the
// server call remains; it is the same one `team chat read`'s mark has.
func bindingIsSession(ctx context.Context, sessionID string) bool {
	ours := false
	_ = withBindingLock(ctx, func() error {
		cur, _, err := readBindingFile(ctx)
		ours = err == nil && cur != nil && cur.SessionID == sessionID
		return nil
	})
	return ours
}

// errWatermarkNotOurs aborts the watermark update without writing. A sentinel
// rather than a nil-return-no-op, because updateBinding must be able to tell
// "nothing to do" from "mutation succeeded" — the latter writes.
var errWatermarkNotOurs = errors.New("watermark not applicable to the current binding")

// errPRAlreadyKnown aborts the PR append when the number is already on the
// binding — checked inside the mutation, against what is on disk now.
var errPRAlreadyKnown = errors.New("pr already recorded on this binding")

// errBindingChangedSession aborts a mutation when the worktree is no longer
// bound to the session the command acted for — a concurrent `session start`
// replaced it while the command was talking to the server.
var errBindingChangedSession = errors.New("the worktree is bound to a different session now")

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
