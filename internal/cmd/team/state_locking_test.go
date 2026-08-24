package team

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// #499 — the worktree binding has four writers and no serialization. Each
// write is atomic (temp + rename), so nobody sees a torn file; but atomic
// replace is not compare-and-swap, so two read-modify-write sequences could
// interleave and the later writer silently discarded the earlier one's edit.
//
// These tests were written against an optimistic scheme and REFUTED it — 24
// racing writers, all reporting success, two edits landing. See
// docs/plans/binding-write-serialization.md; the file is named for what
// shipped, which is a lock.

func casGitDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(GitDirEnv, dir)
	return dir
}

// seedSessionID is the session every fixture here binds; the interesting
// variation in these tests is concurrency, not identity.
const seedSessionID = "s1"

func seedBinding(t *testing.T) {
	t.Helper()
	if _, err := writeBinding(context.Background(), &binding{
		SessionID: seedSessionID, WorkerID: "wkr1", WorkerName: "Iris",
		StartedAt: "2026-08-24T00:00:00Z", PRNumbers: []int{},
	}); err != nil {
		t.Fatal(err)
	}
}

// The counter advances on every update, and starts from 0 on a binding written
// before #499 — no migration, the first update simply writes 1.
func TestUpdateBindingBumpsRevFromLegacyZero(t *testing.T) {
	casGitDir(t)
	seedBinding(t)

	b, _, _ := readBinding(context.Background())
	if b.Rev != 0 {
		t.Fatalf("a freshly written binding starts at rev 0, got %d", b.Rev)
	}
	for want := 1; want <= 3; want++ {
		if err := updateBinding(context.Background(), func(cur *binding) error {
			cur.Tool = "claude-code"
			return nil
		}); err != nil {
			t.Fatalf("update: %v", err)
		}
		got, _, _ := readBinding(context.Background())
		if got.Rev != want {
			t.Errorf("rev = %d, want %d", got.Rev, want)
		}
	}
}

// THE DEFECT ITSELF. Two writers race on different fields; before #499 the
// later one wrote its whole snapshot back and the earlier edit vanished.
//
// A real race with real goroutines, not a simulated interleaving: the point is
// that the scheme holds under whatever ordering the runtime picks, and a
// hand-staged sequence only proves the one ordering its author imagined.
func TestConcurrentUpdatesDoNotLoseEachOther(t *testing.T) {
	casGitDir(t)
	seedBinding(t)
	ctx := context.Background()

	const appenders, watermarks = 12, 12
	var wg sync.WaitGroup
	wg.Add(appenders + watermarks)

	for i := 0; i < appenders; i++ {
		go func(n int) {
			defer wg.Done()
			_ = updateBinding(ctx, func(cur *binding) error {
				cur.PRNumbers = append(cur.PRNumbers, n)
				return nil
			})
		}(i)
	}
	for i := 0; i < watermarks; i++ {
		go func(n int) {
			defer wg.Done()
			recordChatWatermark(ctx, seedSessionID, n+1)
		}(i)
	}
	wg.Wait()

	got, _, err := readBinding(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// EVERY appended PR survives. This is the assertion the old code fails:
	// a watermark write from a stale snapshot used to drop whatever PR numbers
	// had landed since it read.
	seen := map[int]bool{}
	for _, n := range got.PRNumbers {
		seen[n] = true
	}
	for i := 0; i < appenders; i++ {
		if !seen[i] {
			t.Errorf("PR %d was lost — %v", i, got.PRNumbers)
		}
	}
	if len(got.PRNumbers) != appenders {
		t.Errorf("expected exactly %d PRs, got %v", appenders, got.PRNumbers)
	}
	// And the watermark reached its high-water mark rather than being reset by
	// a PR append that started before it.
	if got.ChatSeenSeq == nil || *got.ChatSeenSeq != watermarks {
		t.Errorf("watermark = %v, want %d", got.ChatSeenSeq, watermarks)
	}
	// The counter counts the writes that actually landed, so it is at least
	// one per successful mutation.
	if got.Rev < appenders {
		t.Errorf("rev = %d, want at least %d", got.Rev, appenders)
	}
}

// AN UPDATE NEVER CREATES. `session end` removes the binding; a concurrent
// read-modify-write that recreated it would leave the worktree bound to a
// session the server has already closed, and whoami would report it as live.
//
// This is fixed structurally rather than by the counter — there is no rev to
// compare when the file is gone.
func TestUpdateBindingNeverResurrectsARemovedBinding(t *testing.T) {
	dir := casGitDir(t)
	seedBinding(t)
	ctx := context.Background()

	if err := clearBinding(ctx); err != nil {
		t.Fatal(err)
	}
	err := updateBinding(ctx, func(cur *binding) error {
		cur.Tool = "claude-code"
		return nil
	})
	if err == nil {
		t.Fatal("updating a removed binding must refuse, not recreate it")
	}
	if _, statErr := os.Stat(filepath.Join(dir, bindingFileName)); statErr == nil {
		t.Error("the binding file was RESURRECTED — a worktree now points at an ended session")
	}

	// The same via the watermark path, which is the one that actually did this
	// (it lands after a paginated fetch and a full render).
	recordChatWatermark(ctx, seedSessionID, 42)
	if _, statErr := os.Stat(filepath.Join(dir, bindingFileName)); statErr == nil {
		t.Error("recordChatWatermark resurrected the binding")
	}
}

// A mutation that reports it has nothing to do must NOT write — otherwise
// every no-op bumps the counter and a watermark that is already ahead
// rewrites the file for nothing.
func TestUpdateBindingDoesNotWriteWhenTheMutationAborts(t *testing.T) {
	casGitDir(t)
	seedBinding(t)
	ctx := context.Background()

	if err := updateBinding(ctx, func(cur *binding) error { return errWatermarkNotOurs }); err == nil {
		t.Fatal("an aborting mutation must surface its error")
	}
	got, _, _ := readBinding(ctx)
	if got.Rev != 0 {
		t.Errorf("an aborted mutation must not write: rev = %d, want 0", got.Rev)
	}

	// And the real caller's guard: a watermark for a DIFFERENT session is not
	// ours to record.
	recordChatWatermark(ctx, "someone-else", 99)
	got, _, _ = readBinding(ctx)
	if got.ChatSeenSeq != nil {
		t.Errorf("a watermark for another session must not be recorded: %v", *got.ChatSeenSeq)
	}
	if got.Rev != 0 {
		t.Errorf("...and must not bump the counter: rev = %d", got.Rev)
	}
}

// The mutation runs against what is ON DISK at the time of the attempt, not
// against the caller's snapshot. `session log --pr` relies on this for its
// dedupe: a concurrent agent may have appended the same number since the
// command read, and a dedupe checked against the stale snapshot would let the
// serialized write faithfully preserve a duplicate.
func TestUpdateBindingMutatesTheCurrentState(t *testing.T) {
	casGitDir(t)
	seedBinding(t)
	ctx := context.Background()

	if err := updateBinding(ctx, func(cur *binding) error {
		cur.PRNumbers = append(cur.PRNumbers, 371)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var sawExisting []int
	if err := updateBinding(ctx, func(cur *binding) error {
		sawExisting = append([]int(nil), cur.PRNumbers...)
		cur.PRNumbers = append(cur.PRNumbers, 402)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(sawExisting) != 1 || sawExisting[0] != 371 {
		t.Errorf("the mutation must see what is on disk, saw %v", sawExisting)
	}
}

// The caller's binding must not be mutated when the update aborts —
// updateBinding works on a copy, including the slice, which a naive struct
// copy would still share.
func TestUpdateBindingDoesNotAliasTheCallersSlice(t *testing.T) {
	casGitDir(t)
	seedBinding(t)
	ctx := context.Background()

	if err := updateBinding(ctx, func(cur *binding) error {
		cur.PRNumbers = append(cur.PRNumbers, 1)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before, _, _ := readBinding(ctx)
	_ = updateBinding(ctx, func(cur *binding) error {
		cur.PRNumbers = append(cur.PRNumbers, 2)
		return errWatermarkNotOurs // abort AFTER touching the slice
	})
	after, _, _ := readBinding(ctx)
	if len(after.PRNumbers) != len(before.PRNumbers) {
		t.Errorf("an aborted mutation leaked into the stored binding: %v → %v", before.PRNumbers, after.PRNumbers)
	}
}

// A mutation must be able to refuse when the worktree has been REBOUND under
// it. `session log --pr` reads its binding, talks to the server, and only then
// appends; a concurrent `session start` in that gap replaces the binding, and
// appending anyway files the PR under a session that never logged it —
// contaminating the new session's history with the old one's work.
//
// Found by @codex on PR #519. The watermark path had this guard from the
// start; the append did not, and nothing tested it.
func TestUpdateBindingMutationCanRefuseARebind(t *testing.T) {
	casGitDir(t)
	seedBinding(t)
	ctx := context.Background()

	// The snapshot a command would have read before talking to the server.
	before, _, _ := readBinding(ctx)

	// A concurrent `session start` rebinds the worktree to another session.
	if _, err := writeBinding(ctx, &binding{
		SessionID: "s2-different", WorkerID: "wkr2", WorkerName: "Jonas",
		StartedAt: "2026-08-24T01:00:00Z", PRNumbers: []int{},
	}); err != nil {
		t.Fatal(err)
	}

	err := updateBinding(ctx, func(cur *binding) error {
		if cur.SessionID != before.SessionID {
			return errBindingChangedSession
		}
		cur.PRNumbers = append(cur.PRNumbers, 371)
		return nil
	})
	if !errors.Is(err, errBindingChangedSession) {
		t.Fatalf("the mutation must see the rebind and refuse, got %v", err)
	}
	got, _, _ := readBinding(ctx)
	if len(got.PRNumbers) != 0 {
		t.Errorf("the new session's history was contaminated: %v", got.PRNumbers)
	}
	if got.SessionID != "s2-different" {
		t.Errorf("the rebind must stand: %s", got.SessionID)
	}
}
