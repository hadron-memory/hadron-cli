package hostpoller

import "testing"

// cli#3026 (BLIND question on #3043): Warnings are derived from the file at each load, not a setting.
// fixedChanges compares every exported non-reloadable field, so without a skip, deleting a stale `launch`
// line from a running host's file would log config-restart-needed naming "-" (the yaml tag).
func TestFixedChangesIgnoresLoadWarnings(t *testing.T) {
	a := &Config{App: "x", Warnings: []string{`worker "iris": launch is not used`}}
	b := &Config{App: "x"}
	if got := fixedChanges(a, b); len(got) != 0 {
		t.Fatalf("a changed warning set is not a restart-needed setting: %q", got)
	}
	// A real fixed setting still counts beside a warning difference.
	c := &Config{App: "y"}
	if got := fixedChanges(a, c); len(got) != 1 || got[0] != "app" {
		t.Fatalf("want only app, got %q", got)
	}
}
