//go:build linux

package auth

import "golang.org/x/sys/unix"

const (
	termiosGet = unix.TCGETS
	termiosSet = unix.TCSETS
)
