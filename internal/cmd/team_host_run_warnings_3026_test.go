package cmd

import (
	"os"
	"path/filepath"
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
	_ = root.Execute() // the refused key ends the run; the warning comes first
	got := f.IOStreams.ErrOut.(*strings.Builder).String()
	if n := strings.Count(got, `warning: worker "iris": launch`); n != 1 {
		t.Fatalf("want the warning once on stderr, got %d:\n%s", n, got)
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
