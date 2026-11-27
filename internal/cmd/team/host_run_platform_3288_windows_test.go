//go:build windows

package team

import (
	"os"
	"syscall"
	"testing"
)

// hadron-cli#3288 (S-1 from #3301's BLIND at 0ac7fff9): Windows answers a hard link on a volume without them
// (FAT, exFAT, some network shares) with its own codes, which count as no hard links; other failures do not.
func TestNoHardLinksWindowsCodes3288(t *testing.T) {
	for _, c := range []struct {
		errno syscall.Errno
		want  bool
	}{
		{1, true},    // ERROR_INVALID_FUNCTION
		{50, true},   // ERROR_NOT_SUPPORTED
		{5, false},   // ERROR_ACCESS_DENIED
		{112, false}, // ERROR_DISK_FULL
	} {
		if got := noHardLinks(&os.LinkError{Op: "link", Err: c.errno}); got != c.want {
			t.Errorf("errno %d: noHardLinks %v, want %v", c.errno, got, c.want)
		}
	}
}
