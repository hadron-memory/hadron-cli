package cmd

import (
	"strings"
	"testing"
)

func TestHostUpHostSetupFallbackNamesOnlyMissingRoot2115(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		fallback  bool
	}{
		{"missing root", `{"errors":[{"message":"Cannot query field \"hostSetup\" on type \"Query\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`, true},
		{"nested field", `{"errors":[{"message":"Cannot query field \"futureField\" on type \"HostSetup\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`, false},
		{"unknown argument on the root field", `{"errors":[{"message":"Unknown argument \"future\" on field \"Query.hostSetup\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`, false},
		{"mixed", `{"errors":[{"message":"Cannot query field \"hostSetup\" on type \"Query\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}},{"message":"forbidden","extensions":{"code":"FORBIDDEN"}}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := newHostUpFixture1389(t)
			fx.present("Jonas", 0)
			fx.serverState("APPLY", serverWorker1389("wkr-jonas", "Jonas", "RUNNING", "HOLDS_TASK", "", "hadron-cli/jonas"))
			fx.setupRaw = tc.raw
			dto, raw, err := fx.run("--apply")
			if tc.fallback {
				if err != nil {
					t.Fatalf("old server refused: %v %s", err, raw)
				}
				wantItems1389(t, "applied", dto.Applied, "Jonas worker.start NO_SESSION")
				// The switch was never read, so the report says so: null, not "HOST".
				if dto.SetupSource != nil {
					t.Fatalf("hostSetupSource = %q, want null when hostSetup is missing", *dto.SetupSource)
				}
				return
			}
			if err == nil {
				t.Fatalf("unrelated schema failure demoted to HOST: %s", raw)
			}
			// A refusal that is not the missing root field stops the run with the
			// server's refusal, never a quiet HOST fallback and never exit 0.
			if exitCodeFor(err) == 0 || strings.Contains(raw, `"hostSetupSource": "HOST"`) {
				t.Fatalf("exit %d, output %s: a non-root hostSetup failure must stop the run", exitCodeFor(err), raw)
			}
			if len(fx.mutations()) != 0 || len(fx.issued) != 0 {
				t.Fatalf("unread setup issued operations: %v", fx.mutations())
			}
		})
	}
}

func TestHostUpRejectsDuplicateSameHostAssignments2115(t *testing.T) {
	fx := newHostUpFixture1389(t)
	fx.present("Jonas", 0)
	fx.serverState("APPLY", serverWorker1389("wkr-jonas", "Jonas", "RUNNING", "HOLDS_TASK", "", "hadron-cli/jonas"))
	a := assignment2085("hadron-cli/jonas", "claude")
	fx.setup2085("SERVER", a, a)
	_, raw, err := fx.run("--apply")
	if err == nil {
		t.Fatalf("duplicate assignment silently won: %s", raw)
	}
	wantDuplicateAbort2147(t, err, "wkr-jonas")
	if len(fx.mutations()) != 0 || len(fx.issued) != 0 {
		t.Fatalf("duplicate setup issued operations: %v", fx.mutations())
	}
}

// wantDuplicateAbort2147: a duplicate same-host assignment aborts the WHOLE run,
// exit 1, with the worker named and "nothing planned": the source is malformed
// and a partial assignment map is not trusted (hadron-cli#2147).
func wantDuplicateAbort2147(t *testing.T, err error, worker string) {
	t.Helper()
	if got := exitCodeFor(err); got != 1 {
		t.Fatalf("exit %d, want 1 for a malformed setup source: %v", got, err)
	}
	for _, want := range []string{"duplicate assignments for worker " + worker, "the host (the host id given is not shown)", "nothing planned"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q lacks %q", err, want)
		}
	}
}

// Conflicting duplicates (different workdir or family) are the same malformed
// source as identical ones: the run aborts and never picks one.
func TestHostUpRejectsConflictingDuplicateAssignments2147(t *testing.T) {
	for name, rows := range map[string][2]string{
		"conflicting workdir": {assignment2085("hadron-cli/jonas", "claude"), assignment2085("hadron-cli/jonas-2", "claude")},
		"conflicting family":  {assignment2085("hadron-cli/jonas", "claude"), assignment2085("hadron-cli/jonas", "codex")},
	} {
		t.Run(name, func(t *testing.T) {
			fx := newHostUpFixture1389(t)
			fx.present("Jonas", 0)
			fx.serverState("APPLY", serverWorker1389("wkr-jonas", "Jonas", "RUNNING", "HOLDS_TASK", "", "hadron-cli/jonas"))
			fx.setup2085("SERVER", rows[0], rows[1])
			_, raw, err := fx.run("--apply")
			if err == nil {
				t.Fatalf("a conflicting duplicate silently won: %s", raw)
			}
			wantDuplicateAbort2147(t, err, "wkr-jonas")
			if len(fx.mutations()) != 0 || len(fx.issued) != 0 {
				t.Fatalf("issued operations: %v", fx.mutations())
			}
		})
	}
}

// A duplicate row for a worker that would otherwise be SKIPPED (here: not in the
// desired state at all) still aborts the whole run, and with it the row of the
// worker that would have been started.
func TestHostUpDuplicateRowForASkippedWorkerStillAbortsEverything2147(t *testing.T) {
	fx := newHostUpFixture1389(t)
	fx.present("Jonas", 0)
	fx.serverState("APPLY", serverWorker1389("wkr-jonas", "Jonas", "RUNNING", "HOLDS_TASK", "", "hadron-cli/jonas"))
	ghost := strings.NewReplacer("wkr-jonas", "wkr-ghost", "Jonas", "Ghost").Replace(assignment2085("hadron-cli/ghost", "claude"))
	fx.setup2085("SERVER", assignment2085("hadron-cli/jonas", "claude"), ghost, ghost)
	_, raw, err := fx.run("--apply")
	if err == nil {
		t.Fatalf("a duplicate for a skipped worker was ignored: %s", raw)
	}
	wantDuplicateAbort2147(t, err, "wkr-ghost")
	if len(fx.mutations()) != 0 || len(fx.issued) != 0 {
		t.Fatalf("Jonas was started although the source is malformed: %v %v", fx.mutations(), fx.issued)
	}
}

// An assignment with no worker id never matches a worker (the control): it must
// not change what happens to the real ones, and two of them are duplicates like any.
func TestHostUpEmptyWorkerIDRowsNeverMatchAndTwoAreDuplicates2147(t *testing.T) {
	empty := strings.Replace(assignment2085("hadron-cli/jonas", "claude"), `"workerId":"wkr-jonas"`, `"workerId":""`, 1)
	t.Run("one is inert", func(t *testing.T) {
		fx := newHostUpFixture1389(t)
		fx.present("Jonas", 0)
		fx.serverState("APPLY", serverWorker1389("wkr-jonas", "Jonas", "RUNNING", "HOLDS_TASK", "", "hadron-cli/jonas"))
		fx.setup2085("SERVER", assignment2085("hadron-cli/jonas", "claude"), empty)
		dto, raw, err := fx.run("--apply")
		if err != nil {
			t.Fatalf("an empty-id row changed the run: %v %s", err, raw)
		}
		wantItems1389(t, "applied", dto.Applied, "Jonas worker.start NO_SESSION")
	})
	t.Run("two are duplicates", func(t *testing.T) {
		fx := newHostUpFixture1389(t)
		fx.present("Jonas", 0)
		fx.serverState("APPLY", serverWorker1389("wkr-jonas", "Jonas", "RUNNING", "HOLDS_TASK", "", "hadron-cli/jonas"))
		fx.setup2085("SERVER", assignment2085("hadron-cli/jonas", "claude"), empty, empty)
		_, raw, err := fx.run("--apply")
		if err == nil {
			t.Fatalf("two empty-id rows were accepted: %s", raw)
		}
		wantDuplicateAbort2147(t, err, "")
		if len(fx.mutations()) != 0 || len(fx.issued) != 0 {
			t.Fatalf("issued operations: %v", fx.mutations())
		}
	})
}

func TestHostUpIgnoresOtherHostDuplicateAssignments2115(t *testing.T) {
	fx := newHostUpFixture1389(t)
	fx.present("Jonas", 0)
	fx.serverState("APPLY", serverWorker1389("wkr-jonas", "Jonas", "RUNNING", "HOLDS_TASK", "", "hadron-cli/jonas"))
	a := assignment2085("hadron-cli/jonas", "claude")
	other := strings.ReplaceAll(a, `"hostId":"orca1"`, `"hostId":"orca2"`)
	fx.setup2085("SERVER", a, other, other)
	dto, raw, err := fx.run("--apply")
	if err != nil {
		t.Fatalf("other-host rows changed this host: %v %s", err, raw)
	}
	wantItems1389(t, "applied", dto.Applied, "Jonas worker.start NO_SESSION")
}

// cli#3189 item 3: the duplicate-assignment refusal prints the server's worker id, so a control or
// bidi character in it is neutralised like every other server string in host up's output.
func TestHostUpDuplicateAssignmentRefusalNeutralisesTheWorkerId3189(t *testing.T) {
	const evil = "wkr-\x1b[2Jjonas\u202e"
	fx := newHostUpFixture1389(t)
	fx.present("Jonas", 0)
	fx.serverState("APPLY", serverWorker1389("wkr-jonas", "Jonas", "RUNNING", "HOLDS_TASK", "", "hadron-cli/jonas"))
	a := strings.ReplaceAll(assignment2085("hadron-cli/jonas", "claude"), "wkr-jonas", `wkr-\u001b[2Jjonas\u202e`)
	fx.setup2085("SERVER", a, a)
	_, _, err := fx.run("--apply")
	if err == nil {
		t.Fatal("a duplicate assignment must abort")
	}
	if strings.ContainsAny(err.Error(), "\x1b\u202e") || !strings.Contains(err.Error(), "duplicate assignments for worker wkr-") {
		t.Errorf("the worker id reaches the error unsanitised: %q (raw id %q)", err.Error(), evil)
	}
}
