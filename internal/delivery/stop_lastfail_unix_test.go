//go:build !windows

package delivery

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestLastFailOpenedHandle1675(t *testing.T) {
	dir := t.TempDir()
	regular := filepath.Join(dir, "regular")
	writeBytes1675(t, regular, []byte("private"))
	f, err := openStopLastFail(regular)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	link := filepath.Join(dir, "link")
	if err = os.Symlink(regular, link); err != nil {
		t.Fatal(err)
	}
	f, err = openStopLastFail(link)
	if err == nil {
		_ = f.Close()
		t.Fatal("opener followed a link")
	}
	// Swapping to another regular inode must not validate against the first Lstat.
	fix := mismatch1675(t)
	r := fix.reporter()
	r.beforeLastFailOpen = func(path string) {
		data := readBytes1675(t, path)
		tmp := path + ".new"
		writeBytes1675(t, tmp, data)
		if err := os.Rename(tmp, path); err != nil {
			t.Fatal(err)
		}
	}
	p, err := r.PendingStatus(0)
	if err != nil || p.NeedsPerson != 0 {
		t.Fatal("swapped regular handle accepted")
	}
	// A FIFO substituted after Lstat must never block or be read as status.
	r.beforeLastFailOpen = func(path string) {
		_ = os.Remove(path)
		if err := syscall.Mkfifo(path, 0600); err != nil {
			t.Fatal(err)
		}
	}
	p, err = r.PendingStatus(0)
	if err != nil || p.NeedsPerson != 0 {
		t.Fatal("FIFO accepted")
	}
}
