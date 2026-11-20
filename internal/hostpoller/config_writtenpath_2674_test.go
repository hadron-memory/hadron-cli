//go:build !windows

package hostpoller

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cli#2674 item 1: a relative state_file or key_file resolves against the
// directory the kernel reached for the written config path, not the lexical
// parent. The expected paths are built by concat, never filepath.Join: Join
// cleans the ".." and would pass lexically for the wrong reason.
func TestRelativeStateAndKeyFollowTheKernelDirectoryOfALinkedConfig2674(t *testing.T) {
	root, startPath := linkedConfigSite(t)
	real := filepath.Join(root, "real", "host.yaml")
	text, err := os.ReadFile(real)
	if err != nil {
		t.Fatal(err)
	}
	text = []byte(strings.NewReplacer(
		"key_file: "+filepath.Join(root, "host.key"), "key_file: host.key",
		"state_file: "+root+"/state.json", "state_file: state.json",
	).Replace(string(text)))
	if err := os.WriteFile(real, text, 0o600); err != nil {
		t.Fatal(err)
	}
	// The key the kernel will find next to root/real/host.yaml.
	if err := os.WriteFile(filepath.Join(root, "real", "host.key"), []byte(hostKey), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(startPath)
	if err != nil {
		t.Fatal(err)
	}
	dir := root + "/link/.."
	if want := dir + "/state.json"; cfg.StateFile != want {
		t.Errorf("StateFile = %q, want %q", cfg.StateFile, want)
	}
	if want := dir + "/host.key"; cfg.KeyFile != want {
		t.Errorf("KeyFile = %q, want %q", cfg.KeyFile, want)
	}
	// And the kernel resolves them next to the file that was loaded.
	if _, err := os.Stat(cfg.KeyFile); err != nil {
		t.Errorf("the key is not where the loaded file's directory says: %v", err)
	}
}

// The default state file (no state_file entry) sits beside the config the
// kernel loaded, by the same rule.
func TestDefaultStateFileFollowsTheKernelDirectory2674(t *testing.T) {
	root, startPath := linkedConfigSite(t)
	real := filepath.Join(root, "real", "host.yaml")
	text, err := os.ReadFile(real)
	if err != nil {
		t.Fatal(err)
	}
	text = []byte(strings.Replace(string(text), "state_file: "+root+"/state.json\n", "", 1))
	if err := os.WriteFile(real, text, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(startPath)
	if err != nil {
		t.Fatal(err)
	}
	if want := root + "/link/../hadron-host-state.json"; cfg.StateFile != want {
		t.Errorf("StateFile = %q, want %q", cfg.StateFile, want)
	}
}

// A plain config path keeps its old result: state beside the config, and a
// relative config path is made absolute (it is the working directory plus the
// path as written).
func TestPlainConfigPathStateFileUnchanged2674(t *testing.T) {
	root, _ := linkedConfigSite(t)
	t.Chdir(root)
	cfg, err := LoadConfig("host.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if want := root + "/state.json"; cfg.StateFile != want {
		t.Errorf("StateFile = %q, want %q", cfg.StateFile, want)
	}
}

// The join uses the separator it is given, so an extended-length Windows path
// is not left with a slash the OS will not convert (cli#2674).
func TestJoinWrittenUsesTheNativeSeparator2674(t *testing.T) {
	if got, want := joinWritten(`\\?\C:\cfg`, "host.key", '\\'), `\\?\C:\cfg\host.key`; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if got, want := joinWritten("/r/link/..", "x", '/'), "/r/link/../x"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
