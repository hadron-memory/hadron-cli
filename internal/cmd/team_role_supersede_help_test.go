package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// #441 / hadron-server#1002: `role rm --transfer-register-to` collapses a
// four-step supersede into one App-scoped critical section. This file guards
// the facts a reader gets wrong without help — including one THIS repo got
// wrong in #447, before deleteTeamRole existed: that retiring a role frees
// its names. It does not. The register governs allocation; the roster governs
// identity, and a worker's name is permanent per App either way.
func TestRoleHelpDescribesTheOneCallSupersede(t *testing.T) {
	help := roleHelp(t, "team", "role", "--help")

	// The one-call form, with the flag that makes it one call.
	for _, want := range []string{"role rm <old> --transfer-register-to <new>"} {
		if !strings.Contains(help, want) {
			t.Errorf("group help must show the one-call supersede %q:\n%s", want, help)
		}
	}
	// The refusal that gates a bare retirement — the thing a caller hits.
	if !strings.Contains(help, "TEAM_ROLE_IN_USE") {
		t.Errorf("group help must name the minted-names refusal:\n%s", help)
	}
	// The correction. #447's help asserted the opposite ("Deleting the old
	// definition is what FREES its names"), which deleteTeamRole's contract
	// explicitly contradicts; a regression here is a silent return to advice
	// that would have people expecting a name back.
	if !strings.Contains(help, "free a name for re-casting") {
		t.Errorf("group help must say retiring does NOT free a name:\n%s", help)
	}
	// The four-step sequence is gone; nothing should still be teaching it.
	for _, gone := range []string{"hadron node rm hrn:node:", "DO THIS FIRST"} {
		if strings.Contains(help, gone) {
			t.Errorf("the hand-run sequence is obsolete but help still teaches %q:\n%s", gone, help)
		}
	}
}

// The verb's own help carries the semantics the group help only gestures at.
func TestRoleRmHelpCarriesTheRetirementContract(t *testing.T) {
	help := roleHelp(t, "team", "role", "rm", "--help")
	for _, want := range []string{
		"--transfer-register-to", // the supersede path
		"TEAM_ROLE_IN_USE",       // the bare-delete gate
		"SOFT",                   // recoverable, so a mistake is not fatal
		"EXEMPT",                 // transferred names bypass the successor's range
	} {
		if !strings.Contains(help, want) {
			t.Errorf("`role rm --help` must carry %q:\n%s", want, help)
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

const supersededRoleJSON = `{"data":{"deleteTeamRole":{
	"role":"frontend-engineer","nodesDeleted":2,"transferredNames":["Kai","Linn"],
	"transferredTo":{"role":"svelte-app-engineer","loc":"roles:svelte-app-engineer","nodeId":"n-svelte",
	  "description":null,
	  "register":[{"name":"Mia","taken":false,"heldBy":null},
	              {"name":"Kai","taken":true,"heldBy":{"id":"wkr9","name":"Kai"}},
	              {"name":"Linn","taken":false,"heldBy":null}],
	  "freeCount":2,"exhausted":false,"nameRange":"K-O","nameConvention":null,
	  "roleAgent":null,"hasNamePlaceholder":null}}}}`

// The supersede: one mutation, and the receipt shows the SUCCESSOR's resulting
// register — the thing the caller needs to see to know the move landed.
func TestTeamRoleRmTransfersRegister(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"TeamRoles":       teamRolesRmJSON,
		"TeamAppIdentity": teamAppIdentityJSON,
		"DeleteTeamRole":  supersededRoleJSON,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "role", "rm", "frontend-engineer",
		"--transfer-register-to", "svelte-app-engineer", "--yes",
		"--app", "acme.com:eng-team", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["DeleteTeamRole"], &vars)
	if vars["role"] != "frontend-engineer" || vars["transferTo"] != "svelte-app-engineer" {
		t.Errorf("delete vars: %v", vars)
	}
	// An unset optional must be OMITTED, never sent as null (CLAUDE.md wire
	// semantics — omitted is "preserve", null is "clear").
	if _, present := vars["teamAgentRef"]; present {
		t.Errorf("unset teamAgentRef must be omitted: %v", vars)
	}
	got := out.String()
	for _, want := range []string{
		"✓ retired role frontend-engineer → svelte-app-engineer",
		"2 nodes tombstoned",
		"moved: Kai, Linn",
		"svelte-app-engineer register: Mia, Kai✓, Linn (2 free)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("receipt missing %q:\n%s", want, got)
		}
	}
}

// The --json contract, including the successor's whole register.
func TestTeamRoleRmJSONShape(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"TeamRoles":       teamRolesRmJSON,
		"TeamAppIdentity": teamAppIdentityJSON,
		"DeleteTeamRole":  supersededRoleJSON,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "role", "rm", "frontend-engineer",
		"--transfer-register-to", "svelte-app-engineer", "--yes", "--json",
		"--app", "acme.com:eng-team", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var got struct {
		Role             string   `json:"role"`
		NodesDeleted     int      `json:"nodesDeleted"`
		TransferredNames []string `json:"transferredNames"`
		TransferredTo    *struct {
			Role     string `json:"role"`
			Register []struct {
				Name  string `json:"name"`
				Taken bool   `json:"taken"`
			} `json:"register"`
		} `json:"transferredTo"`
	}
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("unmarshal: %v — %s", err, out.String())
	}
	if got.Role != "frontend-engineer" || got.NodesDeleted != 2 {
		t.Errorf("scalars: %+v", got)
	}
	if len(got.TransferredNames) != 2 || got.TransferredNames[0] != "Kai" {
		t.Errorf("transferredNames: %v", got.TransferredNames)
	}
	if got.TransferredTo == nil || len(got.TransferredTo.Register) != 3 {
		t.Fatalf("the successor's whole register must ride along: %+v", got.TransferredTo)
	}
	if !got.TransferredTo.Register[1].Taken {
		t.Errorf("a transferred MINTED name stays taken: %+v", got.TransferredTo.Register)
	}
}

// A bare retirement of a free register: no transferTo on the wire at all.
func TestTeamRoleRmBareOmitsTransferTo(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"TeamRoles":       teamRolesRmJSON,
		"TeamAppIdentity": teamAppIdentityJSON,
		"DeleteTeamRole": `{"data":{"deleteTeamRole":{"role":"unused-role","nodesDeleted":1,
			"transferredNames":[],"transferredTo":null}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "role", "rm", "unused-role", "--yes",
		"--app", "acme.com:eng-team", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["DeleteTeamRole"], &vars)
	if _, present := vars["transferTo"]; present {
		t.Errorf("a bare retirement must OMIT transferTo, not send null: %v", vars)
	}
	if !strings.Contains(out.String(), "✓ retired role unused-role — 1 node tombstoned") {
		t.Errorf("bare receipt: %s", out.String())
	}
}

// A typo'd SUCCESSOR must not cost the source role. The server compensates a
// failed transfer by restoring it, but not reaching that state at all is
// better — so the successor is resolved before anything is deleted.
func TestTeamRoleRmUnknownSuccessorDeletesNothing(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{"TeamRoles": teamRolesRmJSON})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "role", "rm", "frontend-engineer",
		"--transfer-register-to", "svelte-ap-engineer", "--yes", // typo
		"--app", "acme.com:eng-team", "--server", gql.URL})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), `no role "svelte-ap-engineer"`) {
		t.Fatalf("a typo'd successor must refuse NotFound, got: %v", err)
	}
	if _, sent := captured["DeleteTeamRole"]; sent {
		t.Error("nothing may be deleted when the successor does not resolve")
	}
}

// Transferring a register to itself is a no-op the server would have to
// special-case; refuse it as the usage error it is.
func TestTeamRoleRmRefusesSelfTransfer(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{"TeamRoles": teamRolesRmJSON})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "role", "rm", "frontend-engineer",
		"--transfer-register-to", "Frontend-Engineer", "--yes", // same role, different case
		"--app", "acme.com:eng-team", "--server", gql.URL})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "to itself") {
		t.Fatalf("self-transfer must refuse: %v", err)
	}
	if _, sent := captured["DeleteTeamRole"]; sent {
		t.Error("a self-transfer must not reach the server")
	}
}

const teamRolesRmJSON = `{"data":{"teamRoles":{"total":3,"items":[
	{"role":"frontend-engineer","loc":"roles:frontend-engineer","nodeId":"n-fe","description":null,
	 "register":[{"name":"Kai","taken":true,"heldBy":{"id":"wkr9","name":"Kai"}},
	             {"name":"Linn","taken":false,"heldBy":null}],
	 "freeCount":1,"exhausted":false,"nameRange":"K-O","nameConvention":null,
	 "roleAgent":null,"hasNamePlaceholder":null},
	{"role":"svelte-app-engineer","loc":"roles:svelte-app-engineer","nodeId":"n-svelte","description":null,
	 "register":[{"name":"Mia","taken":false,"heldBy":null}],
	 "freeCount":1,"exhausted":false,"nameRange":"K-O","nameConvention":null,
	 "roleAgent":null,"hasNamePlaceholder":null},
	{"role":"unused-role","loc":"roles:unused-role","nodeId":"n-unused","description":null,
	 "register":[{"name":"Zed","taken":false,"heldBy":null}],
	 "freeCount":1,"exhausted":false,"nameRange":null,"nameConvention":null,
	 "roleAgent":null,"hasNamePlaceholder":null}]}}}`

// A bare retirement of a register holding MINTED names is refused before the
// prompt and before the wire: the server would refuse it TEAM_ROLE_IN_USE, and
// asking someone to confirm a destructive action already known to fail is
// worse than refusing with the remedy. Exit 5 either way (PR #451 review).
func TestTeamRoleRmBareWithMintedNamesRefusesLocally(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{"TeamRoles": teamRolesRmJSON})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "role", "rm", "frontend-engineer", "--yes",
		"--app", "acme.com:eng-team", "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("a bare retirement of a minted register must refuse")
	}
	if got := exitcode.FromError(err); got != exitcode.Conflict {
		t.Errorf("exit code = %d, want %d (Conflict): %v", got, exitcode.Conflict, err)
	}
	for _, want := range []string{"TEAM_ROLE_IN_USE", "Kai", "--transfer-register-to"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal must carry %q: %v", want, err)
		}
	}
	if _, sent := captured["DeleteTeamRole"]; sent {
		t.Error("a refusal known in advance must not reach the server")
	}
}
