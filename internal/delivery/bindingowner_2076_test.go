//go:build unix

package delivery

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLockBindingDeclinesForeignCreation2076(t *testing.T) {
	for _, existingCache := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing cache", true: "missing lock"}[existingCache], func(t *testing.T) {
			work := t.TempDir()
			cache := filepath.Join(work, plainCacheName)
			if existingCache {
				if err := os.Mkdir(cache, 0700); err != nil {
					t.Fatal(err)
				}
			}
			original := bindingEffectiveUID
			bindingEffectiveUID = func() int { return os.Geteuid() + 1000000 }
			t.Cleanup(func() { bindingEffectiveUID = original })
			release, err := LockBinding(work, time.Second)
			if release != nil {
				release()
			}
			if err == nil || !strings.Contains(err.Error(), "nothing was created") || !strings.Contains(err.Error(), "start the worker once") || !strings.Contains(err.Error(), fmt.Sprintf("uid %d", os.Geteuid()+1000000)) || !strings.Contains(err.Error(), fmt.Sprintf("uid %d", os.Geteuid())) {
				t.Errorf("expected actionable uid refusal, got %v", err)
			}
			missing := cache
			if existingCache {
				missing = filepath.Join(cache, bindingLockFileName)
			}
			if _, err := os.Lstat(missing); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("created foreign-owned binding path: %s (%v)", missing, err)
			}
		})
	}
}

func TestLockBindingDeclinesUnknownOwner2076(t *testing.T) {
	for _, existingCache := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing cache", true: "missing lock"}[existingCache], func(t *testing.T) {
			work := t.TempDir()
			cache := filepath.Join(work, plainCacheName)
			if existingCache {
				if err := os.Mkdir(cache, 0700); err != nil {
					t.Fatal(err)
				}
			}
			original := bindingOwner
			bindingOwner = func(string) (os.FileInfo, error) { return nil, os.ErrPermission }
			t.Cleanup(func() { bindingOwner = original })
			release, err := LockBinding(work, time.Second)
			if release != nil {
				release()
			}
			if err == nil || !strings.Contains(err.Error(), "owner") {
				t.Errorf("unknown owner allowed creation: %v", err)
			}
			missing := cache
			if existingCache {
				missing = filepath.Join(cache, bindingLockFileName)
			}
			if _, err := os.Lstat(missing); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("created path despite unknown owner: %v", err)
			}
		})
	}
}

func TestLockBindingExistingLockAllowsForeignUID2076(t *testing.T) {
	work := t.TempDir()
	release, err := LockBinding(work, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	release()
	originalUID, originalOwner := bindingEffectiveUID, bindingOwner
	bindingEffectiveUID = func() int { return os.Geteuid() + 1000000 }
	bindingOwner = func(string) (os.FileInfo, error) {
		t.Error("existing lock must not need owner proof")
		return nil, os.ErrPermission
	}
	t.Cleanup(func() { bindingEffectiveUID, bindingOwner = originalUID, originalOwner })
	release, err = LockBinding(work, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	release()
}

type ownerWithoutUnixStat2076 struct{ os.FileInfo }

func (ownerWithoutUnixStat2076) Sys() any { return nil }

func TestLockBindingDeclinesUnprovenOwnerStat2076(t *testing.T) {
	work := t.TempDir()
	info, err := os.Lstat(work)
	if err != nil {
		t.Fatal(err)
	}
	original := bindingOwner
	bindingOwner = func(string) (os.FileInfo, error) { return ownerWithoutUnixStat2076{info}, nil }
	t.Cleanup(func() { bindingOwner = original })
	release, err := LockBinding(work, time.Second)
	if release != nil {
		release()
	}
	if err == nil || !strings.Contains(err.Error(), "owner") {
		t.Fatalf("non-Unix owner stat accepted: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(work, plainCacheName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("created cache without uid proof")
	}
}

func TestLockBindingForeignUIDCannotRecreateRemovedLock2076(t *testing.T) {
	work := t.TempDir()
	release, err := LockBinding(work, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	release()
	originalUID, originalHook := bindingEffectiveUID, afterDirOpenHook
	bindingEffectiveUID = func() int { return os.Geteuid() + 1000000 }
	afterDirOpenHook = func(dir string) {
		if err := os.Remove(filepath.Join(dir, bindingLockFileName)); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() { bindingEffectiveUID, afterDirOpenHook = originalUID, originalHook })
	release, err = LockBinding(work, time.Second)
	if release != nil {
		release()
	}
	if err == nil || !strings.Contains(err.Error(), "nothing was created") {
		t.Fatalf("foreign uid recreated removed lock: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(work, plainCacheName, bindingLockFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("lock recreated after removal")
	}
}
