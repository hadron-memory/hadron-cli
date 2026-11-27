package hostfacts

import (
	"path/filepath"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/config"
)

// TestStateDirIsTheCredentialStoreRule pins that team host doctor reports the
// state directory the host credential store writes to (hadron-cli#3239 and
// #3236 share one rule, config.StateDirFrom), with and without XDG_STATE_HOME.
func TestStateDirIsTheCredentialStoreRule(t *testing.T) {
	home := t.TempDir()
	for _, xdg := range []string{"", filepath.Join(home, "xdg-state")} {
		t.Setenv("XDG_STATE_HOME", xdg)
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		want, err := config.StateDir()
		if err != nil {
			t.Fatal(err)
		}
		f := New(nil)
		if got := f.StateDir(); got != want {
			t.Fatalf("XDG_STATE_HOME=%q: hostfacts StateDir = %q, config.StateDir = %q", xdg, got, want)
		}
	}
}
