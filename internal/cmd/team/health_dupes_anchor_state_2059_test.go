package team

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// cli#2059 (blind delta P2): a state file that cannot be read must give the
// sweep a usable state with no launch anchors, never nil. Without anchors no
// agent is supervised, so the sweep reports and stops nothing.
func TestLaunchAnchorStateFallsBackToNoAnchors2059(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "state.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	st := launchAnchorState(bad, &out)
	if st == nil {
		t.Fatal("an unreadable state returned nil")
	}
	if got := st.LaunchAnchorsAt(dir, dir); len(got) != 0 {
		t.Fatalf("an unreadable state produced anchors: %+v", got)
	}
	if out.Len() == 0 {
		t.Fatal("an unreadable state was not reported")
	}

	// A missing file is a fresh host: empty, and silent.
	out.Reset()
	st = launchAnchorState(filepath.Join(dir, "absent.json"), &out)
	if st == nil || out.Len() != 0 {
		t.Fatalf("a missing state must read as empty and silent: %v %q", st, out.String())
	}
}
