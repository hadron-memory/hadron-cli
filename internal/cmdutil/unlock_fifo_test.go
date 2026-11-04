//go:build !windows

package cmdutil

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// A FIFO (or any non-regular file) is refused without opening it: opening a
// FIFO for reading would block the invocation until a writer appears.
func TestResolveUnlockRefusesNonRegularFiles(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skip("mkfifo:", err)
	}
	for _, path := range []string{fifo, dir} {
		done := make(chan error, 1)
		go func() {
			ios, _, _ := output.Test()
			_, err := ResolveUnlock(ios, UnlockFlags{Memories: []string{"acme.com:kb"}, Files: []string{path}, FilesSet: true})
			done <- err
		}()
		select {
		case err := <-done:
			if exitcode.FromError(err) != exitcode.Usage || !strings.Contains(err.Error(), "not a regular file") {
				t.Fatalf("%s: err=%v", path, err)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("%s: blocked opening a non-regular file", path)
		}
	}
}

// PR #1616 BLIND P2-2: a FIFO that replaces the file AFTER the pre-open Stat
// must not block the open; the opened-descriptor checks refuse it.
func TestResolveUnlockRefusesAFIFOSwappedInAfterStat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pass")
	if err := os.WriteFile(path, []byte("pw\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	orig := afterPassphraseStat
	t.Cleanup(func() { afterPassphraseStat = orig })
	afterPassphraseStat = func(p string) {
		if err := os.Remove(p); err != nil {
			t.Error(err)
		}
		if err := syscall.Mkfifo(p, 0o600); err != nil {
			t.Error("mkfifo:", err)
		}
	}
	done := make(chan error, 1)
	go func() {
		ios, _, _ := output.Test()
		_, err := ResolveUnlock(ios, UnlockFlags{Memories: []string{"acme.com:kb"}, Files: []string{path}, FilesSet: true})
		done <- err
	}()
	select {
	case err := <-done:
		if exitcode.FromError(err) != exitcode.Usage || !strings.Contains(err.Error(), "changed while it was opened") {
			t.Fatalf("err=%v", err)
		}
	case <-time.After(3 * time.Second):
		// Unblock the stuck open so the goroutine can exit, then fail.
		if f, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			_ = f.Close()
		}
		t.Fatal("blocked opening a FIFO swapped in after the Stat")
	}
}
