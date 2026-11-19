package cmd

import (
	"strings"
	"testing"
)

// cli#2491 F1 (Rook's surviving mutant): a valid App-A worktree binding while
// --app names App B. The instructed flow resolves B ≠ A and falls back to the
// person with the claim kept. This pins the security property end-to-end — no
// request of the fallback carries the App-A worker session, and the mutation
// keeps its claim and targets App B. The named withAsPerson-removal mutant is
// observation-equivalent through today's CLI (the middleware skips queries and
// its own App-identity check declines the mutation here), so no CLI test can
// kill that line alone; this pin kills the equivalent middleware-side mutants
// — removing the identity check, or any future attach-in-fallback — verified
// by running those mutants against it. See docs/plans/2491-availability-followups.md.
func TestAvailabilityInstructionCrossAppFallbackCarriesNoSession(t *testing.T) {
	dir := teamGitDir(t)
	const appB = "capp999999999999999999999" // the binding's App is capp100…
	srv, calls := snapshotServer1614(t, func(c snapshotCall1614) string {
		if c.OperationName == "GetBoundSessionEnd" {
			return `{"data":{"session":{"id":"s-new","endedAt":null,"endReason":null}}}`
		}
		if c.OperationName == "TeamAppIdentity" {
			// B is B: the ref resolves to its own id, never the binding's App.
			return `{"data":{"app":{"id":"` + appB + `"}}}`
		}
		if c.OperationName != "WorkerAvailabilitySetInstructed" {
			t.Errorf("unexpected op %s", c.OperationName)
			return `{}`
		}
		return availabilityAnswer2450("worker")
	})
	writeTeamBindingIn(t, dir, srv.URL)
	f, out, stderr := testFactoryTTY(t, "")
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "availability", "set", "--worker", "Rufus", "--state", "unavailable", "--reason", "OTHER",
		"--app", appB, "--instruction", "task:one", "--server", srv.URL, "--json"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "session binding is for a different App") {
		t.Fatalf("the fallback note should name the cross-App reason, stderr: %q", stderr.String())
	}
	var mutation *snapshotCall1614
	for i, c := range *calls {
		// The pin: headerless, every request — lookups included. A middleware
		// that attaches in this fallback (identity check removed, or any
		// future change) fails here on the mutation, and any ctx-level session
		// fails here on the lookups.
		if c.Session != "" {
			t.Fatalf("request %s carried worker session %q through the person fallback", c.OperationName, c.Session)
		}
		if c.OperationName == "WorkerAvailabilitySetInstructed" {
			mutation = &(*calls)[i]
		}
	}
	if mutation == nil {
		t.Fatalf("no instructed mutation observed: %+v", *calls)
	}
	// The claim survives the fallback: checked by the server, not recorded.
	if mutation.Variables["instruction"] == nil {
		t.Fatal("the person fallback lost the instruction claim")
	}
	if input, ok := mutation.Variables["input"].(map[string]any); !ok || input["appRef"] != appB {
		t.Fatalf("the write must target the named App B: %v", mutation.Variables["input"])
	}
	if out.Len() == 0 {
		t.Fatal("no receipt for the fallback write")
	}
}

// The same topology through the PLAIN worker set (no --instruction, so no
// withAsPerson): setWorkerAvailability is an authored-write field, and the
// middleware's App-identity check is the one guard that keeps a valid App-A
// binding's session off a write targeting App B. This is the single-mutant pin
// for that check (removing it attaches s-new here); the instructed test above
// and this one together leave every guard of the fallback singly pinned except
// availability's own withAsPerson line, whose removal is provably redundant
// with the identity check (killed only as a double mutant — verified in the
// PR, see docs/plans/2491-availability-followups.md).
func TestAvailabilityPlainCrossAppWorkerSetNotAttributed(t *testing.T) {
	dir := teamGitDir(t)
	const appB = "capp999999999999999999999" // the binding's App is capp100…
	srv, calls := snapshotServer1614(t, func(c snapshotCall1614) string {
		if c.OperationName == "GetBoundSessionEnd" {
			return `{"data":{"session":{"id":"s-new","endedAt":null,"endReason":null}}}`
		}
		if c.OperationName == "TeamAppIdentity" {
			return `{"data":{"app":{"id":"` + appB + `"}}}`
		}
		if c.OperationName != "WorkerAvailabilitySet" {
			t.Errorf("unexpected op %s", c.OperationName)
			return `{}`
		}
		return availabilityAnswer2450("worker")
	})
	writeTeamBindingIn(t, dir, srv.URL)
	f, _, stderr := testFactoryTTY(t, "")
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "availability", "set", "--worker", "Rufus", "--state", "unavailable", "--reason", "OTHER",
		"--app", appB, "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, c := range *calls {
		if c.Session != "" {
			t.Fatalf("request %s carried worker session %q: the App-A binding must not ride a write targeting App B", c.OperationName, c.Session)
		}
		if c.OperationName == "WorkerAvailabilitySet" {
			seen = true
			if input, ok := c.Variables["input"].(map[string]any); !ok || input["appRef"] != appB {
				t.Fatalf("the write must target the named App B: %v", c.Variables["input"])
			}
		}
	}
	if !seen {
		t.Fatalf("no plain WorkerAvailabilitySet observed: %+v", *calls)
	}
	if strings.Contains(stderr.String(), "session") {
		t.Fatalf("unexpected session note on a cross-App plain write: %q", stderr.String())
	}
}
