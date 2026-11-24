package cmd

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2894 (companion to hadron-server#4777): `hadron team saturation-wake-roles
// get|set`, the App's saturation-alarm wake-roles list. `get` is any participant's
// read; `set <role>... | --clear` replaces the whole list and is for whoever manages
// the App; `set --instruction` follows `stall-schedule set` (the instructed manager
// write, in its own document so a no-flag request is what it always was). The list
// names ROLES; the result printed is what the server stored.

const (
	wakeRolesApp2894  = "capp100000000000000000000"
	wakeRolesGet2894  = "TeamSaturationWakeRoles"
	wakeRolesSet2894  = "TeamSaturationWakeRolesSet"
	wakeRolesInst2894 = "TeamSaturationWakeRolesSetInstructed"
)

func wakeRolesJSON2894(roles ...string) string {
	b, _ := json.Marshal(roles)
	if roles == nil {
		b = []byte("[]")
	}
	return `{"appId":"` + wakeRolesApp2894 + `","roles":` + string(b) + `}`
}

func wakeRolesResponses2894(stored ...string) map[string]string {
	r := taskResponses()
	r[wakeRolesGet2894] = `{"data":{"saturationAlarmWakeRoles":` + wakeRolesJSON2894(stored...) + `}}`
	r[wakeRolesSet2894] = `{"data":{"setSaturationAlarmWakeRoles":` + wakeRolesJSON2894(stored...) + `}}`
	r[wakeRolesInst2894] = r[wakeRolesSet2894]
	return r
}

func wakeRolesSent2894(t *testing.T, calls *[]attnCall, op string) []any {
	t.Helper()
	c := oneCall2422(t, calls, op)
	if c.Vars["appRef"] != wakeRolesApp2894 {
		t.Fatalf("appRef %v", c.Vars["appRef"])
	}
	roles, present := c.Vars["roles"]
	if !present {
		t.Fatalf("%s must send the roles list (an empty list is sent, never omitted): %v", op, c.Vars)
	}
	list, ok := roles.([]any)
	if !ok {
		t.Fatalf("roles is not a list: %#v", roles)
	}
	return list
}

// get prints the stored list and --json gives the stored array.
func TestSaturationWakeRolesGetShowsTheStoredList2894(t *testing.T) {
	calls, out, err := runTeamTask(t, wakeRolesResponses2894("backend-engineer", "qa-engineer"), "team", "saturation-wake-roles", "get")
	if err != nil {
		t.Fatal(err)
	}
	if got := oneCall2422(t, calls, wakeRolesGet2894).Vars["appRef"]; got != wakeRolesApp2894 {
		t.Fatalf("appRef %v", got)
	}
	for _, want := range []string{"backend-engineer", "qa-engineer"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the stored roles must be listed, missing %q: %q", want, out)
		}
	}
	if strings.Contains(out, "none (nobody is woken)") {
		t.Fatalf("a non-empty list must not say nobody is woken: %q", out)
	}
	_, js, err := runTeamTask(t, wakeRolesResponses2894("backend-engineer", "qa-engineer"), "team", "saturation-wake-roles", "get", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var dto struct {
		AppID string   `json:"appId"`
		Roles []string `json:"roles"`
	}
	if err := json.Unmarshal([]byte(js), &dto); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, js)
	}
	if dto.AppID != wakeRolesApp2894 || !reflect.DeepEqual(dto.Roles, []string{"backend-engineer", "qa-engineer"}) {
		t.Fatalf("json: %+v", dto)
	}
}

// An empty list is shown explicitly, and --json carries [] (never null).
func TestSaturationWakeRolesGetEmptyIsExplicit2894(t *testing.T) {
	_, out, err := runTeamTask(t, wakeRolesResponses2894(), "team", "saturation-wake-roles", "get")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "none (nobody is woken)") {
		t.Fatalf("an empty list reads 'none (nobody is woken)': %q", out)
	}
	_, js, err := runTeamTask(t, wakeRolesResponses2894(), "team", "saturation-wake-roles", "get", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(js, `"roles": []`) && !strings.Contains(js, `"roles":[]`) {
		t.Fatalf("--json must carry an empty array, never null: %s", js)
	}
}

// set sends the whole list as given and prints what the server STORED (the fake
// answers a normalised list, so printing what was sent would fail).
func TestSaturationWakeRolesSetSendsTheWholeListAndPrintsTheStoredOne2894(t *testing.T) {
	r := wakeRolesResponses2894("backend-engineer", "qa-engineer")
	calls, out, err := runTeamTask(t, r, "team", "saturation-wake-roles", "set", "QA-Engineer", "backend-engineer", "QA-Engineer")
	if err != nil {
		t.Fatal(err)
	}
	if got := wakeRolesSent2894(t, calls, wakeRolesSet2894); !reflect.DeepEqual(got, []any{"QA-Engineer", "backend-engineer", "QA-Engineer"}) {
		t.Fatalf("the list is sent as given (duplicates and case are the server's): %v", got)
	}
	if !strings.Contains(out, "backend-engineer") || !strings.Contains(out, "qa-engineer") || strings.Contains(out, "QA-Engineer") {
		t.Fatalf("the printed result is what the server stored: %q", out)
	}
	_, js, err := runTeamTask(t, r, "team", "saturation-wake-roles", "set", "QA-Engineer", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var dto struct {
		Roles []string `json:"roles"`
	}
	if err := json.Unmarshal([]byte(js), &dto); err != nil || !reflect.DeepEqual(dto.Roles, []string{"backend-engineer", "qa-engineer"}) {
		t.Fatalf("--json is the stored array: %v %s", err, js)
	}
}

// set --clear sends the empty list (present, not omitted) and prints the explicit empty line.
func TestSaturationWakeRolesSetClearSendsTheEmptyList2894(t *testing.T) {
	calls, out, err := runTeamTask(t, wakeRolesResponses2894(), "team", "saturation-wake-roles", "set", "--clear")
	if err != nil {
		t.Fatal(err)
	}
	if got := wakeRolesSent2894(t, calls, wakeRolesSet2894); len(got) != 0 {
		t.Fatalf("--clear sends the empty list, got %v", got)
	}
	if !strings.Contains(out, "none (nobody is woken)") {
		t.Fatalf("the cleared list reads 'none (nobody is woken)': %q", out)
	}
}

// Nothing to change, both a role and --clear, and a blank role (an unset variable) send nothing.
func TestSaturationWakeRolesSetUsageRefusalsSendNothing2894(t *testing.T) {
	for name, argv := range map[string][]string{
		"no role and no --clear": {"set"},
		"a role with --clear":    {"set", "qa-engineer", "--clear"},
		"a blank role":           {"set", "qa-engineer", ""},
		"a whitespace role":      {"set", "   "},
	} {
		t.Run(name, func(t *testing.T) {
			calls, _, err := runTeamTask(t, wakeRolesResponses2894(), append([]string{"team", "saturation-wake-roles"}, argv...)...)
			if exitCodeFor(err) != exitcode.Usage {
				t.Fatalf("want usage (2), got %v (%d)", err, exitCodeFor(err))
			}
			for _, c := range *calls {
				if c.Op == wakeRolesSet2894 || c.Op == wakeRolesInst2894 {
					t.Fatalf("nothing may be sent: %+v", c)
				}
			}
		})
	}
}

// The server's refusals map to their typed exit with the server's message and no success receipt.
func TestSaturationWakeRolesServerRefusals2894(t *testing.T) {
	cases := []struct {
		name string
		argv []string
		op   string
		code string
		want int
		msg  string
	}{
		{"not the manager", []string{"set", "qa-engineer"}, wakeRolesSet2894, "APP_MANAGER_REQUIRED", exitcode.Forbidden, "Only whoever manages the App sets its wake roles"},
		{"set unknown app", []string{"set", "qa-engineer"}, wakeRolesSet2894, "APP_NOT_FOUND", exitcode.NotFound, "App not found"},
		{"set bad role", []string{"set", "bad:role"}, wakeRolesSet2894, "BAD_USER_INPUT", exitcode.Usage, "a role has no colon or whitespace"},
		{"get unknown app", []string{"get"}, wakeRolesGet2894, "APP_NOT_FOUND", exitcode.NotFound, "App not found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := wakeRolesResponses2894()
			r[tc.op] = `{"errors":[{"message":"` + tc.msg + `.","extensions":{"code":"` + tc.code + `"}}]}`
			_, out, err := runTeamTask(t, r, append([]string{"team", "saturation-wake-roles"}, tc.argv...)...)
			if err == nil {
				t.Fatalf("the %s refusal must surface", tc.code)
			}
			if got := exitCodeFor(err); got != tc.want {
				t.Fatalf("exit code = %d, want %d (%s)", got, tc.want, tc.code)
			}
			if !strings.Contains(err.Error(), tc.msg) {
				t.Fatalf("the server's message should surface, got %v", err)
			}
			if strings.Contains(out, "✓") || strings.Contains(err.Error(), "graphql") {
				t.Fatalf("no success receipt and no raw GraphQL: out=%q err=%v", out, err)
			}
		})
	}
}

// A null mutation result is an unknown outcome (the write may have been recorded), exit 1,
// never a panic and never a success line.
func TestSaturationWakeRolesSetNullResultIsAnUnknownOutcome2894(t *testing.T) {
	r := wakeRolesResponses2894()
	r[wakeRolesSet2894] = `{"data":{"setSaturationAlarmWakeRoles":null}}`
	_, out, err := runTeamTask(t, r, "team", "saturation-wake-roles", "set", "qa-engineer")
	if err == nil || exitCodeFor(err) != exitcode.Error {
		t.Fatalf("want exit 1, got %v (%d)", err, exitCodeFor(err))
	}
	if !strings.Contains(err.Error(), "team saturation-wake-roles get") || strings.Contains(out, "✓") {
		t.Fatalf("the unknown outcome names the check and prints no receipt: err=%v out=%q", err, out)
	}
}

// Without --instruction the write is the signed-in person's (no worker session, the plain
// document, no instruction); with it the instructed document carries the claim exactly as
// stall-schedule set sends it, the worker session attached and every other variable unchanged.
func TestSaturationWakeRolesSetInstructionFollowsStallSchedule2894(t *testing.T) {
	r := wakeRolesResponses2894("qa-engineer")
	calls, _, err := runTeamTask(t, r, "team", "saturation-wake-roles", "set", "qa-engineer")
	if err != nil {
		t.Fatal(err)
	}
	plain := oneCall2422(t, calls, wakeRolesSet2894)
	if plain.Session != "" || strings.Contains(plain.Query, "instruction") || plain.Vars["instruction"] != nil {
		t.Fatalf("the plain write is the person's and names no instruction: %+v", plain)
	}
	calls, _, err = runTeamTask(t, r, "team", "saturation-wake-roles", "set", "qa-engineer", "--instruction", "team-chat-message:"+taskInstructionRef2422)
	if err != nil {
		t.Fatal(err)
	}
	got := oneCall2422(t, calls, wakeRolesInst2894)
	if got.Session != "s-new" || !strings.Contains(got.Query, "instruction: $instruction") {
		t.Fatalf("the instructed write carries the session and the claim: %+v", got)
	}
	want := map[string]any{"kind": "TEAM_CHAT_MESSAGE", "ref": taskInstructionRef2422}
	if !reflect.DeepEqual(got.Vars["instruction"], want) {
		t.Fatalf("claim = %v", got.Vars["instruction"])
	}
	delete(got.Vars, "instruction")
	if !reflect.DeepEqual(got.Vars, plain.Vars) {
		t.Fatalf("every other variable is unchanged: %v vs %v", got.Vars, plain.Vars)
	}
	// Every call except the instructed write itself (the lookups) carries no worker session and no
	// instruction. This guards that property; it does NOT guard withAsPerson, which reaches no field this
	// command sends (cli#2960: dropping that line changes nothing, so no test can fail on it).
	for _, c := range *calls {
		if c.Op != wakeRolesInst2894 && (c.Session != "" || c.Vars["instruction"] != nil) {
			t.Fatalf("a lookup must not be attributed: %+v", c)
		}
	}
}

// A bad claim is refused before any request (exit 2); `get` takes no --instruction.
func TestSaturationWakeRolesInstructionBadFlagAndGet2894(t *testing.T) {
	for _, raw := range []string{"", "wrong:ref", "task: "} {
		calls, _, err := runTeamTask(t, wakeRolesResponses2894(), "team", "saturation-wake-roles", "set", "qa-engineer", "--instruction", raw)
		if exitCodeFor(err) != exitcode.Usage || len(*calls) != 0 {
			t.Fatalf("%q: %v calls=%v", raw, err, *calls)
		}
	}
	_, _, err := runTeamTask(t, wakeRolesResponses2894(), "team", "saturation-wake-roles", "get", "--instruction", "task:t1")
	if exitCodeFor(err) != exitcode.Usage {
		t.Fatalf("get must refuse --instruction (a read): %v", err)
	}
}

// The help says what the list is: ROLES, empty by default, who may set it, what a listed
// worker gets, and that a change applies only to alarms raised afterwards (the FIT's wording).
func TestSaturationWakeRolesHelpSaysWhatTheListMeans2894(t *testing.T) {
	for _, argv := range [][]string{{"team", "saturation-wake-roles"}, {"team", "saturation-wake-roles", "get"}, {"team", "saturation-wake-roles", "set"}} {
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append(argv, "--help"))
		if err := root.Execute(); err != nil {
			t.Fatalf("%v: %v", argv, err)
		}
		help := strings.Join(strings.Fields(out.String()), " ")
		for _, want := range []string{
			"The list names roles",
			"is woken once",
			"not again for that alarm",
			"applies only to alarms raised afterwards",
			"empty by default",
			"manages the App",
		} {
			if !strings.Contains(help, want) {
				t.Errorf("%v --help must say %q:\n%s", argv, want, help)
			}
		}
	}
}
