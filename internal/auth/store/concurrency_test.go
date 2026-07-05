package store

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// Set through a pre-existing world-readable auth.json must tighten it to 0600
// (the atomic replace installs a fresh 0600 file — #117).
func TestFileStoreTightensLoosePerms(t *testing.T) {
	dir := setupConfigDir(t)
	cfgDir := filepath.Join(dir, "hadron")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	authPath := filepath.Join(cfgDir, "auth.json")
	if err := os.WriteFile(authPath, []byte(`{"hosts":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := (File{}).Set("hadronmemory.com", "hdr_user_x"); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(authPath)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("auth.json perms = %o, want 0600 after Set", perm)
	}
}

// Concurrent Set for DIFFERENT hosts must all survive — without the lock, each
// call reads the map before the others write and last-writer-wins drops tokens
// (#118). This is the regression guard for the flock critical section.
func TestFileStoreConcurrentSetKeepsAllHosts(t *testing.T) {
	setupConfigDir(t)
	hosts := []string{"a.example.com", "b.example.com", "c.example.com", "d.example.com", "e.example.com"}
	var wg sync.WaitGroup
	for _, h := range hosts {
		wg.Add(1)
		go func(h string) {
			defer wg.Done()
			_ = (File{}).Set(h, "tok-"+h)
		}(h)
	}
	wg.Wait()

	for _, h := range hosts {
		if tok, err := (File{}).Get(h); err != nil || tok != "tok-"+h {
			t.Errorf("host %s lost its token under concurrency: got %q, err %v", h, tok, err)
		}
	}
}
