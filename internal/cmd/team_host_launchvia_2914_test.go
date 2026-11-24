package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2914: `host run` refuses a config that still names the removed exec launch path, before any server
// call, saying what to set instead. (Written by hand: writeHostConfig renders the old `launch:` shorthand
// into a supervised worker, which is exactly what these configs must not get.)
func TestTeamHostRunRefusesTheRemovedExecLaunch2914(t *testing.T) {
	for name, worker := range map[string]string{
		"explicit launch_via exec": "    launch_via: exec\n    launch: [claude-worker]\n",
		"launch argv only":         "    launch: [claude-worker]\n",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			key := filepath.Join(dir, "host.key")
			if err := os.WriteFile(key, []byte("hk_host_key\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg := filepath.Join(dir, "host.yaml")
			text := "app: hrn:app:acme.com:eng-team\nlabel: box1\npoll_interval_seconds: 30\nkey_file: " + key + "\nworktrees_root: " + filepath.Join(dir, "trees") +
				"\nworkers:\n  iris:\n    family: claude\n" + worker
			if err := os.WriteFile(cfg, []byte(text), 0o600); err != nil {
				t.Fatal(err)
			}
			srv, calls := hostServerFake(t, map[string]string{"RegisterHost": hostRegistrationAnswer})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"team", "host", "run", "--once", "--config", cfg, "--server", srv.URL})
			err := root.Execute()
			if n := len(calls()); n != 0 {
				t.Fatalf("%d server calls before the refusal", n)
			}
			if exitCodeFor(err) != exitcode.Usage || !strings.Contains(err.Error(), "launcher_profile") || !strings.Contains(err.Error(), "removed") {
				t.Fatalf("want a usage refusal naming launcher_profile and the removal, got exit %d: %v", exitCodeFor(err), err)
			}
		})
	}
}

// The host run and host sweep help and the agent contract no longer describe the removed exec path: the
// host-run entry once still said "default exec; TRANSITIONAL", the reload refusal for unwired Orca, the
// launch-program trust check and "typed Orca-launch hosts".
func TestHostRunDocsDoNotDescribeTheRemovedExecPath2914(t *testing.T) {
	helpOf := func(args ...string) string {
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append(args, "--help"))
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		return strings.Join(strings.Fields(out.String()), " ")
	}
	texts := map[string]string{
		"host run --help":   helpOf("team", "host", "run"),
		"host sweep --help": helpOf("team", "host", "sweep"),
		"agentic-usage":     strings.Join(strings.Fields(string(readContract(t))), " "),
	}
	for _, bad := range []string{
		"default exec", "launch, end and recover programs", "A launch, end, recover or account", "launch/end/recover programs",
		"that wiring is made at start", "exec-launch hosts", "TRANSITIONAL until S1b/C4", "typed Orca-launch hosts", "On typed Orca-launch",
	} {
		for name, text := range texts {
			if strings.Contains(text, bad) {
				t.Errorf("%s still says %q", name, bad)
			}
		}
	}
}

// The platform rule of the supervised launch: only Linux (pidfd pins the launched agent). A pure function so
// every host OS is pinned (the wiring refuses at host run startup, before any poll).
func TestOrcaLaunchNeedsLinux2914(t *testing.T) {
	if err := team.OrcaLaunchPlatformError("linux"); err != nil {
		t.Fatalf("linux launches: %v", err)
	}
	for _, goos := range []string{"darwin", "windows", "freebsd"} {
		err := team.OrcaLaunchPlatformError(goos)
		if exitCodeFor(err) != exitcode.Usage || !strings.Contains(err.Error(), "needs Linux") || !strings.Contains(err.Error(), "cannot run workers") {
			t.Errorf("%s: want a usage refusal naming Linux and that the host cannot run workers, got %v", goos, err)
		}
	}
}
