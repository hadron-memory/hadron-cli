//go:build unix

package delivery

import (
	"io"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

// relaunchStampDir is the directory that holds the binding (and so the
// stamp), or "" when the tree has none.
func relaunchStampDir(worktree string) string {
	dir, state := bindingDir(worktree)
	switch state {
	case bindingDirGit:
		return dir
	case bindingDirPlain:
		return filepath.Join(worktree, plainCacheName)
	}
	return ""
}

// openRelaunchStamp opens the stamp relative to its directory, refusing a
// link at either level, never blocking on a FIFO, and only a regular file.
func openRelaunchStamp(worktree string) (dirFD int, f *os.File, ok bool) {
	dir := relaunchStampDir(worktree)
	if dir == "" {
		return -1, nil, false
	}
	dfd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, nil, false
	}
	fd, err := unix.Openat(dfd, RelaunchStampFile, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		_ = unix.Close(dfd)
		return -1, nil, false
	}
	f = os.NewFile(uintptr(fd), RelaunchStampFile)
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		_ = f.Close()
		_ = unix.Close(dfd)
		return -1, nil, false
	}
	return dfd, f, true
}

// HasRelaunchStamp says whether a readable stamp sits beside the binding. It
// reads and consumes nothing: the poller asks it before any network call, so
// an ordinary exit costs nothing.
func HasRelaunchStamp(worktree string) bool {
	dfd, f, ok := openRelaunchStamp(worktree)
	if !ok {
		return false
	}
	_ = f.Close()
	_ = unix.Close(dfd)
	return true
}

// ConsumeRelaunchStamp reads the stamp once and removes it, then reports
// whether it named exactly this worker and session within ttl of now. The
// caller holds the binding lock. A stamp that was read is gone either way, so
// it can never match a later exit: one stamp, at most one relaunch.
func ConsumeRelaunchStamp(worktree, worker, session string, now time.Time, ttl time.Duration) bool {
	dfd, f, ok := openRelaunchStamp(worktree)
	if !ok {
		return false
	}
	defer func() { _ = unix.Close(dfd) }()
	b, err := io.ReadAll(io.LimitReader(f, maxTerminalRecord+1))
	_ = f.Close()
	// Removed relative to the directory that was opened: a swapped path
	// cannot redirect the unlink.
	if unix.Unlinkat(dfd, RelaunchStampFile, 0) != nil {
		return false // a stamp that cannot be used up must not be honoured
	}
	if err != nil || len(b) > maxTerminalRecord {
		return false
	}
	s, ok := parseRelaunchStamp(b)
	return ok && s.matches(worker, session, now, ttl)
}
