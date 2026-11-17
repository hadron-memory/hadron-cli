package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
)

// cli#1592 (hadron-server#3307, ruling 0096, cor:dmo:050:12): --instruction
// <kind>:<ref> on `team worker update` and `team worker change-agent`.

const (
	taskRef1592    = "hrn:agenttask:hrn:app:acme.com:eng-team:AT1.2"
	instrTask1592  = "task:" + taskRef1592
	otherApp1592   = "capp200000000000000000000"
	instructedUpd  = "UpdateWorkerSigningInstructed"
	instructedChg  = "ChangeWorkerAgentInstructed"
	agentArg1592   = "hrn:agent:acme.com:qa-engineer"
	irisURN1592    = "hrn:worker:acme.com:eng-team:iris"
	instrRefusal   = "FORBIDDEN"
	irisOtherApp   = `"appId":"` + otherApp1592 + `"`
	irisInIrisApp  = `"appId":"capp100000000000000000000"`
	updateOKOp1592 = `{"data":{"updateWorker":` + signedWorkerJSON + `}}`
)

func responses1592() map[string]string {
	r := managerResponses1336()
	r["UpdateWorkerSigning"] = updateOKOp1592
	r[instructedUpd] = updateOKOp1592
	r[instructedChg] = r["ChangeWorkerAgent"]
	r["TeamAppIdentity"] = teamAppIdentityJSON
	r["GetWorkerInApp"] = `{"data":{"worker":` + irisWorkerJSON + `}}`
	return r
}

// run1592 runs one command against a fake server. bound writes the worktree
// binding at the fake server's URL (mutate may then change it); onOp stages a
// change as an operation arrives.
type run1592 struct {
	bound    bool
	mutate   func(b map[string]any)
	onOp     func(op string)
	response map[string]string
	// appearOn writes a complete binding at the fake server's URL into an
	// unbound worktree as that operation arrives (Gizelle, BLIND P2).
	appearOn string
	// appearCorrupt makes what appears unparseable bytes instead.
	appearCorrupt bool
	// noWorktree runs outside any git worktree (the real `git rev-parse`).
	noWorktree bool
	// corrupt replaces the bound binding file with unparseable bytes.
	corrupt bool
}

func (o run1592) exec(t *testing.T, args ...string) (*[]attnCall, keyRun) {
	t.Helper()
	r := o.response
	if r == nil {
		r = responses1592()
	}
	switch {
	case o.noWorktree:
		t.Setenv(team.GitDirEnv, "")
		dir := t.TempDir()
		t.Chdir(dir)
		t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	case o.bound:
		writeTeamBinding(t)
	default:
		teamGitDir(t)
	}
	var srvURL string
	hook := o.onOp
	if o.appearOn != "" {
		hook = func(op string) {
			if op == o.appearOn {
				if o.appearCorrupt {
					if err := os.WriteFile(filepath.Join(os.Getenv(team.GitDirEnv), "hadron-team-session.json"), []byte("{not json"), 0o600); err != nil {
						t.Error(err)
					}
				} else {
					writeTeamBindingIn(t, os.Getenv(team.GitDirEnv), srvURL)
				}
			}
			if o.onOp != nil {
				o.onOp(op)
			}
		}
	}
	srv, calls := attnServerHook(t, r, hook)
	srvURL = srv.URL
	if o.bound {
		setTeamBindingServer(t, srv.URL)
		if o.mutate != nil {
			rewriteBinding1592(t, o.mutate)
		}
		if o.corrupt {
			p := filepath.Join(os.Getenv(team.GitDirEnv), "hadron-team-session.json")
			if err := os.WriteFile(p, []byte("{not json"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"--server", srv.URL}, args...))
	err := root.Execute()
	if err != nil {
		renderFailure(f, args, err)
	}
	return calls, keyRun{out: out.String(), errOut: f.IOStreams.ErrOut.(*strings.Builder).String(), err: err}
}

// writeTeamBindingIn writes the binding fixture, at server, into dir without
// touching the environment (safe from the fake server's goroutine).
func writeTeamBindingIn(t *testing.T, dir, server string) {
	quoted, _ := json.Marshal(server)
	bound := strings.Replace(bindingFixture, `"startedAt"`, `"server":`+string(quoted)+`,"startedAt"`, 1)
	if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(bound), 0o600); err != nil {
		t.Error(err)
	}
}

func rewriteBinding1592(t *testing.T, mutate func(b map[string]any)) {
	t.Helper()
	p := filepath.Join(os.Getenv(team.GitDirEnv), "hadron-team-session.json")
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var b map[string]any
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatal(err)
	}
	mutate(b)
	raw, _ = json.Marshal(b)
	if err := os.WriteFile(p, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func updateArgs1592(target string, extra ...string) []string {
	return append([]string{"team", "worker", "update", target, "--signature", "s"}, extra...)
}

func changeArgs1592(target string, extra ...string) []string {
	return append([]string{"team", "worker", "change-agent", target, "--agent", agentArg1592, "--yes"}, extra...)
}

// writes returns every call to either document of the command's mutation.
func writes1592(calls *[]attnCall, ops ...string) []attnCall {
	var out []attnCall
	for _, c := range *calls {
		for _, op := range ops {
			if c.Op == op {
				out = append(out, c)
			}
		}
	}
	return out
}

func wantOnlyWriteSession(t *testing.T, calls *[]attnCall, writeOp, session string) {
	t.Helper()
	got := writes1592(calls, writeOp)
	if len(got) != 1 {
		t.Fatalf("want exactly one %s, got %+v", writeOp, *calls)
	}
	if got[0].Session != session {
		t.Fatalf("%s session = %q, want %q", writeOp, got[0].Session, session)
	}
	for _, c := range *calls {
		// cli#2007 §6: the advisory lint read after a signature write is the same
		// principal as the write, so it carries exactly the write's session.
		if c.Op == "GetWorkerSignatureLint" {
			if c.Session != session {
				t.Fatalf("lint read session = %q, want the write's %q", c.Session, session)
			}
			continue
		}
		if c.Op != writeOp && c.Session != "" {
			t.Fatalf("ancillary lookup %s carried session %q", c.Op, c.Session)
		}
	}
}

// Acceptance 1 + 2b: the flag sends the instruction document with
// instruction {kind, ref}; the ref is everything after the FIRST colon, byte
// for byte, and each kind maps to its enum.
func TestInstructionWireBothCommands1592(t *testing.T) {
	for _, tc := range []struct {
		flag, kind, ref string
	}{
		{instrTask1592, "TASK", taskRef1592},
		{"team-chat-message:hrn:chatmsg:acme.com:eng-team:70910", "TEAM_CHAT_MESSAGE", "hrn:chatmsg:acme.com:eng-team:70910"},
		{"live-chat:claude-desktop:conv:abc:42", "LIVE_CHAT", "claude-desktop:conv:abc:42"},
	} {
		for _, cmd := range []struct {
			args []string
			op   string
		}{
			{updateArgs1592(irisURN1592, "--instruction", tc.flag), instructedUpd},
			{changeArgs1592(irisURN1592, "--instruction", tc.flag), instructedChg},
		} {
			calls, r := run1592{}.exec(t, cmd.args...)
			if r.err != nil {
				t.Fatalf("%s %s: %v", cmd.op, tc.flag, r.err)
			}
			got := writes1592(calls, cmd.op, "UpdateWorkerSigning", "ChangeWorkerAgent")
			if len(got) != 1 || got[0].Op != cmd.op {
				t.Fatalf("%s: writes %+v", cmd.op, got)
			}
			want := map[string]any{"kind": tc.kind, "ref": tc.ref}
			if !reflect.DeepEqual(got[0].Vars["instruction"], want) {
				t.Fatalf("%s instruction = %#v, want %#v", cmd.op, got[0].Vars["instruction"], want)
			}
			if !strings.Contains(got[0].Query, "instruction: $instruction") {
				t.Fatalf("%s document does not pass the instruction: %s", cmd.op, got[0].Query)
			}
		}
	}
}

// Acceptance 1 + 2b: without the flag the unchanged document goes out: it
// names no instruction argument at all, and the variables have no key.
func TestNoInstructionKeepsThePlainDocuments1592(t *testing.T) {
	for _, cmd := range []struct {
		args []string
		op   string
	}{
		{updateArgs1592(irisURN1592), "UpdateWorkerSigning"},
		{changeArgs1592(irisURN1592), "ChangeWorkerAgent"},
	} {
		calls, r := run1592{}.exec(t, cmd.args...)
		if r.err != nil {
			t.Fatal(r.err)
		}
		got := writes1592(calls, cmd.op, instructedUpd, instructedChg)
		if len(got) != 1 || got[0].Op != cmd.op {
			t.Fatalf("writes %+v", got)
		}
		if strings.Contains(got[0].Query, "instruction") {
			t.Fatalf("%s document names instruction: %s", cmd.op, got[0].Query)
		}
		if strings.Contains(string(got[0].RawVars), "instruction") {
			t.Fatalf("%s variables carry instruction: %s", cmd.op, got[0].RawVars)
		}
	}
}

// Acceptance 3: a bad kind or an empty ref is a usage error with no request
// at all, before change-agent's confirmation and before any lookup.
func TestInstructionShapeRefusedLocally1592(t *testing.T) {
	for _, bad := range []string{"", "task", "task:", ":" + taskRef1592, "TASK:" + taskRef1592, "chat:x", "team_chat_message:x", "task: "} {
		for _, args := range [][]string{
			updateArgs1592(irisURN1592, "--instruction", bad),
			changeArgs1592(irisURN1592, "--instruction", bad),
			{"team", "worker", "change-agent", irisURN1592, "--agent", agentArg1592, "--instruction", bad},
		} {
			calls, r := run1592{bound: true}.exec(t, args...)
			if exitCodeFor(r.err) != 2 || len(*calls) != 0 {
				t.Fatalf("%q %v: exit %d, calls %+v", bad, args[2], exitCodeFor(r.err), *calls)
			}
		}
	}
}

// Acceptance 2, change-agent: confirmation (and its zero-request cancel)
// stays before any lookup, with the flag too.
func TestChangeAgentInstructionConfirmationFirst1592(t *testing.T) {
	calls, r := run1592{bound: true}.exec(t, "team", "worker", "change-agent", irisURN1592, "--agent", agentArg1592, "--instruction", instrTask1592)
	if r.err == nil || len(*calls) != 0 {
		t.Fatalf("non-interactive without --yes must refuse with zero requests: err=%v calls=%+v", r.err, *calls)
	}
}

// Acceptance 2: the attribution matrix WITH the flag, cell by cell.
func TestInstructionAttributionMatrix1592(t *testing.T) {
	otherAppWorker := strings.Replace(irisWorkerJSON, irisInIrisApp, irisOtherApp, 1)
	for _, tc := range []struct {
		name   string
		run    run1592
		target string
		extra  []string
		// session the write carries; update refuses where change-agent goes
		// out headerless (updateRefused).
		session       string
		updateRefused bool
	}{
		{name: "same App, URN target", run: run1592{bound: true}, target: irisURN1592, session: "s-new"},
		{name: "same App, id target", run: run1592{bound: true}, target: "wkr1", session: "s-new"},
		{name: "same App, name target", run: run1592{bound: true}, target: "Iris", session: "s-new"},
		{name: "same App, name with --app alias", run: run1592{bound: true}, target: "Iris", extra: []string{"--app", "acme.com:eng-team"}, session: "s-new"},
		// The target names its own App: an ambient --app elsewhere is not the scope.
		{name: "URN target, ambient --app elsewhere", run: run1592{bound: true}, target: irisURN1592, extra: []string{"--app", otherApp1592}, session: "s-new"},
		{name: "target in another App", run: run1592{bound: true, response: func() map[string]string {
			r := responses1592()
			r["GetWorker"] = `{"data":{"worker":` + otherAppWorker + `}}`
			return r
		}()}, target: irisURN1592, updateRefused: true},
		{name: "foreign server", run: run1592{bound: true, mutate: func(b map[string]any) { b["server"] = "https://elsewhere.example.test" }}, target: irisURN1592, updateRefused: true},
		{name: "incomplete binding", run: run1592{bound: true, mutate: func(b map[string]any) { b["workerId"] = "" }}, target: irisURN1592, updateRefused: true},
		{name: "unbound shell", run: run1592{}, target: irisURN1592},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, cmd := range []struct {
				args    []string
				op      string
				refused bool
			}{
				{updateArgs1592(tc.target, append([]string{"--instruction", instrTask1592}, tc.extra...)...), instructedUpd, tc.updateRefused},
				{changeArgs1592(tc.target, append([]string{"--instruction", instrTask1592}, tc.extra...)...), instructedChg, false},
			} {
				calls, r := tc.run.exec(t, cmd.args...)
				if cmd.refused {
					if exitCodeFor(r.err) != 2 || len(writes1592(calls, cmd.op)) != 0 {
						t.Fatalf("%s: want exit 2 and no write, got exit %d calls %+v", cmd.op, exitCodeFor(r.err), *calls)
					}
					for _, c := range *calls {
						if c.Session != "" {
							t.Fatalf("%s lookup carried session: %+v", cmd.op, c)
						}
					}
					continue
				}
				if r.err != nil {
					t.Fatalf("%s: %v\n%s", cmd.op, r.err, r.errOut)
				}
				wantOnlyWriteSession(t, calls, cmd.op, tc.session)
			}
		})
	}
}

// Acceptance 2: a rebind between the snapshot and the write is never
// attached: update refuses, change-agent goes out headerless.
func TestInstructionRebindDuringLookup1592(t *testing.T) {
	for _, cmd := range []struct {
		args    []string
		op      string
		refused bool
	}{
		{updateArgs1592(irisURN1592, "--instruction", instrTask1592), instructedUpd, true},
		{changeArgs1592(irisURN1592, "--instruction", instrTask1592), instructedChg, false},
	} {
		rebind := func(op string) {
			if op == "GetWorker" {
				rewriteBinding1592(t, func(b map[string]any) { b["sessionId"] = "s-other" })
			}
		}
		calls, r := run1592{bound: true, onOp: rebind}.exec(t, cmd.args...)
		if cmd.refused {
			if exitCodeFor(r.err) != 2 || len(writes1592(calls, cmd.op)) != 0 {
				t.Fatalf("%s: want exit 2, no write; exit %d calls %+v", cmd.op, exitCodeFor(r.err), *calls)
			}
			continue
		}
		if r.err != nil {
			t.Fatal(r.err)
		}
		wantOnlyWriteSession(t, calls, cmd.op, "")
	}
}

// Acceptance 2, unbound start (Gizelle BLIND P2 @45626ad1): a binding that
// APPEARS during the lookup is a change too. update refuses rather than going
// out as the person; change-agent goes out headerless with the note.
func TestInstructionBindingAppearsDuringLookup1592(t *testing.T) {
	for _, cmd := range []struct {
		args    []string
		op      string
		refused bool
	}{
		{updateArgs1592(irisURN1592, "--instruction", instrTask1592), instructedUpd, true},
		{changeArgs1592(irisURN1592, "--instruction", instrTask1592), instructedChg, false},
	} {
		calls, r := run1592{appearOn: "GetWorker"}.exec(t, cmd.args...)
		if cmd.refused {
			if exitCodeFor(r.err) != 2 || len(writes1592(calls, cmd.op)) != 0 {
				t.Fatalf("%s: want exit 2, no write; exit %d calls %+v", cmd.op, exitCodeFor(r.err), *calls)
			}
			if !strings.Contains(r.errOut, "appeared") {
				t.Fatalf("%s: refusal should say the binding appeared, got %q", cmd.op, r.errOut)
			}
			continue
		}
		if r.err != nil {
			t.Fatal(r.err)
		}
		wantOnlyWriteSession(t, calls, cmd.op, "")
		if !strings.Contains(r.errOut, "appeared") {
			t.Fatalf("%s: want the not-used note, got %q", cmd.op, r.errOut)
		}
	}
}

// Rodolfo §10 P3 @45626ad1: a binding missing only its session id, or one
// that can't be read, refuses an instructed update (exit 2, no write) rather
// than sending it as the person.
func TestInstructionUpdateRefusesUnusableBinding1592(t *testing.T) {
	for name, o := range map[string]run1592{
		"no session id": {bound: true, mutate: func(b map[string]any) { delete(b, "sessionId") }},
		"unreadable":    {bound: true, corrupt: true},
		// unbound at the snapshot, unreadable at send time: never the person.
		"appears unreadable": {appearOn: "GetWorker", appearCorrupt: true},
	} {
		calls, r := o.exec(t, updateArgs1592(irisURN1592, "--instruction", instrTask1592)...)
		if exitCodeFor(r.err) != 2 || len(writes1592(calls, instructedUpd)) != 0 {
			t.Fatalf("%s: want exit 2, no write; exit %d calls %+v", name, exitCodeFor(r.err), *calls)
		}
		want := map[string]string{"no session id": "incomplete", "unreadable": "can't be read", "appears unreadable": "appeared"}[name]
		if !strings.Contains(r.errOut, want) {
			t.Fatalf("%s: refusal should say %q, got %q", name, want, r.errOut)
		}
	}
}

// Outside any git worktree there is no binding to snapshot or re-read: the
// instructed call is the signed-in person's, headerless, with no refusal.
func TestInstructionOutsideAnyWorktree1592(t *testing.T) {
	for _, cmd := range []struct {
		args []string
		op   string
	}{
		{updateArgs1592(irisURN1592, "--instruction", instrTask1592), instructedUpd},
		{changeArgs1592(irisURN1592, "--instruction", instrTask1592), instructedChg},
	} {
		calls, r := run1592{noWorktree: true}.exec(t, cmd.args...)
		if r.err != nil {
			t.Fatalf("%s: %v (stderr %q)", cmd.op, r.err, r.errOut)
		}
		wantOnlyWriteSession(t, calls, cmd.op, "")
		if strings.Contains(r.errOut, "binding") {
			t.Fatalf("%s: no binding note expected outside a worktree, got %q", cmd.op, r.errOut)
		}
	}
}

// Acceptance 2: without the flag both commands keep today's attribution:
// update through the verified binding (fail-closed), change-agent headerless.
func TestNoInstructionAttributionUnchanged1592(t *testing.T) {
	calls, r := run1592{bound: true}.exec(t, updateArgs1592(irisURN1592)...)
	if r.err != nil {
		t.Fatal(r.err)
	}
	wantOnlyWriteSession(t, calls, "UpdateWorkerSigning", "s-new")
	calls, r = run1592{bound: true}.exec(t, changeArgs1592(irisURN1592)...)
	if r.err != nil {
		t.Fatal(r.err)
	}
	wantOnlyWriteSession(t, calls, "ChangeWorkerAgent", "")
	calls, r = run1592{bound: true, mutate: func(b map[string]any) { b["server"] = "https://elsewhere.example.test" }}.exec(t, updateArgs1592(irisURN1592)...)
	if exitCodeFor(r.err) != 2 || len(writes1592(calls, "UpdateWorkerSigning")) != 0 {
		t.Fatalf("no-flag update must still fail closed: exit %d calls %+v", exitCodeFor(r.err), *calls)
	}
}

// Acceptance 4: the server's refusals pass through with their codes, in
// --json as serverCode, after exactly one write; never retried.
func TestInstructionRefusalsPassThrough1592(t *testing.T) {
	for _, tc := range []struct {
		code string
		exit int
	}{{"FORBIDDEN", 8}, {"NOT_FOUND", 4}, {"BAD_USER_INPUT", 2}} {
		for _, cmd := range []struct {
			args []string
			op   string
		}{
			{updateArgs1592(irisURN1592, "--instruction", instrTask1592, "--json"), instructedUpd},
			{changeArgs1592(irisURN1592, "--instruction", instrTask1592, "--json"), instructedChg},
		} {
			r := responses1592()
			r[cmd.op] = gqlErrorJSON(tc.code)
			calls, res := run1592{bound: true, response: r}.exec(t, cmd.args...)
			if exitCodeFor(res.err) != tc.exit || len(writes1592(calls, cmd.op)) != 1 {
				t.Fatalf("%s %s: exit %d writes %d", cmd.op, tc.code, exitCodeFor(res.err), len(writes1592(calls, cmd.op)))
			}
			var env struct {
				Error struct {
					Code       int    `json:"code"`
					Message    string `json:"message"`
					ServerCode string `json:"serverCode"`
				} `json:"error"`
			}
			if err := json.Unmarshal([]byte(res.out), &env); err != nil || env.Error.Code != tc.exit || env.Error.ServerCode != tc.code {
				t.Fatalf("%s %s: envelope %s (%v)", cmd.op, tc.code, res.out, err)
			}
			if tc.code == instrRefusal && strings.Contains(env.Error.Message+res.errOut, "unbound shell") {
				t.Fatalf("%s: an instructed refusal must not advise an unbound shell: %s", cmd.op, res.out)
			}
		}
	}
}
