//go:build windows

package team

import (
	"errors"
	"syscall"
)

// platformNoHardLinks: CreateHardLink on a volume without hard links (FAT, exFAT) answers ERROR_INVALID_FUNCTION,
// and some network redirectors ERROR_NOT_SUPPORTED (hadron-cli#3288 S-1).
func platformNoHardLinks(err error) bool {
	const errorInvalidFunction, errorNotSupported syscall.Errno = 1, 50
	return errors.Is(err, errorInvalidFunction) || errors.Is(err, errorNotSupported)
}
