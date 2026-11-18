//go:build !windows

package team

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// #2344: the sweep's fixing loop re-reads the poller state per worker, and an
// empty successful read is unioned with the snapshot instead of replacing it.

func twoTabs2344(handlePrefix, path string) []health2027Term {
	return []health2027Term{
		{Handle: handlePrefix + "_agent", Tab: handlePrefix + "-tab-agent", Screen: []string{"✻ Compiling…", "  go build ./..."}, Path: path},
		{Handle: handlePrefix + "_bare", Tab: handlePrefix + "-tab-bare", Screen: []string{"orca@orca1:~$"}, Path: path},
	}
}

func resetting2344(dto health2027DTO, worker string) bool {
	return health2027Find(dto.Skips, func(s health2027Skip) bool { return s.Code == "resetting" && s.Worker == worker }) != nil
}

// A reset recorded while an EARLIER worker is being handled must still skip a
// LATER worker: the state is read at each worker's turn, not once before the loop.
func TestHealth2344ResetRecordedDuringTheLoopSkipsALaterWorker(t *testing.T) {
	w := health2027SetupTwo(t)
	w.setTerminals(t, append(twoTabs2344("w1", ""), twoTabs2344("w2", w.worktree2)...))
	w.stateFileWrite(t, `{"hostId":"own","resets":{"r1":{"resetId":"r1","workerId":"w1"}}}`)
	w.resetsByID = map[string]string{
		"r1": `{"data":{"contextReset":{"id":"r1","endedAt":null,"phase":"claimed"}}}`,
		"r2": `{"data":{"contextReset":{"id":"r2","endedAt":null,"phase":"claimed"}}}`,
	}
	var once sync.Once
	w.onReset = func(id string) {
		if id != "r1" {
			return
		}
		once.Do(func() { // w1's turn: w2's reset is accepted by the server now
			next := `{"hostId":"own","resets":{"r1":{"resetId":"r1","workerId":"w1"},"r2":{"resetId":"r2","workerId":"w2"}}}`
			if err := os.WriteFile(filepath.Join(w.dir, "state.json"), []byte(next), 0o600); err != nil {
				t.Errorf("write state: %v", err)
			}
		})
	}
	dto, _, err := w.run(t, false, "--dry-run=false")
	if err != nil {
		t.Fatalf("sweep failed: %v", err)
	}
	if !resetting2344(dto, "w1") || !resetting2344(dto, "w2") {
		t.Fatalf("both workers must skip as resetting: %+v", dto.Skips)
	}
	if closes := w.closes(); closes != "" || len(dto.Fixes) != 0 {
		t.Fatalf("acted on a worker whose reset was recorded during the loop: fixes %+v closes %q", dto.Fixes, closes)
	}
}

// An empty successful read (a state file rewritten with no resets) must not
// drop a reset the snapshot already holds; a read that fails keeps it too.
func TestHealth2344EmptyOrFailedReReadKeepsTheSnapshotReset(t *testing.T) {
	for name, fresh := range map[string]string{
		"empty read":  `{"hostId":"own"}`,
		"failed read": `{not json`,
	} {
		t.Run(name, func(t *testing.T) {
			w := health2027Setup(t)
			w.setTerminals(t, twoTabs2344("w1", ""))
			w.stateFileWrite(t, `{"hostId":"own","resets":{"r1":{"resetId":"r1","workerId":"w1"}}}`)
			w.resetsByID = map[string]string{"r1": `{"data":{"contextReset":{"id":"r1","endedAt":null,"phase":"claimed"}}}`}
			w.midSweepReset = fresh // written by the pause handler, after the snapshot
			dto, _, err := w.run(t, false, "--dry-run=false")
			if err != nil {
				t.Fatalf("sweep failed: %v", err)
			}
			if !resetting2344(dto, "w1") {
				t.Fatalf("the snapshot's reset was dropped by the re-read: %+v", dto.Skips)
			}
			if closes := w.closes(); strings.TrimSpace(closes) != "" || len(dto.Fixes) != 0 {
				t.Fatalf("acted on a resetting worker: %+v %q", dto.Fixes, closes)
			}
		})
	}
}

// A fresh read that adds a reset and drops none is the old behaviour: still skips.
func TestHealth2344FreshReadStillAddsNewResets(t *testing.T) {
	w := health2027Setup(t)
	w.setTerminals(t, twoTabs2344("w1", ""))
	w.midSweepReset = `{"hostId":"own","resets":{"r2":{"resetId":"r2","workerId":"w1"}}}`
	w.resetsByID = map[string]string{"r2": `{"data":{"contextReset":{"id":"r2","endedAt":null,"phase":"claimed"}}}`}
	dto, _, err := w.run(t, false, "--dry-run=false")
	if err != nil {
		t.Fatal(err)
	}
	if !resetting2344(dto, "w1") {
		t.Fatalf("a reset recorded mid-sweep went unseen: %+v", dto.Skips)
	}
}
