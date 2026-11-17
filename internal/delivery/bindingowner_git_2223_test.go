//go:build unix

package delivery

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

type ownerUID2223 struct {
	os.FileInfo
	uid uint32
}

func (info ownerUID2223) Sys() any {
	raw := *info.FileInfo.Sys().(*syscall.Stat_t)
	raw.Uid = info.uid
	return &raw
}

func gitOwnerTree2223(t *testing.T, kind string) (work, admin string) {
	t.Helper()
	parent := t.TempDir()
	work = filepath.Join(parent, "work")
	if err := os.Mkdir(work, 0700); err != nil {
		t.Fatal(err)
	}
	if kind == "directory" {
		admin = filepath.Join(work, ".git")
	} else {
		admin = filepath.Join(parent, "admin")
	}
	if err := os.Mkdir(admin, 0700); err != nil {
		t.Fatal(err)
	}
	if kind != "directory" {
		pointer := admin
		if kind == "relative" {
			pointer = "../admin"
		}
		if err := os.WriteFile(filepath.Join(work, ".git"), []byte("gitdir: "+pointer+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return
}
func bindingIdentity2223(t *testing.T, euid int, owner func(string) (os.FileInfo, error)) {
	t.Helper()
	oldUID, oldOwner := bindingEffectiveUID, bindingOwner
	bindingEffectiveUID = func() int { return euid }
	bindingOwner = owner
	t.Cleanup(func() { bindingEffectiveUID, bindingOwner = oldUID, oldOwner })
}

func TestGitBindingMissingLockOwner2223(t *testing.T) {
	for _, kind := range []string{"directory", "absolute", "relative"} {
		t.Run(kind, func(t *testing.T) {
			for _, process := range []struct {
				name    string
				uid     int
				allowed bool
			}{{"same uid", 1001, true}, {"different uid", 2002, false}, {"root is not exempt", 0, false}} {
				t.Run(process.name, func(t *testing.T) {
					work, admin := gitOwnerTree2223(t, kind)
					bindingIdentity2223(t, process.uid, func(path string) (os.FileInfo, error) {
						if path != work {
							t.Errorf("owner source=%q want worktree %q", path, work)
						}
						info, err := os.Lstat(path)
						if err != nil {
							return nil, err
						}
						return ownerUID2223{info, 1001}, nil
					})
					release, err := LockBinding(work, time.Second)
					if release != nil {
						release()
					}
					lock := filepath.Join(admin, bindingLockFileName)
					if process.allowed {
						if err != nil {
							t.Fatalf("owner creation refused: %v", err)
						}
						info, err := os.Stat(lock)
						if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
							t.Fatalf("owner lock not created0600: %v %v", info, err)
						}
					} else {
						if err == nil || release != nil || !strings.Contains(err.Error(), "nothing was created") || !strings.Contains(err.Error(), fmt.Sprintf("uid %d", process.uid)) || !strings.Contains(err.Error(), "uid 1001") {
							t.Errorf("mismatch did not return the shared refusal: release=%v err=%v", release != nil, err)
						}
						if _, err := os.Lstat(lock); !errors.Is(err, os.ErrNotExist) {
							t.Fatalf("non-owner created lock: %v", err)
						}
					}
				})
			}
		})
	}
}

func TestGitBindingExistingLockNeedsNoOwnerProof2223(t *testing.T) {
	work, admin := gitOwnerTree2223(t, "directory")
	if err := os.WriteFile(filepath.Join(admin, bindingLockFileName), nil, 0600); err != nil {
		t.Fatal(err)
	}
	bindingIdentity2223(t, 2002, func(string) (os.FileInfo, error) {
		t.Error("existing lock requested owner proof")
		return nil, os.ErrPermission
	})
	release, err := LockBinding(work, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	release()
}

func TestGitBindingOwnerIsWorktreeNotAdmin2223(t *testing.T) {
	for _, kind := range []string{"absolute", "relative"} {
		t.Run(kind, func(t *testing.T) {
			work, admin := gitOwnerTree2223(t, kind)
			bindingIdentity2223(t, 1001, func(path string) (os.FileInfo, error) {
				if path != work {
					return nil, os.ErrPermission
				} // admin-owner proof is deliberately unavailable
				info, err := os.Lstat(work)
				if err != nil {
					return nil, err
				}
				return ownerUID2223{info, 1001}, nil
			})
			release, err := LockBinding(work, time.Second)
			if err != nil {
				t.Fatalf("admin owner affected worker ownership: %v", err)
			}
			release()
			if _, err := os.Stat(filepath.Join(admin, bindingLockFileName)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestGitBindingUnprovenOwnerCreatesNothing2223(t *testing.T) {
	for _, kind := range []string{"unreadable", "non-Unix stat", "not a directory"} {
		t.Run(kind, func(t *testing.T) {
			work, admin := gitOwnerTree2223(t, "directory")
			file := filepath.Join(t.TempDir(), "file")
			if err := os.WriteFile(file, nil, 0600); err != nil {
				t.Fatal(err)
			}
			bindingIdentity2223(t, 1001, func(path string) (os.FileInfo, error) {
				if path != work {
					t.Errorf("owner source=%q want worktree", path)
				}
				if kind == "unreadable" {
					return nil, os.ErrPermission
				}
				if kind == "not a directory" {
					info, err := os.Lstat(file)
					if err != nil {
						return nil, err
					}
					return ownerUID2223{info, 1001}, nil
				}
				info, err := os.Lstat(work)
				if err != nil {
					return nil, err
				}
				return ownerWithoutUnixStat2076{info}, nil
			})
			release, err := LockBinding(work, time.Second)
			if release != nil {
				release()
			}
			if err == nil || release != nil || !strings.Contains(err.Error(), "owner") {
				t.Errorf("unproven owner allowed: %v", err)
			}
			if _, err := os.Lstat(filepath.Join(admin, bindingLockFileName)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("lock created without owner proof: %v", err)
			}
		})
	}
}
