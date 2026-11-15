package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
			srv, calls := hostServerFake(t, map[string]string{"RegisterHost": hostRegistrationAnswer})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"team", "host", "run", "--once", "--config", cfg, "--server", srv.URL})
			err := root.Execute()
			if n := len(calls()); n != 0 {
				t.Fatalf("%d server calls before the refusal", n)
			}
			if exitCodeFor(err) != exitcode.Usage || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("exit %d (%v), want usage naming %q", exitCodeFor(err), err, tc.want)
			}
		})
	}
}

// The launcher is wired: a collected worker.start of an Orca worker reaches
// the configured orca binary (terminal create), never the exec path, and the
// start reports Orca's refusal (cli#2088 blind P3).
func TestTeamHostRunWiresTheOrcaLauncher2058(t *testing.T) {
	bin := t.TempDir()
	log := filepath.Join(bin, "orca.log")
	orca := filepath.Join(bin, "orca")
	if err := os.WriteFile(orca, []byte("#!/bin/sh\nprintf '%s\\n' \"$1 $2\" >> '"+log+"'\necho '{\"ok\":false,\"error\":{\"code\":\"fake_refused\"}}'\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	top := "account_profiles:\n  holger: {bin: claude-holger}\norca_launch:\n  orca_bin: " + orca + "\n"
	cfg := orcaWorkerConfig(t, orcaProfile+"    worktrees: [repo/iris]\n", top)
	if err := os.MkdirAll(filepath.Join(filepath.Dir(cfg), "trees", "repo", "iris"), 0o700); err != nil {
		t.Fatal(err)
	}
	start := strings.Replace(polledOpJSON("o1", "worker.start", "COLLECTED"), `"args":{}`, `"args":{"workerId":"wkr1","worktree":"repo/iris","family":"claude","taskId":"t1"}`, 1)
	start = strings.Replace(start, `"failureReason":null}`, `"failureReason":null,"launchGrant":"hlg_grant"}`, 1)
	srv, calls := hostServerFake(t, map[string]string{
		"RegisterHost":          hostRegistrationAnswer,
		"CollectHostOperations": `{"data":{"collectHostOperations":[` + start + `]}}`,
		"ReportHostOperation":   `{"data":null,"errors":[{"message":"not bound yet","extensions":{"code":"HOST_OPERATION_NOT_DONE"}}]}`,
		// C5's periodic read is separate from this explicit C3 start. OFF must
		// neither call terminal list nor launch another agent after the refusal.
		"HostRelaunchSnapshot": `{"data":{"myApp":{"id":"app1"},"hostDesiredState":{"hostId":"h1","sweepMode":"OFF","workers":[]}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "host", "run", "--once", "--config", cfg, "--server", srv.URL})
	_ = root.Execute()
	got, _ := os.ReadFile(log)
	if strings.TrimSpace(string(got)) != "terminal create" {
		t.Fatalf("the Orca launcher was not used; orca calls %q\n%s", got, out.String())
	}
	var reported bool
	for _, c := range calls() {
		if c.op == "ReportHostOperation" && strings.Contains(string(c.vars), "through Orca failed") && strings.Contains(string(c.vars), "fake_refused") {
			reported = true
		}
	}
	if !reported {
		t.Fatalf("the start did not report Orca's refusal: %+v", calls())
	}
}

// cli#2121: orca_launch.group would run the command under sg, and a process
// started through sg cannot be read through /proc by the poller whatever the
// group (probed on orca1), so it could never pass the launch proof. The
// config refuses it at load, saying why, with or without an Orca worker.
func TestTeamHostRunRefusesOrcaLaunchGroup2121(t *testing.T) {
	for name, cfg := range map[string]string{
		"with an Orca worker": orcaWorkerConfig(t, orcaProfile, orcaTop+"  group: incus\n"),
		"with no Orca worker": writeHostConfig(t, "orca_launch:\n  orca_bin: /usr/bin/orca\n  group: incus\n"),
	} {
		t.Run(name, func(t *testing.T) {
			// Hermetic (cli#2144): a fake server, and the refusal must come
			// before any call to it.
			srv, calls := hostServerFake(t, map[string]string{"RegisterHost": hostRegistrationAnswer})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"team", "host", "run", "--once", "--config", cfg, "--server", srv.URL})
			err := root.Execute()
			if n := len(calls()); n != 0 {
				t.Fatalf("%d server calls before the refusal: %+v", n, calls())
			}
			if exitCodeFor(err) != exitcode.Usage || !strings.Contains(err.Error(), "ORCA_GROUP_UNPROVABLE") || !strings.Contains(err.Error(), "orca_launch.group") || !strings.Contains(err.Error(), "/proc") {
				t.Fatalf("exit %d (%v)", exitCodeFor(err), err)
			}
		})
	}
}
