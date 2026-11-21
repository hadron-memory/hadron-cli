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
// kill that line alone. Nor does this pin fail on the single identity-check
// removal — withAsPerson still keeps the attach off (the plain pin below is
// the one that fails on that mutant); what this pin kills is any change that
// makes the middleware actually attach in this fallback, today the double
// removal (identity check AND withAsPerson) — verified by running those
// mutants against the pins. See docs/plans/2491-availability-followups.md.
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
		// The pin: headerless, every request — lookups included. Any change
		// that makes the middleware attach in this fallback fails here on the
		// mutation (today that takes the double removal — identity check AND
		// withAsPerson; the identity check alone is caught by the plain pin
		// below), and any ctx-level session fails here on the lookups.
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
	// The claim survives the fallback, checked by the server, not recorded —
	// and it is the typed reference the flag spelled, not just a non-nil
	// variable (cli#2560 F6).
	// Field by field (cli#2707 F4), so a claim that gains a field does not
	// break this pin while one that loses or changes kind/ref still does.
	claim, ok := mutation.Variables["instruction"].(map[string]any)
	if !ok || claim["kind"] != "TASK" || claim["ref"] != "one" {
		t.Fatalf("the person fallback lost or reshaped the instruction claim: %v", mutation.Variables["instruction"])
	}
	if input, ok := mutation.Variables["input"].(map[string]any); !ok || input["appRef"] != appB {
		t.Fatalf("the write must target the named App B: %v", mutation.Variables["input"])
	}
	// The --json receipt is the written record, not merely non-empty output
	// (cli#2560 F6): the person fallback's write round-trips the DTO.
	// Decoded to raw keys, not into a struct of this test's own (cli#2707 F3):
	// the receipt's key names come from the CLI's output, so a rename there fails
	// here instead of being mirrored by a hand-written tag.
	receipt := receiptFields2707(t, []byte(out.String()))
	wantParty2707(t, "the fallback receipt's worker", receipt["worker"], "wkr2", "Rufus", "hrn:worker:acme.com:eng:rufus")
	wantString2707(t, "the fallback receipt", receipt, "state", "UNAVAILABLE")
	wantString2707(t, "the fallback receipt", receipt, "reason", "OUT_OF_TOKENS")
	wantString2707(t, "the fallback receipt", receipt, "setBy", "APP_MANAGER")
	if string(receipt["inEffect"]) != "true" {
		t.Fatalf("wrong inEffect in the receipt: %s", receipt["inEffect"])
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
	bindingChecked := false
	for _, c := range *calls {
		if c.Session != "" {
			t.Fatalf("request %s carried worker session %q: the App-A binding must not ride a write targeting App B", c.OperationName, c.Session)
		}
		if c.OperationName == "GetBoundSessionEnd" {
			bindingChecked = true
		}
		if c.OperationName == "WorkerAvailabilitySet" {
			seen = true
			if input, ok := c.Variables["input"].(map[string]any); !ok || input["appRef"] != appB {
				t.Fatalf("the write must target the named App B: %v", c.Variables["input"])
			}
		}
	}
	if !bindingChecked {
		t.Fatalf("the binding never loaded: no GetBoundSessionEnd read, so the no-session pin above is vacuous: %+v", *calls)
	}
	if !seen {
		t.Fatalf("no plain WorkerAvailabilitySet observed: %+v", *calls)
	}
	if strings.Contains(stderr.String(), "worker session") {
		t.Fatalf("unexpected session note on a cross-App plain write: %q", stderr.String())
	}
}

// cli#2560 (Jessie's LOCAL P3 on #2557): the cross-App pins above cover
// --worker only; this is the --family twin — a valid App-A binding while
// --app names App B, plain family set. The no-session property holds today
// for a different reason than the worker path: setModelFamilyAvailability
// is deliberately absent from the authored-write list (#868,
// write_attribution.go — a plain family write is the person's), so the
// middleware never even considers attaching. What this pin kills is any
// change that makes a session actually ride an App-B family write: adding
// the field to the list alone stays green here (the App-identity check then
// refuses the cross-App attach, as for the worker pin above), so today that
// takes the same double removal — the field AND the identity check; if
// family ever joins the authored set deliberately, this test becomes that
// guard's cross-App pin. The input targeting assert also fixes the routing:
// the write names App B and the codex family, never the binding's App A.
func TestAvailabilityPlainCrossAppFamilySetNotAttributed(t *testing.T) {
	dir := teamGitDir(t)
	const appB = "capp999999999999999999999" // the binding's App is capp100…
	srv, calls := snapshotServer1614(t, func(c snapshotCall1614) string {
		if c.OperationName == "GetBoundSessionEnd" {
			return `{"data":{"session":{"id":"s-new","endedAt":null,"endReason":null}}}`
		}
		if c.OperationName == "TeamAppIdentity" {
			return `{"data":{"app":{"id":"` + appB + `"}}}`
		}
		if c.OperationName != "ModelFamilyAvailabilitySet" {
			t.Errorf("unexpected op %s", c.OperationName)
			return `{}`
		}
		return availabilityAnswer2450("family")
	})
	writeTeamBindingIn(t, dir, srv.URL)
	f, _, stderr := testFactoryTTY(t, "")
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "availability", "set", "--family", "codex", "--state", "unavailable", "--reason", "OTHER",
		"--app", appB, "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	seen := false
	bindingChecked := false
	for _, c := range *calls {
		if c.Session != "" {
			t.Fatalf("request %s carried worker session %q: the App-A binding must not ride a family write targeting App B", c.OperationName, c.Session)
		}
		if c.OperationName == "GetBoundSessionEnd" {
			bindingChecked = true
		}
		if c.OperationName == "ModelFamilyAvailabilitySet" {
			seen = true
			if input, ok := c.Variables["input"].(map[string]any); !ok || input["appRef"] != appB || input["family"] != "codex" {
				t.Fatalf("the write must target the named App B family: %v", c.Variables["input"])
			}
		}
	}
	if !bindingChecked {
		t.Fatalf("the binding never loaded: no GetBoundSessionEnd read, so the no-session pin above is vacuous: %+v", *calls)
	}
	if !seen {
		t.Fatalf("no plain ModelFamilyAvailabilitySet observed: %+v", *calls)
	}
	// Deliberately NOT asserted (cli#2707 F5): a TeamAppIdentity request. A plain
	// family write is not in the authored-write list (see the header), so the
	// middleware never asks for the App identity on this path; the worker twin
	// above is the pin that goes through that check. What proves this pin reached
	// its branch is the loaded binding and the write that targets App B.
	// No note of any kind on a plain cross-App family write, rather than the
	// absence of one message defined elsewhere (cli#2707 F5).
	if stderr.String() != "" {
		t.Fatalf("unexpected note on a cross-App plain family write: %q", stderr.String())
	}
}
