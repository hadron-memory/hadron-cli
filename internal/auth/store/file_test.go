package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func setupConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	return dir
}

func TestFileStoreRoundTrip(t *testing.T) {
	dir := setupConfigDir(t)
	st := File{}

	if _, err := st.Get("hadronmemory.com"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	if err := st.Set("hadronmemory.com", "hdr_user_abc"); err != nil {
		t.Fatal(err)
	}
	token, err := st.Get("hadronmemory.com")
	if err != nil || token != "hdr_user_abc" {
		t.Fatalf("Get() = %q, %v", token, err)
	}

	info, err := os.Stat(filepath.Join(dir, "hadron", "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("auth.json perms = %o, want 0600", perm)
	}

	if err := st.Delete("hadronmemory.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Get("hadronmemory.com"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestFileStoreMultipleHosts(t *testing.T) {
	setupConfigDir(t)
	st := File{}
	if err := st.Set("a.example.com", "token-a"); err != nil {
		t.Fatal(err)
	}
	if err := st.Set("b.example.com", "token-b"); err != nil {
		t.Fatal(err)
	}
	if token, _ := st.Get("a.example.com"); token != "token-a" {
		t.Errorf("host a token = %q", token)
	}
	if token, _ := st.Get("b.example.com"); token != "token-b" {
		t.Errorf("host b token = %q", token)
	}
}
