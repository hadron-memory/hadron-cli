package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// C5's profile input is opt-in. A dry run of a paused worker must accept it
// without preparing launch directories, creating state or mutating the server.
func TestHostUpRelaunchConfigDryRunWritesNothing2060(t *testing.T) {
	fx := newHostUpFixture1389(t)
	fx.present("Iris", 0)
	fx.writeDesired("APPLY", hostUpWorker1389{ID: "wkr-iris", Name: "Iris", Desired: "RUNNING", Reason: "PAUSED", Session: "s-iris"})
	dir := t.TempDir()
	path := filepath.Join(dir, "host.yaml")
	state := filepath.Join(dir, "host-state.json")
	disk := filepath.Join(dir, "disk-tmp")
	text := fmt.Sprintf("app: capp100000000000000000000\nserver: %s\nworktrees_root: %s\nstate_file: %s\nkey_file: private-host-key\naccount_profiles:\n  main: {bin: claude}\norca_launch:\n  orca_bin: %s\nworkers:\n  Iris:\n    family: claude\n    launch_via: orca\n    disk_tmp: %s\n    worktrees: [hadron-cli/iris]\n    launcher_profile: {family: claude, mode: long, account: main}\n", fx.srv.URL, fx.root, state, fx.orcaBin, disk)
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	dto, raw, err := fx.run("--config", path, "--dry-run")
	if err != nil {
		t.Fatalf("C5 profile configuration refused: %v\n%s", err, raw)
	}
	wantItems1389(t, "skipped", dto.Skipped, "Iris - PAUSED")
	for _, p := range []string{state, disk} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("dry run prepared %s: %v", p, err)
		}
	}
	if calls := fx.mutations(); len(calls) != 0 {
		t.Fatalf("dry run mutated server: %v", calls)
	}
}
