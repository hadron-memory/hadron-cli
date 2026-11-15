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

// unusableBinding is what a binding reader returns for a binding file that
// exists but cannot be trusted: unreadable, not JSON, or naming no worker. It
// is deliberately no worker's id, so every comparison against a recipient
// holds. Only a file that does not exist is "no binding" (""): a hold costs
// delay, typing into another worker's terminal breaches the floor.
const unusableBinding = "\x00unreadable-binding"

// readBindingIDs is the workerId a session binding file names — "" when the
// file does not exist, unusableBinding when it exists but is unusable — plus the session id the same file names ("" when
// the worker is "" or unusableBinding, or the file names no session).
func readBindingIDs(path string) (worker, session string) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", ""
		}
		return unusableBinding, ""
	}
	return parseBinding(b)
}

// parseBinding reads a binding file's worker and session ids: unusableBinding
// for anything that is not JSON naming a worker.
func parseBinding(b []byte) (worker, session string) {
	var binding struct {
		WorkerID  string `json:"workerId"`
		SessionID string `json:"sessionId"`
	}
	if json.Unmarshal(b, &binding) != nil || binding.WorkerID == "" {
		return unusableBinding, ""
	}
	return binding.WorkerID, binding.SessionID
}
