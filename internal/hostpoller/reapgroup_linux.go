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

// The waitid answer's si_code values for a waited-out exit (the kernel's
// stable UAPI; this x/sys pin does not export them). Every other si_code
// a WEXITED wait can report is not an exit at all, and reads as
// unknownStatus.
const (
	cldExited = 1
	cldKilled = 2
	cldDumped = 3
)

// waitLeaderExit waits for the leader to exit WITHOUT reaping it — the
// process stays a zombie, and a zombie still holds its pid, and with it
// the process-group id (that same number), allocated until the reap — and
// reads the exit's STATUS from the answer itself: the kernel fills the
// siginfo with how the leader left (si_code) and the word a reaping wait
// would report for it (si_status: the exit code, or the signal). A
// package var so a test can model a kernel where the hold cannot be taken.
//
// The two words are read at their per-architecture places, computed rather
// than hardcoded: si_code through x/sys's Siginfo field, whose declaration
// already encodes the MIPS word-order swap (__ARCH_HAS_SWAPPED_SIGINFO —
// every MIPS, not only MIPS64), and si_status at a pointer-aligned offset
// derived from the build's word size — 24 on a 64-bit build, 20 on a
// 32-bit one —
// because the siginfo union starts after the three leading int32 words,
// padded up to pointer alignment, and si_status is the third word of it.
// This is the same word for every caller with no privilege gate: the
// /proc <pid>/stat exit_code field this read replaces is rendered as a
// literal 0 whenever the kernel's ptrace_may_access check fails — a leader
// running under different credentials (sudo, any setuid wrapper) — which
// read a failed or signalled exit as an authoritative "exit status 0".
var waitLeaderExit = func(pid int) (leaderExit, error) {
	var info unix.Siginfo
	_, _, errno := unix.Syscall6(unix.SYS_WAITID,
		uintptr(unix.P_PID), uintptr(pid), uintptr(unsafe.Pointer(&info)),
		uintptr(unix.WEXITED|unix.WNOWAIT), 0, 0)
	if errno != 0 {
		return leaderExit{kind: unknownStatus}, errno
	}
	// si_code comes through the typed x/sys struct, whose declaration
	// already encodes the MIPS word-order swap (__ARCH_HAS_SWAPPED_SIGINFO
	// — every MIPS, not only MIPS64). si_status sits eight bytes into the
	// union (after its pid and uid words); the union starts at the three
	// leading words rounded up to pointer alignment, so its offset is
	// derived from the build's word size. The struct itself is the waitid
	// buffer — properly aligned by its own type — and the kernel fills it
	// in the build's native byte order.
	union := (12 + int(unsafe.Sizeof(uintptr(0))) - 1) &^ (int(unsafe.Sizeof(uintptr(0))) - 1)
	status := *(*int32)(unsafe.Add(unsafe.Pointer(&info), union+8))
	switch info.Code {
	case cldExited:
		return leaderExit{kind: exitStatus, status: int(status)}, nil
	case cldKilled:
		return leaderExit{kind: killStatus, status: int(status)}, nil
	case cldDumped:
		return leaderExit{kind: dumpStatus, status: int(status)}, nil
	default:
		return leaderExit{kind: unknownStatus}, nil
	}
}

// leaderZombieAlone reports whether /proc shows pid as a zombie whose
// thread group has FULLY exited — state Z with a thread count of one, the
// shape a reap can finalize. A Z state alone is not that proof: a process
// whose main thread exited while other threads still run shows state Z
// with a thread count above one (#1474 — such a leader can still act, so
// drained() must not treat it as the held zombie its scan may exclude).
func leaderZombieAlone(pid int) bool {
	state, f, err := readProcStat(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil || state != 'Z' {
		return false
	}
	threads, _ := strconv.Atoi(f[17]) // readProcStat proved it numeric
	return threads == 1
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
// pgid, other than the leader itself, that can still RUN. The leader is
// excludable only as a zombie whose thread group fully exited — the caller
// (drained, on the hold path) has proved that, and the scan itself
// double-checks it before excluding the entry (scanGroupOnce).
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
// Z, the next pass's previous set. The leader is not a member for this
// purpose, but a leader entry that still shows RUNNING THREADS — state Z
// with a thread count above one, the exited-main-thread shape — is
// proof the group is not drained, so it is checked before the exclusion.
func scanGroupOnce(pgid, leader int, prevZ map[int]bool) (found, void bool, zs map[int]bool) {
	entries, err := readProcDir("/proc")
	if err != nil {
		return true, false, nil
	}
	zs = map[int]bool{}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		pgrp, state, threads, err := readGroupStat(filepath.Join("/proc", e.Name(), "stat"))
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
		if pid == leader {
			if state == 'Z' && threads > 1 {
				return true, false, nil // the leader's own threads still run
			}
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

// readGroupStat is the group scan's read of a /proc/<pid>/stat line:
// field 5 (the process-group id), field 3 (the state) and field 20 (the
// thread count), parsed and validated once by readProcStat.
func readGroupStat(statPath string) (int, byte, int, error) {
	state, f, err := readProcStat(statPath)
	if err != nil {
		return 0, 0, 0, err
	}
	pgrp, _ := strconv.Atoi(f[2])
	threads, _ := strconv.Atoi(f[17])
	return pgrp, state, threads, nil
}

// readProcStat reads and validates one /proc/<pid>/stat line: the state
// (field 3), the whole field slice after comm, and the read error. comm
// (field 2) is parenthesised and may hold spaces and parens, so fields
// are counted from the LAST ')': f[0] is field 3 (state), f[2] is field 5
// (the process-group id), f[17] is field 20 (the thread count), and the
// id and count are parsed here as well-formedness checks — readGroupStat
// returns them, and leaderZombieAlone reads the count, off the same
// validated slice.
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
