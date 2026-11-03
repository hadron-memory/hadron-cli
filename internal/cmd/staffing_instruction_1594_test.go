package cmd

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// cli#1594 (hadron-server#3308 at 517eb1db, rulings 0096/0097,
// cor:dmo:050:12, cor:agt:080:24): --instruction <kind>:<ref> on `team worker
// cast` (and --dry-run), `retire`, `rm`, and `app member add|remove`. All five
// write as the signed-in person today (none is an authored write), so with the
// flag they follow change-agent's row of the #1592 matrix: the worker session
// only through a binding verified for the TARGET App, else headerless with a
// note.

const (
	app1594      = "capp100000000000000000000" // the binding fixture's App
	alias1594    = "acme.com:eng-team"
	otherApp1594 = "capp200000000000000000000"
	agent1594    = "hrn:agent:acme.com:qa-engineer"
)

var otherAppIdentity1594 = `{"data":{"app":{"id":"` + otherApp1594 + `","urn":"hrn:app:acme.com:ops","name":"Ops"}}}`

func responses1594() map[string]string {
	r := responses1592()
	preview := `{"name":"Uma","role":"qa-engineer","agentId":"agt9","agentName":"QA","prompt":null,"teamPlacement":"NO_TEAM","reportsToWorkerId":null}`
	member := `{"role":"member","createdAt":"2026-11-03T00:00:00Z","user":{"id":"u1","handle":"ada","urn":"hrn:user:ada"}}`
	for _, op := range []string{"CastWorker", "CastWorkerInstructed"} {
		r[op] = `{"data":{"castWorker":` + irisWorkerJSON + `}}`
	}
	for _, op := range []string{"CastWorkerPreview", "CastWorkerPreviewInstructed"} {
		r[op] = `{"data":{"castWorkerPreview":` + preview + `}}`
	}
	for _, op := range []string{"RetireWorker", "RetireWorkerInstructed"} {
		r[op] = `{"data":{"retireWorker":` + irisWorkerJSON + `}}`
	}
	for _, op := range []string{"DeleteWorker", "DeleteWorkerInstructed"} {
		r[op] = `{"data":{"deleteWorker":true}}`
	}
	for _, op := range []string{"EnsureAppMember", "EnsureAppMemberInstructed"} {
		r[op] = `{"data":{"ensureAppMember":` + member + `}}`
	}
	for _, op := range []string{"RemoveAppMember", "RemoveAppMemberInstructed"} {
		r[op] = `{"data":{"removeAppMember":true}}`
	}
	return r
}

// cmd1594 is one of the six doors: how to run it against an App ref (the
// staffing doors that resolve a worker take the worker URN and ignore app,
// unless they name the worker by name), the plain document, and the
// instructed one.
type cmd1594 struct {
	name     string
	args     func(app string, extra ...string) []string
	plain    string
	instr    string
	plainDoc string
	// byWorker: the target App is the resolved worker's own (retire, rm).
	byWorker bool
}

func with(args []string, extra []string) []string { return append(args, extra...) }

var cmds1594 = []cmd1594{
	{name: "cast", plain: "CastWorker", instr: "CastWorkerInstructed", plainDoc: gen.CastWorker_Operation,
		args: func(app string, extra ...string) []string {
			return with([]string{"team", "worker", "cast", "--app", app, "--name", "Uma", "--agent", agent1594, "--no-team"}, extra)
		}},
	{name: "cast --dry-run", plain: "CastWorkerPreview", instr: "CastWorkerPreviewInstructed", plainDoc: gen.CastWorkerPreview_Operation,
		args: func(app string, extra ...string) []string {
			return with([]string{"team", "worker", "cast", "--app", app, "--name", "Uma", "--agent", agent1594, "--no-team", "--dry-run"}, extra)
		}},
	{name: "retire", plain: "RetireWorker", instr: "RetireWorkerInstructed", plainDoc: gen.RetireWorker_Operation, byWorker: true,
		args: func(app string, extra ...string) []string {
			return with([]string{"team", "worker", "retire", irisURN1592, "--yes"}, extra)
		}},
	{name: "rm", plain: "DeleteWorker", instr: "DeleteWorkerInstructed", plainDoc: gen.DeleteWorker_Operation, byWorker: true,
		args: func(app string, extra ...string) []string {
			return with([]string{"team", "worker", "rm", irisURN1592, "--yes", "--confirm-no-external-attribution"}, extra)
		}},
	{name: "member add", plain: "EnsureAppMember", instr: "EnsureAppMemberInstructed", plainDoc: gen.EnsureAppMember_Operation,
		args: func(app string, extra ...string) []string {
			return with([]string{"app", "member", "add", "--app", app, "--user", "ada@acme.com", "--role", "member", "--yes"}, extra)
		}},
	{name: "member remove", plain: "RemoveAppMember", instr: "RemoveAppMemberInstructed", plainDoc: gen.RemoveAppMember_Operation,
		args: func(app string, extra ...string) []string {
			return with([]string{"app", "member", "remove", "--app", app, "--user", "ada@acme.com", "--yes"}, extra)
		}},
}

func run1594(o run1592) run1592 {
	if o.response == nil {
		o.response = responses1594()
	}
	return o
}

// Acceptance 1: with the flag the one request is the instructed document and
// carries instruction {kind, ref}; the ref is everything after the FIRST
// colon, byte for byte.
func TestStaffingInstructionWire1594(t *testing.T) {
	for _, tc := range []struct{ flag, kind, ref string }{
		{instrTask1592, "TASK", taskRef1592},
		{"team-chat-message:hrn:chatmsg:acme.com:eng-team:70910", "TEAM_CHAT_MESSAGE", "hrn:chatmsg:acme.com:eng-team:70910"},
		{"live-chat:claude-desktop:conv:abc:42", "LIVE_CHAT", "claude-desktop:conv:abc:42"},
	} {
		for _, c := range cmds1594 {
			calls, r := run1594(run1592{}).exec(t, c.args(app1594, "--instruction", tc.flag)...)
			if r.err != nil {
				t.Fatalf("%s %s: %v\n%s", c.name, tc.flag, r.err, r.errOut)
			}
			got := writes1592(calls, c.plain, c.instr)
			if len(got) != 1 || got[0].Op != c.instr {
				t.Fatalf("%s: writes %+v", c.name, got)
			}
			want := map[string]any{"kind": tc.kind, "ref": tc.ref}
			if !reflect.DeepEqual(got[0].Vars["instruction"], want) {
				t.Fatalf("%s instruction = %#v, want %#v", c.name, got[0].Vars["instruction"], want)
			}
			if !strings.Contains(got[0].Query, "instruction: $instruction") {
				t.Fatalf("%s document does not pass the instruction: %s", c.name, got[0].Query)
			}
		}
	}
}

// Acceptance 1: without the flag the unchanged document goes out, byte for
// byte the generated one, naming no instruction, with no instruction key.
func TestStaffingNoInstructionKeepsThePlainDocuments1594(t *testing.T) {
	for _, c := range cmds1594 {
		calls, r := run1594(run1592{bound: true}).exec(t, c.args(app1594)...)
		if r.err != nil {
			t.Fatalf("%s: %v\n%s", c.name, r.err, r.errOut)
		}
		got := writes1592(calls, c.plain, c.instr)
		if len(got) != 1 || got[0].Op != c.plain {
			t.Fatalf("%s: writes %+v", c.name, got)
		}
		if got[0].Query != c.plainDoc {
			t.Fatalf("%s: the plain document changed:\n%s", c.name, got[0].Query)
		}
		if strings.Contains(got[0].Query, "instruction") || strings.Contains(string(got[0].RawVars), "instruction") {
			t.Fatalf("%s: the plain call names instruction: %s %s", c.name, got[0].Query, got[0].RawVars)
		}
	}
}

// Acceptance 3: a bad kind or blank ref is exit 2 with no request at all,
// before the confirmation (no --yes here, so a later refusal would be the
// confirmation's, which this test tells apart by its message).
func TestStaffingInstructionShapeRefusedLocally1594(t *testing.T) {
	for _, bad := range []string{"", "task", "task:", ":" + taskRef1592, "TASK:" + taskRef1592, "chat:x", "task: "} {
		for _, c := range cmds1594 {
			args := c.args(app1594, "--instruction", bad)
			var noYes []string
			for _, a := range args {
				if a != "--yes" {
					noYes = append(noYes, a)
				}
			}
			for _, a := range [][]string{args, noYes} {
				calls, r := run1594(run1592{bound: true}).exec(t, a...)
				if exitCodeFor(r.err) != 2 || len(*calls) != 0 || !strings.Contains(r.err.Error(), "--instruction") {
					t.Fatalf("%s %q: exit %d, calls %+v, err %v", c.name, bad, exitCodeFor(r.err), *calls, r.err)
				}
			}
		}
	}
}

// Acceptance 2: the attribution matrix with the flag, per command. Every
// lookup is headerless; the write carries the session only when the binding
// is verified for the target App, and otherwise goes out headerless with the
// not-used note (no refusal: these are the person's writes today).
func TestStaffingInstructionAttributionMatrix1594(t *testing.T) {
	otherWorker := strings.Replace(irisWorkerJSON, irisInIrisApp, irisOtherApp, 1)
	foreign := func(b map[string]any) { b["server"] = "https://elsewhere.example.test" }
	incomplete := func(b map[string]any) { b["workerId"] = "" }
	for _, c := range cmds1594 {
		type cell struct {
			name    string
			run     run1592
			app     string
			extra   []string
			session string
			note    string
		}
		cells := []cell{
			{name: "same App by id", run: run1592{bound: true}, app: app1594, session: "s-new"},
			{name: "same App by alias", run: run1592{bound: true}, app: alias1594, session: "s-new"},
			{name: "foreign server", run: run1592{bound: true, mutate: foreign}, app: app1594, note: "belongs to"},
			{name: "incomplete binding", run: run1592{bound: true, mutate: incomplete}, app: app1594, note: "incomplete"},
			{name: "unbound shell", run: run1592{}, app: app1594},
			{name: "outside any worktree", run: run1592{noWorktree: true}, app: app1594},
		}
		if c.byWorker {
			r := responses1594()
			r["GetWorker"] = `{"data":{"worker":` + otherWorker + `}}`
			cells = append(cells,
				// The worker names its own App; an ambient --app elsewhere is not the target.
				cell{name: "ambient --app elsewhere", run: run1592{bound: true}, app: app1594, extra: []string{"--app", otherApp1594}, session: "s-new"},
				cell{name: "worker in another App", run: run1592{bound: true, response: r}, app: app1594, note: "different App"},
			)
		} else {
			r := responses1594()
			r["TeamAppIdentity"] = otherAppIdentity1594
			hidden := responses1594()
			hidden["TeamAppIdentity"] = `{"data":{"app":null}}`
			cells = append(cells,
				cell{name: "target another App", run: run1592{bound: true, response: r}, app: "acme.com:ops", note: "different App"},
				// review F2: app(ref:) is org-member-only; an unreadable App
				// is not called "a different App".
				cell{name: "target App unreadable", run: run1592{bound: true, response: hidden}, app: alias1594, note: "can't be read as you"},
			)
		}
		for _, tc := range cells {
			t.Run(c.name+"/"+tc.name, func(t *testing.T) {
				args := c.args(tc.app, append([]string{"--instruction", instrTask1592}, tc.extra...)...)
				calls, r := run1594(tc.run).exec(t, args...)
				if r.err != nil {
					t.Fatalf("%v\n%s", r.err, r.errOut)
				}
				wantOnlyWriteSession(t, calls, c.instr, tc.session)
				if tc.note != "" && !strings.Contains(r.errOut, "not used (") {
					t.Fatalf("want the not-used note, got %q", r.errOut)
				}
				if tc.note != "" && !strings.Contains(r.errOut, tc.note) {
					t.Fatalf("note should say %q, got %q", tc.note, r.errOut)
				}
				if tc.note == "" && strings.Contains(r.errOut, "not used") {
					t.Fatalf("unexpected note %q", r.errOut)
				}
			})
		}
	}
}

// Acceptance 2: a rebind during the target lookup is never attached: the
// write goes out headerless with the note. The lookup is the worker read for
// retire/rm and the App alias read for cast and the member doors.
func TestStaffingInstructionRebindDuringLookup1594(t *testing.T) {
	for _, c := range cmds1594 {
		lookup := "TeamAppIdentity"
		if c.byWorker {
			lookup = "GetWorker"
		}
		rebind := func(op string) {
			if op == lookup {
				rewriteBinding1592(t, func(b map[string]any) { b["sessionId"] = "s-other" })
			}
		}
		calls, r := run1594(run1592{bound: true, onOp: rebind}).exec(t, c.args(alias1594, "--instruction", instrTask1592)...)
		if r.err != nil {
			t.Fatalf("%s: %v", c.name, r.err)
		}
		if len(writes1592(calls, lookup)) == 0 {
			t.Fatalf("%s: the staged lookup %s never ran: %+v", c.name, lookup, *calls)
		}
		wantOnlyWriteSession(t, calls, c.instr, "")
		if !strings.Contains(r.errOut, "changed") {
			t.Fatalf("%s: want the changed note, got %q", c.name, r.errOut)
		}
	}
}

// Acceptance 2, unbound start: a binding that appears during the worker
// lookup is a change too (retire, rm). Cast and the member doors make no
// lookup when unbound, so the send-time re-read is the only window there.
func TestStaffingInstructionBindingAppears1594(t *testing.T) {
	for _, c := range cmds1594 {
		if !c.byWorker {
			continue
		}
		calls, r := run1594(run1592{appearOn: "GetWorker"}).exec(t, c.args(app1594, "--instruction", instrTask1592)...)
		if r.err != nil {
			t.Fatalf("%s: %v", c.name, r.err)
		}
		wantOnlyWriteSession(t, calls, c.instr, "")
		if !strings.Contains(r.errOut, "appeared") {
			t.Fatalf("%s: want the appeared note, got %q", c.name, r.errOut)
		}
	}
}

// Acceptance 2: without the flag every door keeps today's attribution: the
// person's write, headerless even in a bound worktree.
func TestStaffingNoInstructionAttributionUnchanged1594(t *testing.T) {
	for _, c := range cmds1594 {
		calls, r := run1594(run1592{bound: true}).exec(t, c.args(alias1594)...)
		if r.err != nil {
			t.Fatalf("%s: %v", c.name, r.err)
		}
		wantOnlyWriteSession(t, calls, c.plain, "")
		if strings.Contains(r.errOut, "not used") {
			t.Fatalf("%s: no note without the flag, got %q", c.name, r.errOut)
		}
	}
}

// Ask 4: an instructed preview through the worker session says the claim was
// recorded; one that went out as the person does not.
func TestStaffingInstructedPreviewReceipt1594(t *testing.T) {
	args := cmds1594[1].args(app1594, "--instruction", instrTask1592)
	_, r := run1594(run1592{bound: true}).exec(t, args...)
	if r.err != nil || !strings.Contains(r.out, "went out through the worker session") {
		t.Fatalf("worker preview: err %v out %q", r.err, r.out)
	}
	_, r = run1594(run1592{}).exec(t, args...)
	if r.err != nil || strings.Contains(r.out, "worker session") {
		t.Fatalf("person preview: err %v out %q", r.err, r.out)
	}
}

// Acceptance 4: refusals keep their codes, exit 8 for FORBIDDEN, serverCode
// in --json, after exactly one write; never retried without the claim.
func TestStaffingInstructionRefusalsPassThrough1594(t *testing.T) {
	for _, tc := range []struct {
		code string
		exit int
	}{{"FORBIDDEN", 8}, {"BAD_USER_INPUT", 2}} {
		for _, c := range cmds1594 {
			resp := responses1594()
			resp[c.instr] = gqlErrorJSON(tc.code)
			calls, res := run1594(run1592{bound: true, response: resp}).exec(t, c.args(app1594, "--instruction", instrTask1592, "--json")...)
			if exitCodeFor(res.err) != tc.exit || len(writes1592(calls, c.instr)) != 1 || len(writes1592(calls, c.plain)) != 0 {
				t.Fatalf("%s %s: exit %d calls %+v", c.name, tc.code, exitCodeFor(res.err), *calls)
			}
			var env struct {
				Error struct {
					Code       int    `json:"code"`
					ServerCode string `json:"serverCode"`
				} `json:"error"`
			}
			if err := json.Unmarshal([]byte(res.out), &env); err != nil || env.Error.Code != tc.exit || env.Error.ServerCode != tc.code {
				t.Fatalf("%s %s: envelope %s (%v)", c.name, tc.code, res.out, err)
			}
		}
	}
}

// Ask 4: each door's help carries the shared instructed paragraph with its
// grant, the dry-run caveat on cast, and the minimum server.
func TestStaffingInstructionHelp1594(t *testing.T) {
	for _, tc := range []struct {
		args  []string
		grant string
	}{
		{[]string{"team", "worker", "cast", "--help"}, "workers.staff"},
		{[]string{"team", "worker", "retire", "--help"}, "workers.staff"},
		{[]string{"team", "worker", "rm", "--help"}, "workers.staff"},
		{[]string{"app", "member", "add", "--help"}, "app.members"},
		{[]string{"app", "member", "remove", "--help"}, "app.members"},
	} {
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(tc.args)
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		help := strings.Join(strings.Fields(out.String()), " ")
		for _, want := range []string{"--instruction", "own " + tc.grant + " grant", "host-launched session's instruction is ignored", "517eb1db", "holder answers for the claim", "never that an instruction really happened"} {
			if !strings.Contains(help, want) {
				t.Errorf("%v help lacks %q", tc.args[:3], want)
			}
		}
	}
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "cast", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if help := strings.Join(strings.Fields(out.String()), " "); !strings.Contains(help, "preview is NOT write-free") || strings.Contains(help, "runs the same checks and writes nothing") {
		t.Errorf("cast help must not call an instructed preview write-free")
	}
}

// A failed target-App lookup comes before anything is sent: exit with the
// lookup's own code, no write, and never the member doors' "outcome unknown".
func TestStaffingInstructionLookupFailureSendsNothing1594(t *testing.T) {
	for _, c := range cmds1594 {
		if c.byWorker {
			continue
		}
		for _, code := range []string{"FORBIDDEN", "INTERNAL_SERVER_ERROR"} {
			r := responses1594()
			r["TeamAppIdentity"] = gqlErrorJSON(code)
			calls, res := run1594(run1592{bound: true, response: r}).exec(t, c.args(alias1594, "--instruction", instrTask1592)...)
			if res.err == nil || len(writes1592(calls, c.plain, c.instr)) != 0 {
				t.Fatalf("%s %s: err %v calls %+v", c.name, code, res.err, *calls)
			}
			if code == "FORBIDDEN" && exitCodeFor(res.err) != 8 {
				t.Fatalf("%s: exit %d, want 8", c.name, exitCodeFor(res.err))
			}
			if strings.Contains(res.err.Error(), "outcome unknown") {
				t.Fatalf("%s %s: a lookup failure is not an unknown write outcome: %v", c.name, code, res.err)
			}
		}
	}
}
