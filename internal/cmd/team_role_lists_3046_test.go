package cmd

// cli#3046: polish from the BLIND review of #3044 (cli#3030, `team role-lists`). Pins for the instructed write
// paths, the `set --json` purpose spelling, case-insensitive purposes, and the drift of the local limits check.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

const roleListsClaim3046 = "team-chat-message:" + taskInstructionRef2422

// Item 1: the instructed write keeps the server's code and message (MapErrorKeepingCode), in the text error and
// in the --json envelope, prints no receipt, and is never retried as the plain write.
func TestRoleListsInstructedRefusalKeepsItsCode3046(t *testing.T) {
	for _, tc := range []struct {
		code string
		want int
		msg  string
	}{
		{"APP_MANAGER_REQUIRED", exitcode.Forbidden, "Only whoever manages the App sets its role lists"},
		{"SESSION_NOT_BOUND", exitcode.Usage, "Bind the worker session first"},
		{"APP_UNINSTALLED", exitcode.Conflict, "The App is uninstalled"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			r := roleListsResponses3030("DEFECT_LINK", "qa")
			r[roleListsInst3030] = `{"errors":[{"message":"` + tc.msg + `.","extensions":{"code":"` + tc.code + `"}}]}`
			run := runTeamTaskFactory(t, r, "--json", "team", "role-lists", "set", "defect-link", "qa", "--instruction", roleListsClaim3046)
			if run.Err == nil || exitCodeFor(run.Err) != tc.want {
				t.Fatalf("want exit %d, got %v (%d)", tc.want, run.Err, exitCodeFor(run.Err))
			}
			if run.Out.Len() != 0 || strings.Contains(run.Err.Error(), "graphql") {
				t.Fatalf("no receipt and no raw GraphQL: out=%q err=%v", run.Out.String(), run.Err)
			}
			oneCall2422(t, run.Calls, roleListsInst3030)
			for _, c := range *run.Calls {
				if c.Op == roleListsSet3030 {
					t.Fatal("an instructed refusal is never retried as the plain write")
				}
			}
			renderError(run.Factory, run.Err)
			var envelope struct {
				Error struct {
					ServerCode string `json:"serverCode"`
					Message    string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal([]byte(run.Out.String()), &envelope); err != nil {
				t.Fatalf("not an envelope: %v\n%s", err, run.Out.String())
			}
			if envelope.Error.ServerCode != tc.code || !strings.Contains(envelope.Error.Message, tc.msg) {
				t.Fatalf("the refusal keeps its code and message: %s", run.Out.String())
			}
		})
	}
}

// An instructed null result is the same unknown outcome as a plain one: exit 1, the check named, no receipt.
func TestRoleListsInstructedNullResultIsAnUnknownOutcome3046(t *testing.T) {
	r := roleListsResponses3030("DEFECT_LINK", "qa")
	r[roleListsInst3030] = `{"data":{"setAppRoleList":null}}`
	_, out, err := runTeamTask(t, r, "team", "role-lists", "set", "defect-link", "qa", "--instruction", roleListsClaim3046)
	if err == nil || exitCodeFor(err) != exitcode.Error {
		t.Fatalf("want exit 1, got %v (%d)", err, exitCodeFor(err))
	}
	if !strings.Contains(err.Error(), "team role-lists get") || strings.Contains(out, "✓") {
		t.Fatalf("the unknown outcome names the check and prints no receipt: err=%v out=%q", err, out)
	}
}

// --clear with --instruction sends the instructed document with an empty list ([] present, never null) and the claim.
func TestRoleListsClearWithInstructionSendsAnEmptyListAndTheClaim3046(t *testing.T) {
	r := roleListsResponses3030("REVIEW_WITHDRAW")
	calls, out, err := runTeamTask(t, r, "team", "role-lists", "set", "review-withdraw", "--clear", "--instruction", roleListsClaim3046, "--json")
	if err != nil {
		t.Fatal(err)
	}
	c := oneCall2422(t, calls, roleListsInst3030)
	roles, ok := c.Vars["roles"].([]any)
	if !ok || len(roles) != 0 || c.Vars["purpose"] != "REVIEW_WITHDRAW" {
		t.Fatalf("--clear sends roles [] for the purpose: %v", c.Vars)
	}
	if got, _ := c.Vars["instruction"].(map[string]any); got["ref"] != taskInstructionRef2422 {
		t.Fatalf("the claim rides the write: %v", c.Vars["instruction"])
	}
	var dto struct {
		Roles []string `json:"roles"`
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil || dto.Roles == nil || len(dto.Roles) != 0 {
		t.Fatalf(`--json carries "roles": [] (never null): %v %s`, err, out)
	}
}

// Item 2: `set --json` carries the purpose in the command's own spelling (what the operator typed and `get`'s
// keys are named for), not the server's enum.
func TestRoleListsSetJSONCarriesTheCommandSpelling3046(t *testing.T) {
	for _, tc := range []struct{ arg, enum string }{
		{"defect-link", "DEFECT_LINK"}, {"register-pick", "REGISTER_PICK"}, {"review-withdraw", "REVIEW_WITHDRAW"},
	} {
		t.Run(tc.arg, func(t *testing.T) {
			_, out, err := runTeamTask(t, roleListsResponses3030(tc.enum, "qa"), "team", "role-lists", "set", tc.arg, "qa", "--json")
			if err != nil {
				t.Fatal(err)
			}
			var dto struct {
				Purpose string `json:"purpose"`
			}
			if err := json.Unmarshal([]byte(out), &dto); err != nil || dto.Purpose != tc.arg {
				t.Fatalf("purpose must be the command spelling %q, got %q (%v)\n%s", tc.arg, dto.Purpose, err, out)
			}
		})
	}
}

// Item 3: a purpose is matched without regard to case and surrounding space, and what is SENT is always the
// server's enum from the command's own table, never what was typed: a local match is never looser than the server.
func TestRoleListsPurposeIsCaseInsensitiveAndSendsTheEnum3046(t *testing.T) {
	for _, typed := range []string{"Defect-Link", "DEFECT-LINK", "  defect-link  ", "dEfEcT-lInK"} {
		t.Run(typed, func(t *testing.T) {
			calls, _, err := runTeamTask(t, roleListsResponses3030("DEFECT_LINK", "qa"), "team", "role-lists", "set", typed, "qa")
			if err != nil {
				t.Fatalf("%q is the defect-link purpose: %v", typed, err)
			}
			if p, _ := roleListsSent3030(t, calls); p != "DEFECT_LINK" {
				t.Fatalf("the server's enum is sent, got %q", p)
			}
		})
	}
	// still unknown: not a spelling of any purpose, so refused before any request, with the accepted forms
	for _, typed := range []string{"defectlink", "defect_link", "DEFECT_LINK", "defect-link-x", "register", ""} {
		t.Run("unknown "+typed, func(t *testing.T) {
			calls, _, err := runTeamTask(t, roleListsResponses3030("DEFECT_LINK", "qa"), "team", "role-lists", "set", typed, "qa")
			if exitCodeFor(err) != exitcode.Usage || len(*calls) != 0 {
				t.Fatalf("%q is refused (exit 2) before any request: %v calls=%d", typed, err, len(*calls))
			}
		})
	}
}

// Item 4 (drift): the local limits check mirrors the server's. The server's own description of setAppRoleList in
// the committed schema snapshot states the limits; this pin fails when a schema refresh changes the numbers or
// the normalisation wording the local check (checkRoleList, roleListLimits) was written against, so a stale local
// refusal is caught at the refresh and not in the field.
func TestRoleListLimitsMatchTheServersDescription3046(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "schema", "schema.graphql"))
	if err != nil {
		t.Fatal(err)
	}
	i := strings.Index(string(raw), "setAppRoleList(")
	if i < 0 {
		t.Fatal("setAppRoleList is not in the schema snapshot")
	}
	// the description is the block of text just before the field
	head := string(raw)[:i]
	j := strings.LastIndex(head, `"""`)
	k := strings.LastIndex(head[:j], `"""`)
	desc := head[k:j]
	m := regexp.MustCompile(`at most (\d+) names of 1 to (\d+) characters with no ':' or whitespace`).FindStringSubmatch(desc)
	if m == nil {
		t.Fatalf("the server's description no longer states the limits in the form the local check was written against; re-read normalizeWakeRoles and update checkRoleList:\n%s", desc)
	}
	for _, want := range []string{"trimmed and lower-cased", "duplicates collapse"} {
		if !strings.Contains(desc, want) {
			t.Errorf("the server's description no longer says %q: re-check the local normalisation", want)
		}
	}
	// the numbers the local check enforces and states
	mine := regexp.MustCompile(`at most (\d+) names of (\d+) to (\d+) characters`).FindStringSubmatch(roleListsLimitsForTest3046(t))
	if mine == nil || mine[1] != m[1] || mine[3] != m[2] || mine[2] != "1" {
		t.Fatalf("the local limits (%v) differ from the server's description (%v)", mine, m)
	}
}

// roleListsLimitsForTest3046 reads the limits sentence the command states in its refusals through the refusal
// itself, so the pin is on what an operator sees.
func roleListsLimitsForTest3046(t *testing.T) string {
	t.Helper()
	_, _, err := runTeamTask(t, roleListsResponses3030("DEFECT_LINK", "qa"), "team", "role-lists", "set", "defect-link", "a b")
	if err == nil {
		t.Fatal("a name with whitespace is refused locally")
	}
	return err.Error()
}
