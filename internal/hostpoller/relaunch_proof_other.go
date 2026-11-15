//go:build !linux

package hostpoller

import (
	"context"
	"errors"
	"github.com/hadron-memory/hadron-cli/internal/hostrelaunch"
)

func openRelaunchJournal(string) (hostrelaunch.Journal, uint64, func(), error) {
	return nil, 0, nil, errors.New("relaunch requires Linux")
}
func probeRelaunchWorktree(context.Context, hostrelaunch.Target) (hostrelaunch.Observation, error) {
	return hostrelaunch.Observation{}, errors.New("relaunch requires Linux")
}
func verifyRelaunchProcess(context.Context, Agent, hostrelaunch.Target, hostrelaunch.Observation) error {
	return errors.New("relaunch requires Linux")
}
