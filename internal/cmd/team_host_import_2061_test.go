package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// hadron-cli#2061 (C6): `team host import` is a dry run unless --apply, and
// --apply is refused (nothing written) until the leave-alone write exists.

const importRoster2061 = `{"data":{"teamRoster":{"appId":"capp100000000000000000000","asOf":"2026-11-15T10:00:00Z","workers":[
 {"worker":{"id":"w1","kind":"WORKER","name":"Jonas","urn":"hrn:worker:t:t:jonas"}},
 {"worker":{"id":"w2","kind":"WORKER","name":"Iris","urn":"hrn:worker:t:t:iris"}},
 {"worker":{"id":"w3","kind":"WORKER","name":"Unlisted","urn":"hrn:worker:t:t:unlisted"}}]}}}`

func importResponses2061() map[string]string {
	r := responses2080()
	r["TeamHostImportRoster"] = importRoster2061
	r["TeamAppIdentity"] = teamAppIdentityJSON
	// The server already holds Iris exactly as the file says.
	r["TeamHostSetup"] = `{"data":{"hostSetup":{"source":"HOST","assignments":[{"id":"a9","hostId":"orca1","workerId":"w2","worker":{"name":"Iris"},"workdir":"iris-1","family":"claude","model":null,"createdAt":"2026-11-15T10:00:00Z","updatedAt":"2026-11-15T10:00:00Z"}]}}}`
	return r
}

func hostYAML2061(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	y := "app: hrn:app:t:t\nstate_file: " + filepath.Join(dir, "s.json") + "\nworktrees_root: " + dir + `
workers:
  Jonas:
    family: openai
    launch: ["/usr/bin/codex", "--api-key", "SECRET_LAUNCH_ARG"]
    worktrees: [jonas-1]
  Iris:
    family: claude
    launch: [claude]
    worktrees: [iris-1]
  Ghost:
    family: claude
    launch: [claude]
    worktrees: [ghost-1]
`
	p := filepath.Join(dir, "host.yaml")
	if err := os.WriteFile(p, []byte(y), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestTeamHostImport2061DryRunWritesNothingAndReports(t *testing.T) {
	calls, out, err := runTeamTask(t, importResponses2061(), "team", "host", "import", "--from", hostYAML2061(t), "--host", "orca1", "--json")
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"TeamHostAssign", "TeamHostAssignInstructed", "TeamHostSetSource", "TeamHostUnassign", "TeamRoster"} {
		noCall2080(t, calls, op)
	}
	if strings.Contains(out, "SECRET_LAUNCH_ARG") {
		t.Fatalf("a launch argument was printed: %s", out)
	}
	var got struct {
		Applied bool             `json:"applied"`
		Counts  map[string]int   `json:"counts"`
		Missing []string         `json:"missing"`
		Entries []map[string]any `json:"entries"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if got.Applied || got.Counts["create"] != 1 || got.Counts["unchanged"] != 1 || got.Counts["skip"] != 1 || len(got.Missing) != 1 || got.Missing[0] != "Unlisted" {
		t.Fatalf("report = %+v", got)
	}
	for _, e := range got.Entries {
		if e["profileStatus"] != "unmapped" || e["profile"] != nil {
			t.Errorf("entry %v: launch lines are never mapped before profiles exist", e)
		}
	}
}

func TestTeamHostImport2061RequiresFromAndHost(t *testing.T) {
	for _, args := range [][]string{{"team", "host", "import", "--host", "orca1"}, {"team", "host", "import", "--from", "x.yaml"}} {
		if _, _, err := runTeamTask(t, importResponses2061(), args...); err == nil || exitCodeFor(err) != exitcode.Usage {
			t.Errorf("%v: err = %v, want usage", args, err)
		}
	}
	if _, _, err := runTeamTask(t, importResponses2061(), "team", "host", "import", "--from", "/no/such/host.yaml", "--host", "orca1"); err == nil || exitCodeFor(err) != exitcode.Usage {
		t.Errorf("a missing file must be a usage error: %v", err)
	}
}

// Today's production server has no hostSetup (hadron-server#4241): the dry run
// still works and says so.
func TestTeamHostImport2061OldServerWithoutHostSetup(t *testing.T) {
	r := importResponses2061()
	r["TeamHostSetup"] = `{"errors":[{"message":"Cannot query field \"hostSetup\" on type \"Query\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`
	calls, out, err := runTeamTask(t, r, "team", "host", "import", "--from", hostYAML2061(t), "--host", "orca1", "--json")
	if err != nil {
		t.Fatalf("a dry run must not fail on a server without hostSetup: %v", err)
	}
	noCall2080(t, calls, "TeamHostAssign", "TeamHostAssignInstructed", "TeamHostSetSource")
	var got struct {
		HostSetup string         `json:"hostSetup"`
		Counts    map[string]int `json:"counts"`
		Entries   []struct {
			Ref, Action string
			Reason      *string
		} `json:"entries"`
	}
	if e := json.Unmarshal([]byte(out), &got); e != nil {
		t.Fatalf("not JSON: %v\n%s", e, out)
	}
	if got.HostSetup != "missing" || got.Counts["unchecked"] != 2 || got.Counts["create"] != 0 {
		t.Fatalf("report = %+v", got)
	}
	for _, e := range got.Entries {
		if e.Action == "unchecked" && (e.Reason == nil || !strings.Contains(*e.Reason, "server lacks host setup")) {
			t.Errorf("%s: reason %v", e.Ref, e.Reason)
		}
	}
	// A different failure is still an error.
	r["TeamHostSetup"] = `{"errors":[{"message":"nope","extensions":{"code":"FORBIDDEN"}}]}`
	if _, _, err := runTeamTask(t, r, "team", "host", "import", "--from", hostYAML2061(t), "--host", "orca1"); err == nil {
		t.Fatal("a FORBIDDEN read must not be reported as a missing server feature")
	}
}

// Jamir P3: a human reading a differs row sees what the server holds.
func TestTeamHostImport2061DiffersTextShowsTheServersAssignment(t *testing.T) {
	r := importResponses2061()
	r["TeamHostSetup"] = strings.Replace(r["TeamHostSetup"], `"workdir":"iris-1"`, `"workdir":"iris-OLD"`, 1)
	_, out, err := runTeamTask(t, r, "team", "host", "import", "--from", hostYAML2061(t), "--host", "orca1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "differs") || !strings.Contains(out, "server: iris-OLD/claude") {
		t.Fatalf("the table must show the server's assignment:\n%s", out)
	}
}

func TestTeamHostImport2061AppWarningFollowsServerIdentity(t *testing.T) {
	warnings := func(identity string) ([]string, string) {
		r := importResponses2061()
		r["TeamAppIdentity"] = identity
		_, out, err := runTeamTask(t, r, "team", "host", "import", "--from", hostYAML2061(t), "--host", "orca1", "--json")
		if err != nil {
			t.Fatal(err)
		}
		var got struct {
			FileApp  string   `json:"fileApp"`
			Warnings []string `json:"warnings"`
		}
		if e := json.Unmarshal([]byte(out), &got); e != nil {
			t.Fatal(e)
		}
		return got.Warnings, got.FileApp
	}
	if w, file := warnings(teamAppIdentityJSON); len(w) != 0 || file != "hrn:app:t:t" {
		t.Fatalf("a file App that resolves to the App read must not warn: %v %q", w, file)
	}
	if w, _ := warnings(`{"data":{"app":null}}`); len(w) != 1 || !strings.Contains(w[0], "hrn:app:t:t") {
		t.Fatalf("an unresolvable file App must warn: %v", w)
	}
}

// Jabari F2: an unknown roster is an error, never an all-skip plan.
func TestTeamHostImport2061NullRosterIsAnError(t *testing.T) {
	for name, resp := range map[string]string{"null": `{"data":{"teamRoster":null}}`, "missing": `{"data":{}}`} {
		r := importResponses2061()
		r["TeamHostImportRoster"] = resp
		_, out, err := runTeamTask(t, r, "team", "host", "import", "--from", hostYAML2061(t), "--host", "orca1", "--json")
		if err == nil || strings.Contains(out, `"entries"`) {
			t.Errorf("%s: err=%v out=%s: must fail before planning", name, err, out)
		}
	}
}

// Jabari F3: through the command, two aliases of one worker are a conflict when
// they ask for different things; the same worktree under two names is the host
// runtime's own refusal (a worktree belongs to one worker), reported as usage.
func TestTeamHostImport2061AliasesThroughTheCommand(t *testing.T) {
	write := func(body string) string {
		dir := t.TempDir()
		y := "app: hrn:app:t:t\nstate_file: " + filepath.Join(dir, "s.json") + "\nworktrees_root: " + dir + "\nworkers:\n" + body
		p := filepath.Join(dir, "host.yaml")
		if err := os.WriteFile(p, []byte(y), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	conflict := write("  Jonas:\n    family: claude\n    launch: [c]\n    worktrees: [jonas-1]\n  hrn:worker:t:t:jonas:\n    family: claude\n    launch: [c]\n    worktrees: [jonas-2]\n")
	_, out, err := runTeamTask(t, importResponses2061(), "team", "host", "import", "--from", conflict, "--host", "orca1", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Counts map[string]int `json:"counts"`
	}
	if e := json.Unmarshal([]byte(out), &got); e != nil || got.Counts["conflict"] != 2 || got.Counts["create"] != 0 {
		t.Fatalf("different asks for one worker must conflict: %v %s", e, out)
	}
	same := write("  Jonas:\n    family: claude\n    launch: [c]\n    worktrees: [jonas-1]\n  hrn:worker:t:t:jonas:\n    family: claude\n    launch: [c]\n    worktrees: [jonas-1]\n")
	if _, _, err := runTeamTask(t, importResponses2061(), "team", "host", "import", "--from", same, "--host", "orca1"); err == nil || exitCodeFor(err) != exitcode.Usage {
		t.Fatalf("one worktree under two names is refused when the file loads: %v", err)
	}
}

// Colin F1: a successful answer with a null hostSetup must not read as "no assignments".
func TestTeamHostImport2061NullHostSetupIsAnErrorNotAFalseCreatePlan(t *testing.T) {
	for name, resp := range map[string]string{
		"null":    `{"data":{"hostSetup":null}}`,
		"missing": `{"data":{}}`,
	} {
		r := importResponses2061()
		r["TeamHostSetup"] = resp
		_, out, err := runTeamTask(t, r, "team", "host", "import", "--from", hostYAML2061(t), "--host", "orca1", "--json")
		if err == nil || strings.Contains(out, `"create"`) {
			t.Errorf("%s: err=%v out=%s: must fail, never plan creates", name, err, out)
		}
	}
}

const leaveAloneOK2061 = `{"data":{"setWorkerLeaveAlone":{"workerId":"w1","leaveAlone":true,"reason":"r","by":"u","at":"2026-11-15T10:00:00Z"}}}`

func applyResponses2061() map[string]string {
	r := importResponses2061()
	r["SetWorkerLeaveAlone"] = leaveAloneOK2061
	r["GetWorkerLeaveAlone"] = `{"data":{"worker":{"id":"w1","leaveAlone":false}}}`
	// The confirmation of Jonas's own assignment (w1, orca1, jonas-1, codex).
	r["TeamHostAssign"] = `{"data":{"assignHostWorker":{"id":"a1","hostId":"orca1","workerId":"w1","worker":{"name":"Jonas"},"workdir":"jonas-1","family":"codex","model":null,"createdAt":"2026-11-15T10:00:00Z","updatedAt":"2026-11-15T10:00:00Z"}}}`
	return r
}

func ops2061(calls *[]attnCall, want ...string) []string {
	var got []string
	for _, c := range *calls {
		for _, w := range want {
			if c.Op == w {
				got = append(got, c.Op)
			}
		}
	}
	return got
}

func TestTeamHostImport2061ApplyMarksLeaveAloneThenAssignsOnlyTheCreates(t *testing.T) {
	calls, out, err := runTeamTask(t, applyResponses2061(), "team", "host", "import", "--from", hostYAML2061(t), "--host", "orca1", "--apply", "--yes", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	// Jonas is the only create (Iris is unchanged, Ghost has no roster worker).
	got := ops2061(calls, "GetWorkerLeaveAlone", "SetWorkerLeaveAlone", "TeamHostAssign")
	if len(got) != 3 || got[0] != "GetWorkerLeaveAlone" || got[1] != "SetWorkerLeaveAlone" || got[2] != "TeamHostAssign" {
		t.Fatalf("calls = %v, want read the mark, leave-alone, THEN assign, once each", got)
	}
	la := findCall(t, calls, "SetWorkerLeaveAlone").Vars["input"].(map[string]any)
	if la["worker"] != "w1" || la["leaveAlone"] != true || la["reason"] == nil {
		t.Fatalf("leave-alone input = %v", la)
	}
	if _, ok := la["appRef"]; ok {
		t.Fatalf("a worker id needs no appRef: %v", la)
	}
	as := findCall(t, calls, "TeamHostAssign").Vars["input"].(map[string]any)
	for k, v := range map[string]any{"appRef": "capp100000000000000000000", "worker": "w1", "hostId": "orca1", "workdir": "jonas-1", "family": "codex"} {
		if as[k] != v {
			t.Fatalf("assign input[%s] = %v, want %v (%v)", k, as[k], v, as)
		}
	}
	if _, ok := as["model"]; ok {
		t.Fatalf("no model is in the file: %v", as)
	}
	if _, ok := as["replace"]; ok {
		t.Fatalf("an import never replaces: %v", as)
	}
	var rep struct {
		Applied bool `json:"applied"`
		Entries []struct {
			Ref     string `json:"ref"`
			Applied *struct {
				LeaveAlone bool `json:"leaveAlone"`
				Assigned   bool `json:"assigned"`
			} `json:"applied"`
		} `json:"entries"`
	}
	if e := json.Unmarshal([]byte(out), &rep); e != nil || !rep.Applied {
		t.Fatalf("report: %v %s", e, out)
	}
	for _, e := range rep.Entries {
		if e.Ref == "Jonas" && (e.Applied == nil || !e.Applied.LeaveAlone || !e.Applied.Assigned) {
			t.Fatalf("Jonas = %+v", e)
		}
		if e.Ref != "Jonas" && e.Applied != nil {
			t.Fatalf("%s must not be written", e.Ref)
		}
	}
}

func TestTeamHostImport2061ApplyNeedsYesWhenNotInteractive(t *testing.T) {
	calls, _, err := runTeamTask(t, applyResponses2061(), "team", "host", "import", "--from", hostYAML2061(t), "--host", "orca1", "--apply")
	if err == nil || exitCodeFor(err) != exitcode.Usage {
		t.Fatalf("err = %v, want a usage refusal", err)
	}
	if got := ops2061(calls, "SetWorkerLeaveAlone", "TeamHostAssign"); len(got) != 0 {
		t.Fatalf("written without confirmation: %v", got)
	}
}

func TestTeamHostImport2061ApplyRefusedOnAServerWithoutHostSetup(t *testing.T) {
	r := applyResponses2061()
	r["TeamHostSetup"] = `{"errors":[{"message":"Cannot query field \"hostSetup\" on type \"Query\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`
	calls, _, err := runTeamTask(t, r, "team", "host", "import", "--from", hostYAML2061(t), "--host", "orca1", "--apply", "--yes")
	if err == nil || exitCodeFor(err) != exitcode.Usage || !strings.Contains(err.Error(), "nothing written") {
		t.Fatalf("err = %v", err)
	}
	if got := ops2061(calls, "SetWorkerLeaveAlone", "TeamHostAssign"); len(got) != 0 {
		t.Fatalf("written: %v", got)
	}
}

// A manager-write refusal leaves the SAFE half-state, reported, exit 1.
func TestTeamHostImport2061ApplyAssignRefusalIsReportedAndFails(t *testing.T) {
	r := applyResponses2061()
	r["TeamHostAssign"] = `{"errors":[{"message":"not yours to manage","extensions":{"code":"HOST_ASSIGNMENT_NOT_PERMITTED"}}]}`
	calls, out, err := runTeamTask(t, r, "team", "host", "import", "--from", hostYAML2061(t), "--host", "orca1", "--apply", "--yes", "--json")
	if err == nil || exitCodeFor(err) != exitcode.Error {
		t.Fatalf("err = %v, want exit 1", err)
	}
	if got := ops2061(calls, "SetWorkerLeaveAlone"); len(got) != 1 {
		t.Fatalf("leave-alone must still have been set (the safe half-state): %v", got)
	}
	if !strings.Contains(out, `"assigned": false`) || !strings.Contains(out, `"leaveAlone": true`) || !strings.Contains(out, "not yours to manage") {
		t.Fatalf("the report must show marked-not-assigned with the reason:\n%s", out)
	}
}

func TestTeamHostImport2061DryRunStillWritesNothing(t *testing.T) {
	calls, _, err := runTeamTask(t, applyResponses2061(), "team", "host", "import", "--from", hostYAML2061(t), "--host", "orca1")
	if err != nil {
		t.Fatal(err)
	}
	if got := ops2061(calls, "SetWorkerLeaveAlone", "TeamHostAssign"); len(got) != 0 {
		t.Fatalf("a dry run wrote: %v", got)
	}
}

// Rodolfo P2: the App check is a gate for the writes, not a warning printed after them.
func TestTeamHostImport2061ApplyRefusedWhenTheFileAppDoesNotResolve(t *testing.T) {
	r := applyResponses2061()
	r["TeamAppIdentity"] = `{"data":{"app":null}}`
	calls, _, err := runTeamTask(t, r, "team", "host", "import", "--from", hostYAML2061(t), "--host", "orca1", "--apply", "--yes")
	if err == nil || exitCodeFor(err) != exitcode.Usage || !strings.Contains(err.Error(), "nothing written") {
		t.Fatalf("err = %v, want a usage refusal that says nothing was written", err)
	}
	if got := ops2061(calls, "GetWorkerLeaveAlone", "SetWorkerLeaveAlone", "TeamHostAssign"); len(got) != 0 {
		t.Fatalf("an App mismatch must stop the apply before any worker is touched: %v", got)
	}
}

// Joaquin P2: an empty answer is not a confirmed write.
func TestTeamHostImport2061ApplyEmptyAssignAnswerIsNotReportedAsAssigned(t *testing.T) {
	r := applyResponses2061()
	r["TeamHostAssign"] = `{"data":{"assignHostWorker":null}}`
	_, out, err := runTeamTask(t, r, "team", "host", "import", "--from", hostYAML2061(t), "--host", "orca1", "--apply", "--yes", "--json")
	if err == nil || exitCodeFor(err) != exitcode.Error {
		t.Fatalf("err = %v, want exit 1", err)
	}
	if strings.Contains(out, `"assigned": true`) || !strings.Contains(out, `"unknown": true`) || !strings.Contains(out, "outcome unknown") {
		t.Fatalf("an unconfirmed assignment must read unknown, never assigned:\n%s", out)
	}
}

// Rodolfo P3: server error text is sanitised before it reaches a terminal.
func TestTeamHostImport2061ApplyErrorTextIsSanitised(t *testing.T) {
	r := applyResponses2061()
	r["TeamHostAssign"] = `{"errors":[{"message":"bad \u001b[31mRED\u001b]52;c;x\u0007 text","extensions":{"code":"HOST_ASSIGNMENT_NOT_PERMITTED"}}]}`
	_, out, _ := runTeamTask(t, r, "team", "host", "import", "--from", hostYAML2061(t), "--host", "orca1", "--apply", "--yes")
	if strings.ContainsRune(out, 0x1b) || strings.ContainsRune(out, 0x07) {
		t.Fatalf("control characters reached the terminal: %q", out)
	}
	if !strings.Contains(out, "applied Jonas") {
		t.Fatalf("the applied line is missing:\n%s", out)
	}
}

// Rodolfo P3: a mark the import did not make is kept, and says so.
func TestTeamHostImport2061ApplyKeepsAnExistingLeaveAloneMark(t *testing.T) {
	r := applyResponses2061()
	r["GetWorkerLeaveAlone"] = `{"data":{"worker":{"id":"w1","leaveAlone":true}}}`
	calls, out, err := runTeamTask(t, r, "team", "host", "import", "--from", hostYAML2061(t), "--host", "orca1", "--apply", "--yes", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := ops2061(calls, "SetWorkerLeaveAlone"); len(got) != 0 {
		t.Fatalf("overwrote an existing mark: %v", got)
	}
	if !strings.Contains(out, `"leaveAloneKept": true`) {
		t.Fatalf("the report must say the mark was kept:\n%s", out)
	}
}

// Jibril P2 (and Joaquin P2): through the command, a null or contradictory
// answer to either write is never reported as written and never advances to the
// dependent write.
func TestTeamHostImport2061ApplyUnconfirmedAnswersNeverAdvance(t *testing.T) {
	leave := map[string]string{
		"null leave-alone":        `{"data":{"setWorkerLeaveAlone":null}}`,
		"leave-alone false":       `{"data":{"setWorkerLeaveAlone":{"workerId":"w1","leaveAlone":false}}}`,
		"leave-alone other id":    `{"data":{"setWorkerLeaveAlone":{"workerId":"w9","leaveAlone":true}}}`,
		"missing leave-alone key": `{"data":{}}`,
	}
	for name, body := range leave {
		r := applyResponses2061()
		r["SetWorkerLeaveAlone"] = body
		calls, out, err := runTeamTask(t, r, "team", "host", "import", "--from", hostYAML2061(t), "--host", "orca1", "--apply", "--yes", "--json")
		if err == nil || exitCodeFor(err) != exitcode.Error {
			t.Errorf("%s: err = %v, want exit 1", name, err)
		}
		if got := ops2061(calls, "TeamHostAssign"); len(got) != 0 {
			t.Errorf("%s: assigned a worker whose mark was not confirmed: %v", name, got)
		}
		if strings.Contains(out, `"leaveAlone": true`) || strings.Contains(out, `"assigned": true`) {
			t.Errorf("%s: reported a write the server did not confirm:\n%s", name, out)
		}
	}
	assign := map[string]string{
		"null assign":      `{"data":{"assignHostWorker":null}}`,
		"assign other dir": `{"data":{"assignHostWorker":{"id":"a1","hostId":"orca1","workerId":"w1","workdir":"elsewhere","family":"codex","createdAt":"x","updatedAt":"x"}}}`,
		"assign other id":  `{"data":{"assignHostWorker":{"id":"a1","hostId":"orca1","workerId":"w9","workdir":"jonas-1","family":"codex","createdAt":"x","updatedAt":"x"}}}`,
	}
	for name, body := range assign {
		r := applyResponses2061()
		r["TeamHostAssign"] = body
		_, out, err := runTeamTask(t, r, "team", "host", "import", "--from", hostYAML2061(t), "--host", "orca1", "--apply", "--yes", "--json")
		if err == nil || exitCodeFor(err) != exitcode.Error || strings.Contains(out, `"assigned": true`) || !strings.Contains(out, `"unknown": true`) {
			t.Errorf("%s: err=%v out=%s", name, err, out)
		}
	}
}

// Joaquin P2: a lost answer is reported as UNKNOWN in the human text too, never
// as "NOT assigned" / "NOT written" (which would send an operator the wrong way).
func TestTeamHostImport2061ApplyHumanTextForUnknownOutcomes(t *testing.T) {
	for name, tc := range map[string]struct{ op, body string }{
		"assign":      {"TeamHostAssign", `{"data":{"assignHostWorker":null}}`},
		"leave-alone": {"SetWorkerLeaveAlone", `{"data":{"setWorkerLeaveAlone":null}}`},
	} {
		r := applyResponses2061()
		r[tc.op] = tc.body
		_, out, _ := runTeamTask(t, r, "team", "host", "import", "--from", hostYAML2061(t), "--host", "orca1", "--apply", "--yes")
		if !strings.Contains(out, "applied Jonas: UNKNOWN") || strings.Contains(out, "NOT assigned") || strings.Contains(out, "NOT written") {
			t.Errorf("%s: the text must say UNKNOWN and never NOT assigned/written:\n%s", name, out)
		}
	}
	// A definite refusal still says it was not written.
	r := applyResponses2061()
	r["TeamHostAssign"] = `{"errors":[{"message":"no","extensions":{"code":"HOST_ASSIGNMENT_NOT_PERMITTED"}}]}`
	_, out, _ := runTeamTask(t, r, "team", "host", "import", "--from", hostYAML2061(t), "--host", "orca1", "--apply", "--yes")
	if !strings.Contains(out, "leave-alone set, NOT assigned") {
		t.Errorf("a coded refusal is definite:\n%s", out)
	}
	// Rodolfo P3: an already-set mark that the import did not make is never described as the import's.
	r = applyResponses2061()
	r["GetWorkerLeaveAlone"] = `{"data":{"worker":{"id":"w1","leaveAlone":true}}}`
	r["TeamHostAssign"] = `{"errors":[{"message":"no","extensions":{"code":"HOST_ASSIGNMENT_NOT_PERMITTED"}}]}`
	_, out, _ = runTeamTask(t, r, "team", "host", "import", "--from", hostYAML2061(t), "--host", "orca1", "--apply", "--yes")
	if !strings.Contains(out, "kept, not the import's mark") || strings.Contains(out, "leave-alone set, NOT assigned") {
		t.Errorf("a kept mark must read as kept:\n%s", out)
	}
}

// Jamir P3 1, through the command: a file naming another server warns in the
// dry run and REFUSES --apply (nothing written), a file naming the active server does not.
func TestTeamHostImport2061ServerMismatchWarnsAndRefusesApply(t *testing.T) {
	yaml := func(server string) string {
		dir := t.TempDir()
		y := "server: " + server + "\napp: hrn:app:t:t\nstate_file: " + filepath.Join(dir, "s.json") + "\nworktrees_root: " + dir + "\nworkers:\n  Jonas:\n    family: openai\n    launch: [codex]\n    worktrees: [jonas-1]\n"
		p := filepath.Join(dir, "host.yaml")
		if err := os.WriteFile(p, []byte(y), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	calls, out, err := runTeamTask(t, applyResponses2061(), "team", "host", "import", "--from", yaml("https://another-server.example"), "--host", "orca1", "--json")
	if err != nil || !strings.Contains(out, "another-server.example") || !strings.Contains(out, `"fileServer"`) {
		t.Fatalf("a dry run must warn and name the file's server: %v\n%s", err, out)
	}
	if got := ops2061(calls, "SetWorkerLeaveAlone", "TeamHostAssign"); len(got) != 0 {
		t.Fatalf("a dry run wrote: %v", got)
	}
	calls, _, err = runTeamTask(t, applyResponses2061(), "team", "host", "import", "--from", yaml("https://another-server.example"), "--host", "orca1", "--apply", "--yes")
	if err == nil || exitCodeFor(err) != exitcode.Usage || !strings.Contains(err.Error(), "nothing written") {
		t.Fatalf("err = %v, want a usage refusal", err)
	}
	if got := ops2061(calls, "GetWorkerLeaveAlone", "SetWorkerLeaveAlone", "TeamHostAssign"); len(got) != 0 {
		t.Fatalf("a server mismatch must stop the apply before any worker is touched: %v", got)
	}
}

// A failed identity lookup fails CLOSED too: the apply is refused, nothing written.
func TestTeamHostImport2061ApplyRefusedWhenTheAppLookupFails(t *testing.T) {
	r := applyResponses2061()
	r["TeamAppIdentity"] = `{"errors":[{"message":"boom","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`
	calls, _, err := runTeamTask(t, r, "team", "host", "import", "--from", hostYAML2061(t), "--host", "orca1", "--apply", "--yes")
	if err == nil || exitCodeFor(err) != exitcode.Usage {
		t.Fatalf("err = %v, want a usage refusal", err)
	}
	if got := ops2061(calls, "GetWorkerLeaveAlone", "SetWorkerLeaveAlone", "TeamHostAssign"); len(got) != 0 {
		t.Fatalf("written although the comparison could not be made: %v", got)
	}
}

// Rook P3-6 / #2104 item 6: the success line says WHAT was written, so an operator can check it.
func TestTeamHostImport2061ApplySuccessLineNamesWhatWasWritten(t *testing.T) {
	_, out, err := runTeamTask(t, applyResponses2061(), "team", "host", "import", "--from", hostYAML2061(t), "--host", "orca1", "--apply", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "applied Jonas: assigned and leave-alone (host orca1, workdir jonas-1, family codex)") {
		t.Fatalf("the success line must name host, workdir and family:\n%s", out)
	}
}
