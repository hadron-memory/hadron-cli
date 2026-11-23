package hostpoller

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cli#3026 item 3: a worker that names a launcher_profile starts from it; a `launch` argv beside it
// is the removed exec path's leftover, and it used to be ignored without a word.
func loadStaleLaunchFixture(t *testing.T, workerExtra string) *Config {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "host.yaml")
	body := "app: app1\nworktrees_root: " + dir + "\nworkers:\n  iris:\n    family: claude\n" + workerExtra +
		"    launcher_profile: {family: claude, mode: long, account: acct}\n    disk_tmp: /tmp/stale-launch/iris\n" +
		"account_profiles:\n  acct: {bin: claude}\norca_launch:\n  orca_bin: /usr/bin/orca\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("a launch beside a launcher_profile must still load: %v", err)
	}
	return cfg
}

func TestLoadConfigWarnsOfALaunchArgvBesideALauncherProfile(t *testing.T) {
	cfg := loadStaleLaunchFixture(t, "    launch: [claude-worker]\n")
	if len(cfg.Warnings) != 1 {
		t.Fatalf("want exactly one warning, got %q", cfg.Warnings)
	}
	for _, want := range []string{`worker "iris"`, "launch", "launcher_profile", "remove"} {
		if !strings.Contains(cfg.Warnings[0], want) {
			t.Errorf("the warning must say %q: %s", want, cfg.Warnings[0])
		}
	}
	if clean := loadStaleLaunchFixture(t, ""); len(clean.Warnings) != 0 {
		t.Fatalf("a worker with only a launcher_profile has nothing to warn of: %q", clean.Warnings)
	}
}

// cli#3026 E3: orca is the only launch value now, so the refusals no longer call it "the default".
func TestLaunchRefusalsDoNotCallOrcaTheDefault(t *testing.T) {
	c := &Config{OrcaLaunch: OrcaLaunchConfig{OrcaBin: "/usr/bin/orca"}}
	errs := validateOrcaWorker(c, "iris", WorkerConfig{Family: "claude", Launch: []string{"x"}})
	if len(errs) != 1 || strings.Contains(errs[0], "the default") {
		t.Fatalf("the launch-without-profile refusal must not say \"the default\": %q", errs)
	}
}
