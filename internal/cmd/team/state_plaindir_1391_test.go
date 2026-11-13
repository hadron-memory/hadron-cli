//go:build !windows

package team

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1391 (ruling 0230 = B): outside a git checkout the binding cache lives
// in the directory itself, under the same lock, one worker per directory.

// fakeGit puts a `git` on PATH that fails with the given stderr, so "is this a
// plain directory?" is decided by the answer git gives, not by where the test
// happens to run.
func fakeGit(t *testing.T, stderr string) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\necho '" + stderr + "' >&2\nexit 128\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o700); err != nil { // #nosec G306 — a test shim must be executable
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv(GitDirEnv, "")
}

func plainDir1391(t *testing.T) string {
	t.Helper()
	fakeGit(t, "fatal: not a git repository (or any of the parent directories): .git")
	dir := t.TempDir()
	t.Chdir(dir)
	return dir
}

func TestPlainDir1391ReadsSeeNoCacheAndCreateNothing(t *testing.T) {
	dir := plainDir1391(t)
	ctx := context.Background()
	if _, _, err := readBinding(ctx); !errors.Is(err, errNoWorktree) {
		t.Fatalf("a read with no cache must stay errNoWorktree (the server fallbacks key on it), got %v", err)
	}
	if err := updateBinding(ctx, func(*binding) error { return nil }); !errors.Is(err, errNoWorktree) {
		t.Fatalf("an update with no cache must not find one: %v", err)
	}
	if err := clearBinding(ctx); !errors.Is(err, errNoWorktree) {
		t.Fatalf("a clear with no cache must not find one: %v", err)
	}
	_ = bindingIsSession(ctx, "s1")
	if _, err := os.Stat(filepath.Join(dir, plainDirCacheName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("only a bind may create the cache directory, got %v", err)
	}
	b, plain, err := readBindingForBind(ctx)
	if err != nil || b != nil || !plain {
		t.Fatalf("a bind's read in a plain directory is 'no binding yet', got %v %v %v", b, plain, err)
	}
}

func TestPlainDir1391BindWritesTheCacheUnderTheLockAndReadsBack(t *testing.T) {
	dir := plainDir1391(t)
	ctx := context.Background()
	path, err := writeBinding(ctx, &binding{SessionID: "s1", WorkerID: "wkr1", WorkerName: "Iris"})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, plainDirCacheName, bindingFileName)
	if path != want {
		t.Fatalf("path %s, want %s", path, want)
	}
	st, err := os.Stat(filepath.Join(dir, plainDirCacheName))
	if err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("the cache directory must be owner-only: %v %v", st, err)
	}
	if _, err := os.Stat(filepath.Join(dir, plainDirCacheName, bindingLockFileName)); err != nil {
		t.Fatalf("the bind must take the same lock file a worktree uses: %v", err)
	}
	b, _, err := readBinding(ctx)
	if err != nil || b == nil || b.WorkerName != "Iris" {
		t.Fatalf("read back: %v %v", b, err)
	}
	if err := updateBinding(ctx, func(b *binding) error { b.Tool = "x"; return nil }); err != nil {
		t.Fatalf("an update reaches an existing plain cache: %v", err)
	}
	// The same worker rebinding replaces, exactly like a worktree.
	if _, err := writeBinding(ctx, &binding{SessionID: "s2", WorkerID: "wkr1", WorkerName: "Iris"}); err != nil {
		t.Fatalf("the same worker's rebind must replace: %v", err)
	}
	if err := clearBinding(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readBinding(ctx); !errors.Is(err, errNoWorktree) {
		t.Fatalf("after a clear the directory reads as uncached again: %v", err)
	}
}

// Acceptance 5 by refusal: a second worker never replaces, so never reads,
// another worker's binding in one plain directory.
func TestPlainDir1391SecondWorkerIsRefusedUnderTheLock(t *testing.T) {
	dir := plainDir1391(t)
	ctx := context.Background()
	if _, err := writeBinding(ctx, &binding{SessionID: "s1", WorkerID: "wkr1", WorkerName: "Iris"}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(dir, plainDirCacheName, bindingFileName))
	_, err := writeBinding(ctx, &binding{SessionID: "s9", WorkerID: "wkr2", WorkerName: "Bob"})
	if code := exitcode.FromError(err); code != exitcode.Conflict {
		t.Fatalf("exit %d, want Conflict: %v", code, err)
	}
	for _, want := range []string{"Iris", "one worker", "session end"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must name %q: %v", want, err)
		}
	}
	after, _ := os.ReadFile(filepath.Join(dir, plainDirCacheName, bindingFileName))
	if string(before) != string(after) {
		t.Fatal("a refused bind must leave the first worker's binding byte-identical")
	}
}

// A worktree keeps its documented replace: the one-worker rule is the plain
// directory's, not a change to worktrees.
func TestPlainDir1391WorktreeStillReplaces(t *testing.T) {
	t.Setenv(GitDirEnv, t.TempDir())
	ctx := context.Background()
	if _, err := writeBinding(ctx, &binding{SessionID: "s1", WorkerID: "wkr1", WorkerName: "Iris"}); err != nil {
		t.Fatal(err)
	}
	if _, err := writeBinding(ctx, &binding{SessionID: "s9", WorkerID: "wkr2", WorkerName: "Bob"}); err != nil {
		t.Fatalf("a worktree bind replaces, as documented: %v", err)
	}
}

// Only git's "not a git repository" makes a directory plain. Any other git
// failure inside what may be a real worktree stays the hard error: a binding
// written to the current directory there would be in the wrong place.
func TestPlainDir1391OtherGitFailuresStayHard(t *testing.T) {
	fakeGit(t, "fatal: detected dubious ownership in repository at '/x'")
	dir := t.TempDir()
	t.Chdir(dir)
	ctx := context.Background()
	if _, _, err := readBindingForBind(ctx); !errors.Is(err, errNoWorktree) {
		t.Fatalf("want the hard errNoWorktree, got %v", err)
	}
	if _, err := writeBinding(ctx, &binding{SessionID: "s1", WorkerID: "wkr1"}); !errors.Is(err, errNoWorktree) {
		t.Fatalf("a bind must not fall back to a plain cache: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, plainDirCacheName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("no cache may be created on a non-plain git failure")
	}
}

// Gloria's BLIND F1 on #1968: git localizes "not a git repository". The
// shim answers in German unless the CLI asks git for the C locale, so a plain
// directory under a non-C locale must still read as plain.
func TestPlainDir1391PlainUnderATranslatedGit(t *testing.T) {
	bin := t.TempDir()
	script := "#!/bin/sh\nif [ \"$LC_ALL\" = C ]; then echo 'fatal: not a git repository (or any of the parent directories): .git' >&2; else echo 'Schwerwiegend: Kein Git-Repository (oder irgendeines der Elternverzeichnisse): .git' >&2; fi\nexit 128\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o700); err != nil { // #nosec G306 — a test shim must be executable
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv(GitDirEnv, "")
	t.Setenv("LC_ALL", "de_DE.UTF-8")
	t.Setenv("LANG", "de_DE.UTF-8")
	t.Chdir(t.TempDir())
	if _, plain, err := readBindingForBind(context.Background()); err != nil || !plain {
		t.Fatalf("a plain directory under a German locale must read as plain, got plain=%v err=%v", plain, err)
	}
}

// Gloria's BLIND F2 on #1968: the lock-time check is the only guard for a
// resume and a launch grant, so a cached binding with no worker id — which
// cannot show it is the same worker — refuses rather than being replaced.
func TestPlainDir1391BindingWithoutWorkerIDIsNotReplaced(t *testing.T) {
	dir := plainDir1391(t)
	ctx := context.Background()
	if err := os.MkdirAll(filepath.Join(dir, plainDirCacheName), 0o700); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(dir, plainDirCacheName, bindingFileName)
	legacy := `{"sessionId":"s1","workerName":"Iris","agentId":"agt1"}`
	if err := os.WriteFile(cache, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := writeBinding(ctx, &binding{SessionID: "s9", WorkerID: "wkr2", WorkerName: "Bob"})
	if code := exitcode.FromError(err); code != exitcode.Conflict {
		t.Fatalf("exit %d, want Conflict: %v", code, err)
	}
	if raw, _ := os.ReadFile(cache); string(raw) != legacy {
		t.Fatal("a refused bind must leave the cached binding byte-identical")
	}
}

// Rico's §10 P3 on #1968: git says "not a git repository: <path>" for a
// worktree whose admin dir is gone, or a bad GIT_DIR — a real checkout, not
// a plain directory. Only the discovery form ("... (or any of the parent
// directories)") makes a directory plain.
func TestPlainDir1391BrokenWorktreeIsNotPlain(t *testing.T) {
	for _, msg := range []string{
		"fatal: not a git repository: /src/repo/.git/worktrees/gone",
		"fatal: not a git repository: '/nowhere'",
	} {
		t.Run(msg, func(t *testing.T) {
			fakeGit(t, msg)
			dir := t.TempDir()
			t.Chdir(dir)
			if _, plain, err := readBindingForBind(context.Background()); plain || !errors.Is(err, errNoWorktree) {
				t.Fatalf("want the hard errNoWorktree, got plain=%v err=%v", plain, err)
			}
			if _, err := os.Stat(filepath.Join(dir, plainDirCacheName)); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("no cache may be created in a broken checkout")
			}
		})
	}
}
