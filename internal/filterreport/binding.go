// Package filterreport implements the read-only, operator-run filter report.
// It has no process-control, prompt-delivery or persistence capability.
package filterreport

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Binding is the local identity used only to join a terminal to the team roster.
type Binding struct {
	WorkerID string `json:"workerId"`
	AppID    string `json:"appId"`
	Server   string `json:"server"`
}

func smallFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("unreadable_file")
	}
	f, err := openRead(path)
	if err != nil {
		return nil, errors.New("unreadable_file")
	}
	defer func() { _ = f.Close() }()
	if info, e := f.Stat(); e != nil || !info.Mode().IsRegular() {
		return nil, errors.New("unreadable_file")
	}
	b, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil || len(b) > 65536 {
		return nil, errors.New("unreadable_file")
	}
	return b, nil
}

// ReadBinding reads the git pointer itself; it never invokes git or creates a cache.
func ReadBinding(worktree string) (Binding, error) {
	var b Binding
	home := filepath.Join(worktree, ".git")
	info, err := os.Lstat(home)
	switch {
	case os.IsNotExist(err):
		home = filepath.Join(worktree, ".hadron-team")
	case err != nil:
		return b, errors.New("binding_unreadable")
	case info.IsDir():
	case info.Mode().IsRegular():
		data, e := smallFile(home)
		if e != nil || !strings.HasPrefix(string(data), "gitdir: ") {
			return b, errors.New("binding_unreadable")
		}
		home = strings.TrimSpace(strings.TrimPrefix(string(data), "gitdir: "))
		if !filepath.IsAbs(home) {
			home = filepath.Join(worktree, home)
		}
	default:
		return b, errors.New("binding_unreadable")
	}
	data, err := smallFile(filepath.Join(home, "hadron-team-session.json"))
	if err != nil || json.Unmarshal(data, &b) != nil || b.WorkerID == "" {
		return Binding{}, errors.New("binding_unreadable")
	}
	return b, nil
}
