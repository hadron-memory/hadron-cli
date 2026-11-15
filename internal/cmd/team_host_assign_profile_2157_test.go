package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// hadron-cli#2157 (S1b, hadron-server#4230): `team host assign` writes the launcher
// profile (--model, --effort, --account-profile). A flag that is not given must be
// OMITTED from the request (the server reads a present null on a replace as "clear"),
// a given value is checked with the server's own token rule before any request, and
// the setup read and the assign receipt carry the profile.

const profileAssignmentJSON2157 = `{"id":"a1","hostId":"orca1","workerId":"wkr1","worker":{"name":"Jonas"},
	"workdir":"jonas-3","family":"codex","model":"gpt-5","effort":"high","accountProfile":"team-a",
	"createdAt":"2026-11-15T10:00:00Z","updatedAt":"2026-11-15T10:00:00Z"}`

func profileResponses2157() map[string]string {
	r := responses2080()
	r["TeamHostAssign"] = `{"data":{"assignHostWorker":` + profileAssignmentJSON2157 + `}}`
	r["TeamHostAssignInstructed"] = r["TeamHostAssign"]
	r["TeamHostSetup"] = `{"data":{"hostSetup":{"source":"SERVER","assignments":[` + profileAssignmentJSON2157 + `,` + assignmentJSON2080 + `]}}}`
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
	in := inputOf2157(t, calls, "TeamHostAssign")
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
			in := inputOf2157(t, calls, "TeamHostAssign")
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
	in := inputOf2157(t, calls, "TeamHostAssignInstructed")
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
			noCall2080(t, calls, "TeamHostAssign", "TeamHostAssignInstructed")
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
	r["TeamHostAssign"] = gqlErrorJSON("BAD_LAUNCHER_PROFILE")
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
