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

// A nested relative state_file behind a symlinked ".." must save: the state
// directory is created where the kernel will write, not where filepath.Clean
// would put it (cli#2674, review of a201e68a).
func TestNestedRelativeStateSaves2674(t *testing.T) {
	root, path := linkedConfigSite(t)
	real := root + "/real/host.yaml"
	b, err := os.ReadFile(real)
	if err != nil {
		t.Fatal(err)
	}
	b = []byte(strings.Replace(string(b), "state_file: "+root+"/state.json", "state_file: nested/state.json", 1))
	if err := os.WriteFile(real, b, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	st, err := LoadState(cfg.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.addCreated("iris", "wt"); err != nil {
		t.Fatalf("saving %q: %v", cfg.StateFile, err)
	}
	// It landed where the kernel resolves the written path, and reloads from
	// the direct spelling with the record.
	again, err := LoadState(root + "/real/nested/state.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(again.worktreesFor("iris", nil)) != 1 {
		t.Fatalf("the saved record must be in root/real/nested/state.json: %+v", again)
	}
}

// A root-level state file keeps "/" as its parent, a drive root keeps its
// separator, and a root dir is not given a doubled separator.
func TestWrittenDirRootsAndSlashEntries2674(t *testing.T) {
	for in, want := range map[string]string{"/state.json": "/", "a:/x": "a:", "/a/b": "/a", "x": "."} {
		if got := writtenDir(in); got != want {
			t.Fatalf("writtenDir(%q) = %q, want %q", in, got, want)
		}
	}
	if got := joinWritten("/", "state.json", '/'); got != "/state.json" {
		t.Fatalf("root join = %q", got)
	}
	if got, want := joinWritten(`\\?\C:\cfg`, "keys/host.key", '\\'), `\\?\C:\cfg\keys\host.key`; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// Two spellings of one config select one delivery directory, so their locks
// agree: the default state path through "link/.." and the direct one.
func TestDeliveryStateDirAgreesAcrossSpellings2674(t *testing.T) {
	root, path := linkedConfigSite(t)
	via, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	direct, err := LoadConfig(root + "/real/host.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if a, b := via.DeliveryStateDir(), direct.DeliveryStateDir(); a != b {
		t.Fatalf("delivery dirs differ across spellings: %q vs %q", a, b)
	}
}

// A state directory that does not exist yet is resolved at its deepest
// existing ancestor (the symlink followed), the missing tail kept as written;
// a ".." after a missing name cannot be resolved by the kernel either, so it
// falls back to the lexical parent instead of guessing (cli#2674, blind round 3).
func TestResolvedStateDirMissingTail2674(t *testing.T) {
	root, _ := linkedConfigSite(t)
	real, err := filepath.EvalSymlinks(root + "/real")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := resolvedStateDir(root+"/link/../nested/deep/state.json"), real+"/nested/deep"; got != want {
		t.Fatalf("missing tail under a symlinked ..: got %q want %q", got, want)
	}
	if got, want := resolvedStateDir(root+"/link/../state.json"), real; got != want {
		t.Fatalf("existing parent: got %q want %q", got, want)
	}
	got := resolvedStateDir(root + "/gone/../x/state.json")
	if want := filepath.Dir(root + "/gone/../x/state.json"); got != want {
		t.Fatalf("a '..' after a missing name must fall back lexically: got %q want %q", got, want)
	}
}
