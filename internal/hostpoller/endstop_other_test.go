//go:build !linux

package hostpoller

import (
	"context"
	"strings"
	"testing"
)

// StopAgents cannot identify a process it did not launch without Linux's
// /proc and pidfd (hadron-cli#2057 C2a): it must signal nothing and say so,
// never act on weaker evidence — worker.end's stop then reports that no
// process was touched. Pinned here because the path compiles only off Linux.
func TestStopAgentsOffLinuxErrorsWithoutSignalling2057(t *testing.T) {
	stops, err := (ExecHost{}).StopAgents(context.Background(), t.TempDir(), nil)
	if err == nil || !strings.Contains(err.Error(), "needs Linux") {
		t.Fatalf("want the Linux-only refusal, got stops=%v err=%v", stops, err)
	}
	if len(stops) != 0 {
		t.Fatalf("a refused stop must signal nothing, got %+v", stops)
	}
}
