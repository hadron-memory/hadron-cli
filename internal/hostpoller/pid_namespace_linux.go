//go:build linux

package hostpoller

import "golang.org/x/sys/unix"

func currentPIDNamespace() (PIDNamespaceIdentity, error) {
	var stat unix.Stat_t
	if err := unix.Stat("/proc/self/ns/pid", &stat); err != nil {
		return PIDNamespaceIdentity{}, err
	}
	return PIDNamespaceIdentity{Device: stat.Dev, Inode: stat.Ino}, nil
}
