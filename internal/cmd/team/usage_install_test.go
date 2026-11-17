package team

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

func usageConfigFile(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	y := "memory: hrn:mem:example.com:team\naccounts:\n  - label: acct-a\n    configDir: " + dir + "\n"
	p := filepath.Join(dir, "usage.yaml")
	if err := os.WriteFile(p, []byte(y), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func usageRun(f *cmdutil.Factory, args ...string) (string, string, error) {
	return mountedRun(f, []*cobra.Command{newUsageInstall(f), newUsageUninstall(f), newUsageStatus(f)}, args)
}

func healthRun(f *cmdutil.Factory, args ...string) (string, string, error) {
	return mountedRun(f, healthInstallCmds(f), args)
}

func mountedRun(f *cmdutil.Factory, cmds []*cobra.Command, args []string) (string, string, error) {
	io, out, errOut := output.Test()
	f.IOStreams = io
	root := &cobra.Command{Use: "x", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().BoolVar(&f.JSON, "json", false, "")
	root.AddCommand(cmds...)
	root.SetArgs(args)
	root.SetOut(&bytes.Buffer{})
	err := root.Execute()
	return out.String(), errOut.String(), err
}

// The usage entry is its own: its own unit, its own command, and installing or
// removing it never touches the health sweep's entry (and the reverse).
func TestUsageInstallIsSeparateFromHealth(t *testing.T) {
	orcaOnPath(t)
	_, _, home := healthHarness(t, "systemctl")
	f := &cmdutil.Factory{}
	ucfg := usageConfigFile(t)
	if _, _, err := usageRun(f, "install", "--config", ucfg, "--interval", "15m", "--bin", "/usr/local/bin/hadron"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := healthRun(f, "install", "--config", hostConfig(t, ""), "--interval", "5m", "--bin", "/usr/local/bin/hadron"); err != nil {
		t.Fatal(err)
	}
	unit := filepath.Join(home, ".config", "systemd", "user")
	svc, err := os.ReadFile(filepath.Join(unit, "hadron-team-usage.service"))
	if err != nil {
		t.Fatalf("usage unit missing: %v", err)
	}
	if !strings.Contains(string(svc), `"team" "usage" "collect" "--config" "`+ucfg+`"`) {
		t.Errorf("usage unit runs the wrong command:\n%s", svc)
	}
	if _, err := os.Stat(filepath.Join(unit, "hadron-team-health.timer")); err != nil {
		t.Errorf("health entry missing: %v", err)
	}
	out, _, err := usageRun(f, "uninstall", "--json")
	_ = out
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(unit, "hadron-team-usage.timer")); err == nil {
		t.Error("usage timer survived uninstall")
	}
	if _, err := os.Stat(filepath.Join(unit, "hadron-team-health.timer")); err != nil {
		t.Errorf("usage uninstall removed the health entry: %v", err)
	}
}

func TestUsageInstallStatusUninstallJSON(t *testing.T) {
	_, _, _ = healthHarness(t, "systemctl")
	f := &cmdutil.Factory{}
	ucfg := usageConfigFile(t)
	out, _, err := usageRun(f, "install", "--config", ucfg, "--interval", "15m", "--bin", "/usr/local/bin/hadron", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var inst usageInstallDTO
	if err := json.Unmarshal([]byte(out), &inst); err != nil || inst.Scheduler != "systemd" || !inst.Changed || inst.Interval != "15m0s" || inst.Config != ucfg {
		t.Fatalf("install: %v %+v", err, inst)
	}
	// Installing again changes nothing.
	out, _, _ = usageRun(f, "install", "--config", ucfg, "--interval", "15m", "--bin", "/usr/local/bin/hadron", "--json")
	_ = json.Unmarshal([]byte(out), &inst)
	if inst.Changed {
		t.Error("second install must be a no-op")
	}
	out, _, err = usageRun(f, "status", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var st usageStatusDTO
	if err := json.Unmarshal([]byte(out), &st); err != nil || !st.Installed || st.Binary == nil || *st.Binary != "/usr/local/bin/hadron" || st.Config == nil || *st.Config != ucfg || st.Files == nil || st.Notes == nil {
		t.Fatalf("status: %v %+v", err, st)
	}
	if _, _, err := usageRun(f, "uninstall"); err != nil {
		t.Fatal(err)
	}
	out, _, _ = usageRun(f, "status", "--json")
	_ = json.Unmarshal([]byte(out), &st)
	if st.Installed {
		t.Error("still installed after uninstall")
	}
}

// Under cron both jobs share one crontab; each finds, replaces and removes
// only its own line.
func TestUsageCronLineCoexistsWithHealth(t *testing.T) {
	orcaOnPath(t)
	hf, _, _ := healthHarness(t, "crontab")
	f := &cmdutil.Factory{}
	hf.crontab = "0 3 * * * /usr/bin/backup\n"
	if _, _, err := usageRun(f, "install", "--config", usageConfigFile(t), "--interval", "15m", "--bin", "/usr/local/bin/hadron"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := healthRun(f, "install", "--config", hostConfig(t, ""), "--interval", "5m", "--bin", "/usr/local/bin/hadron"); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(hf.crontab, "# hadron-team-usage config="); n != 1 {
		t.Fatalf("usage cron lines: %d\n%s", n, hf.crontab)
	}
	if n := strings.Count(hf.crontab, "# hadron-team-health config="); n != 1 {
		t.Fatalf("health cron lines: %d\n%s", n, hf.crontab)
	}
	if _, _, err := usageRun(f, "uninstall"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(hf.crontab, "hadron-team-usage") || !strings.Contains(hf.crontab, "hadron-team-health") || !strings.Contains(hf.crontab, "/usr/bin/backup") {
		t.Errorf("uninstall must remove only the usage line:\n%s", hf.crontab)
	}
}

// An invalid config writes nothing: exit 2 before any scheduler call.
func TestUsageInstallRefusesAnInvalidConfig(t *testing.T) {
	hf, _, home := healthHarness(t, "systemctl")
	f := &cmdutil.Factory{}
	p := filepath.Join(t.TempDir(), "usage.yaml")
	_ = os.WriteFile(p, []byte("accounts:\n  - label: me@example.com\n    configDir: /tmp/x\n"), 0o600)
	_, _, err := usageRun(f, "install", "--config", p, "--interval", "15m", "--bin", "/usr/local/bin/hadron")
	if code(err) != 2 {
		t.Fatalf("exit=%d want 2 (%v)", code(err), err)
	}
	if len(hf.calls) != 0 {
		t.Errorf("scheduler was called: %v", hf.calls)
	}
	if _, serr := os.Stat(filepath.Join(home, ".config")); serr == nil {
		t.Error("something was written")
	}
}
