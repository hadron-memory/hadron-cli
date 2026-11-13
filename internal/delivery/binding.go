package delivery

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"strings"
)

// trimGitdir reads a linked worktree's `.git` file: "gitdir: <path>".
func trimGitdir(s string) string {
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "gitdir:"))
}

// unreadableBinding is what a binding reader returns for a binding file that
// exists but cannot be trusted: unreadable, not JSON, or naming no worker. It
// is deliberately no worker's id, so every comparison against a recipient
// holds. Only a file that does not exist is "no binding" (""): a hold costs
// delay, typing into another worker's terminal breaches the floor.
const unreadableBinding = "\x00unreadable-binding"

// readBinding is the workerId a session binding file names: "" when the file
// does not exist, unreadableBinding when it exists but is unusable.
func readBinding(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return ""
		}
		return unreadableBinding
	}
	var binding struct {
		WorkerID string `json:"workerId"`
	}
	if json.Unmarshal(b, &binding) != nil || binding.WorkerID == "" {
		return unreadableBinding
	}
	return binding.WorkerID
}
