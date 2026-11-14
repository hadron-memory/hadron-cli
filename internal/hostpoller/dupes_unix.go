//go:build !windows

package hostpoller

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// WorktreeAgent is one agent process found working in a worktree (#2027):
// its pid, the redacted #1474 display line (the operator's way to tell WHICH
// process it is), its executable's basename, whether its parent is
// worker-supervise (the codebase's supervision anchor; comm is truncated to
// worker-supervis, so the parent is matched on its cmdline), and /proc's
// start-time ticks — bigger means started later, the keep-oldest key.
type WorktreeAgent struct {
	PID        int
	Display    string
	Base       string
	Supervised bool
	StartTicks int64
}

// agentBasenames is the closed set of agent executables the duplicate scan
// judges (#2027: codex or claude).
var agentBasenames = map[string]bool{"claude": true, "codex": true}

// shellBasenames is the interpreter set an agent shim can hide behind: an
// agent launched as a #! script (a family picker shim, or a supervised
// wrapper's child) shows the interpreter as argv[0] and the script path
// after it.
var shellBasenames = map[string]bool{"sh": true, "bash": true, "dash": true, "ash": true, "zsh": true}

// superviseName is the wrapper script every supervised agent runs under; its
// comm is truncated to worker-supervis, so parents are matched on their
// command line instead.
const superviseName = "worker-supervise"

// agentBase names the agent a /proc cmdline runs, or "". A direct invocation
// names the agent as argv[0]; an interpreter invocation counts only the
// script it runs — and never the supervisor wrapper, whose own argv names the
// agent it watches. Everything else (a shell running "-c", a command merely
// mentioning an agent) is not an agent: no positive spelling, no match.
func agentBase(argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	if b := filepath.Base(argv[0]); agentBasenames[b] {
		return b
	}
	if !shellBasenames[filepath.Base(argv[0])] {
		return ""
	}
	for _, a := range argv[1:] {
		b := filepath.Base(a)
		if b == superviseName {
			return ""
		}
		if agentBasenames[b] {
			return b
		}
	}
	return ""
}

// ScanWorktreeAgents lists every process, other than the caller, whose cwd is
// root or under it and whose executable is an agent binary. Where the host
// cannot tell (no /proc), it reports none: detection degrades, never the
// report. A process that cannot be read is skipped, not guessed at.
func ScanWorktreeAgents(root string) []WorktreeAgent {
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	self := os.Getpid()
	out := []WorktreeAgent{}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == self {
			continue
		}
		cwd, err := os.Readlink(filepath.Join("/proc", e.Name(), "cwd"))
		if err != nil {
			continue // gone, or not ours to inspect
		}
		if cwd != real && !strings.HasPrefix(cwd, real+string(os.PathSeparator)) {
			continue
		}
		argv, ok := argvFromCmdline(readProcFile(e.Name(), "cmdline"))
		base := agentBase(argv)
		if !ok || base == "" {
			continue
		}
		ppid, start, ok := procStat(pid)
		if !ok {
			continue // the process left between the reads; not ours to name
		}
		out = append(out, WorktreeAgent{
			PID:        pid,
			Display:    processArgv(e.Name()),
			Base:       base,
			Supervised: supervisedParent(ppid),
			StartTicks: start,
		})
	}
	return out
}

// readProcFile is one bounded /proc read; errors answer nil.
func readProcFile(pidDir, name string) []byte {
	b, _ := os.ReadFile(filepath.Join("/proc", pidDir, name))
	return b
}

// procStat reads a process's parent pid and start-time ticks from
// /proc/<pid>/stat.
func procStat(pid int) (ppid int, start int64, ok bool) {
	b := readProcFile(strconv.Itoa(pid), "stat")
	if len(b) == 0 {
		return 0, 0, false
	}
	return parseStatLine(string(b))
}

// parseStatLine reads ppid and start ticks (stat fields 4 and 22) from one
// stat line. The comm field can contain spaces and parens, so the fields are
// counted after the LAST ')'; fields[0] is state (3), fields[1] ppid (4),
// fields[19] the start time (22). Miscounting names the wrong parent — a
// supervised agent read as unsupervised gets stopped.
func parseStatLine(stat string) (ppid int, start int64, ok bool) {
	i := bytes.LastIndexByte([]byte(stat), ')')
	if i < 0 {
		return 0, 0, false
	}
	fields := strings.Fields(stat[i+1:])
	if len(fields) < 20 {
		return 0, 0, false
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil || ppid <= 0 {
		return 0, 0, false
	}
	start, err = strconv.ParseInt(fields[19], 10, 64)
	if err != nil {
		return 0, 0, false
	}
	return ppid, start, true
}

// supervisedParent reports whether pid's command line names worker-supervise:
// the wrapper is invoked as `bash /path/worker-supervise …` (and re-execs
// through sg), so any argv slot whose basename is the wrapper counts.
func supervisedParent(pid int) bool {
	argv, ok := argvFromCmdline(readProcFile(strconv.Itoa(pid), "cmdline"))
	if !ok {
		return false
	}
	for _, a := range argv {
		if filepath.Base(a) == superviseName {
			return true
		}
	}
	return false
}
