package healthsched

import (
	"strings"
	"testing"
	"time"
)

func TestSweepUnitHasCrashLimitAndOneMinuteTimer2056(t *testing.T) {
	s := Spec{Job: Job{Name: "hadron-team-sweep", Sub: []string{"team", "host", "sweep"}, Describes: "team sweep"}, Bin: "/usr/bin/hadron", Config: "/etc/hadron/host.yaml", Interval: time.Minute}
	unit := SystemdService(s)
	for _, want := range []string{"StartLimitBurst=3", "StartLimitIntervalSec=60", "OnFailure=", "\"team\" \"host\" \"sweep\""} {
		if !strings.Contains(unit, want) {
			t.Errorf("unit lacks %s: %s", want, unit)
		}
	}
	if !strings.Contains(SystemdTimer(s), "OnUnitActiveSec=1min") {
		t.Fatal("sweep timer must be one minute")
	}
}

func TestSweepUnitInstallUninstallRetainsHealth2056(t *testing.T) {
	home := t.TempDir()
	e := Env{Home: home, GOOS: "linux", UID: 1000, Job: Sweep, Which: func(n string) (string, error) { return "/usr/bin/" + n, nil }, Run: func([]string, string) ([]byte, error) { return nil, nil }}
	s := Spec{Bin: "/usr/bin/hadron", Config: "/etc/hadron/host.yaml", Interval: time.Minute, Path: "/usr/bin"}
	health := e
	health.Job = Health
	if _, err := health.Install(s); err != nil {
		t.Fatal(err)
	}
	got, err := e.Install(s)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Files) != 3 {
		t.Fatalf("missing failure unit: %+v", got)
	}
	again, err := e.Install(s)
	if err != nil || again.Changed {
		t.Fatalf("install not idempotent: %+v %v", again, err)
	}
	if _, err := e.Uninstall(); err != nil {
		t.Fatal(err)
	}
	if !health.ReadStatus().Installed {
		t.Fatal("sweep uninstall removed health timer")
	}
}
