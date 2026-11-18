//go:build linux

package hostpoller

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// strictRelaunchCensus is deliberately separate from observational duplicate
// cleanup: unknown does not mean zero, and the caller/ancestors are included.
// authority must prove the proc view is available for the host scan;
// nil never grants that authority. The adapter must also serialize all C3/C5
// launches and verify the one returned pinned process after launch.
// Raw matches are conservative: an ambiguous shim chain refuses as duplicates.
func strictRelaunchCensus(ctx context.Context, proc, root string, authority func(context.Context) error) ([]WorktreeAgent, error) {
	return strictRelaunchCensusCounted(ctx, proc, root, authority, nil)
}

// skipped counts only exclusions from a complete, identity-checked census.
func strictRelaunchCensusCounted(ctx context.Context, proc, root string, authority func(context.Context) error, skipped *int) ([]WorktreeAgent, error) {
	foreign := 0
	if authority == nil {
		return nil, errors.New("relaunch census observer authority is unproven")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := authority(ctx); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(root) {
		return nil, errors.New("relaunch census needs an absolute worktree")
	}
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(real)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("relaunch worktree is not a directory")
	}
	entries, err := os.ReadDir(proc)
	if err != nil {
		return nil, err
	}
	found := []WorktreeAgent{}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		dir := filepath.Join(proc, entry.Name())
		raw, err := readCensusFile(filepath.Join(dir, "cmdline"))
		if err != nil {
			if censusProcessGone(dir) {
				continue
			}
			return nil, fmt.Errorf("relaunch census cmdline unavailable for pid %d: %w", pid, err)
		}
		argv, ok := argvFromCmdline(raw)
		if !ok || len(argv) == 0 || argv[0] == "" {
			// Empty cmdline alone is not absence. Only kernel PF_KTHREAD or
			// a terminal zombie/dead state proves it cannot be a live agent.
			if harmlessEmptyProcess(dir) {
				continue
			}

			if censusProcessGone(dir) {
				continue
			}
			return nil, fmt.Errorf("relaunch census identity unavailable for pid %d", pid)
		}
		base := agentBase(argv)
		if base == "" {
			continue
		}
		before, err := readCensusFile(filepath.Join(dir, "stat"))
		if err != nil {
			if censusProcessGone(dir) {
				continue
			}
			return nil, err
		}
		parent, start, ok := parseStatLine(string(before))
		if !ok || start <= 0 {
			return nil, fmt.Errorf("relaunch census stat invalid for pid %d", pid)
		}
		excluded := false
		cwd, err := os.Readlink(filepath.Join(dir, "cwd"))
		if err != nil {
			if censusProcessGone(dir) {
				continue
			}
			status, statusErr := readCensusFile(filepath.Join(dir, "status"))
			if statusErr != nil {
				if censusProcessGone(dir) {
					continue
				}
				return nil, fmt.Errorf("relaunch census status unavailable for pid %d: %w", pid, statusErr)
			}
			if !censusForeignUID(status, os.Geteuid()) {
				return nil, fmt.Errorf("relaunch census cwd unavailable for pid %d: %w", pid, err)
			}
			excluded = true
		}
		after, err := readCensusFile(filepath.Join(dir, "stat"))
		if err != nil {
			if censusProcessGone(dir) {
				continue
			}
			return nil, err
		}
		afterParent, afterStart, ok := parseStatLine(string(after))
		if !ok || parent != afterParent || start != afterStart {
			return nil, fmt.Errorf("relaunch census pid %d changed during observation", pid)
		}
		if excluded {
			foreign++
			continue
		}
		if cwd != real && !strings.HasPrefix(cwd, real+string(os.PathSeparator)) {
			continue
		}
		// Do not retain argv or environment in a launch receipt.
		found = append(found, WorktreeAgent{PID: pid, Base: base, StartTicks: start})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := authority(ctx); err != nil {
		return nil, err
	}
	if skipped != nil {
		*skipped = foreign
	}
	sort.Slice(found, func(i, j int) bool { return found[i].PID < found[j].PID })
	return found, nil
}
func censusProcessGone(dir string) bool {
	_, err := os.Stat(dir)
	return errors.Is(err, os.ErrNotExist)
}
func readCensusFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	const limit = 4 << 20
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if len(b) > limit {
		return nil, errors.New("relaunch census entry exceeds limit")
	}
	return b, nil
}

// harmlessEmptyProcess reads the kernel-only flag/state twice around an
// identity check. An unknown empty userspace process remains unproven.
func harmlessEmptyProcess(dir string) bool {
	read := func() (uint64, bool) {
		raw, err := readCensusFile(filepath.Join(dir, "stat"))
		if err != nil {
			return 0, false
		}
		text := string(raw)
		end := strings.LastIndexByte(text, ')')
		if end < 0 || !strings.HasPrefix(text, filepath.Base(dir)+" (") {
			return 0, false
		}
		fields := strings.Fields(text[end+1:])
		if len(fields) < 20 {
			return 0, false
		}
		start, err := strconv.ParseUint(fields[19], 10, 64)
		if err != nil || start == 0 {
			return 0, false
		}
		flags, err := strconv.ParseUint(fields[6], 10, 64)
		if err != nil {
			return 0, false
		}
		const pfKthread = 0x00200000
		return start, flags&pfKthread != 0 || fields[0] == "Z" || fields[0] == "X"
	}
	first, ok := read()
	if !ok {
		return false
	}
	second, ok := read()
	return ok && first == second
}

// All four credentials must be known and foreign. A missing, duplicated or
// malformed Uid line cannot exclude a process; neither can any root credential.
func censusForeignUID(status []byte, euid int) bool {
	seen := false
	foreign := true
	for _, line := range strings.Split(string(status), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "Uid:" {
			continue
		}
		if seen || len(fields) != 5 {
			return false
		}
		seen = true
		for _, field := range fields[1:] {
			uid, err := strconv.ParseUint(field, 10, 32)
			if err != nil {
				return false
			}
			if uid == 0 || uid == uint64(euid) {
				foreign = false
			}
		}
	}
	return seen && foreign
}
