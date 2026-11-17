package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2085 over hadron-server#4241 (S1a): under hostSetupSource SERVER,
// `team host up` takes each worker's workdir and family from hostSetup instead
// of the registration; a worker the platform does not assign is reported and
// never touched; HOST (and a server that predates hostSetup) behaves as before.
// A missing worktree is created only from exactly one Worker.repos entry;
// the S1a assignment itself names no repo.

func assignment2085(workdir, family string) string {
	const name = "Jonas"
	const workerID = "wkr-jonas"
	return `{"id":"a-` + workerID + `","hostId":"orca1","workerId":"` + workerID + `","worker":{"name":"` + name + `"},"workdir":"` + workdir + `","family":"` + family + `","model":null,"createdAt":"2026-11-15T10:00:00Z","updatedAt":"2026-11-15T10:00:00Z"}`
}

func (fx *hostUpFixture1389) setup2085(source string, assignments ...string) {
	fx.setupRaw = `{"data":{"hostSetup":{"source":"` + source + `","assignments":[` + strings.Join(assignments, ",") + `]}}}`
}

func TestTeamHostUpServerSetupTakesWorkdirAndFamilyFromTheAssignment2085(t *testing.T) {
	fx := newHostUpFixture1389(t)
	fx.present("jonas-work", 0)
	// The registration names TWO worktrees (SETUP_INVALID under HOST); under
	// SERVER the assignment alone decides.
	fx.serverState("APPLY", serverWorker1389("wkr-jonas", "Jonas", "RUNNING", "LONG_LIVED", "", "hadron-cli/a", "hadron-cli/b"))
	fx.setup2085("SERVER", assignment2085("hadron-cli/jonas-work", "codex"))
	dto, raw, err := fx.run("--apply")
	if err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	wantItems1389(t, "applied", dto.Applied, "Jonas worker.start NO_SESSION")
	if len(fx.issued) != 1 || fx.issued[0]["type"] != "worker.start" {
		t.Fatalf("issued = %v", fx.issued)
	}
	args := fx.issued[0]["args"].(map[string]any)
	if args["worktree"] != "hadron-cli/jonas-work" || args["family"] != "codex" {
		t.Fatalf("the start carries the assignment's workdir and family: %v", args)
	}
	if dto.SetupSource == nil || *dto.SetupSource != "SERVER" {
		t.Fatalf("hostSetupSource = %v", dto.SetupSource)
	}
	if len(fx.setupVars) != 1 {
		t.Fatalf("hostSetup read %d times", len(fx.setupVars))
	}
	var host, app string
	_ = json.Unmarshal(fx.setupVars[0]["hostId"], &host)
	_ = json.Unmarshal(fx.setupVars[0]["appRef"], &app)
	if host != "orca1" || app == "" {
		t.Fatalf("hostSetup vars: host %q app %q", host, app)
	}
	reads := 0
	for _, c := range fx.calls {
		if c.Op == "TeamHostSetup" {
			reads++
			if c.Session != "" {
				t.Fatalf("hostSetup carried X-Hadron-Session %q: a read carries no session", c.Session)
			}
		}
	}
	if reads != 1 {
		t.Fatalf("recorded %d hostSetup calls", reads)
	}
}

// A missing assigned worktree is created only from a repo the server names:
// the worker's role's repos, exactly one (the rule `worker onboard` follows).
func TestTeamHostUpServerSetupCreatesAMissingWorktreeFromTheOneRepo2085(t *testing.T) {
	fx := newHostUpFixture1389(t)
	fx.serverState("APPLY", serverWorker1389("wkr-jonas", "Jonas", "RUNNING", "LONG_LIVED", ""))
	fx.setup2085("SERVER", assignment2085("hadron-cli/jonas", "claude"))
	fx.repos = map[string][]string{"wkr-jonas": {"hadron-memory/hadron-cli"}}
	dto, raw, err := fx.run("--apply")
	if err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	wantItems1389(t, "applied", dto.Applied, "Jonas worktree.create WORKTREE_MISSING", "Jonas worker.start NO_SESSION")
	if len(fx.issued) != 2 || fx.issued[0]["type"] != "worktree.create" || fx.issued[1]["type"] != "worker.start" {
		t.Fatalf("issued = %v", fx.issued)
	}
	args := fx.issued[0]["args"].(map[string]any)
	if args["repo"] != "hadron-memory/hadron-cli" || args["baseBranch"] != "main" || args["branchName"] != "jonas/jonas" || args["label"] != "jonas" {
		t.Fatalf("worktree.create args = %v", args)
	}
	if len(fx.reposReads) != 1 || fx.reposReads[0] != "wkr-jonas" {
		t.Fatalf("repos read %v", fx.reposReads)
	}
	for _, c := range fx.calls {
		if c.Op == "HostUpWorkerRepos" && c.Session != "" {
			t.Fatalf("Worker.repos carried X-Hadron-Session %q", c.Session)
		}
	}
}

// Zero or several repos, a read the caller cannot make, or a repo that does not
// match the workdir: reported, never created, never guessed.
func TestTeamHostUpServerSetupNeverGuessesARepo2085(t *testing.T) {
	for name, tc := range map[string]struct {
		repos []string
		err   string
		want  string
	}{
		"none":       {nil, "", "Jonas worktree.create NO_WORKTREE_SOURCE"},
		"several":    {[]string{"hadron-memory/hadron-cli", "hadron-memory/hadron-server"}, "", "Jonas worktree.create NO_WORKTREE_SOURCE"},
		"unreadable": {nil, "FORBIDDEN", "Jonas worktree.create NO_WORKTREE_SOURCE"},
		"malformed":  {[]string{"../etc"}, "", "Jonas worktree.create NO_WORKTREE_SOURCE"},
		"mismatch":   {[]string{"hadron-memory/other"}, "", "Jonas worktree.create SETUP_INVALID"},
	} {
		t.Run(name, func(t *testing.T) {
			fx := newHostUpFixture1389(t)
			fx.serverState("APPLY", serverWorker1389("wkr-jonas", "Jonas", "RUNNING", "LONG_LIVED", ""))
			fx.setup2085("SERVER", assignment2085("hadron-cli/jonas", "claude"))
			fx.repos = map[string][]string{"wkr-jonas": tc.repos}
			fx.reposErr = tc.err
			dto, raw, err := fx.run("--apply")
			if err != nil {
				t.Fatalf("an unreadable repo does not fail the run: %v\n%s", err, raw)
			}
			wantItems1389(t, "planned", dto.Planned)
			wantItems1389(t, "skipped", dto.Skipped, tc.want)
			if tc.err != "" {
				// An unreadable list is said to be unreadable, not "none".
				if d := dto.Skipped[0].Detail; d == nil || !strings.Contains(*d, "could not be read") {
					t.Fatalf("detail = %v", d)
				}
			}
			if m := fx.mutations(); len(m) != 0 || len(fx.issued) != 0 {
				t.Fatalf("mutations %v issued %v", m, fx.issued)
			}
		})
	}
}

// The repos are read only for a worker whose worktree is missing, and never
// under HOST.
func TestTeamHostUpReadsReposOnlyForAMissingWorktreeUnderServer2085(t *testing.T) {
	fx := newHostUpFixture1389(t)
	fx.present("jonas", 0)
	fx.serverState("APPLY", serverWorker1389("wkr-jonas", "Jonas", "RUNNING", "LONG_LIVED", ""))
	fx.setup2085("SERVER", assignment2085("hadron-cli/jonas", "claude"))
	fx.repos = map[string][]string{"wkr-jonas": {"hadron-memory/hadron-cli"}}
	if _, raw, err := fx.run("--apply"); err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	if len(fx.reposReads) != 0 {
		t.Fatalf("the worktree exists, yet the repos were read: %v", fx.reposReads)
	}
	fx = newHostUpFixture1389(t)
	fx.serverState("APPLY", serverWorker1389("wkr-jonas", "Jonas", "RUNNING", "LONG_LIVED", "", "hadron-cli/jonas"))
	fx.setup2085("HOST")
	fx.repos = map[string][]string{"wkr-jonas": {"hadron-memory/hadron-cli"}}
	dto, raw, err := fx.run("--apply")
	if err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	wantItems1389(t, "skipped", dto.Skipped, "Jonas worktree.create NO_WORKTREE_SOURCE")
	if len(fx.reposReads) != 0 || len(fx.issued) != 0 {
		t.Fatalf("HOST never reads repos or creates: reads %v issued %v", fx.reposReads, fx.issued)
	}
}

func TestTeamHostUpServerSetupNeverTouchesAnUnassignedWorker2085(t *testing.T) {
	fx := newHostUpFixture1389(t)
	fx.present("jonas", 0)
	fx.present("iris", 0)
	fx.serverState("APPLY",
		serverWorker1389("wkr-iris", "Iris", "RUNNING", "LONG_LIVED", "", "hadron-cli/iris"), // registered, not assigned
		serverWorker1389("wkr-jonas", "Jonas", "RUNNING", "LONG_LIVED", "", "hadron-cli/jonas"))
	fx.setup2085("SERVER", assignment2085("hadron-cli/jonas", "claude"))
	dto, raw, err := fx.run("--apply")
	if err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	wantItems1389(t, "applied", dto.Applied, "Jonas worker.start NO_SESSION")
	wantItems1389(t, "skipped", dto.Skipped, "Iris - NOT_ASSIGNED")
	for _, in := range fx.issued {
		if in["worker"] == "wkr-iris" {
			t.Fatalf("an operation was issued for an unassigned worker: %v", in)
		}
	}
	// Even a worker the platform wants running, with a session and nothing else
	// to do, is only reported.
	fx2 := newHostUpFixture1389(t)
	fx2.serverState("APPLY", serverWorker1389("wkr-iris", "Iris", "RUNNING", "LONG_LIVED", "s-iris", "hadron-cli/iris"))
	fx2.setup2085("SERVER")
	dto, _, err = fx2.run("--apply")
	if err != nil {
		t.Fatal(err)
	}
	wantItems1389(t, "skipped", dto.Skipped, "Iris - NOT_ASSIGNED")
	if m := fx2.mutations(); len(m) != 0 {
		t.Fatalf("mutations: %v", m)
	}
}

// Assigned to this host but not in its desired state (its registration does
// not list it): reported, nothing to act on.
func TestTeamHostUpServerSetupReportsAnAssignedWorkerTheHostDoesNotRegister2085(t *testing.T) {
	fx := newHostUpFixture1389(t)
	fx.serverState("APPLY")
	fx.setup2085("SERVER", assignment2085("hadron-cli/jonas", "claude"))
	dto, raw, err := fx.run("--apply")
	if err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	wantItems1389(t, "skipped", dto.Skipped, "Jonas - NOT_REGISTERED")
	if m := fx.mutations(); len(m) != 0 || len(fx.issued) != 0 {
		t.Fatalf("mutations %v issued %v", m, fx.issued)
	}
}

// A malformed assignment never reaches the host or an argv.
func TestTeamHostUpServerSetupRefusesAMalformedAssignment2085(t *testing.T) {
	fx := newHostUpFixture1389(t)
	fx.serverState("APPLY", serverWorker1389("wkr-jonas", "Jonas", "RUNNING", "LONG_LIVED", ""))
	fx.setup2085("SERVER", assignment2085("../etc", "claude"))
	dto, raw, err := fx.run("--apply")
	if err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	wantItems1389(t, "skipped", dto.Skipped, "Jonas - SETUP_INVALID")
	if m := fx.mutations(); len(m) != 0 {
		t.Fatalf("mutations: %v", m)
	}
}

// HOST: today's behaviour, byte for byte (the registration names the worktree).
func TestTeamHostUpHostSetupSourceHostIsUnchanged2085(t *testing.T) {
	fx := newHostUpFixture1389(t)
	fx.present("Jonas", 0)
	fx.serverState("APPLY", serverWorker1389("wkr-jonas", "Jonas", "RUNNING", "HOLDS_TASK", "", "hadron-cli/jonas"))
	fx.setup2085("HOST", assignment2085("hadron-cli/other", "codex")) // ignored under HOST
	dto, raw, err := fx.run("--apply")
	if err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	wantItems1389(t, "applied", dto.Applied, "Jonas worker.start NO_SESSION")
	args := fx.issued[0]["args"].(map[string]any)
	if args["worktree"] != "hadron-cli/jonas" || args["family"] != "claude" {
		t.Fatalf("HOST keeps the registration's worktree and family: %v", args)
	}
	if dto.SetupSource == nil || *dto.SetupSource != "HOST" {
		t.Fatalf("hostSetupSource = %v", dto.SetupSource)
	}
}

// A server that predates hostSetup (schema skew) is HOST behaviour and the
// source is reported as unknown.
func TestTeamHostUpOldServerWithoutHostSetupIsUnchanged2085(t *testing.T) {
	fx := newHostUpFixture1389(t)
	fx.present("Jonas", 0)
	fx.serverState("APPLY", serverWorker1389("wkr-jonas", "Jonas", "RUNNING", "HOLDS_TASK", "", "hadron-cli/jonas"))
	dto, raw, err := fx.run("--apply")
	if err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	wantItems1389(t, "applied", dto.Applied, "Jonas worker.start NO_SESSION")
	if dto.SetupSource != nil {
		t.Fatalf("hostSetupSource = %v, want null", *dto.SetupSource)
	}
	if len(fx.setupVars) != 1 {
		t.Fatalf("hostSetup is asked once, got %d", len(fx.setupVars))
	}
}

// Any other failure to read the switch stops the run: the rules to plan under
// are not known, so nothing is planned or issued.
func TestTeamHostUpUnreadableHostSetupPlansNothing2085(t *testing.T) {
	for name, tc := range map[string]struct {
		raw  string
		exit int
	}{
		"forbidden": {`{"data":null,"errors":[{"message":"refused","extensions":{"code":"FORBIDDEN"}}]}`, exitcode.Forbidden},
		"null":      {`{"data":{"hostSetup":null}}`, exitcode.Error},
		"missing":   {`{"data":{}}`, exitcode.Error},
	} {
		t.Run(name, func(t *testing.T) {
			fx := newHostUpFixture1389(t)
			fx.present("Jonas", 0)
			fx.serverState("APPLY", serverWorker1389("wkr-jonas", "Jonas", "RUNNING", "HOLDS_TASK", "", "hadron-cli/jonas"))
			fx.setupRaw = tc.raw
			_, raw, err := fx.run("--apply")
			if exitCodeFor(err) != tc.exit {
				t.Fatalf("exit %d (%v), want %d\n%s", exitCodeFor(err), err, tc.exit, raw)
			}
			if m := fx.mutations(); len(m) != 0 || len(fx.issued) != 0 {
				t.Fatalf("mutations %v issued %v", m, fx.issued)
			}
		})
	}
}

// BLIND/LOCAL P2 (Julius, #2109): only an explicit HOST or SERVER names the
// rules to plan under. An empty, missing or unknown source is not "HOST": the
// run stops, and nothing is planned or issued.
func TestTeamHostUpAnUnknownHostSetupSourceStopsTheRun2085(t *testing.T) {
	for name, source := range map[string]string{"empty": `""`, "null": `null`, "future": `"FUTURE"`, "lowercase": `"server"`} {
		t.Run(name, func(t *testing.T) {
			fx := newHostUpFixture1389(t)
			fx.present("Jonas", 0)
			fx.serverState("APPLY", serverWorker1389("wkr-jonas", "Jonas", "RUNNING", "HOLDS_TASK", "", "hadron-cli/jonas"))
			fx.setupRaw = `{"data":{"hostSetup":{"source":` + source + `,"assignments":[]}}}`
			_, raw, err := fx.run("--apply")
			if err == nil || exitCodeFor(err) != exitcode.Error {
				t.Fatalf("want exit %d, got %v (exit %d)\n%s", exitcode.Error, err, exitCodeFor(err), raw)
			}
			if m := fx.mutations(); len(m) != 0 || len(fx.issued) != 0 {
				t.Fatalf("nothing is planned or issued for a source nobody can name: mutations %v issued %v", m, fx.issued)
			}
		})
	}
}

// Rook's P3s (#2109): the family and the derived branch are tokens too.
func TestTeamHostUpAssignedFamilyAndBranchAreTokens2085(t *testing.T) {
	fx := newHostUpFixture1389(t)
	fx.present("jonas", 0)
	fx.serverState("APPLY", serverWorker1389("wkr-jonas", "Jonas", "RUNNING", "LONG_LIVED", ""))
	fx.setup2085("SERVER", assignment2085("hadron-cli/jonas", "claude --dangerously"))
	dto, raw, err := fx.run("--apply")
	if err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	wantItems1389(t, "skipped", dto.Skipped, "Jonas - SETUP_INVALID")
	if m := fx.mutations(); len(m) != 0 || len(fx.issued) != 0 {
		t.Fatalf("mutations %v issued %v", m, fx.issued)
	}
	// A worker name that cannot be a branch token gets no created worktree.
	fx = newHostUpFixture1389(t)
	fx.serverState("APPLY", serverWorker1389("wkr-jonas", "Jo nas", "RUNNING", "LONG_LIVED", ""))
	fx.setup2085("SERVER", `{"id":"a1","hostId":"orca1","workerId":"wkr-jonas","worker":{"name":"Jo nas"},"workdir":"hadron-cli/jonas","family":"claude","model":null,"createdAt":"2026-11-15T10:00:00Z","updatedAt":"2026-11-15T10:00:00Z"}`)
	fx.repos = map[string][]string{"wkr-jonas": {"hadron-memory/hadron-cli"}}
	dto, raw, err = fx.run("--apply")
	if err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	wantItems1389(t, "skipped", dto.Skipped, "Jo nas worktree.create NO_WORKTREE_SOURCE")
	if len(fx.issued) != 0 || len(fx.reposReads) != 0 {
		t.Fatalf("issued %v, repos read %v", fx.issued, fx.reposReads)
	}
}
