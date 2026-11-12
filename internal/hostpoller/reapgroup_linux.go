//go:build linux

package hostpoller

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// leaderHoldCapable is the platform fact the stop's ownership proof rests
// on: Linux can wait a leader out WITHOUT reaping it (waitid WNOWAIT), so
// the zombie holds the launch's pid — the pgid — allocated until the stop
// reaps it (#1474 delta round 3, Ada #102351). A pidfd was tried first and
// does NOT do this: it holds the pid object for operations through the fd,
// while free_pid drops the allocator's idr entry at the reap, so the NUMBER
// becomes reusable even with the fd open (Jane D1 / Jonas N1 / Cameron's
// QA reproduction, all forced-reuse in private PID namespaces).
const leaderHoldCapable = true

// waitLeaderExit waits for the leader to exit WITHOUT reaping it: the
// process stays a zombie, and a zombie still holds its pid — and with it
// the process-group id, which is that same number — allocated, so nothing
// can reuse the group's number from the leader's exit until the reap. A
// package var so a test can model a kernel where the hold cannot be taken.
var waitLeaderExit = func(pid int) error {
	var info unix.Siginfo
	return unix.Waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WNOWAIT, nil)
}

// groupHasOtherMembers reports whether /proc shows a member of process group
// pgid other than the leader itself — the held zombie, which still belongs
// to the group and would otherwise make every answer read "alive". A /proc
// that cannot be listed answers true, and so does a member whose stat cannot
// be read: absence of evidence must never read as an empty group — an
// unproven-empty group keeps the stop probing and signalling, which at worst
// signals the number it owns while the zombie holds it, never skips a live
// group's stop.
func groupHasOtherMembers(pgid, leader int) bool {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return true
	}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == leader {
			continue
		}
		pgrp, err := readPgrp(filepath.Join("/proc", e.Name(), "stat"))
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				return true // present but unreadable: not proof of absence
			}
			continue // gone between the listing and the read
		}
		if pgrp == pgid {
			return true
		}
	}
	return false
}

// readPgrp is /proc/<pid>/stat field 5, the process-group id. comm (field 2)
// is parenthesised and may hold spaces and parens, so fields are counted
// from the LAST ')' — the same convention as parseStartTime.
func readPgrp(statPath string) (int, error) {
	b, err := os.ReadFile(statPath)
	if err != nil {
		return 0, err
	}
	stat := string(b)
	i := strings.LastIndexByte(stat, ')')
	if i < 0 {
		return 0, fmt.Errorf("malformed stat line")
	}
	f := strings.Fields(stat[i+1:])
	// f[0] is field 3 (state), f[1] is ppid; field 5 is f[2].
	if len(f) < 3 {
		return 0, fmt.Errorf("malformed stat line: %d fields after comm", len(f))
	}
	pgrp, err := strconv.Atoi(f[2])
	if err != nil {
		return 0, fmt.Errorf("malformed process-group id %q", f[2])
	}
	return pgrp, nil
}
