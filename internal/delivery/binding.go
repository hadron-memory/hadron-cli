package delivery

import (
	"encoding/json"
	"os"
	"strings"
)

// trimGitdir reads a linked worktree's `.git` file: "gitdir: <path>".
func trimGitdir(s string) string {
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "gitdir:"))
}

// readBinding is the workerId a session binding file names, or "".
func readBinding(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var binding struct {
		WorkerID string `json:"workerId"`
	}
	if json.Unmarshal(b, &binding) != nil {
		return ""
	}
	return binding.WorkerID
}
