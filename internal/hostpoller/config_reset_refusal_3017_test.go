package hostpoller

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cli#3017: the reset-context host handler was deleted, so a host can no longer
// honestly declare worker.reset-context (cor:agt:070:03: a declaration claims the
// host performs the operation). A config that still names the capability — or the
// reserved reset_context block — must be refused at load, so the registration
// payload can never carry the name again.
func loadResetContextFixture(t *testing.T, capabilities, resetBlock string) error {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "host.yaml")
	body := "app: app1\nworktrees_root: " + dir + "\n" + capabilities + resetBlock +
		"workers:\n  iris:\n    family: claude\n    worktrees: [wt-iris]\n" +
		"    launcher_profile: {family: claude, mode: long, account: acct}\n    disk_tmp: /tmp/reset-refusal/iris\n" +
		"account_profiles:\n  acct: {bin: claude}\norca_launch:\n  orca_bin: /usr/bin/orca\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadConfig(path)
	return err
}

func TestLoadConfigRefusesTheResetContextCapability(t *testing.T) {
	err := loadResetContextFixture(t,
		"capabilities: [worker.reset-context]\n",
		"reset_context:\n  orca_bin: /usr/bin/orca\n")
	if err == nil {
		t.Fatal("a config declaring worker.reset-context must be refused: no host performs it (hadron-cli#3017)")
	}
	for _, want := range []string{"worker.reset-context", "hadron-cli#3017"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must say %q: %v", want, err)
		}
	}
}

func TestLoadConfigRefusesTheCapabilityWithoutABlock(t *testing.T) {
	if err := loadResetContextFixture(t, "capabilities: [worker.reset-context]\n", ""); err == nil {
		t.Fatal("declaring the capability alone must be refused, not silently registered without a handler")
	}
}

func TestLoadConfigRefusesAReservedResetContextBlock(t *testing.T) {
	err := loadResetContextFixture(t, "", "reset_context:\n  orca_bin: /usr/bin/orca\n")
	if err == nil {
		t.Fatal("reset_context is no longer read (hadron-cli#3017); a config setting it must be told so")
	}
	if !strings.Contains(err.Error(), "reset_context") {
		t.Errorf("the refusal must name reset_context: %v", err)
	}
}

// The same refusal must hold for a Config built in code (not only one parsed from
// YAML): the registration payload is built from cfg.Capabilities verbatim, so
// validate() is the last gate before registerHost.
func TestValidateRefusesTheResetContextCapabilityOnABuiltConfig(t *testing.T) {
	dir := t.TempDir()
	c := &Config{
		App:           "app1",
		StateFile:     filepath.Join(dir, "state.json"),
		WorktreesRoot: dir,
		Capabilities:  []string{CapabilityResetContext},
	}
	if err := c.validate(); err == nil || !strings.Contains(err.Error(), "worker.reset-context") {
		t.Fatalf("validate() must refuse the capability: %v", err)
	}
}
