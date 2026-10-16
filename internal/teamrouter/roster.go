package teamrouter

// Onboarding changes only config and a reload receipt. It never opens the
// attention token, ledger or liveness files.
import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// CheckRosterEntry performs the same identity checks used under the writer lock.
func CheckRosterEntry(c *Config, entry RosterEntry) error {
	for _, row := range c.Roster {
		if row.Worker == entry.Worker && realPath(row.Worktree) != realPath(entry.Worktree) {
			return fmt.Errorf("worker already has a different roster worktree")
		}
		if row.Worker != entry.Worker && realPath(row.Worktree) == realPath(entry.Worktree) {
			return fmt.Errorf("worktree belongs to another roster worker")
		}
	}
	return nil
}

// PutRosterEntry preserves unknown config fields and refuses a reassignment.
// The caller holds its onboarding lock for the complete operation sequence;
// the router writer lock additionally excludes an in-flight delivery here.
func PutRosterEntry(path string, entry RosterEntry) (string, error) {
	c, err := LoadConfig(path)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(c.StateDir, 0o700); err != nil {
		return "", err
	}
	return withLock(c.StateDir, func(owned func() bool) (string, error) {
		raw, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		current, err := parseConfig(raw, path)
		if err != nil {
			return "", err
		}
		if current.Fingerprint != c.Fingerprint {
			return "", fmt.Errorf("router config changed before the roster lock; retry")
		}
		if err := CheckRosterEntry(current, entry); err != nil {
			return "", err
		}
		var doc map[string]json.RawMessage
		if err := json.Unmarshal(raw, &doc); err != nil {
			return "", err
		}
		rows := current.Roster
		var rawRows []map[string]json.RawMessage
		if err := json.Unmarshal(doc["roster"], &rawRows); err != nil && len(rows) > 0 {
			return "", err
		}
		found := false
		for i, row := range rows {
			if row.Worker == entry.Worker {
				if realPath(row.Worktree) != realPath(entry.Worktree) {
					return "", fmt.Errorf("worker already has a different roster worktree")
				}
				// Keep a pinned terminal and unrelated watch flags.
				rawRows[i]["name"], err = json.Marshal(entry.Name)
				if err != nil {
					return "", err
				}
				rawRows[i]["coordinator"], err = json.Marshal(entry.Coordinator)
				if err != nil {
					return "", err
				}
				found = true
			} else if realPath(row.Worktree) == realPath(entry.Worktree) {
				return "", fmt.Errorf("worktree belongs to another roster worker")
			}
		}
		if !found {
			b, err := json.Marshal(entry)
			if err != nil {
				return "", err
			}
			var row map[string]json.RawMessage
			if err := json.Unmarshal(b, &row); err != nil {
				return "", err
			}
			rawRows = append(rawRows, row)
		}
		doc["roster"], err = json.Marshal(rawRows)
		if err != nil {
			return "", err
		}
		out, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return "", err
		}
		out = append(out, '\n')
		if _, err := parseConfig(out, path); err != nil {
			return "", err
		}
		if !owned() {
			return "", fmt.Errorf("router lock lost")
		}
		if err := atomicRosterFile(path, out); err != nil {
			return "", err
		}
		loaded, err := LoadConfig(path)
		if err != nil {
			return "", err
		}
		return loaded.Fingerprint, nil
	})
}

func atomicRosterFile(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".onboard-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err = f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// ReloadReceipt confirms a loop loaded these exact config bytes between polls.
type ReloadReceipt struct {
	Fingerprint string    `json:"fingerprint"`
	At          time.Time `json:"at"`
}

func RecordReload(c *Config) error {
	if err := os.MkdirAll(c.StateDir, 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(ReloadReceipt{Fingerprint: c.Fingerprint, At: time.Now().UTC()})
	if err != nil {
		return err
	}
	return atomicRosterFile(filepath.Join(c.StateDir, "config-loaded.json"), b)
}

func ReadReload(c *Config) (ReloadReceipt, error) {
	var receipt ReloadReceipt
	b, err := os.ReadFile(filepath.Join(c.StateDir, "config-loaded.json"))
	if err != nil {
		return receipt, err
	}
	err = json.Unmarshal(b, &receipt)
	return receipt, err
}
