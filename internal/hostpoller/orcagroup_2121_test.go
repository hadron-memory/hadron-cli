package hostpoller

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// cli#2121 (Rook's P3 on #2088 at 9e715787): the busy-tree refusal holds for
// an Orca worker too. A process working in the tree means nothing is
// launched through Orca.
func TestWorkerStartRefusesABusyTreeForAnOrcaWorker2121(t *testing.T) {
	p, srv, host := testPoller(t)
	orcaWorker(p, filepath.Join(t.TempDir(), "disk"), LauncherProfile{Family: "claude", Mode: ModeLong, Account: "a"}, AccountProfile{Bin: "claude-holger"})
	rec := &recordedOrca{err: errors.New("must not be called")}
	p.EnableOrcaLaunch(rec.launch, ConfigProfiles{Cfg: p.cfg})
	host.occupant = "pid 77 (claude)"
	srv.notDone["s1"] = 1
	srv.batches = [][]Operation{{startOp()}}
	step(t, p)
	if len(rec.specs) != 0 || len(host.launches) != 0 {
		t.Fatalf("a busy tree launched: orca=%d exec=%d", len(rec.specs), len(host.launches))
	}
	if r := only(t, srv); r.outcome != Failed || !strings.Contains(r.reason, "already working") {
		t.Fatalf("%+v", r)
	}
}
