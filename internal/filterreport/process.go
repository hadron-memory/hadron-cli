package filterreport

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type process struct {
	pid, parent             int
	cwd                     string
	foreign, wrapper, agent bool
}

func within(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func wrapper(argv []string) bool {
	if len(argv) == 0 {
		return false
	}
	if filepath.Base(argv[0]) == "worker-supervise" {
		return true
	}
	if len(argv) < 2 {
		return false
	}
	switch filepath.Base(argv[0]) {
	case "bash", "sh", "dash", "zsh":
		return filepath.Base(argv[1]) == "worker-supervise"
	}
	return false
}

// readProcesses reads metadata only. Foreign-uid argv is never inspected.
func readProcesses(root string, uid int) ([]process, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, errors.New("process_scan_failed")
	}
	result := []process{}
	for _, entry := range entries {
		pid, e := strconv.Atoi(entry.Name())
		if e != nil || pid <= 0 {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		status, e := smallFile(filepath.Join(dir, "status"))
		if e != nil {
			continue
		}
		owner := -1
		for _, line := range strings.Split(string(status), "\n") {
			if strings.HasPrefix(line, "Uid:") {
				fields := strings.Fields(line)
				if len(fields) >= 3 {
					real, rerr := strconv.Atoi(fields[1])
					effective, eerr := strconv.Atoi(fields[2])
					if rerr == nil && eerr == nil && real == effective {
						owner = real
					}
				}
				break
			}
		}
		cwd, e := os.Readlink(filepath.Join(dir, "cwd"))
		if e != nil || !filepath.IsAbs(cwd) {
			continue
		}
		p := process{pid: pid, cwd: filepath.Clean(cwd), foreign: owner != uid}
		if p.foreign {
			result = append(result, p)
			continue
		}
		stat, e := smallFile(filepath.Join(dir, "stat"))
		if e != nil {
			continue
		}
		end := strings.LastIndexByte(string(stat), ')')
		if end < 0 {
			continue
		}
		fields := strings.Fields(string(stat)[end+1:])
		if len(fields) < 2 {
			continue
		}
		p.parent, e = strconv.Atoi(fields[1])
		if e != nil {
			continue
		}
		args, e := smallFile(filepath.Join(dir, "cmdline"))
		if e != nil {
			continue
		}
		argv := strings.Split(strings.TrimRight(string(args), "\x00"), "\x00")
		p.wrapper = wrapper(argv)
		p.agent = len(argv) >= 2 && filepath.Base(argv[0]) == "node" && filepath.Base(argv[1]) == "codex"
		result = append(result, p)
	}
	// /proc lists in directory order, which is arbitrary; a pid-sorted read
	// makes every downstream answer independent of that order (cli#2483 F8).
	sort.Slice(result, func(i, j int) bool { return result[i].pid < result[j].pid })
	return result, nil
}

// resolvedPath is p with symlinks evaluated when that succeeds, and p itself
// otherwise — a conservative comparison, never a broader match. Fixture
// worktrees reached through symlinked temp paths (macOS /var → /private/var)
// then compare in the same form as the resolved worktree root (cli#2483 F16).
func resolvedPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// treeState reports the wrapper→agent shape around one worktree. The answer
// is order-independent: problems are collected first, then resolved by a
// fixed precedence — foreign_uid, cwd_outside_worktree, multiple_wrappers,
// multiple_agents, no_wrapper, no_agent, ok — so coexisting problems always
// yield the same code whatever order the processes arrived in (cli#2483 F8).
// Two codes are deliberately conservative, and the command's help says so
// (cli#2483 F7): cwd_outside_worktree also covers a wrapper working strictly
// below the worktree root and a wrapper↔agent edge crossing its boundary;
// no_wrapper also covers a wrapper whose agent is not its child.
func treeState(all []process, worktree string) string {
	root, err := filepath.EvalSymlinks(worktree)
	if err != nil {
		return "cwd_outside_worktree"
	}
	byPID := map[int]process{}
	inside := map[int]bool{}
	wrappers, agents := []process{}, []process{}
	foreign, cwdAnomaly := false, false
	for _, p := range all {
		byPID[p.pid] = p
		cwd := resolvedPath(p.cwd) // once per process: EvalSymlinks stats the path
		in := within(cwd, root)
		inside[p.pid] = in
		if !in {
			continue
		}
		if p.foreign {
			foreign = true
			continue
		}
		if p.wrapper {
			if cwd != root {
				cwdAnomaly = true
			}
			wrappers = append(wrappers, p)
		}
		if p.agent {
			agents = append(agents, p)
		}
	}
	// A direct wrapper/agent edge crossing the worktree boundary is never
	// hidden by a second, well-formed child inside the worktree.
	for _, p := range all {
		if parent, ok := byPID[p.parent]; p.agent && ok && parent.wrapper && inside[p.pid] != inside[parent.pid] {
			cwdAnomaly = true
		}
	}
	switch {
	case foreign:
		return "foreign_uid"
	case cwdAnomaly:
		return "cwd_outside_worktree"
	case len(wrappers) > 1:
		return "multiple_wrappers"
	case len(agents) > 1:
		return "multiple_agents"
	case len(wrappers) == 0:
		return "no_wrapper"
	case len(agents) == 0:
		// A wrapper whose only agent child works outside the worktree is a
		// boundary crossing, not a missing agent.
		for _, p := range all {
			if p.agent && p.parent == wrappers[0].pid && !inside[p.pid] {
				return "cwd_outside_worktree"
			}
		}
		return "no_agent"
	case agents[0].parent != wrappers[0].pid:
		return "no_wrapper"
	}
	return "ok"
}
