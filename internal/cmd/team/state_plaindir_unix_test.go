//go:build !windows

package team

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

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
