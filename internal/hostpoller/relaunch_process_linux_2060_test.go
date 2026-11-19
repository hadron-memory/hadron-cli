//go:build linux

package hostpoller

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/hostrelaunch"
)

// A private PTY adapter executes the real fixed command; only its own process
// set is the fixture's census authority. Kernel pidfd/tty/exec identity is real.
// This does not claim the independent container/real-Orca acceptance leg.
func TestPeriodicRelaunchPinsRealProcessInPlainDirectory2060(t *testing.T) {
	p, _, _ := testPoller(t)
	dir := p.startCfg.WorktreePath("repo/iris")
	if err := os.MkdirAll(filepath.Join(dir, ".hadron-team"), 0700); err != nil {
		t.Fatal(err)
	}
	binding := map[string]string{"workerId": "id-iris", "sessionId": "still-open", "appId": "app-id", "server": "https://private.invalid"}
	raw, _ := json.Marshal(binding)
	if err := os.WriteFile(filepath.Join(dir, ".hadron-team", "hadron-team-session.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	bins := t.TempDir()
	agentBin := filepath.Join(bins, "claude")
	launcherBin := filepath.Join(bins, "slow-launch")
	if err := os.WriteFile(agentBin, []byte("#!/bin/sh\ntrap 'exit 0' TERM INT\nwhile :; do sleep 1; done\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(launcherBin, []byte("#!/bin/sh\nsleep 0.15\nexec "+shq(agentBin)+" \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	orcaWorker(p, filepath.Join(t.TempDir(), "disk"), LauncherProfile{Family: "claude", Mode: ModeLong, Account: "private"}, AccountProfile{Bin: launcherBin})
	adapter := newFakeOrca(t)
	launcher := orcaLauncherFor(t, adapter)
	var made Agent
	p.EnableOrcaLaunch(func(ctx context.Context, s OrcaLaunchSpec) (Agent, error) {
		a, err := launcher.Launch(ctx, s)
		if a != nil {
			made = a
			return a, err
		}
		return nil, err
	}, ConfigProfiles{Cfg: p.startCfg})
	source := &loopSource2060{snapshot: RelaunchSnapshot{AppID: "app-id", HostID: "h1", Server: "https://private.invalid", SweepMode: "APPLY", Workers: []RelaunchWorker{{WorkerID: "id-iris", Family: "claude", Desired: "RUNNING", Reason: "LONG_LIVED", SessionID: "still-open", Worktrees: []string{"repo/iris"}}}}}
	startupZeros := 0
	deps := RelaunchDependencies{Source: source, Ready: func(context.Context) error { return nil }, PostLaunchWait: 3 * time.Second,
		Probe: func(_ context.Context, _ hostrelaunch.Target) (hostrelaunch.Observation, error) {
			out := hostrelaunch.Observation{Proven: true, PIDs: []int{}}
			if made == nil {
				return out, nil
			}
			raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(made.PID()), "cmdline"))
			if err != nil {
				return hostrelaunch.Observation{}, err
			}
			args, ok := argvFromCmdline(raw)
			if !ok {
				return hostrelaunch.Observation{}, nil
			}
			if agentBase(args) != "" {
				out.PIDs = []int{made.PID()}
			} else {
				startupZeros++
			}
			return out, nil
		}}
	if err := p.EnableRelaunch(deps); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := p.Step(context.Background()); err != nil {
			t.Fatal(err)
		}
		p.Wait()
	}
	if made == nil {
		t.Fatal("no real agent launched")
	}
	defer func() { _, _ = made.Terminate() }()
	if p.agents["id-iris"] != made || p.agentSessions["id-iris"] != "still-open" {
		t.Fatal("real launch was not verified/tracked")
	}
	if startupZeros == 0 {
		t.Fatal("cold exec window was not exercised")
	}
	record := p.state.recordedAgent("id-iris")
	if record.PID != made.PID() || record.Start == "" {
		t.Fatalf("pinned identity not persisted: %+v", record)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); !os.IsNotExist(err) {
		t.Fatal("fixture unexpectedly needs Git")
	}
}
