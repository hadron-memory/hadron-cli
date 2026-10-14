//go:build !windows

package hostpoller

import "syscall"

func syscallKill(pid int) error { return syscall.Kill(pid, syscall.SIGKILL) }
