//go:build unix

package team

import (
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestCheckArtifactRemainsReadableUnderRestrictiveUmask(t *testing.T) {
	cache := t.TempDir()
	old := syscall.Umask(0777)
	defer syscall.Umask(old)
	path, e := publishCheckArtifact(cache, checkArtifact{Messages: []teamChatMessageDTO{{Seq: 1}}}, io.Discard)
	if e != nil {
		t.Fatal(e)
	}
	file, e := os.Stat(path)
	if e != nil {
		t.Fatal(e)
	}
	dir, e := os.Stat(filepath.Dir(path))
	if e != nil {
		t.Fatal(e)
	}
	if file.Mode().Perm() != 0600 || dir.Mode().Perm() != 0700 {
		t.Fatal(file.Mode(), dir.Mode())
	}
	if _, e = os.ReadFile(path); e != nil {
		t.Fatal(e)
	}
}
