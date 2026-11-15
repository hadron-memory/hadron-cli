package delivery

import (
	"encoding/json"
	"path/filepath"
)

// LaunchBindingIdentity is a complete local binding, required for relaunch.
// Older incomplete bindings remain usable by legacy readers but prove nothing
// for a new process launch. Reading it never refreshes or rewrites a session.
type LaunchBindingIdentity struct {
	WorkerID  string `json:"workerId"`
	SessionID string `json:"sessionId"`
	AppID     string `json:"appId"`
	Server    string `json:"server"`
	Usable    bool   `json:"-"`
}

func WorktreeBindingIdentity(worktree string) LaunchBindingIdentity {
	dir, state := bindingDir(worktree)
	switch state {
	case bindingDirPlain:
		dir = filepath.Join(worktree, plainCacheName)
	case bindingDirGit:
	default:
		return LaunchBindingIdentity{}
	}
	raw, ok := readSmallRegular(filepath.Join(dir, bindingFileName))
	if !ok {
		return LaunchBindingIdentity{}
	}
	var b LaunchBindingIdentity
	if json.Unmarshal(raw, &b) != nil {
		return LaunchBindingIdentity{}
	}
	b.Usable = b.WorkerID != "" && b.SessionID != "" && b.AppID != "" && b.Server != ""
	return b
}
