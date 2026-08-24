package cmd

import (
	"fmt"
	"os"
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
	dir := os.Getenv(team.GitDirEnv)
	if dir == "" {
		t.Fatal("the worktree binding is NOT sandboxed — tests would read the developer's real binding locally and nothing on CI, which is a false green")
	}
	if _, err := os.Stat(dir + "/hadron-team-session.json"); err == nil {
		t.Fatalf("the sandbox at %s already holds a binding — tests that assert the UNBOUND path would take the bound one", dir)
	}
}
