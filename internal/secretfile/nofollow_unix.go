//go:build unix

package secretfile

import "syscall"

// noFollow refuses a symlink as the final path component.
const noFollow = syscall.O_NOFOLLOW
