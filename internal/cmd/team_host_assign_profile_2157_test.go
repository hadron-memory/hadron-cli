package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// hadron-cli#2157 (S1b, hadron-server#4230): `team host assign` writes the launcher
// profile (--model, --effort, --account-profile). A flag that is not given must be
// OMITTED from the request (the server reads a present null on a replace as "clear"),
// a given value is checked with the server's own token rule before any request, and
// the setup read and the assign receipt carry the profile.

// What a server that predates S1b answers to a document selecting effort and
// accountProfile: a validation refusal naming both fields.
const oldServerProfileRefusal2157 = `{"errors":[{"message":"Cannot query field \"effort\" on type \"HostWorkerAssignment\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}},{"message":"Cannot query field \"accountProfile\" on type \"HostWorkerAssignment\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`

const profileAssignmentJSON2157 = `{"id":"a1","hostId":"orca1","workerId":"wkr1","worker":{"name":"Jonas"},
	"workdir":"jonas-3","family":"codex","model":"gpt-5","effort":"high","accountProfile":"team-a",
	"createdAt":"2026-11-15T10:00:00Z","updatedAt":"2026-11-15T10:00:00Z"}`

func profileResponses2157() map[string]string {
	r := responses2080()
	// The S1b documents answer with the profile; the S1a ones (host up, import,
	// a plain assign) answer as before, without it.
	r["TeamHostAssignWithProfile"] = `{"data":{"assignHostWorker":` + profileAssignmentJSON2157 + `}}`
	r["TeamHostAssignWithProfileInstructed"] = r["TeamHostAssignWithProfile"]
	r["TeamHostSetupWithProfile"] = `{"data":{"hostSetup":{"source":"SERVER","assignments":[` + profileAssignmentJSON2157 + `,` + assignmentJSON2080 + `]}}}`
	return r
}

func assignProfileArgs2157(extra ...string) []string {
	return append([]string{"team", "host", "assign", "Jonas", "--host", "orca1", "--workdir", "jonas-3", "--family", "codex"}, extra...)
}

func inputOf2157(t *testing.T, calls *[]attnCall, op string) map[string]any {
	t.Helper()
	return findCall(t, calls, op).Vars["input"].(map[string]any)
}

func TestHostAssignSendsTheGivenProfileValues2157(t *testing.T) {
	calls, out, err := runTeamTask(t, profileResponses2157(), assignProfileArgs2157("--model", "gpt-5", "--effort", "high", "--account-profile", "team-a")...)
	if err != nil {
		t.Fatal(err)
	}
	noCall2080(t, calls, "TeamHostAssign", "TeamHostAssignInstructed")
	in := inputOf2157(t, calls, "TeamHostAssignWithProfile")
	for k, want := range map[string]any{"model": "gpt-5", "effort": "high", "accountProfile": "team-a", "family": "codex", "workdir": "jonas-3", "hostId": "orca1", "worker": "Jonas"} {
		if in[k] != want {
			t.Fatalf("input[%s] = %v, want %v (%v)", k, in[k], want, in)
		}
	}
	for _, w := range []string{"model gpt-5", "effort high", "account profile team-a"} {
		if !strings.Contains(out, w) {
			t.Fatalf("receipt lacks %q:\n%s", w, out)
		}
	}
}

// The wire contract that matters: a flag left out is ABSENT, never null.
func TestHostAssignOmitsEveryProfileFlagThatWasNotGiven2157(t *testing.T) {
	for name, tc := range map[string]struct {
		extra   []string
		present []string
	}{
		"none":               {nil, nil},
		"only effort":        {[]string{"--effort", "high"}, []string{"effort"}},
		"only account":       {[]string{"--account-profile", "team-a"}, []string{"accountProfile"}},
		"only model":         {[]string{"--model", "gpt-5"}, []string{"model"}},
		"replace and effort": {[]string{"--replace", "--effort", "high"}, []string{"replace", "effort"}},
		"effort and account": {[]string{"--effort", "high", "--account-profile", "team-a"}, []string{"effort", "accountProfile"}},
	} {
		t.Run(name, func(t *testing.T) {
			calls, _, err := runTeamTask(t, profileResponses2157(), assignProfileArgs2157(tc.extra...)...)
			if err != nil {
				t.Fatal(err)
			}
			op := "TeamHostAssign"
			for _, k := range tc.present {
				if k == "effort" || k == "accountProfile" {
					op = "TeamHostAssignWithProfile"
				}
			}
			in := inputOf2157(t, calls, op)
			want := map[string]bool{"appRef": true, "worker": true, "hostId": true, "workdir": true, "family": true}
			for _, k := range tc.present {
				want[k] = true
			}
			for k, v := range in {
				if !want[k] {
					t.Errorf("input carries %q = %v although its flag was not given (a null here would CLEAR a value)", k, v)
				}
			}
			for k := range want {
				if _, ok := in[k]; !ok {
					t.Errorf("input lacks %q", k)
				}
			}
		})
	}
}

// The instructed write is the same request with a claim: the same omissions.
func TestHostAssignInstructedOmitsProfileFlagsToo2157(t *testing.T) {
	calls, _, err := runTeamTask(t, profileResponses2157(), assignProfileArgs2157("--effort", "high", "--instruction", "task:hrn:task:acme.com:eng:7")...)
	if err != nil {
		t.Fatal(err)
	}
	in := inputOf2157(t, calls, "TeamHostAssignWithProfileInstructed")
	for _, k := range []string{"accountProfile", "model", "replace"} {
		if _, ok := in[k]; ok {
			t.Errorf("the instructed input carries %q although its flag was not given: %v", k, in)
		}
	}
	if in["effort"] != "high" {
		t.Fatalf("effort = %v", in["effort"])
	}
}

// A value the server would refuse BAD_LAUNCHER_PROFILE never leaves the machine:
// the rule is the server's own, and an explicit empty value is not "omitted".
func TestHostAssignRefusesABadProfileTokenBeforeAnyRequest2157(t *testing.T) {
	long := strings.Repeat("a", 129)
	for _, flag := range []string{"--model", "--effort", "--account-profile"} {
		for _, bad := range []string{"", "has space", "-lead", ".lead", "a/b", "../x", "x;rm", "$(x)", "a\nb", "a'b", long} {
			calls, _, err := runTeamTask(t, profileResponses2157(), assignProfileArgs2157(flag, bad)...)
			if err == nil || exitCodeFor(err) != exitcode.Usage {
				t.Errorf("%s %q: err = %v, want a usage refusal", flag, bad, err)
				continue
			}
			noCall2080(t, calls, "TeamHostAssign", "TeamHostAssignInstructed", "TeamHostAssignWithProfile", "TeamHostAssignWithProfileInstructed")
		}
	}
	// The boundary and the allowed alphabet pass.
	for _, ok := range []string{"a", "A9", "gpt-5.1_x", "opus-5.5", strings.Repeat("a", 128)} {
		for _, flag := range []string{"--model", "--effort", "--account-profile"} {
			if _, _, err := runTeamTask(t, profileResponses2157(), assignProfileArgs2157(flag, ok)...); err != nil {
				t.Errorf("%s %q must be accepted: %v", flag, ok, err)
			}
		}
	}
}

// The server's refusal keeps its code and maps to a usage error (exit 2).
func TestHostAssignMapsABadLauncherProfileRefusal2157(t *testing.T) {
	r := profileResponses2157()
	r["TeamHostAssignWithProfile"] = gqlErrorJSON("BAD_LAUNCHER_PROFILE")
	_, _, err := runTeamTask(t, r, assignProfileArgs2157("--effort", "high")...)
	if err == nil || exitCodeFor(err) != exitcode.Usage {
		t.Fatalf("err = %v, want exit 2", err)
	}
	if !strings.Contains(err.Error(), "BAD_LAUNCHER_PROFILE") {
		t.Fatalf("the server's code must be kept: %v", err)
	}
}

// The setup read carries the profile in JSON (a value or an explicit null) and in the table.
func TestHostSetupShowsTheLauncherProfile2157(t *testing.T) {
	_, out, err := runTeamTask(t, profileResponses2157(), "team", "host", "setup", "--host", "orca1", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Assignments []map[string]any `json:"assignments"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || len(got.Assignments) != 2 {
		t.Fatalf("%v\n%s", err, out)
	}
	with, without := got.Assignments[0], got.Assignments[1]
	if with["effort"] != "high" || with["accountProfile"] != "team-a" {
		t.Fatalf("set profile = %v", with)
	}
	for _, k := range []string{"effort", "accountProfile"} {
		if v, ok := without[k]; !ok || v != nil {
			t.Fatalf("an unset %s must be an explicit null, got %v (present %v)", k, v, ok)
		}
	}
	_, text, err := runTeamTask(t, profileResponses2157(), "team", "host", "setup", "--host", "orca1")
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"EFFORT", "ACCOUNT", "high", "team-a"} {
		if !strings.Contains(text, w) {
			t.Fatalf("the table lacks %q:\n%s", w, text)
		}
	}
}

// Jessie's note on #2157: the INSTRUCTED path must omit what was not given too, with
// NO profile flag at all (the request a null would have corrupted: model, effort,
// accountProfile and replace are all absent, not null).
func TestHostAssignInstructedWithNoOptionalFlagSendsNoOptionalKey2157(t *testing.T) {
	calls, _, err := runTeamTask(t, profileResponses2157(), assignProfileArgs2157("--instruction", "task:hrn:task:acme.com:eng:7")...)
	if err != nil {
		t.Fatal(err)
	}
	in := inputOf2157(t, calls, "TeamHostAssignInstructed")
	for k, v := range in {
		switch k {
		case "appRef", "worker", "hostId", "workdir", "family":
		default:
			t.Errorf("the instructed input carries %q = %v although its flag was not given", k, v)
		}
	}
	// The plain path, the same assertion, for the same four keys.
	calls, _, err = runTeamTask(t, profileResponses2157(), assignProfileArgs2157()...)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"model", "effort", "accountProfile", "replace"} {
		if v, ok := inputOf2157(t, calls, "TeamHostAssign")[k]; ok {
			t.Errorf("the plain input carries %q = %v although its flag was not given", k, v)
		}
	}
}

// The mock server answers with the profile whatever was asked, so pin the REQUEST:
// the S1b documents select both fields, and the S1a documents (host up, import,
// the plain assign) select NEITHER, so a server that predates S1b still validates
// them (the split ruled by the coordinator, #109796).
func TestOnlyTheProfileDocumentsSelectTheLauncherProfile2157(t *testing.T) {
	for name, doc := range map[string]string{
		"TeamHostSetupWithProfile":            gen.TeamHostSetupWithProfile_Operation,
		"TeamHostAssignWithProfile":           gen.TeamHostAssignWithProfile_Operation,
		"TeamHostAssignWithProfileInstructed": gen.TeamHostAssignWithProfileInstructed_Operation,
	} {
		for _, field := range []string{"effort", "accountProfile"} {
			if !strings.Contains(doc, field) {
				t.Errorf("%s no longer selects %q: the profile would silently read as unset", name, field)
			}
		}
	}
	for name, doc := range map[string]string{
		"TeamHostSetup":            gen.TeamHostSetup_Operation,
		"TeamHostAssign":           gen.TeamHostAssign_Operation,
		"TeamHostAssignInstructed": gen.TeamHostAssignInstructed_Operation,
	} {
		for _, field := range []string{"effort", "accountProfile"} {
			if strings.Contains(doc, field) {
				t.Errorf("%s selects %q: an older server would refuse it at validation, breaking host up, import and the plain assign", name, field)
			}
		}
	}
}

// ---- the old-server leg (the coordinator's split, #109796) ----------------
// A server that predates S1b refuses a document that selects effort or
// accountProfile at validation. host up (the sweep's core), host import and the
// plain assign must keep working on it; only a command that shows or sets the
// profile may notice, and it says so by name.

func oldServerResponses2157() map[string]string {
	r := responses2080() // models a server without the S1b documents
	r["TeamHostSetup"] = `{"data":{"hostSetup":{"source":"SERVER","assignments":[` + assignmentJSON2080 + `]}}}`
	return r
}

func opsNamed2157(calls *[]attnCall) []string {
	var out []string
	for _, c := range *calls {
		out = append(out, c.Op)
	}
	return out
}

func noProfileDocument2157(t *testing.T, calls *[]attnCall) {
	t.Helper()
	for _, op := range opsNamed2157(calls) {
		if strings.Contains(op, "WithProfile") {
			t.Errorf("%s was sent: that command must keep working on a server that predates S1b", op)
		}
	}
}

func TestOldServerPlainAssignStillWorks2157(t *testing.T) {
	calls, out, err := runTeamTask(t, oldServerResponses2157(), assignProfileArgs2157("--model", "gpt-5")...)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	noProfileDocument2157(t, calls)
	if len(callsFor(calls, "TeamHostAssign")) != 1 {
		t.Fatalf("the S1a document must carry a plain assign: %v", opsNamed2157(calls))
	}
}

func TestOldServerRefusesAProfileFlagByNameAndNeverFallsBackToAWriteWithoutIt2157(t *testing.T) {
	for _, extra := range [][]string{{"--effort", "high"}, {"--account-profile", "team-a"}, {"--effort", "high", "--instruction", "task:hrn:task:acme.com:eng:7"}} {
		calls, _, err := runTeamTask(t, oldServerResponses2157(), assignProfileArgs2157(extra...)...)
		if err == nil || exitCodeFor(err) != exitcode.Usage {
			t.Fatalf("%v: err = %v, want a usage refusal", extra, err)
		}
		for _, w := range []string{"S1b", "hadron-server#4230", "Nothing was written"} {
			if !strings.Contains(err.Error(), w) {
				t.Errorf("%v: the refusal must say %q: %v", extra, w, err)
			}
		}
		// The write that WOULD have dropped the profile is never made.
		noCall2080(t, calls, "TeamHostAssign", "TeamHostAssignInstructed")
	}
}

func TestOldServerSetupFallsBackAndSaysSoOnce2157(t *testing.T) {
	_, out, err := runTeamTask(t, oldServerResponses2157(), "team", "host", "setup", "--host", "orca1", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var got struct {
		Profile     string           `json:"profile"`
		Assignments []map[string]any `json:"assignments"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || got.Profile != "unavailable" || len(got.Assignments) != 1 {
		t.Fatalf("%v\n%s", err, out)
	}
	if v, ok := got.Assignments[0]["effort"]; !ok || v != nil {
		t.Fatalf("effort must be an explicit null (unknown): %v %v", v, ok)
	}
	_, text, err := runTeamTask(t, oldServerResponses2157(), "team", "host", "setup", "--host", "orca1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(text, "launcher profile: unavailable") != 1 || strings.Contains(text, "EFFORT") || strings.Contains(text, "ACCOUNT") {
		t.Fatalf("one note, no profile columns:\n%s", text)
	}
}

// Only a refusal that names the profile fields is "an older server": any other
// failure of the profile read is a failure, never quietly the S1a document.
func TestSetupDoesNotFallBackOnAnyOtherRefusal2157(t *testing.T) {
	r := oldServerResponses2157()
	r["TeamHostSetupWithProfile"] = gqlErrorJSON("FORBIDDEN")
	calls, _, err := runTeamTask(t, r, "team", "host", "setup", "--host", "orca1")
	if err == nil || exitCodeFor(err) != exitcode.Forbidden {
		t.Fatalf("err = %v, want exit 8", err)
	}
	noCall2080(t, calls, "TeamHostSetup")
}

func TestOldServerImportDryRunStillWorks2157(t *testing.T) {
	r := oldServerResponses2157()
	r["TeamHostImportRoster"] = importRoster2061
	r["TeamAppIdentity"] = teamAppIdentityJSON
	calls, out, err := runTeamTask(t, r, "team", "host", "import", "--from", hostYAML2061(t), "--host", "orca1", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	noProfileDocument2157(t, calls)
}

// host up is the core of the every-minute sweep: it reads the S1a setup document
// and must never send a document that selects the profile.
func TestHostUpNeverSendsTheProfileDocuments2157(t *testing.T) {
	fx := newHostUpFixture1389(t)
	fx.present("Jonas", 0)
	fx.serverState("APPLY", serverWorker1389("wkr-jonas", "Jonas", "RUNNING", "HOLDS_TASK", "", "hadron-cli/jonas"))
	fx.setup2085("SERVER", assignment2085("hadron-cli/jonas", "claude"))
	if _, raw, err := fx.run("--apply"); err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	fx.mu.Lock()
	defer fx.mu.Unlock()
	sawSetup := false
	for _, c := range fx.calls {
		if strings.Contains(c.Op, "WithProfile") {
			t.Errorf("host up sent %s: it must not break on a server that predates S1b", c.Op)
		}
		if c.Op == "TeamHostSetup" {
			sawSetup = true
		}
	}
	if !sawSetup {
		t.Fatalf("host up must read the S1a setup document: %v", fx.calls)
	}
}
