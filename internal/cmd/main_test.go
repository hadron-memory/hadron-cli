package cmd

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
)

// #498 — every command-level test in this package runs with the worktree
// binding SANDBOXED, whether or not it asks.
//
// Before this, a test that did not call teamGitDir(t) read whatever binding the
// developer's own checkout held, and nothing at all on CI. That orientation is
// the dangerous one: such a test can FAIL LOCALLY AND PASS ON CI, so its green
// tick means "this machine had no binding", not "this behaviour holds" — and
// nobody watches CI for false greens. It bit TestTeamChatJSONKeepsItsShape,
// which asserts the UNBOUND reader's behaviour and silently took the bound path
// on the author's machine.
//
// Doing it here rather than in testFactory is not a style choice. All 35 tests
// that use teamGitDir's RETURN value call it BEFORE testFactory, so a
// t.Setenv there would clobber the directory they then assert against. A
// package-level sandbox has no ordering relationship with anything, needs no
// test to opt in, and cannot be forgotten by a test written next year — which
// is the property the issue asked for and the per-test call cannot give.
//
// "No binding" is a state that has to be ESTABLISHED, not assumed.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "hadron-cmd-tests-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "sandbox: %v\n", err)
		os.Exit(1)
	}
	// Set for the whole package. A test that WRITES a binding still calls
	// teamGitDir(t) for a private one — t.Setenv overrides this and restores
	// it afterwards — so nothing shares state through here; this directory
	// exists to be reliably EMPTY for everyone else.
	if err := os.Setenv(team.GitDirEnv, dir); err != nil {
		// Refuse to run rather than run unsandboxed: an unsandboxed suite is
		// precisely the false-green this exists to remove, and it would look
		// like a normal green.
		fmt.Fprintf(os.Stderr, "sandbox: %v\n", err)
		os.Exit(1)
	}

	code := m.Run()
	_ = os.RemoveAll(dir) // best-effort: a leftover temp dir is not worth failing a green suite over
	os.Exit(code)
}

// The guard for the guard: assert the package sandbox is actually in effect.
//
// Delete TestMain and this goes red — on CI because the variable is unset, and
// on a developer machine because it then points at a real checkout whose
// binding file exists. Without it, the sandbox is a line of setup nothing
// exercises, which is the shape this repo keeps finding
// (review:a-mutation-check-can-itself-be-a-no-op).
func TestPackageSandboxIsInEffect(t *testing.T) {
	if err := sandboxProblem(os.Getenv(team.GitDirEnv)); err != nil {
		t.Fatal(err)
	}
}

// sandboxProblem reports why dir is not a usable binding sandbox, or nil.
//
// Extracted from the test above so each branch can be EXERCISED. It cannot be
// driven through the environment any more: TestMain overrides
// HADRON_TEAM_GIT_DIR for the whole package, which is the point of it — so a
// guard that only reads that variable has branches nothing can reach, which is
// the defect this file exists to remove, one level up.
func sandboxProblem(dir string) error {
	if dir == "" {
		return errors.New("the worktree binding is NOT sandboxed — tests would read the developer's real binding locally and nothing on CI, which is a false green")
	}
	// filepath.Join, not a hard-coded separator: this repo ships a Windows
	// binary, so its tests are expected to run there too.
	_, err := os.Stat(filepath.Join(dir, "hadron-team-session.json"))
	switch {
	case err == nil:
		return fmt.Errorf("the sandbox at %s already holds a binding — tests that assert the UNBOUND path would take the bound one", dir)
	case !errors.Is(err, fs.ErrNotExist):
		// UNKNOWN IS NOT NONE. A Stat that fails for any other reason —
		// permissions, an unusable path — leaves "is this directory empty of
		// bindings?" unanswered, and passing on an unanswered question is the
		// same false green this change exists to remove
		// (review:a-claim-must-not-outrun-its-evidence). Caught by @copilot on
		// PR #518, in the guard written to enforce exactly that rule.
		return fmt.Errorf("cannot tell whether the sandbox at %s is clean: %w", dir, err)
	}
	return nil
}

// Each branch of the guard, driven directly — the env var cannot reach it.
func TestSandboxProblemDetectsEachFailure(t *testing.T) {
	t.Run("unset", func(t *testing.T) {
		if err := sandboxProblem(""); err == nil {
			t.Error("an unset sandbox must be reported")
		}
	})
	t.Run("clean", func(t *testing.T) {
		if err := sandboxProblem(t.TempDir()); err != nil {
			t.Errorf("an empty temp dir is a valid sandbox: %v", err)
		}
	})
	t.Run("already holds a binding", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		err := sandboxProblem(dir)
		if err == nil {
			t.Fatal("a sandbox holding a binding must be reported")
		}
		// WHICH error, not merely that there is one. Without this the
		// already-holds branch can be deleted and the unknown branch below
		// catches the fall-through with a different message — so the subtest
		// passes while the case it names is gone. Found by mutating it.
		if !strings.Contains(err.Error(), "already holds a binding") {
			t.Errorf("it must name the binding, not report an unanswered question: %v", err)
		}
	})
	t.Run("unreadable is UNKNOWN, not clean", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root reads anything, so the permission case cannot be staged")
		}
		dir := t.TempDir()
		if err := os.Chmod(dir, 0o000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
		err := sandboxProblem(dir)
		if err == nil {
			t.Fatal("an unreadable sandbox is UNKNOWN, and unknown must not pass as clean")
		}
		if !strings.Contains(err.Error(), "cannot tell") {
			t.Errorf("it must say the question is unanswered, not that a binding exists: %v", err)
		}
	})
}
