//go:build !windows

package hostpoller

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// WorktreeAgent is one LOGICAL agent found working in a worktree (#2027):
// its pid (the chain's oldest member when a shim or nested one-shot runs
// under it), the redacted #1474 display line (the operator's way to tell
// WHICH process it is), its executable's basename, whether any ancestor names
// worker-supervise (the codebase's supervision anchor; comm is truncated to
// worker-supervis, so ancestors are matched on their cmdline), and /proc's
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

// ScanWorktreeAgents lists every LOGICAL agent process, other than the caller
// and its own ancestors, whose cwd is root or under it and whose executable is
// an agent binary. One logical agent is one process chain: a shim that runs
// the binary as a child, or an agent's own nested one-shot behind a tool
// shell, is the SAME agent as the chain's top (#2037 review: counting the
// chain twice made the real agent of a single healthy worker a stop subject).
// A chain member's descendants merge into the chain's oldest member; an agent
// is supervised when ANY ancestor up to the worktree boundary names
// worker-supervise — the wrapper may sit several layers out (sg, shims) and
// its tree stays inside the worktree, while the typed stray's chain leaves
// through the terminal emulator. Where the host cannot tell
// (no /proc), it reports none: detection degrades, never the report. A
// process that cannot be read is skipped, not guessed at.
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
	mine := ownAncestry()
	found := []rawAgent{}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == self || mine[pid] {
			continue // the caller, or the tree the caller runs in: never subjects
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
		found = append(found, rawAgent{agent: WorktreeAgent{
			PID:        pid,
			Display:    processArgv(e.Name()),
			Base:       base,
			StartTicks: start,
		}, ppid: ppid})
	}
	return collapseChains(found, real)
}

// chainWalkBound caps every ancestry walk (own, merge and supervision) in
// HOPS: deep chains exist (agent → tool shell → one-shot → …), and a pid
// cycle from a reuse race must cost one bounded walk, not a hang — a map-size
// bound would not fire there, its key set stops growing (#2037 review).
const chainWalkBound = 64

// ownAncestry is the set of the caller's ancestors: the sweep never counts —
// so never stops — the process tree it runs in. A hand-run sweep inside a
// live agent must not find its own parent as an unsupervised duplicate.
func ownAncestry() map[int]bool {
	out := map[int]bool{}
	for pid, hops := os.Getppid(), 0; pid > 1 && hops < chainWalkBound; hops++ {
		out[pid] = true
		next, _, ok := procStat(pid)
		if !ok {
			break
		}
		if next == pid {
			break
		}
		pid = next
	}
	return out
}

// collapseChains merges every raw match that is a descendant of another match
// (through any intermediary — a shim above the binary, a tool shell between
// an agent and its one-shot) into that match, keeping the chain's oldest
// member as the logical agent, and marks the survivor supervised when any
// ancestor up to the worktree boundary names worker-supervise.
func collapseChains(found []rawAgent, root string) []WorktreeAgent {
	byPid := map[int]rawAgent{}
	for _, r := range found {
		byPid[r.agent.PID] = r
	}
	// rootOf walks r's live ancestry (bounded, worktree-bounded): the first
	// census member it reaches is r's logical agent; r itself when the chain
	// has none above the boundary.
	rootOf := func(r rawAgent) int {
		pid := r.agent.PID
		for p, hops := r.ppid, 0; p > 1 && hops < chainWalkBound; hops++ {
			if up, ok := byPid[p]; ok {
				pid = up.agent.PID
				p = up.ppid
				continue // walk the member's own parent next
			}
			if !withinRoot(p, root) {
				break // the chain left the worktree: no member can be above
			}
			next, _, ok := procStat(p)
			if !ok || next == p {
				break
			}
			p = next
		}
		return pid
	}
	out := []WorktreeAgent{}
	for _, r := range found {
		if top := rootOf(r); top != r.agent.PID {
			continue // a descendant of another match: the same logical agent
		}
		r.agent.Supervised = supervisedChain(r.agent.PID, root)
		out = append(out, r.agent)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PID < out[j].PID })
	return out
}

// rawAgent is one /proc match before the chain collapse (#2027).
type rawAgent struct {
	agent WorktreeAgent
	ppid  int
}

// supervisedChain reports whether any ancestor of pid, up to the worktree
// boundary, names worker-supervise: the wrapper may launch through sg or a
// shim, so the immediate parent alone under-detects supervision, and the
// wrapper's own tree stays inside the worktree — a typed stray's chain leaves
// through the terminal emulator, so the boundary is what tells the worker's
// own process tree from a shell someone typed into. The walk reads live
// parentage; a process never GAINS worker-supervise above it (orphan
// re-parenting goes to init or a subreaper, never to a fresh wrapper), so a
// supervision read at census time can only be stale toward KEEPING an agent.
func supervisedChain(pid int, root string) bool {
	ppid, _, ok := procStat(pid)
	if !ok {
		return false
	}
	for p, hops := ppid, 0; p > 1 && hops < chainWalkBound; hops++ {
		if supervisedParent(p) {
			return true
		}
		if !withinRoot(p, root) {
			break // past the worktree boundary: nobody's supervision anchor
		}
		next, _, ok := procStat(p)
		if !ok || next == p {
			break
		}
		p = next
	}
	return false
}

// withinRoot reports whether pid's current working directory is root or under
// it — the walk boundary of #2027's census: the worktree's own process tree
// stays within it, and any chain that leaves it (a terminal emulator, the
// test runner above a fixture) is somebody else's.
func withinRoot(pid int, root string) bool {
	cwd, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), "cwd"))
	if err != nil {
		return false
	}
	return cwd == root || strings.HasPrefix(cwd, root+string(os.PathSeparator))
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
