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
// The exit's STATUS is not read here: the raw siginfo the waitid answer
// fills is laid out differently per architecture (si_status sits at 24 on
// 64-bit non-MIPS kernels, 20 on 32-bit ones, and MIPS moves si_code), so
// reading offsets off that buffer would silently misreport the exit on
// exactly the builds the offsets do not fit. readLeaderExitStatus reads
// the same word the kernel would report to a reaping wait, off /proc,
// with no layout assumption at all.
var waitLeaderExit = func(pid int) error {
	// The kernel insists on a siginfo pointer even though its contents are
	// never read — the status comes from /proc (readLeaderExitStatus), not
	// from this buffer, whose layout differs per architecture.
	var raw [128]byte
	_, _, errno := unix.Syscall6(unix.SYS_WAITID,
		uintptr(unix.P_PID), uintptr(pid), uintptr(unsafe.Pointer(&raw[0])),
		uintptr(unix.WEXITED|unix.WNOWAIT), 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

// readLeaderExitStatus reads the leader's exit status from
// /proc/<pid>/stat's exit_code field — the last field of the line, the
// same word a reaping wait would report (fs/proc/array.c renders
// task->exit_code; WIFEXITED is code&0xff==0 with the status in code>>8,
// a signal death is code&0x7f with WCOREDUMP in code&0x80). It works on
// every architecture for exactly that reason: no siginfo layout is
// involved. The leader is the unreaped zombie waitLeaderExit just waited
// out (or that polling saw), and nothing but this process — the zombie's
// only parent — can reap it, so the word is stable once it exists; a
// zombie that cannot be read (no field yet, or /proc hidden) reads as
// unknownStatus rather than guessed at.
func readLeaderExitStatus(pid int) leaderExit {
	state, f, err := readProcStat(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil || state != 'Z' || len(f) < exitCodeField+1 {
		return leaderExit{kind: unknownStatus}
	}
	w, err := strconv.ParseInt(f[exitCodeField], 10, 64)
	if err != nil || w < 0 || w > 0xffffffff {
		return leaderExit{kind: unknownStatus}
	}
	switch {
	case w&0x7f == 0:
		return leaderExit{kind: exitStatus, status: int(w>>8) & 0xff}
	case w&0x80 != 0:
		return leaderExit{kind: dumpStatus, status: int(w & 0x7f)}
	default:
		return leaderExit{kind: killStatus, status: int(w & 0x7f)}
	}
}

// exitCodeField is exit_code's index among the whitespace fields after
// comm — /proc/<pid>/stat field 52 counting pid as 1, so f[49] counting
// state (field 3) as f[0]. It is the line's LAST field on every kernel
// that has it; a kernel too old to render it simply leaves the line
// shorter, which reads as unknownStatus above.
const exitCodeField = 49

// leaderZombie reports whether /proc shows pid as a zombie — the polling
// hold's exit notice (below), and drained()'s hint that a leader whose
// waiter has not stored exited yet is already the held zombie.
func leaderZombie(pid int) bool {
	state, _, err := readProcStat(fmt.Sprintf("/proc/%d/stat", pid))
	return err == nil && state == 'Z'
}

// pollLeaderState is one poll of the polling hold below: it reports the
// leader's /proc state, or an error when the stat cannot be read. A
// missing entry is fs.ErrNotExist: nothing but this process can reap the
// leader, so the leader disappearing entirely is the pathological case
// the polling hold treats as an exit it cannot describe, not as liveness.
func pollLeaderState(pid int) (byte, error) {
	state, _, err := readProcStat(fmt.Sprintf("/proc/%d/stat", pid))
	return state, err
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
// A member in state Z is not counted while its THREAD COUNT is one: a
// zombie cannot run, and it keeps the number allocated until whoever reaps
// it does, so treating it as drained can never open reuse — while counting
// it made every stop of a group whose orphaned members landed on a subreaper
// (a container's entrypoint is PID 1, and nothing reaps them) read "still
// has members" until the stop's bounded wait gave out: an unconfirmable
// stop whose records are then kept forever. A member in state Z whose
// thread count is ABOVE one is counted as a live member: its thread-group
// leader has exited — /proc shows the tgid entry in state Z — but its
// other threads still run, so the process can still act and the group is
// not drained (#1474; num_threads is field 20 of the same stat
// line, so the two facts come from one read).
//
// A /proc that cannot be listed answers true, and so does a member whose
// stat cannot be read but that still exists: absence of evidence must never
// read as an empty group — an unproven-empty group keeps the stop probing
// and signalling, which at worst signals the number it owns while the
// zombie holds it, never skips a live group's stop.
//
// The listing itself can lie by omission twice over, and both lies void
// the pass the same way — only a whole pass that proves nothing may be
// retried, and only a pass with nothing to retract may answer "empty":
//
//   - A member that forks after the listing and exits before its own stat
//     read is gone from between the two, while the child it forked — which
//     existed before its parent vanished — first appears in the NEXT
//     listing. The daemonising double-fork hits exactly that window.
//   - A member that forks after the listing and only THEN becomes Z is
//     still listed (a zombie stays in /proc until reaped, so nothing
//     "vanished"), but its new child — born after the listing — is absent
//     from it: the pass sees a quiet Z member and no child, and would
//     answer "empty" for a group whose child runs (#1474).
//
// A pass in which any listed process vanished therefore proves nothing,
// and so does a pass that reads a group member as Z for the first time: a
// Z member could have forked right up to its death, and only the NEXT
// listing can show what it left behind — a Z observed in two consecutive
// passes, in contrast, excludes late children for good (nothing dead can
// fork; its children were born before its death, so any that still run
// appear in the second pass's own listing). A running member found answers
// at once — a live member of the group is proof whatever else the pass
// saw.
func groupHasOtherMembers(pgid, leader int) bool {
	prevZ := map[int]bool(nil) // pass 0 has no predecessor: every Z member is new
	for pass := 0; ; pass++ {
		found, void, zs := scanGroupOnce(pgid, leader, prevZ)
		if !void {
			return found
		}
		if pass+1 >= maxGroupScanPasses {
			return true // /proc never held still: not proof of an empty group
		}
		prevZ = zs
	}
}

// scanGroupOnce is one listing of /proc: found reports whether a running
// member of pgid was seen; void reports that the pass proves nothing —
// some listed process disappeared before its stat could be read, or a
// group member was read as Z that the previous pass had not also read as
// Z (it could have forked up to its death; only a Z stable across passes
// excludes late children); zs is this pass's set of member pids in state
// Z, the next pass's previous set.
func scanGroupOnce(pgid, leader int, prevZ map[int]bool) (found, void bool, zs map[int]bool) {
	entries, err := readProcDir("/proc")
	if err != nil {
		return true, false, nil
	}
	zs = map[int]bool{}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == leader {
			continue
		}
		pgrp, state, threads, err := readPgrp(filepath.Join("/proc", e.Name(), "stat"))
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				return true, false, nil // present but unreadable: not proof of absence
			}
			void = true // listed, then gone before the read: the pass proves nothing
			continue
		}
		if pgrp != pgid {
			continue
		}
		if state != 'Z' || threads > 1 {
			return true, false, nil // a member that can still run
		}
		zs[pid] = true
		if !prevZ[pid] {
			void = true // first sighting of this Z: it could have forked up to its death
		}
	}
	return false, void, zs
}

// readPgrp is /proc/<pid>/stat field 5 (the process-group id), field 3
// (the state) and field 20 (the thread count). comm (field 2) is
// parenthesised and may hold spaces and parens, so fields are counted
// from the LAST ')'.
func readPgrp(statPath string) (int, byte, int, error) {
	b, err := os.ReadFile(statPath)
	if err != nil {
		return 0, 0, 0, err
	}
	stat := string(b)
	i := strings.LastIndexByte(stat, ')')
	if i < 0 {
		return 0, 0, 0, fmt.Errorf("malformed stat line")
	}
	f := strings.Fields(stat[i+1:])
	// f[0] is field 3 (state), f[1] is ppid; field 5 is f[2]; field 20
	// (num_threads) is f[17].
	if len(f) < 18 || f[0] == "" {
		return 0, 0, 0, fmt.Errorf("malformed stat line: %d fields after comm", len(f))
	}
	pgrp, err := strconv.Atoi(f[2])
	if err != nil {
		return 0, 0, 0, fmt.Errorf("malformed process-group id %q", f[2])
	}
	threads, err := strconv.Atoi(f[17])
	if err != nil {
		return 0, 0, 0, fmt.Errorf("malformed thread count %q", f[17])
	}
	return pgrp, f[0][0], threads, nil
}

// readProcStat is readPgrp's validation plus the state, the whole field
// slice and the read error, for readLeaderExitStatus's exit_code read at
// the end of the line and pollLeaderState's liveness polls. The
// process-group id and thread count are parsed only as well-formedness
// checks: these readers key on the state and the fields, not on group
// membership or thread counts.
func readProcStat(statPath string) (byte, []string, error) {
	b, err := os.ReadFile(statPath)
	if err != nil {
		return 0, nil, err
	}
	stat := string(b)
	i := strings.LastIndexByte(stat, ')')
	if i < 0 {
		return 0, nil, fmt.Errorf("malformed stat line")
	}
	f := strings.Fields(stat[i+1:])
	if len(f) < 18 || f[0] == "" {
		return 0, nil, fmt.Errorf("malformed stat line: %d fields after comm", len(f))
	}
	if _, err := strconv.Atoi(f[2]); err != nil {
		return 0, nil, fmt.Errorf("malformed process-group id %q", f[2])
	}
	if _, err := strconv.Atoi(f[17]); err != nil {
		return 0, nil, fmt.Errorf("malformed thread count %q", f[17])
	}
	return f[0][0], f, nil
}
