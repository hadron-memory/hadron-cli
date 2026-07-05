package config

import (
	"os"
	"strings"
	"sync"
	"testing"
)

func TestConfigSetGetRoundTrip(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Set("app", "urn:app:acme"); err != nil {
		t.Fatal(err)
	}
	// A fresh Load sees the persisted value.
	c2, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := c2.App(); got != "urn:app:acme" {
		t.Errorf("app = %q, want urn:app:acme", got)
	}
	// The file is 0600.
	p, _ := File()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("config.toml perms = %o, want 0600", perm)
	}
	// The default server was not pinned into the file.
	if b, _ := os.ReadFile(p); strings.Contains(string(b), DefaultServer) {
		t.Errorf("write must not pin the default server:\n%s", b)
	}
}

// Concurrent Set of DIFFERENT keys must all survive — the lock + fresh reload
// prevents last-writer-wins from dropping another writer's key (#118).
func TestConfigConcurrentSetKeepsAllKeys(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if _, err := EnsureDir(); err != nil {
		t.Fatal(err)
	}
	keys := []string{"app", "memory", "server"}
	var wg sync.WaitGroup
	for _, k := range keys {
		wg.Add(1)
		go func(k string) {
			defer wg.Done()
			c, err := Load()
			if err != nil {
				return
			}
			_ = c.Set(k, "v-"+k)
		}(k)
	}
	wg.Wait()

	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		if got, _ := c.Get(k); got != "v-"+k {
			t.Errorf("key %q lost under concurrency: got %q", k, got)
		}
	}
}
