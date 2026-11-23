package hostpoller

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCleanupConfigOptIn2193(t *testing.T) {
	for _, tc := range []struct {
		name, extra string
		want, bad   bool
	}{
		{name: "omitted"}, {name: "explicit false", extra: "orca_launch: {orca_bin: /usr/bin/orca, cleanup_dead_tabs: false}\n"},
		{name: "explicit true", extra: "orca_launch: {orca_bin: /usr/bin/orca, cleanup_dead_tabs: true}\n", want: true},
		{name: "invalid type", extra: "orca_launch: {orca_bin: /usr/bin/orca, cleanup_dead_tabs: [true]}\n", bad: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "host.yaml")
			text := "server: http://x\napp: hrn:app:t:t\nkey_file: " + filepath.Join(dir, "k") + "\nstate_file: " + filepath.Join(dir, "s.json") + "\nworktrees_root: " + dir + "\nworkers:\n  w1:\n    family: codex\n    launch: [fake]\n" + tc.extra
			if err := os.WriteFile(path, []byte(supervisedYAML(text)), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadConfig(path)
			if tc.bad {
				if err == nil {
					t.Fatal("invalid cleanup value accepted")
				}
				return
			}
			if err != nil || cfg.OrcaLaunch.CleanupDeadTabs != tc.want {
				t.Fatalf("config=%+v err=%v want opt-in=%v", cfg, err, tc.want)
			}
		})
	}
}
