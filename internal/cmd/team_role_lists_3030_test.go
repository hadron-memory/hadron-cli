package cmd

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#3030 (companion to hadron-server#4921 slice A, server 330e16176): `hadron team role-lists get|set`, the
// App's three role lists: who may link a defect (defect-link), the register pick's role route (register-pick)
// and who may withdraw a negative review verdict besides its reviewer (review-withdraw). `get` is any
// participant's read; `set <purpose> <role>... | --clear` replaces ONE list whole and is for whoever manages
// the App; `set --instruction` follows `saturation-wake-roles set` (the instructed manager write, in its own
// document). An empty list means the platform's default for that purpose, said in the output.

const (
	roleListsApp3030  = "capp100000000000000000000"
	roleListsGet3030  = "TeamRoleLists"
	roleListsSet3030  = "TeamRoleListSet"
	roleListsInst3030 = "TeamRoleListSetInstructed"
)

func roleListsGetJSON3030(defect, register, withdraw []string) string {
	enc := func(l []string) string {
		if l == nil {
			return "[]"
		}
		b, _ := json.Marshal(l)
		return string(b)
	}
	return `{"appId":"` + roleListsApp3030 + `","defectLink":` + enc(defect) + `,"registerPick":` + enc(register) + `,"reviewWithdraw":` + enc(withdraw) + `}`
}

func roleListsSetJSON3030(purpose string, stored ...string) string {
	b, _ := json.Marshal(stored)
	if stored == nil {
		b = []byte("[]")
	}
	return `{"appId":"` + roleListsApp3030 + `","purpose":"` + purpose + `","roles":` + string(b) + `}`
}

func roleListsResponses3030(purpose string, stored ...string) map[string]string {
	r := taskResponses()
	r[roleListsGet3030] = `{"data":{"appRoleLists":` + roleListsGetJSON3030([]string{"qa-engineer"}, nil, []string{"coordinator", "qa-engineer"}) + `}}`
	r[roleListsSet3030] = `{"data":{"setAppRoleList":` + roleListsSetJSON3030(purpose, stored...) + `}}`
	r[roleListsInst3030] = r[roleListsSet3030]
	return r
}

func roleListsSent3030(t *testing.T, calls *[]attnCall) (string, []any) {
	t.Helper()
	c := oneCall2422(t, calls, roleListsSet3030)
	if c.Vars["appRef"] != roleListsApp3030 {
		t.Fatalf("appRef %v", c.Vars["appRef"])
	}
	roles, present := c.Vars["roles"]
	if !present {
		t.Fatalf("the set must send the roles list (an empty list is sent, never omitted): %v", c.Vars)
	}
	list, ok := roles.([]any)
	if !ok {
		t.Fatalf("roles is not a list: %#v", roles)
	}
	purpose, _ := c.Vars["purpose"].(string)
	return purpose, list
}

// get prints the three lists, an empty one saying the default applies; --json is the three arrays (never null).
func TestRoleListsGetShowsTheThreeLists3030(t *testing.T) {
	calls, out, err := runTeamTask(t, roleListsResponses3030("DEFECT_LINK"), "team", "role-lists", "get")
	if err != nil {
		t.Fatal(err)
	}
	if got := oneCall2422(t, calls, roleListsGet3030).Vars["appRef"]; got != roleListsApp3030 {
		t.Fatalf("appRef %v", got)
	}
	for _, want := range []string{"defect-link: qa-engineer", "register-pick: none (the default applies)", "review-withdraw: coordinator, qa-engineer"} {
		if !strings.Contains(out, want) {
			t.Errorf("the output must show %q:\n%s", want, out)
		}
	}
	_, js, err := runTeamTask(t, roleListsResponses3030("DEFECT_LINK"), "team", "role-lists", "get", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var dto struct {
		AppID          string   `json:"appId"`
		DefectLink     []string `json:"defectLink"`
		RegisterPick   []string `json:"registerPick"`
		ReviewWithdraw []string `json:"reviewWithdraw"`
	}
	if err := json.Unmarshal([]byte(js), &dto); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, js)
	}
	if dto.AppID != roleListsApp3030 || !reflect.DeepEqual(dto.DefectLink, []string{"qa-engineer"}) || dto.RegisterPick == nil || len(dto.RegisterPick) != 0 || !reflect.DeepEqual(dto.ReviewWithdraw, []string{"coordinator", "qa-engineer"}) {
		t.Fatalf("json: %+v\n%s", dto, js)
	}
}

// set sends ONE mutation with the whole list for ONE purpose (the enum value), as typed, and prints what the
// server STORED (the fake answers a normalised list).
func TestRoleListsSetSendsOnePurposesWholeList3030(t *testing.T) {
	for cli, enum := range map[string]string{"defect-link": "DEFECT_LINK", "register-pick": "REGISTER_PICK", "review-withdraw": "REVIEW_WITHDRAW"} {
		t.Run(cli, func(t *testing.T) {
			r := roleListsResponses3030(enum, "backend-engineer", "qa-engineer")
			calls, out, err := runTeamTask(t, r, "team", "role-lists", "set", cli, "QA-Engineer", "backend-engineer", "QA-Engineer")
			if err != nil {
				t.Fatal(err)
			}
			purpose, roles := roleListsSent3030(t, calls)
			if purpose != enum || !reflect.DeepEqual(roles, []any{"QA-Engineer", "backend-engineer", "QA-Engineer"}) {
				t.Fatalf("purpose %q roles %v: the list goes as typed, for one purpose", purpose, roles)
			}
			if !strings.Contains(out, cli+": backend-engineer, qa-engineer") || strings.Contains(out, "QA-Engineer") {
				t.Fatalf("the printed result is what the server stored: %q", out)
			}
			_, js, err := runTeamTask(t, r, "team", "role-lists", "set", cli, "qa-engineer", "--json")
			if err != nil {
				t.Fatal(err)
			}
			var dto struct {
				AppID   string   `json:"appId"`
				Purpose string   `json:"purpose"`
				Roles   []string `json:"roles"`
			}
			if err := json.Unmarshal([]byte(js), &dto); err != nil || dto.Purpose != cli || !reflect.DeepEqual(dto.Roles, []string{"backend-engineer", "qa-engineer"}) {
				// purpose is the command's spelling since cli#3046 (it was the server's enum)
				t.Fatalf("--json is the stored list: %v %s", err, js)
			}
		})
	}
}

// --clear sends the empty list (present, not omitted) and says the default applies.
func TestRoleListsSetClearSendsTheEmptyList3030(t *testing.T) {
	calls, out, err := runTeamTask(t, roleListsResponses3030("DEFECT_LINK"), "team", "role-lists", "set", "defect-link", "--clear")
	if err != nil {
		t.Fatal(err)
	}
	if purpose, got := roleListsSent3030(t, calls); purpose != "DEFECT_LINK" || len(got) != 0 {
		t.Fatalf("--clear sends the empty list for the purpose, got %q %v", purpose, got)
	}
	if !strings.Contains(out, "defect-link: none (the default applies)") {
		t.Fatalf("the cleared list says the default applies: %q", out)
	}
	_, js, err := runTeamTask(t, roleListsResponses3030("DEFECT_LINK"), "team", "role-lists", "set", "defect-link", "--clear", "--json")
	if err != nil || !strings.Contains(js, `"roles": []`) {
		t.Fatalf("--clear --json carries roles: [] (never null): %v %s", err, js)
	}
}

// Usage and local limits refuse before anything is sent: no purpose, an unknown purpose, nothing to set and no
// --clear, a role with --clear, and each name the server would refuse (blank, a ':' or whitespace inside, over
// 64 characters) and more than 32 names after collapsing duplicates; the server's limits are in the message.
func TestRoleListsSetRefusesLocallyBeforeAnyRequest3030(t *testing.T) {
	long := strings.Repeat("a", 65)
	var thirtyThree []string
	for i := 0; i < 33; i++ {
		thirtyThree = append(thirtyThree, "r"+strings.Repeat("x", i%5)+string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	for name, argv := range map[string][]string{
		"no purpose":          {"set"},
		"unknown purpose":     {"set", "qa-engineer-only", "qa"},
		"no role, no --clear": {"set", "defect-link"},
		"a role with --clear": {"set", "defect-link", "qa", "--clear"},
		"a blank role":        {"set", "defect-link", "qa", ""},
		"a colon":             {"set", "defect-link", "a:b"},
		"whitespace":          {"set", "defect-link", "a b"},
		"U+FEFF inside":       {"set", "defect-link", "a\ufeffb"},
		"too long":            {"set", "defect-link", long},
		"over 32 names":       append([]string{"set", "defect-link"}, thirtyThree...),
	} {
		t.Run(name, func(t *testing.T) {
			calls, _, err := runTeamTask(t, roleListsResponses3030("DEFECT_LINK"), append([]string{"team", "role-lists"}, argv...)...)
			if exitCodeFor(err) != exitcode.Usage {
				t.Fatalf("want usage (2), got %v (%d)", err, exitCodeFor(err))
			}
			for _, c := range *calls {
				if c.Op == roleListsSet3030 || c.Op == roleListsInst3030 {
					t.Fatalf("nothing may be sent: %+v", c)
				}
			}
			if strings.Contains(name, "colon") || strings.Contains(name, "whitespace") || strings.Contains(name, "FEFF") || strings.Contains(name, "long") || strings.Contains(name, "32") {
				if !strings.Contains(err.Error(), "at most 32 names of 1 to 64 characters") || !strings.Contains(err.Error(), "':' or whitespace") {
					t.Fatalf("the local refusal states the server's limits: %v", err)
				}
			}
		})
	}
	// Names the server accepts are NOT refused locally: surrounding space, upper case, and duplicates that
	// collapse (the server trims, lower-cases and de-duplicates before it counts).
	// Forty names that collapse to ten are under the 32 limit, a 64-character name is the longest accepted, and a
	// multi-byte name is measured in UTF-16 units like the server's (40 CJK characters are 40 units, 120 bytes).
	var many []string
	for i := 0; i < 40; i++ {
		many = append(many, "role"+string(rune('a'+i%10)))
	}
	cjk := strings.Repeat("界", 40)
	// The server's whitespace is JavaScript's: U+0085 is NOT whitespace there (so a name holding it is accepted),
	// U+FEFF IS (so a name holding it is refused, and one only padded with it is trimmed, then accepted).
	for name, names := range map[string][]string{"forty names, ten distinct": many, "exactly 64 characters": {strings.Repeat("a", 64)}, "multi-byte under the limit": {cjk},
		"U+0085 inside is not whitespace to the server": {"a\u0085b"}, "U+FEFF padding is trimmed": {"\ufeffqa\ufeff"}} {
		calls, _, err := runTeamTask(t, roleListsResponses3030("DEFECT_LINK", "x"), append([]string{"team", "role-lists", "set", "defect-link"}, names...)...)
		if err != nil {
			t.Errorf("%s: a list the server accepts is not refused locally: %v", name, err)
			continue
		}
		if _, got := roleListsSent3030(t, calls); len(got) != len(names) {
			t.Errorf("%s: sent as typed (%d names), got %d", name, len(names), len(got))
		}
	}
	calls, _, err := runTeamTask(t, roleListsResponses3030("DEFECT_LINK", "qa"), "team", "role-lists", "set", "defect-link", " QA ", "qa", "Qa")
	if err != nil {
		t.Fatalf("a name the server normalises is not refused locally: %v", err)
	}
	if _, got := roleListsSent3030(t, calls); !reflect.DeepEqual(got, []any{" QA ", "qa", "Qa"}) {
		t.Fatalf("sent as typed: %v", got)
	}
}

// The server's refusals map to their typed exit with the server's message and no success receipt.
func TestRoleListsServerRefusals3030(t *testing.T) {
	for _, tc := range []struct {
		name string
		argv []string
		op   string
		code string
		want int
		msg  string
	}{
		{"not the manager", []string{"set", "defect-link", "qa"}, roleListsSet3030, "APP_MANAGER_REQUIRED", exitcode.Forbidden, "Only whoever manages the App sets its role lists"},
		{"set unknown app", []string{"set", "defect-link", "qa"}, roleListsSet3030, "APP_NOT_FOUND", exitcode.NotFound, "App not found"},
		{"get unknown app", []string{"get"}, roleListsGet3030, "APP_NOT_FOUND", exitcode.NotFound, "App not found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := roleListsResponses3030("DEFECT_LINK")
			r[tc.op] = `{"errors":[{"message":"` + tc.msg + `.","extensions":{"code":"` + tc.code + `"}}]}`
			_, out, err := runTeamTask(t, r, append([]string{"team", "role-lists"}, tc.argv...)...)
			if err == nil || exitCodeFor(err) != tc.want || !strings.Contains(err.Error(), tc.msg) {
				t.Fatalf("want exit %d naming %q, got %v (%d)", tc.want, tc.msg, err, exitCodeFor(err))
			}
			if strings.Contains(out, "✓") {
				t.Fatalf("no success receipt: %q", out)
			}
		})
	}
}

// A null answer is an unknown outcome for the write (exit 1 naming `get`) and a command error for the read.
func TestRoleListsNullAnswers3030(t *testing.T) {
	r := roleListsResponses3030("DEFECT_LINK")
	r[roleListsSet3030] = `{"data":{"setAppRoleList":null}}`
	_, out, err := runTeamTask(t, r, "team", "role-lists", "set", "defect-link", "qa")
	if exitCodeFor(err) != exitcode.Error || !strings.Contains(err.Error(), "team role-lists get") || strings.Contains(out, "✓") {
		t.Fatalf("a null set is an unknown outcome naming the check: %v out=%q", err, out)
	}
	r = roleListsResponses3030("DEFECT_LINK")
	r[roleListsGet3030] = `{"data":{"appRoleLists":null}}`
	_, out, err = runTeamTask(t, r, "team", "role-lists", "get")
	if exitCodeFor(err) != exitcode.Error || strings.Contains(out, "none (the default applies)") {
		t.Fatalf("a null read must not read as empty lists: %v out=%q", err, out)
	}
}

// Without --instruction the write is the signed-in person's (no worker session, the plain document); with it
// the instructed document carries the claim exactly as the other instructed manager writes send it.
func TestRoleListsSetInstructionFollowsSaturationWakeRoles3030(t *testing.T) {
	r := roleListsResponses3030("REGISTER_PICK", "qa")
	calls, _, err := runTeamTask(t, r, "team", "role-lists", "set", "register-pick", "qa")
	if err != nil {
		t.Fatal(err)
	}
	plain := oneCall2422(t, calls, roleListsSet3030)
	if plain.Session != "" || strings.Contains(plain.Query, "instruction") || plain.Vars["instruction"] != nil {
		t.Fatalf("the plain write is the person's and names no instruction: %+v", plain)
	}
	calls, _, err = runTeamTask(t, r, "team", "role-lists", "set", "register-pick", "qa", "--instruction", "team-chat-message:"+taskInstructionRef2422)
	if err != nil {
		t.Fatal(err)
	}
	got := oneCall2422(t, calls, roleListsInst3030)
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
	// get takes no --instruction (a read); a bad claim is refused before any request.
	if _, _, err := runTeamTask(t, r, "team", "role-lists", "get", "--instruction", "task:t1"); exitCodeFor(err) != exitcode.Usage {
		t.Fatalf("get must refuse --instruction: %v", err)
	}
	calls, _, err = runTeamTask(t, r, "team", "role-lists", "set", "register-pick", "qa", "--instruction", "wrong:ref")
	if exitCodeFor(err) != exitcode.Usage || len(*calls) != 0 {
		t.Fatalf("a bad claim is refused before any request: %v calls=%v", err, *calls)
	}
}

// The help says what the lists are and that an empty one means the default, and names the server floor; it
// names no team role (the App names the roles; the platform names none).
func TestRoleListsHelpSaysWhatTheListsAre3030(t *testing.T) {
	for _, argv := range [][]string{{"team", "role-lists"}, {"team", "role-lists", "get"}, {"team", "role-lists", "set"}} {
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append(argv, "--help"))
		if err := root.Execute(); err != nil {
			t.Fatalf("%v: %v", argv, err)
		}
		help := strings.Join(strings.Fields(out.String()), " ")
		for _, want := range []string{"defect-link", "register-pick", "review-withdraw", "manages the App", "an empty list means the default", "the server only stores the lists: no door reads them yet"} {
			if !strings.Contains(help, want) {
				t.Errorf("%v --help must say %q:\n%s", argv, want, help)
			}
		}
		for _, role := range []string{"qa-engineer", "coordinator", "cli-engineer"} {
			if strings.Contains(help, role) {
				t.Errorf("%v --help must name no team role, found %q", argv, role)
			}
		}
	}
	for _, argv := range [][]string{{"team", "role-lists", "get"}, {"team", "role-lists", "set"}} {
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append(argv, "--help"))
		_ = root.Execute()
		if !strings.Contains(strings.Join(strings.Fields(out.String()), " "), "Needs server 2026-11-22 or later.") {
			t.Errorf("%v --help must close with the floor sentence", argv)
		}
	}
}
