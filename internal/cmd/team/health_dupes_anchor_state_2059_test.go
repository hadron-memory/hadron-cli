package team

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// cli#2059 (blind delta P2): a state file that cannot be read must give the
// sweep a usable state with no launch anchors, never nil, and one that is bound
// to no file. Without anchors no agent is supervised, so the sweep reports and
// stops nothing.
func TestLaunchAnchorStateFallsBackToNoAnchors2059(t *testing.T) {
	dir := t.TempDir()
	wt := filepath.Join(dir, "wt")
	valid := `{"agents":{"w1":{"worktree":"wt","pid":4242,"start":"99"}}}`

	// Control: a readable state with a launch record yields its anchor.
	good := filepath.Join(dir, "good.json")
	if err := os.WriteFile(good, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if got := launchAnchorState(good, &out).LaunchAnchorsAt(dir, wt); len(got) != 1 || got[0].PID != 4242 {
		t.Fatalf("a readable state lost its anchor: %+v", got)
	}

	// The same record, truncated so it cannot parse: no anchor, reported, and
	// the file is left as it was (the fallback is bound to no file).
	bad := filepath.Join(dir, "bad.json")
	truncated := valid[:len(valid)-6]
	if err := os.WriteFile(bad, []byte(truncated), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	st := launchAnchorState(bad, &out)
	if st == nil {
		t.Fatal("an unreadable state returned nil")
	}
	if got := st.LaunchAnchorsAt(dir, wt); len(got) != 0 {
		t.Fatalf("an unreadable state produced anchors: %+v", got)
	}
	if out.Len() == 0 {
		t.Fatal("an unreadable state was not reported")
	}
	if b, _ := os.ReadFile(bad); string(b) != truncated {
		t.Fatalf("the unreadable state file was rewritten: %q", b)
	}

	// A missing file is a fresh host: empty, and silent.
	out.Reset()
	if st = launchAnchorState(filepath.Join(dir, "absent.json"), &out); st == nil || out.Len() != 0 {
		t.Fatalf("a missing state must read as empty and silent: %v %q", st, out.String())
	}
}
