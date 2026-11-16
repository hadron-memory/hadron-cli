//go:build unix

package hostpoller

import (
	"testing"
	"time"
)

// waitFor polls cond for up to five seconds.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// patientKillWait gives ONE test a generous post-SIGKILL bound. The product's own
// default (2 s) is a reporting bound, not a promise that a killed group is SEEN gone
// that fast: on a host saturated with runnable processes the scan that confirms it
// was measured taking 1-39 s after a SIGKILL that had already been delivered (#2173),
// and the stop then reports stopped=false, the conservative answer. A test that asserts
// a SUCCESSFUL stop is asserting an eventually-true outcome, so it waits for it. The
// tests that pin the "did not exit" path set their own short bound and are untouched.
// postKillWait is a package variable, so this is safe only while no test in the package
// runs in parallel; none does (a search of the package for Parallel calls is empty,
// the property exechost_linux_2082_test.go also relies on).
func patientKillWait(t *testing.T) {
	t.Helper()
	old := postKillWait
	postKillWait = 120 * time.Second
	t.Cleanup(func() { postKillWait = old })
}
