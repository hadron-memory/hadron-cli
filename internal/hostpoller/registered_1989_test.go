package hostpoller

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Config.Registered feeds the deliverer's claim checks (cli#1989): every
// worker, as ABSOLUTE resolved paths, configured and poller-created alike.
func TestConfigRegisteredIsEveryWorkersAbsolutePaths(t *testing.T) {
	root := t.TempDir()
	body := "app: app1\nworktrees_root: " + root + "\nkey_file: key\nworkers:\n" +
		"  iris:\n    family: claude\n    launch: [claude]\n    worktrees: [plain/iris]\n" +
		"  uma:\n    family: claude\n    launch: [claude]\n    worktrees: [hadron-cli/uma-1]\n" +
		"  empty:\n    family: claude\n    launch: [claude]\n"
	path := filepath.Join(root, "host.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	st, err := LoadState(filepath.Join(root, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.addCreated("uma", "hadron-cli/uma-2"); err != nil {
		t.Fatal(err)
	}
	got := cfg.Registered(st)
	want := map[string][]string{
		"iris":  {filepath.Join(root, "plain", "iris")},
		"uma":   {filepath.Join(root, "hadron-cli", "uma-1"), filepath.Join(root, "hadron-cli", "uma-2")},
		"empty": {},
	}
	if len(got) != len(want) {
		t.Fatalf("workers = %v, want %v", got, want)
	}
	for ref, dirs := range want {
		if !reflect.DeepEqual(got[ref], dirs) && (len(dirs) != 0 || len(got[ref]) != 0) {
			t.Fatalf("%s = %v, want %v", ref, got[ref], dirs)
		}
		for _, d := range got[ref] {
			if !filepath.IsAbs(d) {
				t.Fatalf("%s: %q is not absolute", ref, d)
			}
		}
	}
	// And it agrees with what Recipient hands the deliverer for the same worker.
	if r := cfg.Recipient(st, "iris", "w-iris"); !reflect.DeepEqual(r.Worktrees, got["iris"]) {
		t.Fatalf("Registered %v disagrees with Recipient %v", got["iris"], r.Worktrees)
	}
}
