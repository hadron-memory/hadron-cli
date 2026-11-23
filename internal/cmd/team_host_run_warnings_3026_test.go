package cmd

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// writeStaleLaunchHostConfig writes a host file whose worker names a launcher_profile AND a leftover
// launch argv. It is written as is: hostyaml.Supervised drops the shorthand beside a profile.
func writeStaleLaunchHostConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	key := filepath.Join(dir, "host.key")
	if err := os.WriteFile(key, []byte("hk_host_key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := "app: hrn:app:acme.com:eng-team\nlabel: box1\npoll_interval_seconds: 30\nkey_file: " + key + "\nworktrees_root: " + filepath.Join(dir, "trees") + "\n" +
		"workers:\n  iris:\n    family: claude\n    launch: [claude-worker]\n    launcher_profile: {family: claude, mode: long, account: acct}\n    disk_tmp: " + filepath.Join(dir, "tmp") + "\n" +
		"account_profiles:\n  acct: {bin: claude}\norca_launch:\n  orca_bin: /usr/bin/orca\n"
	p := filepath.Join(dir, "host.yaml")
	if err := os.WriteFile(p, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// cli#3026: `host run` says the stale-launch warning once, on stderr, at start; `host status` reloads the
// same file and says nothing. Pins the call site, not only the writer.
func TestHostRunSaysTheStaleLaunchWarningOnceAndStatusStaysQuiet(t *testing.T) {
	cfg := writeStaleLaunchHostConfig(t)
	srv, _ := hostServerFake(t, map[string]string{
		"RegisterHost":      `{"errors":[{"message":"no host scope","extensions":{"code":"APP_KEY_SCOPE_REQUIRED"}}],"data":null}`,
		"HostRegistrations": `{"data":{"hostRegistrations":[]}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "host", "run", "--once", "--config", cfg, "--server", srv.URL})
	err := root.Execute()
	// cli#3045 item 8: the warning is written before the Linux-only wiring refusal, so it is the first
	// thing stderr says on any OS. What ENDS the run is OS-dependent (off Linux the Orca wiring refuses
	// before registration), so the refused-key ending is asserted on Linux only.
	if runtime.GOOS == "linux" && (err == nil || !strings.Contains(err.Error(), "no host scope")) {
		t.Fatalf("the run must end on the refused key, got %v", err)
	}
	got := f.IOStreams.ErrOut.(*strings.Builder).String()
	if n := strings.Count(got, `warning: worker "iris": launch`); n != 1 {
		t.Fatalf("want the warning once on stderr, got %d:\n%s", n, got)
	}
	if !strings.HasPrefix(got, "warning: worker \"iris\": launch") {
		t.Fatalf("the load warning is the first thing stderr says:\n%s", got)
	}

	f2, _ := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"team", "host", "status", "--config", cfg, "--server", srv.URL})
	if err := root2.Execute(); err != nil {
		t.Fatal(err)
	}
	if quiet := f2.IOStreams.ErrOut.(*strings.Builder).String(); strings.Contains(quiet, "warning:") {
		t.Fatalf("status must stay quiet:\n%s", quiet)
	}
}

// cli#3045 item 2: a warning is advice. A stderr that cannot be written must not stop `host run`: an
// abort ends the check-ins, and a host that stops checking in lapses (cor:agt:070:01). The run goes on
// to register, so what ends it here is the server's refusal, not the write.
func TestHostRunGoesOnWhenItCannotWriteTheLoadWarning3045(t *testing.T) {
	cfg := writeStaleLaunchHostConfig(t)
	srv, calls := hostServerFake(t, map[string]string{
		"RegisterHost":      `{"errors":[{"message":"no host scope","extensions":{"code":"APP_KEY_SCOPE_REQUIRED"}}],"data":null}`,
		"HostRegistrations": `{"data":{"hostRegistrations":[]}}`,
	})
	f, _ := testFactory(t)
	f.IOStreams.ErrOut = failingWriter{}
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "host", "run", "--once", "--config", cfg, "--server", srv.URL})
	err := root.Execute()
	if err == nil || strings.Contains(err.Error(), "write failed") {
		t.Fatalf("a failed warning write must not end the run; got %v", err)
	}
	if runtime.GOOS != "linux" {
		return // off Linux the Orca wiring refuses before registration; the write-failure check above holds
	}
	registered := false
	for _, c := range calls() {
		if strings.HasPrefix(c.op, "RegisterHost") {
			registered = true
		}
	}
	if !registered {
		t.Fatalf("the run never reached its registration: %+v", calls())
	}
}
