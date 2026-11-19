package hostpoller

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestPollerLaunchReservationSerializesC3AndC52060(t *testing.T) {
	p := New(nil, nil, nil, nil, "", nil)
	release, err := p.reserveWorktreeLaunch("repo/w")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.reserveWorktreeLaunch("repo/w"); err == nil {
		t.Fatal("second start admitted")
	}
	other, err := p.reserveWorktreeLaunch("repo/other")
	if err != nil {
		t.Fatal(err)
	}
	other()
	release()
	release, err = p.reserveWorktreeLaunch("repo/w")
	if err != nil {
		t.Fatal(err)
	}
	release()
}

func TestWorkerStartCannotBypassRelaunchReservation2060(t *testing.T) {
	p, srv, host := testPoller(t)
	orcaWorker(p, filepath.Join(t.TempDir(), "disk"), LauncherProfile{Family: "claude", Mode: ModeLong, Account: "a"}, AccountProfile{Bin: "claude"})
	rec := &recordedOrca{}
	p.EnableOrcaLaunch(rec.launch, ConfigProfiles{Cfg: p.startCfg})
	release, err := p.reserveWorktreeLaunch("repo/iris")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	srv.notDone["s1"] = 1
	if err := p.register(context.Background()); err != nil {
		t.Fatal(err)
	}
	p.workerStart(context.Background(), startOp())
	if len(rec.specs) != 0 || len(host.launches) != 0 {
		t.Fatal("worker.start bypassed relaunch owner")
	}
	if r := only(t, srv); r.outcome != Failed || !strings.Contains(r.reason, "another poller launch") {
		t.Fatalf("%+v", r)
	}
}

func TestRelaunchLocalCauseScrubsHostKeyAndControls2060(t *testing.T) {
	p := New(nil, nil, nil, nil, "private-host-key", nil)
	got := p.relaunchCause("failed private-host-key\nnext\x00line")
	if strings.Contains(got, "private-host-key") || strings.ContainsAny(got, "\n\x00") || !strings.Contains(got, "[redacted]") {
		t.Fatal("host key or control leaked")
	}
	if len([]rune(p.relaunchCause(strings.Repeat("x", 1000)))) > 513 {
		t.Fatal("cause is unbounded")
	}
}
