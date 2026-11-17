//go:build unix

package delivery

import (
	"fmt"
	"os"
	"syscall"
)

// These narrow seams let tests model a host running as another uid without
// changing process credentials or the ownership of shared paths.
var bindingEffectiveUID = os.Geteuid
var bindingOwner = os.Lstat

// allowBindingCreation is consulted only when LockBinding would create a
// plain-directory cache or its lock. Existing files do not require uid proof.
func allowBindingCreation(worktree string) error {
	info, err := bindingOwner(worktree)
	if err != nil {
		return fmt.Errorf("the worktree owner of %s cannot be read: nothing was created: %w", worktree, err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() {
		return fmt.Errorf("the worktree owner of %s cannot be proven: nothing was created", worktree)
	}
	uid := bindingEffectiveUID()
	if uint64(uid) != uint64(stat.Uid) {
		return fmt.Errorf("the binding cache in %s is missing and this host runs as uid %d but the worktree belongs to uid %d: nothing was created; start the worker once so its own session start creates it, or run the host as that user", worktree, uid, stat.Uid)
	}
	return nil
}
