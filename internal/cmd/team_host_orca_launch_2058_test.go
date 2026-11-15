package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2058 C3: a host config can say a worker starts through Orca from a
// typed launcher profile. The config loads only when everything the launch
// needs is there; nothing is discovered missing at a start.

func orcaWorkerConfig(t *testing.T, workerExtra, topExtra string) string {
	t.Helper()
	disk := filepath.Join(t.TempDir(), "disk")
	return writeHostConfig(t, "    launch_via: orca\n    disk_tmp: "+disk+"\n"+workerExtra+topExtra)
}

const orcaProfile = "    launcher_profile: {family: claude, mode: short, model: opus, account: holger}\n"
const orcaTop = "account_profiles:\n  holger: {bin: claude-holger}\norca_launch:\n  orca_bin: /usr/bin/orca\n"

func TestTeamHostStatusAcceptsAnOrcaLaunchedWorker2058(t *testing.T) {
	cfg := orcaWorkerConfig(t, orcaProfile, orcaTop)
	if err := os.WriteFile(filepath.Join(filepath.Dir(cfg), "hadron-host-state.json"), []byte(`{"created":{},"keep":{},"hostId":"h1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	srv, _ := hostServerFake(t, map[string]string{"HostRegistrations": `{"data":{"hostRegistrations":[]}}`})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "host", "status", "--config", cfg, "--server", srv.URL, "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("an Orca-launched worker config was refused: %v\n%s", err, out.String())
	}
}

func TestTeamHostRunRefusesAnIncompleteOrcaWorker2058(t *testing.T) {
	for name, tc := range map[string]struct{ worker, top, want string }{
		"no profile":         {"", orcaTop, "launcher_profile"},
		"unknown account":    {orcaProfile, "orca_launch:\n  orca_bin: /usr/bin/orca\n", "account_profiles"},
		"no orca binary":     {orcaProfile, "account_profiles:\n  holger: {bin: claude-holger}\n", "orca_launch.orca_bin"},
		"effort on claude":   {"    launcher_profile: {family: claude, mode: long, effort: high, account: holger}\n", orcaTop, "effort"},
		"unknown launch_via": {"", "", "launch_via"},
	} {
		t.Run(name, func(t *testing.T) {
			var cfg string
			if name == "unknown launch_via" {
				cfg = writeHostConfig(t, "    launch_via: systemd\n")
			} else {
				cfg = orcaWorkerConfig(t, tc.worker, tc.top)
			}
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"team", "host", "run", "--once", "--config", cfg})
			err := root.Execute()
			if exitCodeFor(err) != exitcode.Usage || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("exit %d (%v), want usage naming %q", exitCodeFor(err), err, tc.want)
			}
		})
	}
}

// A valid Orca worker config runs: the launcher is wired and nothing is
// refused at startup (no start is collected here, so Orca is never called).
func TestTeamHostRunWiresTheOrcaLauncher2058(t *testing.T) {
	stubNamespacePreflight(t, nil)
	cfg := orcaWorkerConfig(t, orcaProfile, orcaTop)
	srv, calls := hostServerFake(t, map[string]string{
		"RegisterHost":          hostRegistrationAnswer,
		"CollectHostOperations": `{"data":{"collectHostOperations":[]}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "host", "run", "--once", "--config", cfg, "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("run --once with an Orca worker: %v", err)
	}
	if got := calls(); len(got) != 2 {
		t.Fatalf("want register and collect; got %+v", got)
	}
}

func stubNamespacePreflight(t *testing.T, err error) {
	t.Helper()
	old := team.AgentNamespacePreflight
	team.AgentNamespacePreflight = func() error { return err }
	t.Cleanup(func() { team.AgentNamespacePreflight = old })
}

// A host that cannot create the agent's PID namespace refuses at startup,
// rather than failing every start (Wolfgang #108196: a pkill by one worker
// must see only its own namespace).
func TestTeamHostRunRefusesWithoutAgentNamespaces2058(t *testing.T) {
	stubNamespacePreflight(t, errors.New("unshare: Operation not permitted"))
	cfg := orcaWorkerConfig(t, orcaProfile, orcaTop)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "host", "run", "--once", "--config", cfg})
	err := root.Execute()
	if exitCodeFor(err) != exitcode.Usage || !strings.Contains(err.Error(), "PID namespace") {
		t.Fatalf("exit %d (%v)", exitCodeFor(err), err)
	}
}
