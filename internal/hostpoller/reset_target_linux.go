//go:build linux

package hostpoller

import (
	"errors"
	"strconv"
)

// ValidateResetTarget reuses C2a's process census/stat reader. The one logical
// agent in the target worktree must belong to the launch's own process group.
// A foreground wrapper is allowed; a detached or ambiguous agent is not.
// This is process validation only, never a terminal association or idle proof.
func ValidateResetTarget(t ResetIdleTarget) error {
	root, err := readStatErr(t.Process.PID)
	if err != nil || root.start != t.Process.StartTime || root.state == "Z" || root.pgrp != t.Process.PID {
		return errors.New("PROCESS_CHANGED")
	}
	agents := ScanWorktreeAgents(t.Worktree)
	if len(agents) != 1 {
		return errors.New("PROCESS_AMBIGUOUS")
	}
	a := agents[0]
	st, err := readStatErr(a.PID)
	if err != nil || st.state == "Z" || st.start != strconv.FormatInt(a.StartTicks, 10) || st.pgrp != t.Process.PID || a.Base != t.Family {
		return errors.New("PROCESS_CHANGED")
	}
	return nil
}
