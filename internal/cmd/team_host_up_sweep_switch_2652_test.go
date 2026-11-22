package cmd

import (
	"os"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2652: --apply reads the App's server sweep mode and refuses unless it is
// APPLY, whatever the desired state says. The server does not enforce the
// switch when a host operation is issued, so a --desired-state-file naming
// "sweepMode":"APPLY" must not be a way around an App set to OFF or OBSERVE.

func TestHostUpApplyRefusedWhenAppSweepModeIsNotApply2652(t *testing.T) {
	for _, mode := range []string{"OFF", "OBSERVE"} {
		t.Run(mode, func(t *testing.T) {
			fx := newHostUpFixture1389(t)
			fx.irisRunningJonasMissing() // the file says APPLY
			fx.sweepMode = mode
			_, raw, err := fx.run("--apply")
			if exitCodeFor(err) != exitcode.Conflict {
				t.Fatalf("exit = %d (err %v), want %d\n%s", exitCodeFor(err), err, exitcode.Conflict, raw)
			}
			msg := err.Error()
			for _, want := range []string{"sweep mode is " + mode, "nothing issued", "hadron team app sweep-mode"} {
				if !strings.Contains(msg, want) {
					t.Fatalf("refusal %q lacks %q", msg, want)
				}
			}
			if strings.Contains(msg, fx.srv.URL) {
				t.Fatalf("refusal prints the server URL: %q", msg)
			}
			if len(fx.issued) != 0 || len(fx.mutations()) != 0 {
				t.Fatalf("an operation was issued: %v %v", fx.issued, fx.mutations())
			}
			if _, serr := os.Stat(fx.dir("Jonas")); !os.IsNotExist(serr) {
				t.Fatalf("the worktree was created: %v", serr)
			}
		})
	}
}

func TestHostUpApplyProceedsWhenAppSweepModeIsApply2652(t *testing.T) {
	fx := newHostUpFixture1389(t)
	fx.irisRunningJonasMissing()
	dto, raw, err := fx.run("--apply")
	if err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	wantItems1389(t, "applied", dto.Applied, "Jonas worktree.create WORKTREE_MISSING", "Jonas worker.start NO_SESSION")
	if fx.sweepReads != 1 {
		t.Fatalf("mode reads = %d, want 1", fx.sweepReads)
	}
}

func TestHostUpApplyFailsClosedWhenSweepModeUnreadable2652(t *testing.T) {
	fx := newHostUpFixture1389(t)
	fx.irisRunningJonasMissing()
	fx.sweepErr = "FORBIDDEN"
	_, raw, err := fx.run("--apply")
	if exitCodeFor(err) != exitcode.Forbidden {
		t.Fatalf("exit = %d (err %v), want %d (the server's refusal keeps its code)\n%s", exitCodeFor(err), err, exitcode.Forbidden, raw)
	}
	if !strings.Contains(err.Error(), "could not read the App's sweep mode") || !strings.Contains(err.Error(), "nothing issued") {
		t.Fatalf("refusal = %q", err)
	}
	if len(fx.issued) != 0 || len(fx.mutations()) != 0 {
		t.Fatalf("an operation was issued: %v", fx.issued)
	}
}

// A mode answer with no App in it is unreadable too: refused as not found,
// nothing issued.
func TestHostUpApplyFailsClosedWhenSweepModeAnswerHasNoApp2652(t *testing.T) {
	fx := newHostUpFixture1389(t)
	fx.irisRunningJonasMissing()
	fx.sweepNoApp = true
	_, raw, err := fx.run("--apply")
	if exitCodeFor(err) != exitcode.NotFound {
		t.Fatalf("exit = %d (err %v), want %d\n%s", exitCodeFor(err), err, exitcode.NotFound, raw)
	}
	if !strings.Contains(err.Error(), "could not read the App's sweep mode") || !strings.Contains(err.Error(), "nothing issued") {
		t.Fatalf("refusal = %q", err)
	}
	if len(fx.issued) != 0 || len(fx.mutations()) != 0 {
		t.Fatalf("an operation was issued: %v", fx.issued)
	}
}

// A dry run is unchanged: it reads no mode and never refuses on it.
func TestHostUpDryRunReadsNoSweepMode2652(t *testing.T) {
	fx := newHostUpFixture1389(t)
	fx.irisRunningJonasMissing()
	fx.sweepMode = "OFF"
	if _, raw, err := fx.run(); err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	if _, raw, err := fx.run("--dry-run"); err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	if fx.sweepReads != 0 {
		t.Fatalf("a dry run read the mode %d times", fx.sweepReads)
	}
}

// The help says so (substrings of the flattened help, not a paragraph).
func TestHostUpHelpSaysApplyChecksTheServerSwitch2652(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "host", "up", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	h := strings.Join(strings.Fields(out.String()), " ")
	for _, want := range []string{"--apply reads the App's sweep mode", "refuses unless it is APPLY", "whatever the file says"} {
		if !strings.Contains(h, want) {
			t.Fatalf("help lacks %q", want)
		}
	}
	for _, gone := range []string{"a --desired-state-file names its own sweepMode"} {
		if strings.Contains(h, gone) {
			t.Fatalf("help still says %q", gone)
		}
	}
}
