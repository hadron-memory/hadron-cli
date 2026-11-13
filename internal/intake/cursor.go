package intake

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/hadron-memory/hadron-cli/internal/config"
)

// CursorStore keeps, per server, App and adapter, the server's OPAQUE cursor
// for the reconcile pass (cli#1962): where the CLI last read the App's
// "tasks with an external id changed" feed. It is the server's value, never a
// client clock (cor:agt:120, minimal write-back). A file beside the CLI's
// config, written atomically under the config lock so parallel runs do not
// lose each other's updates.
type CursorStore struct {
	// Path is the JSON file; its directory must exist or be creatable.
	Path string
}

// DefaultCursorStore is the store beside the CLI's config.
func DefaultCursorStore() (*CursorStore, error) {
	dir, err := config.Dir()
	if err != nil {
		return nil, err
	}
	return &CursorStore{Path: filepath.Join(dir, "intake-cursors.json")}, nil
}

// CursorKey names one feed: the server, the App and the adapter. The adapter
// is part of the key so two adapters in one App each see every change.
func CursorKey(server, appRef, adapter string) string {
	return server + "\x00" + appRef + "\x00" + adapter
}

func (s *CursorStore) read() (map[string]string, error) {
	b, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// Get returns the stored cursor, or "" when the feed was never read.
func (s *CursorStore) Get(key string) (string, error) {
	m, err := s.read()
	if err != nil {
		return "", err
	}
	return m[key], nil
}

// Set stores cursor for key.
func (s *CursorStore) Set(key, cursor string) error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	return config.WithLock(func() error {
		m, err := s.read()
		if err != nil {
			return err
		}
		m[key] = cursor
		b, err := json.MarshalIndent(m, "", "  ")
		if err != nil {
			return err
		}
		return config.WriteFileAtomic(s.Path, b, 0o600)
	})
}
