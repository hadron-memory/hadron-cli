package hostpoller

import (
	"os"
	"path/filepath"
	"testing"
)

// hadron-cli#3017 removed the reset-context host handler and with it the
// Resets map a state file used to carry. A state file written by an older
// binary still holds its "resets" key; loading it must succeed and simply
// not carry the records: the server re-delivers any operation it still
// holds, and this host now answers it FAILED (HOST_UNSUPPORTED) instead of
// resuming an in-flight reset, so a dropped record converges to a terminal
// outcome rather than vanishing mid-flight.
func TestLoadStateDropsLegacyResets3017(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hadron-host-state.json")
	legacy := `{"created":{"iris":["wt-acme"]},"keep":{},"agents":{},` +
		`"resets":{"r1":{"resetId":"r1","operationId":"op1","workerId":"w1","epoch":1}}}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := LoadState(path)
	if err != nil {
		t.Fatalf("a legacy state file holding resets must still load: %v", err)
	}
	if len(s.Created) != 1 || len(s.Created["iris"]) != 1 || len(s.Agents) != 0 || len(s.Keep) != 0 {
		t.Fatalf("the live fields must survive the drop: %+v", s)
	}
}
