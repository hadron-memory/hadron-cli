//go:build unix

package settingscheck

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A FIFO must not hang the check, and is never off.
func TestFIFOIsUnreadable2545(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	if err := syscall.Mkfifo(p, 0o600); err != nil {
		t.Skip(err)
	}
	done := make(chan string, 1)
	go func() { done <- ReadFile(p) }()
	// A reader blocked in open is released by this writer.
	var got string
	for got == "" {
		select {
		case got = <-done:
		default:
			if w, err := os.OpenFile(p, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
				_ = w.Close()
				t.Fatal("the FIFO was opened for reading")
			}
		}
	}
	if got != StateUnreadable {
		t.Errorf("state %s", got)
	}
}

// Where the main checkout's local file must NOT decide.
func TestLocalRootIsSkipped2545(t *testing.T) {
	r, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	wt, acct := filepath.Join(r, "wt"), filepath.Join(r, "acct")
	link := func(common string) {
		write(t, filepath.Join(r, common, "worktrees", "wt", "commondir"), "../..")
		write(t, filepath.Join(wt, ".git"), "gitdir: "+filepath.Join(r, common, "worktrees", "wt"))
	}
	write(t, filepath.Join(r, "main", ".claude", "settings.local.json"), `{"idleCompaction":false}`)
	link("main/.git")
	if got := Effective(r, wt, acct, r); got.State != StateOff {
		t.Fatalf("control: %+v", got)
	}
	if got := Effective(r, wt, acct, filepath.Join(r, "main")); got.State != StateUnset {
		t.Errorf("a repository at the home directory: %+v", got)
	}
	ownedByUser = func(string) bool { return false }
	got := Effective(r, wt, acct, r)
	ownedByUser = pathOwnedByUser
	if got.State != StateUnset {
		t.Errorf("another user's repository: %+v", got)
	}
	link("main/bare.git")
	if got := Effective(r, wt, acct, r); got.State != StateUnset {
		t.Errorf("a bare repository has no main checkout: %+v", got)
	}
	write(t, filepath.Join(wt, ".git"), "nonsense")
	if got := Effective(r, wt, acct, r); got.State != StateUnreadable || got.Source != "" {
		t.Errorf("an unreadable .git file: %+v", got)
	}
}

func TestReadRegularRefusesAFIFO2545(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f")
	if err := syscall.Mkfifo(p, 0o600); err != nil {
		t.Skip(err)
	}
	if _, err := readRegular(p); err == nil {
		t.Error("a FIFO was read")
	}
}

func TestAtLeastMalformed2545(t *testing.T) {
	for _, v := range []string{"", "2.1", "2.1.x", "2.1.290.1"} {
		if AtLeast(v, MinVersion) {
			t.Errorf("%q", v)
		}
	}
}

func TestSubdirectoryReadsTheRepositoryRoot2545(t *testing.T) {
	r, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(r, "repo", "sub")
	for _, d := range []string{sub, filepath.Join(r, "repo", ".git")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(r, "repo", ".claude", "settings.local.json"), `{"idleCompaction":true}`)
	if got := Effective(r, sub, filepath.Join(r, "acct"), r); got.State != StateOn {
		t.Errorf("%+v", got)
	}
}

// cli#3201: a .git file whose gitdir does not exist is unreadable, not a
// submodule. Reading a missing commondir as "submodule" also covered a gitdir
// that is gone, so the account-level off could win over a local file the check
// never consulted. A submodule (the gitdir exists, with no commondir) is
// unchanged.
func TestMissingGitdirIsUnreadable3201(t *testing.T) {
	r, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	wt, acct := filepath.Join(r, "wt"), filepath.Join(r, "acct")
	write(t, filepath.Join(acct, "settings.json"), `{"idleCompaction":false}`)

	// The gitdir the file names does not exist.
	write(t, filepath.Join(wt, ".git"), "gitdir: "+filepath.Join(r, "gone", "worktrees", "wt"))
	got := Effective(r, wt, acct, r)
	if got.State != StateUnreadable || got.Source != "" {
		t.Fatalf("a missing gitdir: %+v", got)
	}

	// A relative gitdir that does not exist is the same.
	write(t, filepath.Join(wt, ".git"), "gitdir: ../nowhere/modules/wt")
	if got := Effective(r, wt, acct, r); got.State != StateUnreadable || got.Source != "" {
		t.Fatalf("a missing relative gitdir: %+v", got)
	}

	// A submodule: the gitdir exists and has no commondir. Unchanged: it reads
	// the directory itself, so the account file decides (off).
	if err := os.MkdirAll(filepath.Join(r, "super", ".git", "modules", "wt"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(wt, ".git"), "gitdir: "+filepath.Join(r, "super", ".git", "modules", "wt"))
	if got := Effective(r, wt, acct, r); got.State != StateOff {
		t.Fatalf("a submodule must be unchanged: %+v", got)
	}
}
