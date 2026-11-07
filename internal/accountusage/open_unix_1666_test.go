//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package accountusage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodex1666RefusesSwappedParentSymlink(t *testing.T) {
	home, path := oneRollout(t)
	parent := filepath.Dir(path)
	moved := parent + "-moved"
	afterRolloutStat = func() {
		if err := os.Rename(parent, moved); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(moved, parent); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { afterRolloutStat = func() {} })
	snap, err := ReadCodex(codexAcct(home))
	if err == nil || snap.Present {
		t.Fatalf("parent link must refuse even when it points at the same inode: present=%v err=%v", snap.Present, err)
	}
	if strings.Contains(err.Error(), home) {
		t.Fatalf("refusal leaked home: %v", err)
	}
}

func TestOpenRollout1666RejectsLinksAtEveryComponent(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(real, "rollout.jsonl")
	if err := os.WriteFile(file, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, target string }{{"directory", real}, {"file", file}} {
		t.Run(tc.name, func(t *testing.T) {
			link := filepath.Join(root, tc.name+"-link")
			if err := os.Symlink(tc.target, link); err != nil {
				t.Fatal(err)
			}
			if tc.name == "directory" {
				link = filepath.Join(link, "rollout.jsonl")
			}
			f, err := openRollout(link)
			if f != nil {
				_ = f.Close()
			}
			if err == nil {
				t.Fatal("followed symlink")
			}
		})
	}
	f, err := openRollout(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCodex1666PreservesHostPrefixAlias(t *testing.T) {
	home, _ := oneRollout(t)
	parent := filepath.Dir(home)
	alias := filepath.Join(t.TempDir(), "host-prefix")
	if err := os.Symlink(parent, alias); err != nil {
		t.Fatal(err)
	}
	snap, err := ReadCodex(codexAcct(filepath.Join(alias, filepath.Base(home))))
	if err != nil || !snap.Present {
		t.Fatalf("host prefix alias must remain readable: present=%v err=%v", snap.Present, err)
	}
}

func TestCodex1666RefusesSwappedHomeSymlink(t *testing.T) {
	home, _ := oneRollout(t)
	moved := home + "-moved"
	t.Cleanup(func() { _ = os.RemoveAll(moved) })
	afterRolloutStat = func() {
		if err := os.Rename(home, moved); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(moved, home); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { afterRolloutStat = func() {} })
	snap, err := ReadCodex(codexAcct(home))
	if err == nil || snap.Present {
		t.Fatalf("ConfigDir itself must be nofollow: present=%v err=%v", snap.Present, err)
	}
}
