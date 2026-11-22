package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2960: pins for the instructed path and the null reads of `team saturation-wake-roles`
// (cli#2894's follow-ups), the three test gaps, and the help wording as the FIT settles it.
// Each pin names the mutant it kills.

const instructedArg2960 = "team-chat-message:" + taskInstructionRef2422

// The instructed write keeps the server's code in the --json envelope (api.MapErrorKeepingCode, the
// stall-schedule precedent); the plain write keeps its established envelope (no serverCode).
// Kills: the instructed write mapped with api.MapError.
func TestSaturationWakeRolesInstructedRefusalsKeepTheCode2960(t *testing.T) {
	for _, tc := range []struct {
		code string
		want int
		msg  string
	}{
		{"APP_MANAGER_REQUIRED", exitcode.Forbidden, "Only whoever manages the App sets its wake roles"},
		{"SESSION_NOT_BOUND", exitcode.Usage, "Bind the worker session first"},
		{"APP_UNINSTALLED", exitcode.Conflict, "The App is uninstalled"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			r := wakeRolesResponses2894()
			r[wakeRolesInst2894] = `{"errors":[{"message":"` + tc.msg + `.","extensions":{"code":"` + tc.code + `"}}]}`
			run := runTeamTaskFactory(t, r, "--json", "team", "saturation-wake-roles", "set", "qa-engineer", "--instruction", instructedArg2960)
			if run.Err == nil || exitCodeFor(run.Err) != tc.want {
				t.Fatalf("want exit %d, got %v (%d)", tc.want, run.Err, exitCodeFor(run.Err))
			}
			if run.Out.Len() != 0 || strings.Contains(run.Err.Error(), "graphql") {
				t.Fatalf("no success receipt and no raw GraphQL: out=%q err=%v", run.Out.String(), run.Err)
			}
			oneCall2422(t, run.Calls, wakeRolesInst2894)
			for _, c := range *run.Calls {
				if c.Op == wakeRolesSet2894 {
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
			rendered := run.Out.String() + stderrOf2552(t, run.Factory)
			if err := json.Unmarshal([]byte(rendered), &envelope); err != nil {
				t.Fatalf("not an envelope: %v\n%s", err, rendered)
			}
			if envelope.Error.ServerCode != tc.code || !strings.Contains(envelope.Error.Message, tc.msg) {
				t.Fatalf("the instructed refusal keeps its code and message: %s", rendered)
			}
		})
	}
}

// An instructed null result is the same unknown outcome as a plain one: exit 1, the check named,
// no receipt. Kills: the instructed null guard removed (it would dereference nil).
func TestSaturationWakeRolesInstructedNullResultIsAnUnknownOutcome2960(t *testing.T) {
	r := wakeRolesResponses2894()
	r[wakeRolesInst2894] = `{"data":{"setSaturationAlarmWakeRoles":null}}`
	_, out, err := runTeamTask(t, r, "team", "saturation-wake-roles", "set", "qa-engineer", "--instruction", instructedArg2960)
	if err == nil || exitCodeFor(err) != exitcode.Error {
		t.Fatalf("want exit 1, got %v (%d)", err, exitCodeFor(err))
	}
	if !strings.Contains(err.Error(), "team saturation-wake-roles get") || strings.Contains(out, "✓") {
		t.Fatalf("the unknown outcome names the check and prints no receipt: err=%v out=%q", err, out)
	}
}

// `get`'s null read is a command error, never a panic or an empty list.
// Kills: get's null guard removed.
func TestSaturationWakeRolesGetNullReadIsACommandError2960(t *testing.T) {
	r := wakeRolesResponses2894()
	r[wakeRolesGet2894] = `{"data":{"saturationAlarmWakeRoles":null}}`
	_, out, err := runTeamTask(t, r, "team", "saturation-wake-roles", "get")
	if err == nil || exitCodeFor(err) != exitcode.Error {
		t.Fatalf("want exit 1, got %v (%d)", err, exitCodeFor(err))
	}
	if strings.Contains(out, "none (nobody is woken)") {
		t.Fatalf("a null read must not read as an empty list: %q", out)
	}
}

// set --clear --json emits the stored empty array, never null.
func TestSaturationWakeRolesSetClearJSONEmitsAnEmptyArray2960(t *testing.T) {
	_, js, err := runTeamTask(t, wakeRolesResponses2894(), "team", "saturation-wake-roles", "set", "--clear", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var dto struct {
		Roles []string `json:"roles"`
	}
	if err := json.Unmarshal([]byte(js), &dto); err != nil || dto.Roles == nil || len(dto.Roles) != 0 {
		t.Fatalf(`--clear --json carries "roles": [] (never null): %v %s`, err, js)
	}
}

// The help says the plain write is the signed-in person's "including from a bound worktree": this
// test STATES the binding (a worktree binding with a worker session exists) and shows the write still
// carries no worker session and no instruction.
func TestSaturationWakeRolesPlainSetFromABoundWorktreeIsThePersons2960(t *testing.T) {
	r := wakeRolesResponses2894("qa-engineer")
	calls, _, err := runTeamTask(t, r, "team", "saturation-wake-roles", "set", "qa-engineer")
	if err != nil {
		t.Fatal(err)
	}
	b, rerr := os.ReadFile(filepath.Join(os.Getenv(team.GitDirEnv), "hadron-team-session.json"))
	if rerr != nil || !strings.Contains(string(b), `"sessionId"`) {
		t.Fatalf("the scaffold must run from a BOUND worktree (a binding with a session): %v %s", rerr, b)
	}
	plain := oneCall2422(t, calls, wakeRolesSet2894)
	if plain.Session != "" || plain.Vars["instruction"] != nil {
		t.Fatalf("bound or not, the plain write is the person's: %+v", plain)
	}
}

// The floor is the UTC day the server half merged; the help of both verbs and the --instruction
// flag usage name it. Kills: the floor wording dropped or changed.
func TestSaturationWakeRolesFloorWordingIsPinned2960(t *testing.T) {
	for _, argv := range [][]string{{"team", "saturation-wake-roles", "get"}, {"team", "saturation-wake-roles", "set"}} {
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append(argv, "--help"))
		if err := root.Execute(); err != nil {
			t.Fatalf("%v: %v", argv, err)
		}
		help := strings.Join(strings.Fields(out.String()), " ")
		if !strings.Contains(help, "Needs server 2026-11-22 or later") {
			t.Errorf("%v --help must say the floor:\n%s", argv, help)
		}
	}
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "saturation-wake-roles", "set", "--help"})
	_ = root.Execute()
	if !strings.Contains(strings.Join(strings.Fields(out.String()), " "), "Needs server 2026-11-22 or later") {
		t.Fatal("the --instruction flag usage names the floor")
	}
}

// The FIT's wording: one wake when the alarm is raised and not again for that alarm; a worker who is
// never due (retired, paused or dormant) is not woken; a change applies only to later alarms. Not the
// jargon "unless it is never due", and not the server detail "while its episode is open".
func TestSaturationWakeRolesHelpWordingIsTheFITs2960(t *testing.T) {
	want := []string{
		"is offered one wake when a saturation alarm is raised, and not again for that alarm",
		"a worker who is never due (retired, paused or dormant) is not woken",
		"applies only to alarms raised afterwards",
	}
	for _, argv := range [][]string{{"team", "saturation-wake-roles"}, {"team", "saturation-wake-roles", "get"}, {"team", "saturation-wake-roles", "set"}} {
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append(argv, "--help"))
		if err := root.Execute(); err != nil {
			t.Fatalf("%v: %v", argv, err)
		}
		help := strings.Join(strings.Fields(out.String()), " ")
		for _, w := range want {
			if !strings.Contains(help, w) {
				t.Errorf("%v --help must say %q:\n%s", argv, w, help)
			}
		}
		for _, bad := range []string{"unless it is never due", "while its episode is open", "never again for that alarm"} {
			if strings.Contains(help, bad) {
				t.Errorf("%v --help must not say %q", argv, bad)
			}
		}
	}
	contract := strings.Join(strings.Fields(string(readContract(t))), " ")
	i := strings.Index(contract, "hadron team saturation-wake-roles get | set")
	if i < 0 {
		t.Fatal("agentic-usage has no saturation-wake-roles entry")
	}
	entry := contract[i:]
	if len(entry) > 4000 {
		entry = entry[:4000]
	}
	for _, w := range []string{"offered one wake when a saturation alarm is raised, and not again for that alarm", "a worker who is never due (retired, paused or dormant) is not woken"} {
		if !strings.Contains(entry, w) {
			t.Errorf("the agentic-usage entry must say %q", w)
		}
	}
	for _, bad := range []string{"unless it is never due", "while its episode is open"} {
		if strings.Contains(entry, bad) {
			t.Errorf("the agentic-usage entry must not say %q", bad)
		}
	}
}
