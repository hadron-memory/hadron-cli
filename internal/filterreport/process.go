package filterreport

import (
	"errors"
	"os"
	"path/filepath"
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
	return result, nil
}

func treeState(all []process, worktree string) string {
	root, err := filepath.EvalSymlinks(worktree)
	if err != nil {
		return "cwd_outside_worktree"
	}
	byPID := map[int]process{}
	wrappers, agents := []process{}, []process{}
	for _, p := range all {
		byPID[p.pid] = p
		if within(p.cwd, root) {
			if p.foreign {
				return "foreign_uid"
			}
			if p.wrapper {
				if p.cwd != root {
					return "cwd_outside_worktree"
				}
				wrappers = append(wrappers, p)
			}
			if p.agent {
				agents = append(agents, p)
			}
		}
	}
	// A direct wrapper/agent edge crossing the worktree boundary is never
	// hidden by a second, well-formed child inside the worktree.
	for _, p := range all {
		if parent, ok := byPID[p.parent]; p.agent && ok && parent.wrapper && within(p.cwd, root) != within(parent.cwd, root) {
			return "cwd_outside_worktree"
		}
	}
	if len(wrappers) > 1 {
		return "multiple_wrappers"
	}
	if len(agents) > 1 {
		return "multiple_agents"
	}
	if len(wrappers) == 0 {
		if len(agents) == 1 {
			if parent, ok := byPID[agents[0].parent]; ok && parent.wrapper {
				return "cwd_outside_worktree"
			}
		}
		return "no_wrapper"
	}
	if len(agents) == 0 {
		for _, p := range all {
			if p.parent == wrappers[0].pid && p.agent && !within(p.cwd, root) {
				return "cwd_outside_worktree"
			}
		}
		return "no_agent"
	}
	if agents[0].parent != wrappers[0].pid {
		return "no_wrapper"
	}
	return "ok"
}
