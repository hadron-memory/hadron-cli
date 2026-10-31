package team

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/healthsched"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

type healthFake struct {
	crontab string
	have    map[string]bool
	calls   []string
}

func (h *healthFake) run(argv []string, stdin string) ([]byte, error) {
	k := strings.Join(argv, " ")
	h.calls = append(h.calls, k)
	switch k {
	case "crontab -l":
		if h.crontab == "" {
			return []byte("no crontab for u"), errors.New("exit 1")
		}
		return []byte(h.crontab), nil
	case "crontab -":
		h.crontab = stdin
	}
	return nil, nil
}

// healthHarness mounts the three verbs under a throwaway `health` parent (the
// real group is registered by the sweep's own slice) with a fake scheduler.
func healthHarness(t *testing.T, tools ...string) (*healthFake, func(args ...string) (string, string, error), string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", "")
	home := t.TempDir()
	hf := &healthFake{have: map[string]bool{}}
	for _, x := range tools {
		hf.have[x] = true
	}
	prev := healthEnv
	t.Cleanup(func() { healthEnv = prev })
	healthEnv = func(*cmdutil.Factory) healthsched.Env {
		return healthsched.Env{Home: home, GOOS: "linux", UID: 1000, Run: hf.run, Which: func(n string) (string, error) {
			if hf.have[n] {
				return "/usr/bin/" + n, nil
			}
			return "", exec.ErrNotFound
		}}
	}
	run := func(args ...string) (string, string, error) {
		io, out, errOut := output.Test()
		f := &cmdutil.Factory{IOStreams: io}
		root := &cobra.Command{Use: "health", SilenceUsage: true, SilenceErrors: true}
		root.PersistentFlags().BoolVar(&f.JSON, "json", false, "")
		root.AddCommand(healthInstallCmds(f)...)
		root.SetArgs(args)
		root.SetOut(&bytes.Buffer{})
		err := root.Execute()
		return out.String(), errOut.String(), err
	}
	return hf, run, home
}

func orcaOnPath(t *testing.T) {
	t.Helper()
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "orca"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", d+":"+os.Getenv("PATH"))
}

func hostConfig(t *testing.T, orcaBin string) string {
	t.Helper()
	dir := t.TempDir()
	key := filepath.Join(dir, "host.key")
	if err := os.WriteFile(key, []byte("hdr_app_SENTINELKEY"), 0o600); err != nil {
		t.Fatal(err)
	}
	y := "app: hrn:app:example.com:team\nkey_file: " + key + "\nworktrees_root: " + dir + "\npoll_interval_seconds: 30\nworkers:\n  iris:\n    family: claude\n    launch: [claude-worker]\n"
	if orcaBin != "" {
		y += "delivery:\n  orca_bin: " + orcaBin + "\n"
	}
	p := filepath.Join(dir, "host.yaml")
	if err := os.WriteFile(p, []byte(y), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func code(err error) int {
	if err == nil {
		return 0
	}
	var ce *exitcode.CodedError
	if errors.As(err, &ce) {
		return ce.Code
	}
	return exitcode.Error
}

func TestHealthInstallStatusUninstallJSON(t *testing.T) {
	orcaOnPath(t)
	_, run, home := healthHarness(t, "systemctl")
	cfg := hostConfig(t, "")
	out, _, err := run("install", "--config", cfg, "--interval", "5m", "--bin", "/usr/local/bin/hadron", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var in map[string]any
	if json.Unmarshal([]byte(out), &in) != nil || in["scheduler"] != "systemd" || in["changed"] != true || in["config"] != cfg || strings.Contains(out, "SENTINELKEY") {
		t.Fatal(out)
	}
	unit, _ := os.ReadFile(filepath.Join(home, ".config/systemd/user/hadron-team-health.service"))
	if !strings.Contains(string(unit), `ExecStart="/usr/local/bin/hadron" "team" "health" "sweep" "--config" "`+cfg+`"`) || strings.Contains(string(unit), "SENTINELKEY") {
		t.Fatalf("unit %s", unit)
	}

	out, _, err = run("status", "--json")
	var st map[string]any
	if err != nil || json.Unmarshal([]byte(out), &st) != nil || st["installed"] != true || st["interval"] != "5m0s" || st["binary"] != "/usr/local/bin/hadron" || st["orca"] == nil || st["lastRun"] == nil {
		t.Fatalf("%v %s", err, out)
	}

	out, _, err = run("uninstall", "--json")
	if err != nil || !strings.Contains(out, `"removed": true`) {
		t.Fatalf("%v %s", err, out)
	}
	out, _, _ = run("status", "--json")
	if !strings.Contains(out, `"installed": false`) || !strings.Contains(out, `"lastRun": null`) || !strings.Contains(out, `"files": []`) {
		t.Fatal(out)
	}
	if out, _, err = run("uninstall"); err != nil || !strings.Contains(out, "nothing installed") {
		t.Fatalf("%v %q", err, out)
	}
}

func TestHealthInstallUsageRefusals(t *testing.T) {
	orcaOnPath(t)
	cfg := hostConfig(t, "")
	for name, args := range map[string][]string{
		"interval missing":    {"install", "--config", cfg},
		"interval blank":      {"install", "--config", cfg, "--interval", " "},
		"interval garbage":    {"install", "--config", cfg, "--interval", "often"},
		"interval sub-minute": {"install", "--config", cfg, "--interval", "30s"},
		"interval zero":       {"install", "--config", cfg, "--interval", "0"},
		"interval negative":   {"install", "--config", cfg, "--interval", "-5m"},
		"config missing":      {"install", "--interval", "5m"},
		"config unreadable":   {"install", "--config", "/nonexistent/host.yaml", "--interval", "5m"},
		"bin relative":        {"install", "--config", cfg, "--interval", "5m", "--bin", "rel/hadron"},
	} {
		t.Run(name, func(t *testing.T) {
			hf, run, home := healthHarness(t, "systemctl")
			_, _, err := run(args...)
			if code(err) != exitcode.Usage {
				t.Fatalf("exit = %d (%v)", code(err), err)
			}
			if n := strings.Count(strings.Join(hf.calls, "\n"), "daemon-reload"); n != 0 {
				t.Errorf("a refused install called the scheduler: %q", hf.calls)
			}
			if _, err := os.Stat(filepath.Join(home, ".config")); err == nil {
				t.Error("a refused install wrote files")
			}
		})
	}
}

func TestHealthInstallRefusesWhenOrcaDoesNotResolve(t *testing.T) {
	t.Setenv("PATH", "/nonexistent")
	hf, run, home := healthHarness(t, "systemctl")
	_, _, err := run("install", "--config", hostConfig(t, ""), "--interval", "5m", "--bin", "/usr/local/bin/hadron")
	if code(err) != exitcode.Usage || !strings.Contains(err.Error(), "orca") {
		t.Fatalf("got %d %v", code(err), err)
	}
	if len(hf.calls) != 0 {
		t.Errorf("scheduler called: %q", hf.calls)
	}
	if _, err := os.Stat(filepath.Join(home, ".config")); err == nil {
		t.Error("wrote files")
	}
}

// delivery.orca_bin is honoured when the PATH has no orca: the sweep resolves
// orca as the host does.
func TestHealthInstallAcceptsOrcaBinWithoutOrcaOnPath(t *testing.T) {
	t.Setenv("PATH", "/nonexistent")
	orca := filepath.Join(t.TempDir(), "orca")
	_ = os.WriteFile(orca, []byte("#!/bin/sh\n"), 0o755)
	_, run, _ := healthHarness(t, "systemctl")
	out, _, err := run("install", "--config", hostConfig(t, orca), "--interval", "5m", "--bin", "/usr/local/bin/hadron", "--json")
	if err != nil || !strings.Contains(out, `"orca": "`+orca+`"`) {
		t.Fatalf("%v %s", err, out)
	}
}

func TestHealthInstallNoSchedulerAndCronIntervalAreExit2(t *testing.T) {
	orcaOnPath(t)
	cfg := hostConfig(t, "")
	_, run, home := healthHarness(t)
	if _, _, err := run("install", "--config", cfg, "--interval", "5m", "--bin", "/usr/local/bin/hadron"); code(err) != exitcode.Usage {
		t.Fatalf("no scheduler: %d %v", code(err), err)
	}
	if _, err := os.Stat(filepath.Join(home, ".config")); err == nil {
		t.Error("wrote files")
	}
	hf, run, _ := healthHarness(t, "crontab")
	if _, _, err := run("install", "--config", cfg, "--interval", "7m", "--bin", "/usr/local/bin/hadron"); code(err) != exitcode.Usage || hf.crontab != "" {
		t.Fatalf("cron 7m: %d %v %q", code(err), err, hf.crontab)
	}
}

func TestHealthStatusUnderCronSaysUnknown(t *testing.T) {
	orcaOnPath(t)
	_, run, _ := healthHarness(t, "crontab")
	if _, _, err := run("install", "--config", hostConfig(t, ""), "--interval", "15m", "--bin", "/usr/local/bin/hadron"); err != nil {
		t.Fatal(err)
	}
	out, _, _ := run("status")
	if !strings.Contains(out, "last run:  unknown") || !strings.Contains(out, "scheduler: cron") {
		t.Fatal(out)
	}
	out, _, _ = run("status", "--json")
	if !strings.Contains(out, `"at": null`) || !strings.Contains(out, `"exitCode": null`) {
		t.Fatal(out)
	}
}
