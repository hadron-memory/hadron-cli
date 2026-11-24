package hostpoller

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cli#2914 (RULED by Holger via Vera, decisions 2026-11-22 item 1): the transitional exec launch path is
// removed. The default is the gate's call: an EMPTY launch_via means the supervised (orca) launch, and an
// explicit launch_via: exec is refused at config load with an error that names the replacement, so a stale
// config fails fast instead of silently changing launch shape. cor:agt:070:08 stays as it is provided the
// supervised path still starts the worker in its EXISTING worktree: pinned below.

// An empty launch_via launches through the supervised path: one orca launch, no exec launch, in the
// worker's existing worktree (Dir is the registered worktree's path).
func TestEmptyLaunchViaLaunchesThroughTheSupervisedPath2914(t *testing.T) {
	p, srv, host := testPoller(t)
	disk := filepath.Join(t.TempDir(), "disk")
	prof := LauncherProfile{Family: "claude", Mode: ModeLong, Account: "a"}
	w := p.startCfg.Workers["iris"]
	w.LaunchVia, w.Profile, w.DiskTmp = "", &prof, disk // launch_via LEFT EMPTY
	p.startCfg.Workers["iris"] = w
	p.startCfg.Accounts = map[string]AccountProfile{"a": {Bin: "claude-holger"}}
	p.startCfg.OrcaLaunch = OrcaLaunchConfig{OrcaBin: "/usr/bin/orca"}
	rec := &recordedOrca{}
	p.EnableOrcaLaunch(rec.launch, ConfigProfiles{Cfg: p.startCfg})
	srv.notDone["s1"] = 1
	srv.batches = [][]Operation{{startOp()}}
	step(t, p)
	if len(host.launches) != 0 || len(rec.specs) != 1 {
		t.Fatalf("an empty launch_via launches through the supervised path: exec launches %d, orca launches %d", len(host.launches), len(rec.specs))
	}
	if want := p.startCfg.WorktreePath("repo/iris"); rec.specs[0].Dir != want {
		t.Fatalf("the worker starts in its EXISTING worktree (cor:agt:070:08): dir %q, want %q", rec.specs[0].Dir, want)
	}
}

func orcaPrereqConfig2914(launchVia string) *Config {
	return &Config{App: "hrn:app:acme.com:team", Label: "box1", PollIntervalSeconds: 30, WorktreesRoot: "/w", StateFile: "/w/s.json",
		Workers: map[string]WorkerConfig{"iris": {Family: "claude", LaunchVia: launchVia, DiskTmp: "/disk/iris", Worktrees: []string{"r/iris"},
			Profile: &LauncherProfile{Family: "claude", Mode: ModeLong, Account: "a"}}},
		Accounts:   map[string]AccountProfile{"a": {Bin: "claude-holger"}},
		OrcaLaunch: OrcaLaunchConfig{OrcaBin: "/usr/bin/orca"},
	}
}

// An explicit launch_via: exec is refused at load, naming the replacement; nothing is started.
func TestExplicitExecLaunchViaIsRefusedNamingTheReplacement2914(t *testing.T) {
	c := orcaPrereqConfig2914("exec")
	w := c.Workers["iris"]
	w.Launch = []string{"claude-worker", "--model", "opus"} // a complete exec-style worker, so only launch_via is at issue
	c.Workers["iris"] = w
	err := c.validate()
	if err == nil {
		t.Fatal("launch_via: exec must be refused at config load")
	}
	for _, want := range []string{`worker "iris"`, "launch_via: exec", "removed", "launch_via: orca", "launcher_profile"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must say %q: %v", want, err)
		}
	}
}

// An empty launch_via has the orca path's requirements, with the existing messages: a launcher_profile
// and an absolute disk_tmp.
func TestEmptyLaunchViaNeedsTheSupervisedPrerequisites2914(t *testing.T) {
	if err := orcaPrereqConfig2914("").validate(); err != nil {
		t.Fatalf("an empty launch_via with a profile and a disk_tmp is valid: %v", err)
	}
	for name, tc := range map[string]struct {
		mutate func(c *Config)
		want   string
	}{
		"no launcher_profile": {func(c *Config) { w := c.Workers["iris"]; w.Profile = nil; c.Workers["iris"] = w }, "needs a launcher_profile"},
		"no disk_tmp":         {func(c *Config) { w := c.Workers["iris"]; w.DiskTmp = ""; c.Workers["iris"] = w }, "needs an absolute disk_tmp"},
	} {
		t.Run(name, func(t *testing.T) {
			c := orcaPrereqConfig2914("")
			tc.mutate(c)
			err := c.validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
}

// What an operator with an old exec-style config (a launch argv, no launch_via) now sees: the old key is
// named as belonging to the removed path, with what to set instead.
func TestAnOldExecStyleConfigSaysWhatToSetInstead2914(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "host.key")
	if err := os.WriteFile(key, []byte("hk_key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "host.yaml")
	cfg := "app: hrn:app:acme.com:eng-team\nlabel: box1\npoll_interval_seconds: 30\nkey_file: " + key + "\nworktrees_root: " + filepath.Join(dir, "trees") +
		"\nworkers:\n  iris:\n    family: claude\n    launch: [claude-worker]\n"
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("an exec-style config (launch: argv, no launcher_profile) must no longer load")
	}
	for _, want := range []string{`worker "iris"`, "launch", "exec launch path, which was removed", "launcher_profile", "disk_tmp"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must say %q: %v", want, err)
		}
	}
}

// `host import` reads a LEGACY exec-style config to migrate it: it validates everything except the
// per-worker launch-path rules, which such a config cannot satisfy, while host run's loader refuses it.
func TestImportLoaderStillReadsALegacyExecConfig2914(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "host.key")
	if err := os.WriteFile(key, []byte("hk_key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "host.yaml")
	legacy := "app: hrn:app:acme.com:eng-team\nlabel: box1\npoll_interval_seconds: 30\nkey_file: " + key + "\nworktrees_root: " + filepath.Join(dir, "trees") +
		"\nworkers:\n  iris:\n    family: claude\n    launch_via: exec\n    launch: [claude-worker, --model, opus]\n    worktrees: [r/iris]\n"
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("host run's loader must refuse a legacy exec config")
	}
	cfg, err := LoadConfigForImport(path)
	if err != nil {
		t.Fatalf("host import must still read the legacy config it migrates: %v", err)
	}
	if w := cfg.Workers["iris"]; strings.Join(w.Launch, " ") != "claude-worker --model opus" {
		t.Fatalf("import reads the legacy launch argv: %v", w.Launch)
	}
	// Everything else is still validated: a bad worktree is refused here too.
	bad := strings.Replace(legacy, "worktrees: [r/iris]", "worktrees: [../up]", 1)
	if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfigForImport(path); err == nil {
		t.Fatal("the import loader skips only the launch-path rules, not the rest")
	}
}

// A family with no launch template (anything but claude, glm, kimi and codex) is refused AT LOAD, not at
// every worker.start: validation builds the profile's launch argv.
func TestAFamilyWithNoLaunchTemplateIsRefusedAtLoad2914(t *testing.T) {
	c := orcaPrereqConfig2914("")
	w := c.Workers["iris"]
	w.Family = "private.a"
	w.Profile = &LauncherProfile{Family: "private.a", Mode: ModeLong, Account: "a"}
	c.Workers["iris"] = w
	err := c.validate()
	if err == nil || !strings.Contains(err.Error(), `no launch template for family "private.a"`) {
		t.Fatalf("a family with no launch template must be refused at load naming it, got %v", err)
	}
}
