//go:build linux

package hostpoller

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// StopAgents stops every agent process working inside path that this host
// did not launch itself, each with its process group (hadron-cli#2057 C2a).
// The agents are the /proc census the duplicate sweep uses
// (ScanWorktreeAgents, #2027): one entry per logical agent whose working
// directory is in the tree.
func (h ExecHost) StopAgents(_ context.Context, path string, authorize Authorize) ([]AgentStop, error) {
	grace := h.TerminateGrace
	if grace <= 0 {
		grace = 5 * time.Second
	}
	return stopWorktreeAgents(path, grace, authorize)
}

// stopSignalLease is the pinned-signal window ProcessHandle wants. A stop
// here has no server lease to respect, so it only has to outlast the whole
// procedure.
const stopSignalLease = time.Hour

func stopWorktreeAgents(path string, grace time.Duration, authorize Authorize) ([]AgentStop, error) {
	root, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		return nil, fmt.Errorf("no /proc to identify processes by: %w", err)
	}
	out := []AgentStop{}
	for _, a := range ScanWorktreeAgents(root) {
		out = append(out, stopAgentGroup(a, root, grace, authorize))
	}
	return out, nil
}

// pinned is one process held by a pidfd: a signal through it reaches that
// process or nothing, never a later holder of the number.
type pinned struct {
	pid int
	h   ProcessHandle
}

// gone reports whether the pinned process no longer runs, and only on a
// DEFINITIVE answer: its pidfd says no such process, its /proc entry is
// missing or is another process's (the number was reaped and retaken), or it
// is a zombie, which cannot run. Any other failure of the liveness probe
// (a permission error, a clock fault) is not evidence of an exit: it reads as
// still running, so the stop is reported unconfirmed, never as done.
func (m pinned) gone(ctrl LinuxProcessControl, t0 time.Duration) bool {
	if _, err := m.h.SignalWithin(syscall.Signal(0), t0, stopSignalLease, ctrl.Now); err != nil {
		return errors.Is(err, syscall.ESRCH)
	}
	st, err := statReader(m.pid)
	if err != nil {
		// Only a missing /proc entry is an exit; a permission, I/O or parse
		// failure says nothing about the process.
		return errors.Is(err, os.ErrNotExist)
	}
	return st.start != m.h.Identity().StartTime || st.state == "Z"
}

// statReader reads /proc/<pid>/stat; a test swaps it.
var statReader = readStatErr

// readStatErr is readStat with the failure kept: os.ErrNotExist for a process
// that is gone, any other error for one that cannot be read or understood.
func readStatErr(pid int) (procStatLine, error) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return procStatLine{}, err
	}
	st, ok := parseProcStat(string(b))
	if !ok {
		return procStatLine{}, fmt.Errorf("malformed /proc/%d/stat", pid)
	}
	return st, nil
}

// procStatLine is the part of /proc/<pid>/stat a stop decides on.
type procStatLine struct {
	state string
	pgrp  int
	start string
}

// readStat reads state, process group and start time (stat fields 3, 5 and
// 22); !ok for any failure (callers that must tell absence from a failure use
// readStatErr).
func readStat(pid int) (procStatLine, bool) {
	st, err := readStatErr(pid)
	return st, err == nil
}

// parseProcStat counts the fields after the LAST ')' since comm can hold
// spaces and parens.
func parseProcStat(s string) (procStatLine, bool) {
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return procStatLine{}, false
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 20 {
		return procStatLine{}, false
	}
	pgrp, err := strconv.Atoi(f[2])
	if err != nil {
		return procStatLine{}, false
	}
	return procStatLine{state: f[0], pgrp: pgrp, start: f[19]}, true
}

// pinGroup pins every running member of process group pgid and reports
// whether the WHOLE group may be signalled: every member works inside root
// and none is this process or one of its ancestors. A group with a member
// elsewhere is not the worker's alone — a terminal multiplexer or a daemon
// that started the agent without a group of its own would be in it — so it
// is never signalled as a group. skip holds pids already pinned.
func pinGroup(ctrl LinuxProcessControl, pgid int, root string, skip map[int]bool) (members []pinned, whole bool) {
	whole = true
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, false
	}
	self := os.Getpid()
	mine := ownAncestry()
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		st, ok := readStat(pid)
		if !ok || st.pgrp != pgid || st.state == "Z" {
			continue
		}
		if pid == self || mine[pid] {
			whole = false
			continue
		}
		if skip[pid] {
			continue
		}
		h, err := ctrl.Observe(pid)
		if err != nil {
			continue // it left between the listing and the pin
		}
		// The pin holds the process; the reads below are its own only if its
		// start time still matches and it is still in the group.
		again, ok := readStat(pid)
		if !ok || again.start != h.Identity().StartTime || again.pgrp != pgid {
			h.Close()
			continue
		}
		if !withinRoot(pid, root) {
			whole = false
			h.Close()
			continue
		}
		members = append(members, pinned{pid: pid, h: h})
	}
	return members, whole
}

// stopAgentGroup stops one census agent: pinned SIGTERM to its process
// group, grace, pinned SIGKILL to whatever still runs, a short wait. Every
// signal goes through a pidfd, so a number that was reaped and reused is
// never signalled. When the agent's group is not the worker's alone, only
// the agent process itself is signalled and the detail says so.
func stopAgentGroup(a WorktreeAgent, root string, grace time.Duration, authorize Authorize) AgentStop {
	return stopAgentGroupWith(a, root, grace, authorize, LinuxProcessControl{}.Observe)
}

// stopAgentGroupWith is stopAgentGroup with the process pin injectable.
func stopAgentGroupWith(a WorktreeAgent, root string, grace time.Duration, authorize Authorize, observe func(int) (ProcessHandle, error)) AgentStop {
	name := fmt.Sprintf("pid %d (%s)", a.PID, a.Display)
	ctrl := LinuxProcessControl{}
	t0, err := ctrl.Now()
	if err != nil {
		return AgentStop{PID: a.PID, Detail: fmt.Sprintf("%s was not touched: %v", name, err)}
	}
	lead, err := observe(a.PID)
	if err != nil {
		// Only a process that is gone has exited. Any other failure to pin it
		// (another user's process, no free descriptors, an unreadable /proc
		// entry) means nothing was signalled and nothing is known.
		if errors.Is(err, syscall.ESRCH) || errors.Is(err, os.ErrNotExist) {
			return AgentStop{PID: a.PID, Stopped: true, Detail: name + " had already exited"}
		}
		return AgentStop{PID: a.PID, Detail: fmt.Sprintf("%s was not touched: it could not be pinned: %v", name, err)}
	}
	if lead.Identity().StartTime != strconv.FormatInt(a.StartTicks, 10) {
		lead.Close()
		return AgentStop{PID: a.PID, Detail: name + " was not touched: the process now holding that pid is not the one found in the worktree"}
	}
	targets := []pinned{{pid: a.PID, h: lead}}
	defer func() {
		for _, m := range targets {
			m.h.Close()
		}
	}()
	// The agent may have moved since the census: its CURRENT directory must
	// still be inside the worktree before it is a signal target.
	if !withinRoot(a.PID, root) {
		return AgentStop{PID: a.PID, Detail: name + " was not touched: it is no longer working inside the worktree"}
	}
	pinnedPIDs := map[int]bool{a.PID: true}
	scope := "its process group"
	pgid, err := unix.Getpgid(a.PID)
	whole := err == nil && pgid > 1 && pgid != unix.Getpgrp()
	if whole {
		var more []pinned
		more, whole = pinGroup(ctrl, pgid, root, pinnedPIDs)
		if whole {
			for _, m := range more {
				pinnedPIDs[m.pid] = true
			}
			targets = append(targets, more...)
		} else {
			for _, m := range more {
				m.h.Close()
			}
		}
	}
	if !whole {
		scope = "the agent process only (its process group has members outside the worktree, which were not touched)"
	}
	signal := func(sig syscall.Signal) error {
		var first error
		for _, m := range targets {
			if _, err := m.h.SignalWithin(sig, t0, stopSignalLease, ctrl.Now); err != nil && !errors.Is(err, syscall.ESRCH) && first == nil {
				first = err
			}
		}
		return first
	}
	allGone := func(wait time.Duration) bool {
		deadline := time.Now().Add(wait)
		for {
			running := false
			for _, m := range targets {
				if !m.gone(ctrl, t0) {
					running = true
					break
				}
			}
			if !running {
				return true
			}
			if !time.Now().Before(deadline) {
				return false
			}
			time.Sleep(groupPollInterval)
		}
	}
	if authorize == nil {
		authorize = allow
	}
	// signalHeld sends one signal with the binding held unchanged from the
	// check through the signal, and released before anything waits.
	signalHeld := func(sig syscall.Signal) (err error, declined bool) {
		release, ok := authorize()
		if !ok {
			return nil, true
		}
		defer release()
		return signal(sig), false
	}
	const changed = "the worktree's session binding changed, or could not be locked, while this end was running"
	if err, declined := signalHeld(syscall.SIGTERM); declined {
		return AgentStop{PID: a.PID, Detail: name + " was not touched: " + changed}
	} else if err != nil && !allGone(0) {
		return AgentStop{PID: a.PID, Detail: fmt.Sprintf("%s could not be stopped: %v", name, err)}
	}
	if allGone(grace) {
		return AgentStop{PID: a.PID, Stopped: true, Detail: fmt.Sprintf("terminated %s, %s", name, scope)}
	}
	if whole {
		// A member forked while the group was being terminated is in the
		// group too: pin what joined before the kill.
		more, still := pinGroup(ctrl, pgid, root, pinnedPIDs)
		if still {
			targets = append(targets, more...)
		} else {
			for _, m := range more {
				m.h.Close()
			}
		}
	}
	if err, declined := signalHeld(syscall.SIGKILL); declined {
		return AgentStop{PID: a.PID, Detail: name + " ignored SIGTERM for " + grace.String() + " and was not killed: " + changed}
	} else if err != nil && !allGone(0) {
		return AgentStop{PID: a.PID, Detail: fmt.Sprintf("%s ignored SIGTERM for %s and SIGKILL failed: %v", name, grace, err)}
	}
	if allGone(postKillWait) {
		return AgentStop{PID: a.PID, Stopped: true, Detail: fmt.Sprintf("killed %s, %s, after it ignored SIGTERM for %s", name, scope, grace)}
	}
	return AgentStop{PID: a.PID, Detail: fmt.Sprintf("%s did not exit within %s of SIGKILL", name, postKillWait)}
}
