package team

import (
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/hostup"
)

// cli#2156 F15: an item with no worker identifier has no lawful S10 line
// (cor:agt:070:09), so it is not counted as a worker either.
func TestSweepWorkerCountIgnoresEmptyIdentifiers2156(t *testing.T) {
	items := []hostup.Item{{WorkerID: "w1"}, {Worker: "w2"}, {WorkerID: "w1"}, {}, {WorkerID: "", Worker: ""}}
	if got := sweepWorkerCount(items); got != 2 {
		t.Fatalf("sweepWorkerCount = %d, want 2 (empty identifiers are not workers)", got)
	}
}
