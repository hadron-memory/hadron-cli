package cmd

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// cli#1625: `app join <app-ref>` and `app leave <app-ref>` (joinApp /
// leaveApp coverage, sub-issue of #63). The CLI passes the server's answer
// through: it decides no policy (#1714 self-join, 0082 (A) leave refusals) and
// makes no "already a member" claim, because JoinAppPayload carries nothing
// that says whether a join created the row.

const (
	joinApp1625  = "hrn:app:acme.com:eng-team"
	joinMember   = `{"appId":"capp100000000000000000000","role":"member","createdAt":"2026-11-04T00:00:00Z","isOrgExternal":true,"user":{"id":"usr1","handle":"ada","urn":"hrn:user:ada"},"app":{"urn":"hrn:app:acme.com:eng-team","name":"Eng Team"}}`
	leavePayload = `{"appId":"capp100000000000000000000","userId":"usr1"}`
)

func responses1625() map[string]string {
	r := responses1594()
	r["JoinApp"] = `{"data":{"joinApp":{"appMember":` + joinMember + `}}}`
	for _, op := range []string{"LeaveApp", "LeaveAppInstructed"} {
		r[op] = `{"data":{"leaveApp":` + leavePayload + `}}`
	}
	return r
}

func run1625(resp map[string]string) run1592 {
	if resp == nil {
		resp = responses1625()
	}
	return run1592{response: resp}
}

func TestAppJoin1625(t *testing.T) {
	t.Run("joins and prints the member row with its role", func(t *testing.T) {
		calls, r := run1625(nil).exec(t, "app", "join", joinApp1625, "--json")
		if r.err != nil {
			t.Fatalf("join: %v\n%s", r.err, r.errOut)
		}
		got := writes1592(calls, "JoinApp")
		if len(got) != 1 || got[0].Vars["appId"] != joinApp1625 {
			t.Fatalf("want one JoinApp with the ref unchanged, got %+v", *calls)
		}
		var dto map[string]any
		if err := json.Unmarshal([]byte(r.out), &dto); err != nil {
			t.Fatalf("json: %v %s", err, r.out)
		}
		want := map[string]any{
			"app": joinApp1625, "appId": "capp100000000000000000000", "appUrn": "hrn:app:acme.com:eng-team",
			"appName": "Eng Team", "role": "member", "createdAt": "2026-11-04T00:00:00Z", "isOrgExternal": true,
			"user": map[string]any{"id": "usr1", "handle": "ada", "urn": "hrn:user:ada"},
		}
		if !reflect.DeepEqual(dto, want) {
			t.Fatalf("--json shape:\n got %#v\nwant %#v", dto, want)
		}
	})
	t.Run("a join returning an existing row prints that row and claims nothing", func(t *testing.T) {
		// The server owns idempotency. An older row with a non-default role
		// distinguishes rendering its answer from inventing a fresh membership.
		existing := strings.Replace(joinMember, `"role":"member"`, `"role":"reviewer"`, 1)
		existing = strings.Replace(existing, "2026-11-04T00:00:00Z", "2025-01-02T03:04:05Z", 1)
		resp := responses1625()
		resp["JoinApp"] = `{"data":{"joinApp":{"appMember":` + existing + `}}}`
		calls, r := run1625(resp).exec(t, "app", "join", joinApp1625)
		want := "✓ You are a member of hrn:app:acme.com:eng-team with role reviewer\n" +
			"  app: Eng Team\n  member since: 2025-01-02T03:04:05Z\n"
		if r.err != nil || r.out != want || r.errOut != "" {
			t.Fatalf("existing row: err=%v out=%q stderr=%q want=%q", r.err, r.out, r.errOut, want)
		}
		if len(*calls) != 1 || (*calls)[0].Op != "JoinApp" || (*calls)[0].Vars["appId"] != joinApp1625 {
			t.Fatalf("one join without a membership pre-read or retry: %+v", *calls)
		}
	})
	t.Run("needs exactly one app-ref and sends nothing without it", func(t *testing.T) {
		for _, args := range [][]string{{"app", "join"}, {"app", "join", "a", "b"}, {"app", "join", "  "}} {
			calls, r := run1625(nil).exec(t, args...)
			if r.err == nil || exitCodeFor(r.err) != 2 || len(*calls) != 0 {
				t.Fatalf("%v: exit %d calls %+v err %v", args, exitCodeFor(r.err), *calls, r.err)
			}
		}
	})
}

// Every code the SDL lists is a nonzero exit with the server's own sentence,
// and never the "outcome unknown" retry-advice of a lost response.
var typedCodes1625 = []struct{ code, msg string }{
	{"SelfJoinNotAllowedError", "This App is closed to self-join"},
	{"MaxMembersExceededError", "Member limit reached"},
	{"AppNotFoundError", "App not found"},
	{"AppUninstalledError", "App is uninstalled"},
	{"OrphanAppError", "App lost its Agents"},
	{"InvalidRoleError", "Role not accepted"},
	{"UNAUTHENTICATED", "You must be logged in"},
}

func TestAppJoinTypedErrors1625(t *testing.T) {
	for _, c := range typedCodes1625 {
		resp := responses1625()
		resp["JoinApp"] = memberGQLError(c.msg, c.code)
		calls, r := run1625(resp).exec(t, "app", "join", joinApp1625)
		if r.err == nil || exitCodeFor(r.err) == 0 {
			t.Fatalf("%s: want a nonzero exit, got %v", c.code, r.err)
		}
		if !strings.Contains(r.err.Error(), c.msg) || strings.Contains(r.err.Error(), "outcome unknown") {
			t.Fatalf("%s: server sentence must pass through: %v", c.code, r.err)
		}
		if r.out != "" || len(writes1592(calls, "JoinApp")) != 1 {
			t.Fatalf("%s: no success output and one request, got out=%q calls=%+v", c.code, r.out, *calls)
		}
	}
	// The SelfJoinNotAllowedError code rides on the --json envelope.
	resp := responses1625()
	resp["JoinApp"] = memberGQLError("closed", "SelfJoinNotAllowedError")
	_, r := run1625(resp).exec(t, "app", "join", joinApp1625, "--json")
	if r.err == nil || !strings.Contains(r.out+r.errOut, "SelfJoinNotAllowedError") {
		t.Fatalf("serverCode missing from the failure: err=%v out=%q errOut=%q", r.err, r.out, r.errOut)
	}
}

func TestAppLeave1625(t *testing.T) {
	t.Run("leaves with --yes and reports the state now", func(t *testing.T) {
		calls, r := run1625(nil).exec(t, "app", "leave", joinApp1625, "--yes", "--json")
		if r.err != nil {
			t.Fatalf("leave: %v\n%s", r.err, r.errOut)
		}
		got := writes1592(calls, "LeaveApp", "LeaveAppInstructed")
		if len(got) != 1 || got[0].Op != "LeaveApp" || got[0].Vars["appId"] != joinApp1625 {
			t.Fatalf("want one plain LeaveApp with the ref unchanged, got %+v", *calls)
		}
		var dto map[string]any
		if err := json.Unmarshal([]byte(r.out), &dto); err != nil {
			t.Fatalf("json: %v %s", err, r.out)
		}
		want := map[string]any{"app": joinApp1625, "appId": "capp100000000000000000000", "userId": "usr1", "status": "absent"}
		if !reflect.DeepEqual(dto, want) {
			t.Fatalf("--json shape:\n got %#v\nwant %#v", dto, want)
		}
	})
	t.Run("a non-member's leave is the server's idempotent no-op, exit 0", func(t *testing.T) {
		// The server answers the same payload whether or not a row existed; the
		// CLI reads no membership first and says only "not a member now".
		calls, r := run1625(nil).exec(t, "app", "leave", joinApp1625, "--yes")
		if r.err != nil || len(*calls) == 0 {
			t.Fatalf("err=%v calls=%+v", r.err, *calls)
		}
		for _, c := range *calls {
			if c.Op != "LeaveApp" {
				t.Fatalf("no read may precede the leave: %+v", *calls)
			}
		}
		if strings.Contains(strings.ToLower(r.out), "was a member") || !strings.Contains(r.out, "not a member") {
			t.Fatalf("say the state now, not the history: %q", r.out)
		}
	})
	t.Run("no TTY and no --yes is refused before any request", func(t *testing.T) {
		calls, r := run1625(nil).exec(t, "app", "leave", joinApp1625)
		if r.err == nil || exitCodeFor(r.err) != 2 || len(*calls) != 0 || !strings.Contains(r.err.Error(), "--yes") {
			t.Fatalf("exit %d calls %+v err %v", exitCodeFor(r.err), *calls, r.err)
		}
	})
	t.Run("the confirmation says the personal memory is kept, and a no sends nothing", func(t *testing.T) {
		srv, calls, _ := countingGraphQL(t, responses1625())
		f, out, errOut := testFactoryTTY(t, "n\n")
		root := NewRootCmd(f)
		root.SetArgs([]string{"app", "leave", joinApp1625, "--server", srv.URL})
		err := root.Execute()
		if err == nil || calls("LeaveApp") != 0 {
			t.Fatalf("a no must send nothing: err=%v calls=%d", err, calls("LeaveApp"))
		}
		text := strings.ToLower(out.String() + errOut.String())
		for _, want := range []string{"personal memory", "kept", "join again"} {
			if !strings.Contains(text, want) {
				t.Fatalf("confirmation lacks %q: %q", want, text)
			}
		}
	})
	t.Run("needs exactly one app-ref", func(t *testing.T) {
		for _, args := range [][]string{{"app", "leave", "--yes"}, {"app", "leave", "a", "b", "--yes"}} {
			calls, r := run1625(nil).exec(t, args...)
			if r.err == nil || exitCodeFor(r.err) != 2 || len(*calls) != 0 {
				t.Fatalf("%v: exit %d calls %+v", args, exitCodeFor(r.err), *calls)
			}
		}
	})
}

// --instruction is passed through unchanged (request-shape pin): the instructed
// document, instruction {kind, ref} with ref everything after the first colon.
func TestAppLeaveInstructionWire1625(t *testing.T) {
	for _, tc := range []struct{ flag, kind, ref string }{
		{instrTask1592, "TASK", taskRef1592},
		{"team-chat-message:hrn:chatmsg:acme.com:eng-team:70910", "TEAM_CHAT_MESSAGE", "hrn:chatmsg:acme.com:eng-team:70910"},
		{"live-chat:claude-desktop:conv:abc:42", "LIVE_CHAT", "claude-desktop:conv:abc:42"},
	} {
		calls, r := run1625(nil).exec(t, "app", "leave", app1594, "--yes", "--instruction", tc.flag)
		if r.err != nil {
			t.Fatalf("%s: %v\n%s", tc.flag, r.err, r.errOut)
		}
		got := writes1592(calls, "LeaveApp", "LeaveAppInstructed")
		if len(got) != 1 || got[0].Op != "LeaveAppInstructed" {
			t.Fatalf("%s: writes %+v", tc.flag, got)
		}
		if want := map[string]any{"kind": tc.kind, "ref": tc.ref}; !reflect.DeepEqual(got[0].Vars["instruction"], want) {
			t.Fatalf("instruction = %#v, want %#v", got[0].Vars["instruction"], want)
		}
		if !strings.Contains(got[0].Query, "instruction: $instruction") {
			t.Fatalf("document does not pass the instruction: %s", got[0].Query)
		}
	}
	// Without the flag the plain document goes out, byte for byte, naming no
	// instruction.
	calls, r := run1625(nil).exec(t, "app", "leave", app1594, "--yes")
	if r.err != nil {
		t.Fatal(r.err)
	}
	got := writes1592(calls, "LeaveApp", "LeaveAppInstructed")
	if len(got) != 1 || got[0].Op != "LeaveApp" || got[0].Query != gen.LeaveApp_Operation ||
		strings.Contains(got[0].Query, "instruction") || strings.Contains(string(got[0].RawVars), "instruction") {
		t.Fatalf("plain document changed: %+v", got)
	}
	// A bad kind or blank ref is exit 2 with no request, before the prompt.
	for _, bad := range []string{"", "task", "task:", ":" + taskRef1592, "chat:x", "task: "} {
		for _, extra := range [][]string{{"--yes"}, {}} {
			args := append([]string{"app", "leave", app1594, "--instruction", bad}, extra...)
			calls, r := run1625(nil).exec(t, args...)
			if exitCodeFor(r.err) != 2 || len(*calls) != 0 || !strings.Contains(r.err.Error(), "--instruction") {
				t.Fatalf("%q %v: exit %d calls %+v err %v", bad, extra, exitCodeFor(r.err), *calls, r.err)
			}
		}
	}
}

// The resolver's FORBIDDEN (hadron-server#3342) and ruling 0082 (A)'s refusals
// (impersonation, an App run, an unattributable session) reach the person
// unchanged: the server's code and sentence, a nonzero exit, no success output,
// no "outcome unknown", no retry.
func TestAppLeaveRefusalsPassThrough1625(t *testing.T) {
	for _, c := range []struct{ name, code, msg string }{
		{"worker session without instruction or grant", "FORBIDDEN", "Forbidden: self-leave requires an instruction or own app.members grant"},
		{"impersonation", "FORBIDDEN", "Forbidden: an impersonating session may not leave an App"},
		{"an App run", "FORBIDDEN", "Forbidden: an App run may not leave an App"},
		{"unattributable session", "FORBIDDEN", "Forbidden: the session cannot be attributed"},
	} {
		for _, extra := range [][]string{nil, {"--instruction", instrTask1592}} {
			resp := responses1625()
			resp["LeaveApp"] = memberGQLError(c.msg, c.code)
			resp["LeaveAppInstructed"] = memberGQLError(c.msg, c.code)
			args := append([]string{"app", "leave", app1594, "--yes", "--json"}, extra...)
			calls, r := run1625(resp).exec(t, args...)
			if r.err == nil || exitCodeFor(r.err) == 0 {
				t.Fatalf("%s %v: want a nonzero exit, got %v", c.name, extra, r.err)
			}
			if !strings.Contains(r.err.Error(), c.msg) || strings.Contains(r.err.Error(), "outcome unknown") {
				t.Fatalf("%s %v: sentence must pass through verbatim: %v", c.name, extra, r.err)
			}
			if !strings.Contains(r.out+r.errOut, `"FORBIDDEN"`) {
				t.Fatalf("%s %v: serverCode FORBIDDEN missing: out=%q errOut=%q", c.name, extra, r.out, r.errOut)
			}
			if n := len(writes1592(calls, "LeaveApp", "LeaveAppInstructed")); n != 1 {
				t.Fatalf("%s %v: one request, no retry; got %d", c.name, extra, n)
			}
		}
	}
	for _, c := range typedCodes1625 {
		resp := responses1625()
		resp["LeaveApp"] = memberGQLError(c.msg, c.code)
		_, r := run1625(resp).exec(t, "app", "leave", joinApp1625, "--yes")
		if r.err == nil || exitCodeFor(r.err) == 0 || !strings.Contains(r.err.Error(), c.msg) {
			t.Fatalf("%s: %v", c.code, r.err)
		}
	}
}

// A lost or ambiguous answer to a write is not a refusal: never a success
// line, never retried, and the check is named.
func TestAppLeaveMalformedAnswerIsNotSuccess1625(t *testing.T) {
	for _, tc := range []struct {
		name, op string
		extra    []string
	}{
		{"plain", "LeaveApp", nil},
		{"instructed", "LeaveAppInstructed", []string{"--instruction", instrTask1592}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := responses1625()
			resp[tc.op] = `{"data":{"leaveApp":null}}`
			args := append([]string{"app", "leave", joinApp1625, "--yes"}, tc.extra...)
			calls, r := run1625(resp).exec(t, args...)
			if exitCodeFor(r.err) != 7 || r.out != "" || !strings.Contains(r.errOut, "outcome unknown") {
				t.Fatalf("exit=%d err=%v out=%q stderr=%q", exitCodeFor(r.err), r.err, r.out, r.errOut)
			}
			if len(*calls) != 1 || (*calls)[0].Op != tc.op {
				t.Fatalf("one %s, no retry: %+v", tc.op, *calls)
			}
		})
	}
}

func TestAppLeaveUsage1772(t *testing.T) {
	_, r := run1625(nil).exec(t, "app", "leave", "--help")
	if r.err != nil || !strings.Contains(r.out, "hadron app leave <app-ref> [--yes] [--instruction <kind>:<ref>]") {
		t.Fatalf("leave usage must show --yes: err=%v help=%q", r.err, r.out)
	}
	_, join := run1625(nil).exec(t, "app", "join", "--help")
	if join.err != nil || strings.Contains(join.out, "--yes") {
		t.Fatalf("join must not advertise --yes: err=%v help=%q", join.err, join.out)
	}
}
