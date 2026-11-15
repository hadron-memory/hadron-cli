//go:build unix

package delivery

import "syscall"

// noFollow makes opening the lock refuse a symlink at its last component.
const noFollow = syscall.O_NOFOLLOW
