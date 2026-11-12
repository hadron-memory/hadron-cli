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

// noteGroupBeforeReap runs in the launch's waiter goroutine, before cmd.Wait
// reaps the leader (#1474 delta review P2-1, Jane D1). It blocks until the
// leader has exited but is still a zombie — waitid with WNOWAIT waits
// without reaping — so the process-group id cannot be reused while the scan
// runs, and then reads /proc for any OTHER member of the group:
//
//   - none: the group is provably empty at reap time. The id becomes
//     reusable the instant the zombie is reaped, so the agent is marked
//     (groupEmpty; Terminate's stopGuard) and a later Terminate confirms the
//     stop without ever probing or signalling a number that may since have
//     been reused — the old probe could hit a reused number whenever
//     Terminate ran after the leader's exit, which the exited-before-binding
//     path always does.
//   - members: the group stays allocated while any member lives, and
//     Terminate probes and signals as before — that is exactly the group a
//     wrapper's child keeps alive, the stop this exists to make. The
//     empty-then-reused window shrinks from "any time after the leader's
//     exit" to "the group emptied after this scan and the number was reused
//     before Terminate's probe answered".
//
// Non-Linux Unix builds have no /proc to scan and keep the probe behaviour;
// see reapgroup_other.go.
func (a *execAgent) noteGroupBeforeReap() {
	var info unix.Siginfo
	if err := unix.Waitid(unix.P_PID, a.pid, &info, unix.WEXITED|unix.WNOWAIT, nil); err != nil {
		return // no proof: leave unmarked, keep the probe behaviour
	}
	if !groupHasOtherMembers(a.pid, a.pid) {
		a.groupEmpty.Store(true)
	}
}

// groupHasOtherMembers reports whether /proc shows a member of process group
// pgid other than leader itself (the zombie leader, which still holds the
// group while this runs). A /proc that cannot be listed answers true, and so
// does a member whose stat cannot be read: absence of evidence must never
// read as an empty group — an unmarked agent keeps probing, which at worst
// signals the number it always signalled, never skips a live group's stop.
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
