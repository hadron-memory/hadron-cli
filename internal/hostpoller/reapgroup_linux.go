//go:build linux

package hostpoller

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"
)

// leaderHoldCapable is the platform fact the stop's ownership proof rests
// on: Linux can wait a leader out WITHOUT reaping it (waitid WNOWAIT), so
// the zombie holds the launch's pid — the pgid — allocated until the stop
// reaps it (#1474). A pidfd does not do this: it holds the pid object for
// operations through the fd, while free_pid drops the allocator's idr entry
// at the reap, so the NUMBER becomes reusable even with the fd open.
const leaderHoldCapable = true

// waitLeaderExit waits for the leader to exit WITHOUT reaping it: the
// process stays a zombie, and a zombie still holds its pid — and with it
// the process-group id, which is that same number — allocated, so nothing
// can reuse the group's number from the leader's exit until the reap. A
// package var so a test can model a kernel where the hold cannot be taken.
//
// The exit's STATUS is read from the same waitid answer. x/sys's Siginfo
// leaves the SIGCHLD union opaque (no exported Status field), and no other
// reader exists that does not reap: wait4 fills its status word only by
// reaping, and under WNOWAIT the kernel writes the status to siginfo alone.
// So this calls waitid directly with a buffer laid out as the kernel's
// siginfo_t — si_code at offset 8, si_status at 24, native byte order — a
// UAPI the kernel cannot reshape without breaking every C program.
var waitLeaderExit = func(pid int) (leaderExit, error) {
	var raw [128]byte
	_, _, errno := unix.Syscall6(unix.SYS_WAITID,
		uintptr(unix.P_PID), uintptr(pid), uintptr(unsafe.Pointer(&raw[0])),
		uintptr(unix.WEXITED|unix.WNOWAIT), 0, 0)
	if errno != 0 {
		return leaderExit{}, errno
	}
	return leaderExit{
		code:   int32(binary.NativeEndian.Uint32(raw[8:12])),
		status: binary.NativeEndian.Uint32(raw[24:28]),
	}, nil
}

// maxGroupScanPasses bounds the scan's retries on a /proc that never holds
// still (a host churning short-lived processes through every pass): past
// the bound the scan answers "members" — the fail-safe direction, which
// keeps the stop probing and the zombie held rather than reading a live
// group as empty.
const maxGroupScanPasses = 8

// readProcDir lists /proc, as a package var so a test can hand the scan a
// listing that changes between passes (the vanishing-entry race).
var readProcDir = os.ReadDir

// groupHasOtherMembers reports whether /proc shows a member of process group
// pgid, other than the leader itself, that can still RUN.
//
// A member in state Z is not counted: a zombie cannot run, and it keeps the
// number allocated until whoever reaps it does so, so treating it as
// drained can never open reuse — while counting it made every stop of a
// group whose orphaned members landed on a subreaper (a container's
// entrypoint is PID 1, and nothing reaps them) read "still has members"
// until the stop's bounded wait gave out: an unconfirmable stop whose
// records are then kept forever.
//
// A /proc that cannot be listed answers true, and so does a member whose
// stat cannot be read but that still exists: absence of evidence must never
// read as an empty group — an unproven-empty group keeps the stop probing
// and signalling, which at worst signals the number it owns while the
// zombie holds it, never skips a live group's stop.
//
// The listing itself can lie by omission: a member that forks after the
// listing and exits before its own stat read is gone from between the two,
// while the child it forked — which existed before its parent vanished —
// first appears in the NEXT listing. The daemonising double-fork hits
// exactly that window, and the scan would read a live group as empty. A
// pass in which any listed process vanished therefore proves nothing: only
// a whole pass with nothing vanishing may answer "empty", and a vanishing
// pass is retried. A running member found answers at once — a live member
// of the group is proof whatever else the pass saw.
func groupHasOtherMembers(pgid, leader int) bool {
	for pass := 0; ; pass++ {
		found, vanished := scanGroupOnce(pgid, leader)
		if !vanished {
			return found
		}
		if pass+1 >= maxGroupScanPasses {
			return true // /proc never held still: not proof of an empty group
		}
	}
}

// scanGroupOnce is one listing of /proc: found reports whether a running
// member of pgid was seen; vanished reports that some listed process
// disappeared before its stat could be read, which voids the pass's "empty"
// answer.
func scanGroupOnce(pgid, leader int) (found, vanished bool) {
	entries, err := readProcDir("/proc")
	if err != nil {
		return true, false
	}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == leader {
			continue
		}
		pgrp, state, err := readPgrp(filepath.Join("/proc", e.Name(), "stat"))
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				return true, false // present but unreadable: not proof of absence
			}
			vanished = true // listed, then gone before the read: the pass proves nothing
			continue
		}
		if pgrp == pgid && state != 'Z' {
			return true, false
		}
	}
	return false, vanished
}

// readPgrp is /proc/<pid>/stat field 5 (the process-group id) and field 3
// (the state). comm (field 2) is parenthesised and may hold spaces and
// parens, so fields are counted from the LAST ')'.
func readPgrp(statPath string) (int, byte, error) {
	b, err := os.ReadFile(statPath)
	if err != nil {
		return 0, 0, err
	}
	stat := string(b)
	i := strings.LastIndexByte(stat, ')')
	if i < 0 {
		return 0, 0, fmt.Errorf("malformed stat line")
	}
	f := strings.Fields(stat[i+1:])
	// f[0] is field 3 (state), f[1] is ppid; field 5 is f[2].
	if len(f) < 3 || f[0] == "" {
		return 0, 0, fmt.Errorf("malformed stat line: %d fields after comm", len(f))
	}
	pgrp, err := strconv.Atoi(f[2])
	if err != nil {
		return 0, 0, fmt.Errorf("malformed process-group id %q", f[2])
	}
	return pgrp, f[0][0], nil
}
