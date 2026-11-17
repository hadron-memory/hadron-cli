package cmd

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1389 server adapter: `team host up` reads hostDesiredState (S3,
// hadron-server#4242) when no --desired-state-file is given. And cli#2069:
// the queue's stalled, COLLECTED and FAILED transitions.

// serverWorker1389 is one hostDesiredState row as the server shapes it.
func serverWorker1389(id, name, desired, reason, session string, worktrees ...string) map[string]any {
	row := map[string]any{"workerId": id, "name": name, "lifecycle": "LONG_LIVED", "reason": reason,
		"desired": nil, "wouldBe": nil, "wouldBeReason": nil, "openSessionId": nil, "family": "claude", "worktrees": worktrees}
	if desired != "" {
		row["desired"] = desired
	}
	if session != "" {
		row["openSessionId"] = session
	}
	return row
}

func (fx *hostUpFixture1389) serverState(sweep string, workers ...map[string]any) {
	if workers == nil {
		workers = []map[string]any{}
	}
	fx.desiredSrv = map[string]any{"hostId": "orca1", "sweepMode": sweep, "workers": workers}
}

// Without --desired-state-file the plan comes from the server: Iris runs,
// Jonas has his worktree and no session, so only his worker.start is
// planned. The read names the App and host, and no call carries the session.
func TestTeamHostUpReadsTheServersDesiredState1389(t *testing.T) {
	fx := newHostUpFixture1389(t)
	fx.present("Iris", 1)
	fx.present("Jonas", 0)
	fx.serverState("APPLY",
		serverWorker1389("wkr-iris", "Iris", "RUNNING", "LONG_LIVED", "s-iris", "hadron-cli/iris"),
		serverWorker1389("wkr-jonas", "Jonas", "RUNNING", "HOLDS_TASK", "", "hadron-cli/jonas"))
	dto, raw, err := fx.run()
	if err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	if dto.Source != "server" || dto.SweepMode == nil || *dto.SweepMode != "APPLY" {
		t.Fatalf("source = %q, sweepMode = %v", dto.Source, dto.SweepMode)
	}
	wantItems1389(t, "planned", dto.Planned, "Jonas worker.start NO_SESSION")
	wantItems1389(t, "skipped", dto.Skipped, "Iris - IN_SYNC")
	if len(fx.desiredVars) != 1 {
		t.Fatalf("hostDesiredState read %d times", len(fx.desiredVars))
	}
	var host string
	_ = json.Unmarshal(fx.desiredVars[0]["hostId"], &host)
	var app string
	_ = json.Unmarshal(fx.desiredVars[0]["appRef"], &app)
	if host != "orca1" || app == "" {
		t.Fatalf("hostDesiredState vars = appRef %q hostId %q", app, host)
	}
	for _, c := range fx.calls {
		if c.Session != "" {
			t.Fatalf("%s carried X-Hadron-Session %q", c.Op, c.Session)
		}
	}
	if m := fx.mutations(); len(m) != 0 {
		t.Fatalf("a dry run sent mutations: %v", m)
	}
	// The file source still says so.
	fx2 := newHostUpFixture1389(t)
	fx2.irisRunningJonasMissing()
	if dto, _, _ := fx2.run(); dto.Source != "file" {
		t.Fatalf("file source = %q", dto.Source)
	}
}

// Apply from the server source issues the start, as the person.
func TestTeamHostUpAppliesFromTheServersDesiredState1389(t *testing.T) {
	fx := newHostUpFixture1389(t)
	fx.present("Jonas", 0)
	fx.serverState("APPLY", serverWorker1389("wkr-jonas", "Jonas", "RUNNING", "LONG_LIVED", "", "hadron-cli/jonas"))
	dto, raw, err := fx.run("--apply")
	if err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	wantItems1389(t, "applied", dto.Applied, "Jonas worker.start NO_SESSION")
	if len(fx.issued) != 1 || fx.issued[0]["type"] != "worker.start" {
		t.Fatalf("issued = %v", fx.issued)
	}
}

// A null desired (LEAVE_ALONE, SWEEP_OFF) is never acted on, whatever
// wouldBe says; it is reported under its reason.
func TestTeamHostUpNeverActsOnANullDesired1389(t *testing.T) {
	for _, reason := range []string{"LEAVE_ALONE", "SWEEP_OFF"} {
		t.Run(reason, func(t *testing.T) {
			fx := newHostUpFixture1389(t)
			row := serverWorker1389("wkr-jonas", "Jonas", "", reason, "", "hadron-cli/jonas")
			row["wouldBe"], row["wouldBeReason"] = "RUNNING", "LONG_LIVED"
			fx.serverState("APPLY", row)
			dto, raw, err := fx.run("--apply")
			if err != nil {
				t.Fatalf("%v\n%s", err, raw)
			}
			wantItems1389(t, "planned", dto.Planned)
			wantItems1389(t, "skipped", dto.Skipped, "Jonas - "+reason)
			if m := fx.mutations(); len(m) != 0 {
				t.Fatalf("mutations: %v", m)
			}
		})
	}
	// A null desired under any other reason is still not acted on.
	fx := newHostUpFixture1389(t)
	fx.present("Jonas", 0)
	fx.serverState("APPLY", serverWorker1389("wkr-jonas", "Jonas", "", "LONG_LIVED", "", "hadron-cli/jonas"))
	dto, _, err := fx.run("--apply")
	if err != nil {
		t.Fatal(err)
	}
	wantItems1389(t, "skipped", dto.Skipped, "Jonas - UNKNOWN_DESIRED")
}

// Before S1a the server names no repo, so a missing worktree is reported,
// never created; a worker with zero, several or a malformed worktree is
// reported before the host is read for it.
func TestTeamHostUpServerSetupIsReported1389(t *testing.T) {
	fx := newHostUpFixture1389(t)
	fx.serverState("APPLY",
		serverWorker1389("wkr-iris", "Iris", "RUNNING", "LONG_LIVED", ""),
		serverWorker1389("wkr-jonas", "Jonas", "RUNNING", "LONG_LIVED", "", "hadron-cli/jonas"),
		serverWorker1389("wkr-kim", "Kim", "RUNNING", "LONG_LIVED", "", "hadron-cli/kim", "hadron-cli/kim-2"),
		serverWorker1389("wkr-lee", "Lee", "RUNNING", "LONG_LIVED", "", "../etc"))
	fx.registry = []string{"wkr-iris", "wkr-jonas", "wkr-kim", "wkr-lee"}
	dto, raw, err := fx.run("--apply")
	if err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	wantItems1389(t, "skipped", dto.Skipped, "Iris - SETUP_INVALID", "Jonas worktree.create NO_WORKTREE_SOURCE", "Kim - SETUP_INVALID", "Lee - SETUP_INVALID")
	if m := fx.mutations(); len(m) != 0 {
		t.Fatalf("mutations: %v", m)
	}
	log := orcaLog1389(t, fx)
	for _, name := range []string{"kim", "etc"} {
		if strings.Contains(log, name) {
			t.Fatalf("the host was read for an invalid setup (%s):\n%s", name, log)
		}
	}
}

// The server's refusal keeps its code and nothing is issued; an answer for
// another host is refused.
func TestTeamHostUpServerRefusals1389(t *testing.T) {
	fx := newHostUpFixture1389(t)
	fx.desiredErr = "FORBIDDEN"
	_, raw, err := fx.run("--apply")
	if exitCodeFor(err) != exitcode.Forbidden {
		t.Fatalf("exit %d (%v)\n%s", exitCodeFor(err), err, raw)
	}
	if m := fx.mutations(); len(m) != 0 {
		t.Fatalf("mutations: %v", m)
	}
	fx = newHostUpFixture1389(t)
	fx.serverState("APPLY")
	fx.desiredSrv["hostId"] = "orca2"
	_, raw, err = fx.run("--apply")
	// The server answered for a host nobody asked about: a failure (1), not
	// the caller's usage (2).
	if exitCodeFor(err) != exitcode.Error || !strings.Contains(err.Error(), "orca2") {
		t.Fatalf("an answer for another host: exit %d (%v)\n%s", exitCodeFor(err), err, raw)
	}
}

// cli#2069: an operation that never completes times out and is reported
// with its id, left running; one that goes through COLLECTED succeeds; an
// adopted one that ends FAILED is a failed step; no case issues twice.
func TestTeamHostUpQueueTransitions2069(t *testing.T) {
	start := func(fx *hostUpFixture1389) {
		fx.present("Jonas", 0)
		fx.writeDesired("APPLY", hostUpWorker1389{ID: "wkr-jonas", Name: "Jonas", Desired: "RUNNING", Reason: "LONG_LIVED"})
	}
	t.Run("stalled", func(t *testing.T) {
		fx := newHostUpFixture1389(t)
		start(fx)
		fx.async = true
		fx.stall = map[string]bool{"hop1": true}
		dto, raw, err := fx.run("--apply", "--timeout", "1s")
		if exitCodeFor(err) != exitcode.Error {
			t.Fatalf("exit %d (%v)\n%s", exitCodeFor(err), err, raw)
		}
		wantItems1389(t, "failed", dto.Failed, "Jonas worker.start ACTION_FAILED")
		if id := dto.Failed[0].OperationID; id == nil || *id != "hop1" {
			t.Fatalf("operationId = %v", id)
		}
		if fx.stored[0]["status"] != "ISSUED" || len(fx.issued) != 1 {
			t.Fatalf("the stalled operation was not left running once: %v", fx.stored)
		}
	})
	t.Run("collected then succeeded", func(t *testing.T) {
		fx := newHostUpFixture1389(t)
		start(fx)
		fx.async = true
		fx.transitions = map[string][]string{"hop1": {"COLLECTED", "SUCCEEDED"}}
		dto, raw, err := fx.run("--apply")
		if err != nil {
			t.Fatalf("%v\n%s", err, raw)
		}
		wantItems1389(t, "applied", dto.Applied, "Jonas worker.start NO_SESSION")
		if len(fx.issued) != 1 {
			t.Fatalf("issued = %v", fx.issued)
		}
	})
	t.Run("adopted then failed", func(t *testing.T) {
		fx := newHostUpFixture1389(t)
		start(fx)
		fx.stored = append(fx.stored, map[string]any{"id": "hop-live", "appId": "capp100000000000000000000", "hostId": "orca1", "workerId": "wkr-jonas",
			"type": "worker.start", "args": map[string]any{"workerId": "wkr-jonas", "worktree": "hadron-cli/jonas", "family": "claude"}, "status": "COLLECTED"})
		fx.transitions = map[string][]string{"hop-live": {"FAILED"}}
		dto, raw, err := fx.run("--apply")
		if exitCodeFor(err) != exitcode.Error {
			t.Fatalf("exit %d (%v)\n%s", exitCodeFor(err), err, raw)
		}
		wantItems1389(t, "failed", dto.Failed, "Jonas worker.start ACTION_FAILED")
		if id := dto.Failed[0].OperationID; id == nil || *id != "hop-live" {
			t.Fatalf("operationId = %v", id)
		}
		if m := fx.mutations(); len(m) != 0 {
			t.Fatalf("adoption sent mutations: %v", m)
		}
	})
	// Rook G1 on #2064: while polling, the polled operation is followed even
	// when the page lists a newer identical one first.
	t.Run("polled wins over a newer identical", func(t *testing.T) {
		fx := newHostUpFixture1389(t)
		start(fx)
		fx.async = true
		fx.stall = map[string]bool{"hop0-newer": true}
		fx.onIssue = func() {
			// A newer identical operation appears ahead of ours, still active.
			fx.stored = append([]map[string]any{{"id": "hop0-newer", "appId": "capp100000000000000000000", "hostId": "orca1", "workerId": "wkr-jonas",
				"type": "worker.start", "args": map[string]any{"workerId": "wkr-jonas", "worktree": "hadron-cli/jonas", "family": "claude"}, "status": "ISSUED"}}, fx.stored...)
		}
		dto, raw, err := fx.run("--apply")
		if err != nil {
			t.Fatalf("%v\n%s", err, raw)
		}
		wantItems1389(t, "applied", dto.Applied, "Jonas worker.start NO_SESSION")
		if id := dto.Applied[0].OperationID; id == nil || *id != "hop1" {
			t.Fatalf("followed %v, want the polled hop1", id)
		}
	})
}

func orcaLog1389(t *testing.T, fx *hostUpFixture1389) string {
	t.Helper()
	b, _ := os.ReadFile(fx.orcaLog)
	return string(b)
}

// The App's switch, as the server reports it: nothing is planned unless the
// sweep mode is APPLY (S3 defaults every App to OFF).
func TestTeamHostUpServerSweepModeGatesThePlan1389(t *testing.T) {
	for _, mode := range []string{"OFF", "OBSERVE"} {
		t.Run(mode, func(t *testing.T) {
			fx := newHostUpFixture1389(t)
			fx.present("Jonas", 0)
			fx.serverState(mode, serverWorker1389("wkr-jonas", "Jonas", "RUNNING", "LONG_LIVED", "", "hadron-cli/jonas"))
			dto, raw, err := fx.run("--apply")
			if err != nil {
				t.Fatalf("%v\n%s", err, raw)
			}
			wantItems1389(t, "planned", dto.Planned)
			wantItems1389(t, "skipped", dto.Skipped, "Jonas worker.start SWEEP_NOT_APPLY")
			if m := fx.mutations(); len(m) != 0 {
				t.Fatalf("mutations: %v", m)
			}
		})
	}
}

// cli#2077 blind P2: a successful answer without the desired state (a null
// or missing field) is a server failure, exit 1 with a JSON result, never a
// panic, and nothing is issued.
func TestTeamHostUpMalformedServerAnswer2077(t *testing.T) {
	for name, raw := range map[string]string{
		"null field":    `{"data":{"hostDesiredState":null}}`,
		"missing field": `{"data":{}}`,
	} {
		t.Run(name, func(t *testing.T) {
			fx := newHostUpFixture1389(t)
			fx.desiredRaw = raw
			dto, out, err := fx.run("--apply")
			if exitCodeFor(err) != exitcode.Error {
				t.Fatalf("exit %d (%v)\n%s", exitCodeFor(err), err, out)
			}
			if dto.Error == nil || dto.Source != "server" {
				t.Fatalf("no JSON result for the failure: %s", out)
			}
			if m := fx.mutations(); len(m) != 0 {
				t.Fatalf("mutations: %v", m)
			}
		})
	}
}

// cli#2077 blind P3: an empty or malformed model family is SETUP_INVALID,
// reported before the host is read, like a bad worktree.
func TestTeamHostUpServerFamilyIsValidated2077(t *testing.T) {
	fx := newHostUpFixture1389(t)
	empty := serverWorker1389("wkr-iris", "Iris", "RUNNING", "LONG_LIVED", "", "hadron-cli/iris")
	empty["family"] = ""
	bad := serverWorker1389("wkr-jonas", "Jonas", "RUNNING", "LONG_LIVED", "", "hadron-cli/jonas")
	bad["family"] = "../claude"
	fx.serverState("APPLY", empty, bad)
	dto, raw, err := fx.run("--apply")
	if err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	wantItems1389(t, "skipped", dto.Skipped, "Iris - SETUP_INVALID", "Jonas - SETUP_INVALID")
	if log := orcaLog1389(t, fx); strings.Contains(log, "terminal list") {
		t.Fatalf("the host was read for an invalid family:\n%s", log)
	}
	if m := fx.mutations(); len(m) != 0 {
		t.Fatalf("mutations: %v", m)
	}
}
