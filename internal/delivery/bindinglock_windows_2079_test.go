//go:build windows

package delivery

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func bindingCache2079(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	cache := filepath.Join(root, plainCacheName)
	if err := os.Mkdir(cache, 0700); err != nil {
		t.Fatal(err)
	}
	return root, cache
}

func junction2079(t *testing.T, link, target string) {
	t.Helper()
	out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput()
	if err != nil {
		t.Fatalf("create owned junction: %v\n%s", err, out)
	}
}

func emptyOutside2079(t *testing.T, outside string) {
	t.Helper()
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("outside directory changed: %v", entries)
	}
}

// Positive control: refusing every path must not satisfy the reparse tests.
func TestLockBinding2079PlainDirectoryControl(t *testing.T) {
	root, cache := bindingCache2079(t)
	release, err := LockBinding(root, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if _, err := os.Stat(filepath.Join(cache, bindingLockFileName)); err != nil {
		t.Fatal(err)
	}
}

// The junction is planted after LockBinding's Lstat, before its directory open.
func TestLockBinding2079DirectorySwapJunction(t *testing.T) {
	root, _ := bindingCache2079(t)
	outside := t.TempDir()
	called := false
	lockOpenHook = func(dir string) {
		called = true
		if err := os.Rename(dir, dir+".moved"); err != nil {
			t.Fatal(err)
		}
		junction2079(t, dir, outside)
	}
	defer func() { lockOpenHook = nil }()
	release, err := LockBinding(root, time.Second)
	if release != nil {
		release()
	}
	if !called {
		t.Fatal("directory swap hook did not run")
	}
	if err == nil {
		t.Error("directory reparse point was followed")
	}
	emptyOutside2079(t, outside)
}

func symlink2079(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		if errors.Is(err, windows.ERROR_PRIVILEGE_NOT_HELD) {
			if os.Getenv("GITHUB_ACTIONS") == "" {
				t.Skipf("Windows symlink privilege unavailable: %v", err)
			}
			t.Fatalf("Windows symlink privilege unavailable in CI: %v", err)
		}
		t.Fatalf("create owned file symlink: %v", err)
	}
}

func TestLockBinding2079LockFileReparse(t *testing.T) {
	for _, exists := range []bool{false, true} {
		name := "missing-target"
		if exists {
			name = "existing-target"
		}
		t.Run(name, func(t *testing.T) {
			root, _ := bindingCache2079(t)
			target := filepath.Join(t.TempDir(), "target")
			sentinel := []byte("outside target must remain unchanged")
			if exists {
				if err := os.WriteFile(target, sentinel, 0600); err != nil {
					t.Fatal(err)
				}
			}
			called := false
			lockOpenHook = func(dir string) { called = true; symlink2079(t, target, filepath.Join(dir, bindingLockFileName)) }
			defer func() { lockOpenHook = nil }()
			release, err := LockBinding(root, time.Second)
			if release != nil {
				release()
			}
			if !called {
				t.Fatal("file swap hook did not run")
			}
			if err == nil {
				t.Error("file reparse point was followed")
			} else if !strings.Contains(err.Error(), "the session binding location is a link, so it was not used") {
				t.Errorf("unexpected reparse refusal: %v", err)
			}
			data, readErr := os.ReadFile(target)
			if exists {
				if readErr != nil || !bytes.Equal(data, sentinel) {
					t.Errorf("outside target changed: %q, %v", data, readErr)
				}
			} else if !errors.Is(readErr, os.ErrNotExist) {
				t.Errorf("outside missing target was created or unreadable: %q, %v", data, readErr)
			}
		})
	}
}

// Once openLockFile holds the directory without delete-sharing, a late swap
// must be blocked, or the lock open must refuse it without touching outside.
func TestLockBinding2079HeldDirectoryBlocksSwap(t *testing.T) {
	root, _ := bindingCache2079(t)
	outside := t.TempDir()
	before, after := false, false
	var swapErr error
	lockOpenHook = func(string) { before = true }
	afterDirOpenHook = func(dir string) {
		after = true
		if !before {
			t.Fatal("directory handle hook ran before initial open hook")
		}
		swapErr = os.Rename(dir, dir+".moved")
		if swapErr == nil {
			junction2079(t, dir, outside)
		}
	}
	defer func() { lockOpenHook = nil; afterDirOpenHook = nil }()
	release, err := LockBinding(root, time.Second)
	if release != nil {
		release()
	}
	if !before || !after {
		t.Fatalf("hooks not exercised: before=%t after=%t", before, after)
	}
	if swapErr != nil {
		if !errors.Is(swapErr, windows.ERROR_SHARING_VIOLATION) {
			t.Errorf("late directory swap failed unexpectedly: %v", swapErr)
		}
		if err != nil {
			t.Errorf("unchanged held directory could not be locked: %v", err)
		}
	} else if err == nil {
		t.Error("late directory reparse point was followed")
	}
	emptyOutside2079(t, outside)
}
