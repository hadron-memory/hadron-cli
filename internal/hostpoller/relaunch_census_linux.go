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
// authority must prove that this proc view sees the Orca host's agent namespace;
// nil never grants that authority. The adapter must also serialize all C3/C5
// launches and verify the one returned pinned process after launch.
// Raw matches are conservative: an ambiguous shim chain refuses as duplicates.
func strictRelaunchCensus(ctx context.Context, proc, root string, authority func(context.Context) error) ([]WorktreeAgent, error) {
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
			// Empty live entries cannot be assumed to be harmless kernel threads.
			// A production witness may expose only its complete managed-agent census;
			// a raw proc adapter must distinguish kernel entries before enabling C5.
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
		cwd, err := os.Readlink(filepath.Join(dir, "cwd"))
		if err != nil {
			if censusProcessGone(dir) {
				continue
			}
			return nil, fmt.Errorf("relaunch census cwd unavailable for pid %d: %w", pid, err)
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
