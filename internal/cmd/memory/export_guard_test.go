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
