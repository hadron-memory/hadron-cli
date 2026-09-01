package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// `role rm` was a supersede: `--transfer-register-to <new>` retired a role and
// handed its name register to a successor in one App-scoped critical section
// (#441 / hadron-server#1002), and a role holding MINTED names refused
// TEAM_ROLE_IN_USE without it. hadron-server#1050 removed the register, so the
// gate and the hand-off both went with it and the retirement is unconditional.
//
// ONE fact survives the removal, and it is the one this repo already got wrong
// once (#447 help asserted the opposite): retiring a role does NOT free its
// names. It never did — the register governed ALLOCATION and the roster
// governs IDENTITY. That the register could be deleted without amending a
// durable clause of cor:agt:020:02 is the proof.
func TestRoleHelpSaysRetiringNeverFreesAName(t *testing.T) {
	for _, help := range []string{
		roleHelp(t, "team", "role", "--help"),
		roleHelp(t, "team", "role", "rm", "--help"),
	} {
		if !strings.Contains(help, "free a name for re-casting") {
			t.Errorf("help must say retiring does NOT free a name:\n%s", help)
		}
	}
}

// The removed surface must not survive in help, in either direction: a reader
// told to pass a flag that no longer parses is worse off than one told
// nothing, and the four-step hand-run sequence this replaced is older still.
func TestRoleHelpNoLongerTeachesTheRegister(t *testing.T) {
	help := roleHelp(t, "team", "role", "--help") + roleHelp(t, "team", "role", "rm", "--help") +
		roleHelp(t, "team", "role", "create", "--help") + roleHelp(t, "team", "role", "update", "--help")
	for _, gone := range []string{
		"--transfer-register-to <successor>",
		"--name-range F-J",
		"--allow-out-of-range overrides",
		"hadron node rm hrn:node:", // the pre-#441 hand-run sequence
		"DO THIS FIRST",
	} {
		if strings.Contains(help, gone) {
			t.Errorf("help still teaches the removed register surface %q", gone)
		}
	}
	// And the group help must SAY it went, rather than just omitting it: every
	// task node, doc and habit in the team still says `role names add`, so the
	// reader arriving with the old model needs the correction, not silence.
	group := roleHelp(t, "team", "role", "--help")
	if !strings.Contains(group, "hadron-server#1050") {
		t.Errorf("group help must name the removal so the old model gets corrected:\n%s", group)
	}
}

// The removed FLAGS must not parse. A flag cobra silently accepts and ignores
// is the worst outcome — the caller believes the register moved.
func TestRoleRemovedFlagsAreRejected(t *testing.T) {
	for _, args := range [][]string{
		{"team", "role", "rm", "frontend-engineer", "--transfer-register-to", "svelte-app-engineer", "--yes"},
		{"team", "role", "create", "backend-engineer", "--names", "Fred,Gwen"},
		{"team", "role", "create", "backend-engineer", "--name-range", "F-J"},
		{"team", "role", "update", "backend-engineer", "--name-convention", "Nordic"},
		{"team", "role", "update", "backend-engineer", "--clear-name-range"},
		// `role names …` is NOT here: a removed SUBCOMMAND is cobra's generic
		// unknown-command path (exit 2 from the binary, same as any typo), not
		// this group's business. Only removed FLAGS need pinning, because a
		// flag is the thing that could come back as accepted-and-ignored.
	} {
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append(args, "--app", "acme.com:eng-team"))
		err := root.Execute()
		if err == nil {
			t.Errorf("%v: the removed surface must refuse, not silently ignore", args)
			continue
		}
		if code := exitCodeFor(err); code != exitcode.Usage {
			t.Errorf("%v: exit %d, want %d (Usage) — err: %v", args, code, exitcode.Usage, err)
		}
	}
}

func roleHelp(t *testing.T, args ...string) string {
	t.Helper()
	// Cobra writes help to its own writer, not the Factory's IOStreams.
	buf := &strings.Builder{}
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetOut(buf)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	return buf.String()
}

const retiredRoleJSON = `{"data":{"deleteTeamRole":{"role":"frontend-engineer","nodesDeleted":2}}}`

// A retirement is now ONE unconditional call. The role goes over the wire as
// the SERVER spells it (the scan resolves it case-insensitively first), and an
// unset optional is OMITTED rather than sent as null — omitted is "preserve"
// on this server, null is "clear" (CLAUDE.md wire semantics).
func TestTeamRoleRmIsUnconditional(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"TeamRoles":       teamRolesRmJSON,
		"TeamAppIdentity": teamAppIdentityJSON,
		"DeleteTeamRole":  retiredRoleJSON,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	// Lower-cased on purpose: the scan matches case-insensitively, and what
	// goes on the wire must be the server's spelling.
	root.SetArgs([]string{"team", "role", "rm", "FRONTEND-ENGINEER", "--yes",
		"--app", "acme.com:eng-team", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["DeleteTeamRole"], &vars)
	if vars["role"] != "frontend-engineer" {
		t.Errorf("the wire must carry the server's spelling: %v", vars)
	}
	if _, present := vars["teamAgentRef"]; present {
		t.Errorf("unset teamAgentRef must be omitted, not null: %v", vars)
	}
	// transferTo is not in the operation at all now; if it ever reappears in
	// the variables, something reintroduced the register hand-off.
	if _, present := vars["transferTo"]; present {
		t.Errorf("transferTo is gone from the mutation: %v", vars)
	}
	got := out.String()
	for _, want := range []string{
		"✓ retired role frontend-engineer",
		"2 nodes tombstoned (soft, recoverable)",
		"app: hrn:app:acme.com:eng-team — Eng Team (from --app)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("receipt missing %q:\n%s", want, got)
		}
	}
}

// The --json contract for `role rm`. `transferredNames`/`transferredTo` are
// GONE rather than emitted empty: keeping them would promise a hand-off the
// server cannot perform, which is a worse contract than a breaking change.
func TestTeamRoleRmJSONShape(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"TeamRoles":       teamRolesRmJSON,
		"TeamAppIdentity": teamAppIdentityJSON,
		"DeleteTeamRole":  retiredRoleJSON,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "role", "rm", "frontend-engineer", "--yes", "--json",
		"--app", "acme.com:eng-team", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("--json must parse: %v (%s)", err, out.String())
	}
	if dto["role"] != "frontend-engineer" || dto["nodesDeleted"] != float64(2) {
		t.Errorf("the documented shape must hold: %s", out.String())
	}
	for _, gone := range []string{"transferredNames", "transferredTo"} {
		if _, present := dto[gone]; present {
			t.Errorf("%q describes a hand-off that no longer exists: %s", gone, out.String())
		}
	}
}

// An unknown role is an honest NotFound BEFORE anything is deleted — the scan
// resolves it first, so a typo never reaches the mutation.
func TestTeamRoleRmUnknownRoleDeletesNothing(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{"TeamRoles": teamRolesRmJSON})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "role", "rm", "nope", "--yes",
		"--app", "acme.com:eng-team", "--server", gql.URL})
	err := root.Execute()
	if code := exitCodeFor(err); code != exitcode.NotFound {
		t.Errorf("exit %d, want %d (NotFound); err: %v", code, exitcode.NotFound, err)
	}
	if _, called := captured["DeleteTeamRole"]; called {
		t.Error("an unknown role must not reach the mutation")
	}
}

const teamRolesRmJSON = `{"data":{"teamRoles":{"total":2,"items":[
	{"role":"frontend-engineer","loc":"roles:frontend-engineer","nodeId":"n-fe",
	 "description":null,"roleAgent":null,"hasNamePlaceholder":null},
	{"role":"svelte-app-engineer","loc":"roles:svelte-app-engineer","nodeId":"n-svelte",
	 "description":null,"roleAgent":null,"hasNamePlaceholder":null}]}}}`

// The nameless-cast remedy must point at a listing that is actually COMPLETE.
// `worker list` hides retired staff by default, and a retired worker keeps its
// name forever — so the plain listing under-reports what is taken, and a
// reader picking an apparently-free name from it gets WORKER_NAME_TAKEN.
// A remedy naming an incomplete answer is the failure the message exists to
// prevent (PR #500 review).
func TestCastNamelessRemedyNamesTheCompleteListing(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "cast", "--role", "cli-engineer",
		"--app", "acme.com:eng-team"})
	err := root.Execute()
	if code := exitCodeFor(err); code != exitcode.Usage {
		t.Fatalf("a nameless cast must be Usage, got exit %d (err: %v)", code, err)
	}
	if !strings.Contains(err.Error(), "worker list --include-retired") {
		t.Errorf("the remedy must name the complete listing: %v", err)
	}
	// And it must say WHY, or the flag reads as noise and gets dropped.
	if !strings.Contains(err.Error(), "retired workers keep theirs") {
		t.Errorf("the remedy must say why --include-retired matters: %v", err)
	}
}

// A worker's name is PERMANENT for its App (cor:agt:020:02) — no rename, and
// `worker rm` only helps while the worker has never been used. So a name that
// reaches the server carrying stray whitespace is an unfixable mistake, and
// `--name " Iris "` is one keystroke away in any shell.
//
// The guard validated `strings.TrimSpace(name)` and then sent the RAW value,
// which is the worst of both: whitespace non-semantic for the check, semantic
// on the wire. It also produced a WORKER_NAME_TAKEN nobody could explain,
// since the roster shows the trimmed spelling (PR #500 review).
func TestCastNormalizesTheNameBeforeTheWire(t *testing.T) {
	for _, tc := range []struct{ name, args string }{
		{"cast", "CastWorker"},
		{"dry-run", "CastWorkerPreview"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gql, captured := captureGraphQL(t, map[string]string{
				"CastWorker": `{"data":{"castWorker":` + irisWorkerJSON + `}}`,
				"CastWorkerPreview": `{"data":{"castWorkerPreview":{"name":"Iris","role":"backend-engineer",
					"agentId":"agt1","agentName":"backend-engineer","prompt":null,"hasNamePlaceholder":true}}}`,
			})
			argv := []string{"team", "worker", "cast", "--app", "acme.com:eng-team",
				"--role", "backend-engineer", "--name", "  Iris  ", "--server", gql.URL}
			if tc.name == "dry-run" {
				argv = append(argv, "--dry-run")
			}
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(argv)
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			var vars map[string]any
			_ = json.Unmarshal(captured[tc.args], &vars)
			if vars["name"] != "Iris" {
				t.Errorf("the permanent name must reach the server trimmed, got %q", vars["name"])
			}
		})
	}

	// And a name that is ONLY whitespace is still the missing-name case, not a
	// cast of "" — the remedy has to fire.
	gql, captured := captureGraphQL(t, map[string]string{})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "cast", "--app", "acme.com:eng-team",
		"--role", "backend-engineer", "--name", "   ", "--server", gql.URL})
	err := root.Execute()
	if code := exitCodeFor(err); code != exitcode.Usage {
		t.Errorf("a whitespace-only name is no name: exit %d, want %d", code, exitcode.Usage)
	}
	if _, called := captured["CastWorker"]; called {
		t.Error("a whitespace-only name must not reach the server")
	}
}
