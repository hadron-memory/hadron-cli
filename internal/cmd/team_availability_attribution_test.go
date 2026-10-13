package cmd

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// #868 (Chip's reproduction, #10690): setModelFamilyAvailability is the App
// manager's write (cor:agt:060:09), and the server's actorManagesApp accepts
// a user actor only. Sent with a bound worktree's worker session, it refused
// the very manager it was meant for (exit 8 bound, exit 0 unbound). The
// family write is therefore the person's, never the session's — the same rule
// as `team stall-schedule set` (#866).
func TestTeamAvailabilitySetFamilyNotAttributedToWorker(t *testing.T) {
	calls, _, err := runTeamTask(t, taskResponses(),
		"team", "availability", "set", "--family", "codex", "--state", "unavailable", "--reason", "OTHER")
	if err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, c := range *calls {
		if c.Op != "ModelFamilyAvailabilitySet" {
			continue
		}
		seen = true
		if c.Session != "" {
			t.Fatalf("family availability set carried worker session %q; the server refuses a worker actor", c.Session)
		}
	}
	if !seen {
		t.Fatal("no ModelFamilyAvailabilitySet call observed")
	}
}

// Control: a WORKER record stays an authored write. The worker itself may
// mark itself unavailable (cor:agt:060:08), which needs the session to say who
// is acting — #868 narrows the family write only.
func TestTeamAvailabilitySetWorkerStillAttributed(t *testing.T) {
	calls, _, err := runTeamTask(t, taskResponses(),
		"team", "availability", "set", "--worker", "Rufus", "--state", "unavailable", "--reason", "OTHER")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range *calls {
		if c.Op == "WorkerAvailabilitySet" {
			if c.Session != "s-new" {
				t.Fatalf("worker availability set lost its attribution: session %q", c.Session)
			}
			return
		}
	}
	t.Fatal("no WorkerAvailabilitySet call observed")
}

// Control: a caller who does not manage the App is still refused — the
// person-attributed write narrows WHO acts, not who may.
func TestTeamAvailabilitySetFamilyNonManagerRefused(t *testing.T) {
	r := taskResponses()
	r["ModelFamilyAvailabilitySet"] = `{"errors":[{"message":"Only whoever manages the App sets a model family's availability.","extensions":{"code":"AVAILABILITY_NOT_PERMITTED"}}]}`
	_, out, err := runTeamTask(t, r,
		"team", "availability", "set", "--family", "codex", "--state", "unavailable", "--reason", "OTHER")
	if err == nil {
		t.Fatal("a non-manager's family set must be refused")
	}
	if got := exitCodeFor(err); got != exitcode.Forbidden {
		t.Fatalf("exit %d, want Forbidden (%d)", got, exitcode.Forbidden)
	}
	if !strings.Contains(err.Error(), "Only whoever manages the App") {
		t.Fatalf("the server's message should surface, got %v", err)
	}
	if strings.Contains(out, "✓") {
		t.Fatalf("no receipt may precede a refusal: %q", out)
	}
}
