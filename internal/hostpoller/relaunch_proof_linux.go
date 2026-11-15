//go:build linux

package hostpoller

import (
	"context"
	"errors"

	"github.com/hadron-memory/hadron-cli/internal/hostrelaunch"
	"golang.org/x/sys/unix"
)

func openRelaunchJournal(path string) (hostrelaunch.Journal, uint64, func(), error) {
	j, err := hostrelaunch.OpenJournal(path, false)
	if err != nil {
		return nil, 0, nil, err
	}
	return j, j.Sweep(), func() { _ = j.Close() }, nil
}
func probeRelaunchWorktree(ctx context.Context, t hostrelaunch.Target) (hostrelaunch.Observation, error) {
	agents, err := strictRelaunchCensus(ctx, "/proc", t.Worktree, func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		var stat unix.Statfs_t
		if err := unix.Statfs("/proc", &stat); err != nil {
			return err
		}
		if stat.Type != unix.PROC_SUPER_MAGIC {
			return errors.New("proc is not the kernel proc filesystem")
		}
		return nil
	})
	if err != nil {
		return hostrelaunch.Observation{}, err
	}
	out := hostrelaunch.Observation{Proven: true, PIDs: []int{}}
	for _, a := range agents {
		out.PIDs = append(out.PIDs, a.PID)
	}
	return out, nil
}
func verifyRelaunchProcess(ctx context.Context, a Agent, _ hostrelaunch.Target, o hostrelaunch.Observation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	agent, ok := a.(*OrcaAgent)
	if !ok || agent.h == nil || agent.unclosed || len(o.PIDs) != 1 || o.PIDs[0] != agent.PID() {
		return errors.New("no unique pinned agent identity")
	}
	select {
	case <-agent.Done():
		return errors.New("agent already exited")
	default:
	}
	observed, err := (LinuxProcessControl{}).Observe(agent.PID())
	if err != nil {
		return err
	}
	defer observed.Close()
	if observed.Identity() != agent.h.Identity() {
		return errors.New("agent process identity changed")
	}
	select {
	case <-agent.Done():
		return errors.New("agent exited during verification")
	default:
	}
	return nil
}
