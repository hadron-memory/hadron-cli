package hostpoller

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hadron-cli#3017 removed the reset-context host handler and with it the
// Resets map a state file used to carry. A state file written by an older
// binary still holds its "resets" key; loading it must succeed and simply
// not carry the records: the server re-delivers any operation it still
// holds, and this host now answers it FAILED (HOST_UNSUPPORTED) instead of
// resuming an in-flight reset, so a dropped record converges to a terminal
// outcome rather than vanishing mid-flight. The save round-trip pins the
// drop for good: a state written by this binary carries no "resets" key,
// and the live fields survive it.
func TestLoadStateDropsLegacyResets3017(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hadron-host-state.json")
	legacy := `{"created":{"iris":["wt-acme"]},` +
		`"keep":{"wt-keep":"a worker.end for its worker failed (delivery lost)"},"agents":{},` +
		`"resets":{"r1":{"resetId":"r1","operationId":"op1","workerId":"w1","epoch":1}}}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := LoadState(path)
	if err != nil {
		t.Fatalf("a legacy state file holding resets must still load: %v", err)
	}
	if got := s.Created["iris"]; len(got) != 1 || got[0] != "wt-acme" {
		t.Fatalf("the created worktrees must survive the drop: %+v", s.Created)
	}
	if got, ok := s.Keep["wt-keep"]; !ok || got != "a worker.end for its worker failed (delivery lost)" {
		t.Fatalf("the keep records must survive the drop: %+v", s.Keep)
	}
	if len(s.Agents) != 0 {
		t.Fatalf("no agents in the fixture: %+v", s.Agents)
	}
	if err := s.save(); err != nil {
		t.Fatalf("saving the loaded state: %v", err)
	}
	rewritten, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rewritten), `"resets"`) {
		t.Fatalf("a state saved by this binary carries no resets key: %s", rewritten)
	}
	if !strings.Contains(string(rewritten), `"wt-keep"`) {
		t.Fatalf("a state saved by this binary keeps the live fields: %s", rewritten)
	}
}
