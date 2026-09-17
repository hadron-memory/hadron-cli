package memory

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRefuseImplicitExportIntoOnlyRefusesANonEmptyRepo pins the gate's exact
// shape (#583). It refuses ONLY the combination that turns a convenience into
// damage — inside a git work tree AND not empty — because those two together
// are what make the output look like repo content that `git add -A` stages and
// that an overwrite replaces silently.
//
// Every other combination must pass, or the guard starts refusing the ordinary
// case of exporting into a scratch directory.
func TestRefuseImplicitExportIntoOnlyRefusesANonEmptyRepo(t *testing.T) {
	// repo + non-empty: the only refusal.
	t.Run("non-empty git work tree is refused", func(t *testing.T) {
		dir := t.TempDir()
		mustMkdir(t, filepath.Join(dir, ".git"))
		mustWrite(t, filepath.Join(dir, "main.go"), "package main")
		if err := refuseImplicitExportInto(dir); err == nil {
			t.Fatal("expected a refusal inside a non-empty repository")
		}
	})

	t.Run("a nested directory inside the work tree is refused", func(t *testing.T) {
		dir := t.TempDir()
		mustMkdir(t, filepath.Join(dir, ".git"))
		nested := filepath.Join(dir, "internal", "cmd")
		mustMkdir(t, nested)
		mustWrite(t, filepath.Join(nested, "x.go"), "package cmd")
		// The .git is several levels up — the hazard is the same, so the walk
		// must find it rather than checking only the target directory.
		if err := refuseImplicitExportInto(nested); err == nil {
			t.Fatal("expected a refusal deep inside a repository")
		}
	})

	t.Run("an EMPTY directory inside a repo is allowed", func(t *testing.T) {
		dir := t.TempDir()
		mustMkdir(t, filepath.Join(dir, ".git"))
		empty := filepath.Join(dir, "kb")
		mustMkdir(t, empty)
		if err := refuseImplicitExportInto(empty); err != nil {
			t.Errorf("an empty directory has nothing to scatter among: %v", err)
		}
	})

	t.Run("a non-empty directory OUTSIDE a repo is allowed", func(t *testing.T) {
		dir := t.TempDir()
		mustWrite(t, filepath.Join(dir, "notes.txt"), "hi")
		if err := refuseImplicitExportInto(dir); err != nil {
			t.Errorf("outside a repository this is an ordinary export: %v", err)
		}
	})

	t.Run("a missing directory is allowed", func(t *testing.T) {
		dir := t.TempDir()
		mustMkdir(t, filepath.Join(dir, ".git"))
		// export creates it; creating a directory is not the hazard.
		if err := refuseImplicitExportInto(filepath.Join(dir, "does-not-exist")); err != nil {
			t.Errorf("a directory export would create is not a scatter risk: %v", err)
		}
	})
}

// TestRefusalNamesBothRemedies — the message must give the reader something to
// type. `--out <dir>` is the fix; `--out .` is how you say you meant it, and
// omitting it would leave a caller who genuinely wants the cwd with no way
// through (the same defect as an error naming a flag that does not exist).
func TestRefusalNamesBothRemedies(t *testing.T) {
	dir := t.TempDir()
	mustMkdir(t, filepath.Join(dir, ".git"))
	mustWrite(t, filepath.Join(dir, "main.go"), "package main")
	err := refuseImplicitExportInto(dir)
	if err == nil {
		t.Fatal("expected a refusal")
	}
	for _, want := range []string{"--out <dir>", "--out ."} {
		if !contains(err.Error(), want) {
			t.Errorf("the refusal must name %q, got: %v", want, err)
		}
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", p, err)
	}
}

func mustWrite(t *testing.T, p, body string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}

// TestRefuseImplicitExportIntoResolvesSymlinks — @copilot on #599.
//
// A shell exports PWD, so os.Getwd (and therefore filepath.Abs(".")) can return
// a SYMLINKED path. A lexical parent walk then climbs the link's parents and
// never reaches the real checkout's .git, so the guard would wave through
// exactly the case it exists to refuse.
//
// Measured before fixing: with PWD set to a symlink into a repo, Abs(".") kept
// the link path and the walk returned false for a directory plainly inside a
// work tree.
func TestRefuseImplicitExportIntoResolvesSymlinks(t *testing.T) {
	root := t.TempDir()

	repo := filepath.Join(root, "realrepo")
	mustMkdir(t, filepath.Join(repo, ".git"))
	// The link points at a SUBDIRECTORY of the repo, not its root. That is what
	// makes the walk matter: os.Stat follows symlinks, so a link to the repo
	// root would find .git on the first probe and the test would pass with the
	// resolution deleted — it would measure nothing.
	sub := filepath.Join(repo, "internal", "cmd")
	mustMkdir(t, sub)
	mustWrite(t, filepath.Join(sub, "x.go"), "package cmd")

	// The link lives OUTSIDE the repo, so walking up its own parents lexically
	// leaves the checkout and finds no .git at all.
	outside := filepath.Join(root, "outside")
	mustMkdir(t, outside)
	link := filepath.Join(outside, "link")
	if err := os.Symlink(sub, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if err := refuseImplicitExportInto(link); err == nil {
		t.Fatal("a repository reached through a symlink must still be refused — the files land in the real checkout either way")
	}

	// And the converse still holds: a symlink to a directory that is NOT a
	// repo must not start being refused.
	plain := filepath.Join(root, "plain")
	mustMkdir(t, plain)
	mustWrite(t, filepath.Join(plain, "notes.txt"), "hi")
	plainLink := filepath.Join(outside, "plainlink")
	if err := os.Symlink(plain, plainLink); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := refuseImplicitExportInto(plainLink); err != nil {
		t.Errorf("resolving symlinks must not widen the refusal: %v", err)
	}
}
