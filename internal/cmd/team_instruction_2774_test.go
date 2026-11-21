package cmd

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2774 (child of #1617): --instruction on `team pause`/`resume`, `team
// stop set`/`lift`, `team wake-settings set`, `team host issue` and the
// `team wakes due` read (plain and --preview). With the flag the claim is
// sent once, exactly as parsed from the flag value — the trailing space in
// "client: 7 " must survive — on the Instructed twin document, through the
// verified manager-held session. Without it the request is byte-identical to
// the pre-2774 wire: the plain operation, the plain document, no instruction
// variable and no session header.

const instruction2774Claim = "live-chat:client: 7 "

func instruction2774Cases() []struct {
	name string
	args []string // without --server/--json extras; --json included
	op   string   // plain operation name
	body string   // canned response body for both twins
	// plainSession is the session header the UNCLAIMED run carries in a bound
	// worktree: issueHostOperation is an authored write (#871), so its plain
	// run rides the bound worker's session; the other doors' plain runs are
	// headerless. Every CLAIMED run rides the binding's verified session.
	plainSession string
} {
	return []struct {
		name         string
		args         []string
		op           string
		body         string
		plainSession string
	}{
		{"pause", []string{"team", "pause", "--json"}, "SetTeamPause",
			`{"data":{"setTeamPause":{"teamPausedAt":"2026-10-31T05:00:00Z"}}}`, ""},
		{"resume", []string{"team", "resume", "--json"}, "SetTeamPause",
			`{"data":{"setTeamPause":{"teamPausedAt":null}}}`, ""},
		{"stop-set", []string{"team", "stop", "set", "--scope", "worker", "--level", "pause", "--worker", "wkr1", "--json"}, "TeamStopSignalSet",
			`{"data":{"setTeamStopSignal":` + stopSignalJSON + `}}`, ""},
		{"stop-lift", []string{"team", "stop", "lift", "sig1", "--json"}, "TeamStopSignalLift",
			`{"data":{"liftTeamStopSignal":{"changed":true,"signal":` + endedStopSignalJSON + `}}}`, ""},
		{"wake-settings-set", []string{"team", "wake-settings", "set", "--worker", c1292Worker, "--mentions", "off", "--json"}, "SetWorkerWakeSettings",
			`{"data":{"setWorkerWakeSettings":` + c1292Stored + `}}`, ""},
		{"host-issue", []string{"team", "host", "issue", "check", "--host", "orca1", "--worker", "Jonas", "--json"}, "IssueHostOperation",
			`{"data":{"issueHostOperation":{"id":"hop1","appId":"capp100000000000000000000","hostId":"orca1","type":"check","args":{"x":"1"},"workerId":"wkr1","worker":{"id":"wkr1","name":"Iris"},"status":"ISSUED","issuedBy":null,"issuedByUserId":"usr1","issuedByWorkerId":null,"issuedAt":"2026-11-21T09:00:00Z","collectedAt":null,"finishedAt":null,"result":null,"failureReason":null}}}`, "s-new"},
		{"wakes-due", []string{"team", "wakes", "due", "--json"}, "TeamDueWakes",
			c1279Page("OK", 0, c1279TickWake), ""},
		{"wakes-due-preview", []string{"team", "wakes", "due", "--preview", "--json"}, "TeamDueWakesPreview",
			c1279Page("OK", 0, c1279TickWake), ""},
	}
}

// instruction2774PlainVars is the variable map of one call without its
// instruction entry, for the claimed-vs-plain comparison.
func instruction2774PlainVars(vars map[string]any) map[string]any {
	out := make(map[string]any, len(vars))
	for k, v := range vars {
		if k != "instruction" {
			out[k] = v
		}
	}
	return out
}

func TestTeamInstruction2774Wire(t *testing.T) {
	for _, tc := range instruction2774Cases() {
		t.Run(tc.name, func(t *testing.T) {
			dir := teamGitDir(t)
			var plainOut string
			var plainVars map[string]any
			for _, claimed := range []bool{false, true} {
				// Both twins are answerable so a wrong one still completes and
				// is caught by the call assertions below, not by a transport
				// error.
				responses := map[string]string{
					tc.op:                tc.body,
					tc.op + "Instructed": tc.body,
				}
				srv, calls := attnServer(t, responses)
				writeTeamBindingIn(t, dir, srv.URL)
				f, out := testFactory(t)
				root := NewRootCmd(f)
				args := append([]string{"--server", srv.URL}, tc.args...)
				if claimed {
					args = append(args, "--instruction", instruction2774Claim)
				}
				root.SetArgs(args)
				if err := root.Execute(); err != nil {
					renderFailure(f, args, err)
					t.Fatal(err)
				}
				wantOp := tc.op
				wantQuery := plain2774Query(tc.op)
				wantSession := tc.plainSession
				if claimed {
					wantOp += "Instructed"
					wantQuery = instructed2774Query(tc.op)
					wantSession = "s-new"
				}
				var hits []attnCall
				var otherTwin int
				for _, c := range *calls {
					switch c.Op {
					case wantOp:
						hits = append(hits, c)
					case tc.op, tc.op + "Instructed":
						otherTwin++
					default:
						t.Errorf("unexpected op %s", c.Op)
					}
				}
				if len(hits) != 1 || otherTwin != 0 {
					t.Fatalf("target calls %d, other twin %d: %+v", len(hits), otherTwin, *calls)
				}
				hit := hits[0]
				if hit.Query != wantQuery {
					t.Fatalf("document is not the %s twin", map[bool]string{false: "plain", true: "Instructed"}[claimed])
				}
				if hit.Session != wantSession {
					t.Fatalf("session %q, want %q", hit.Session, wantSession)
				}
				if claimed {
					wantClaim := map[string]any{"kind": "LIVE_CHAT", "ref": "client: 7 "}
					if got := hit.Vars["instruction"]; !reflect.DeepEqual(got, wantClaim) {
						t.Fatalf("instruction %v, want %v (exactly as the flag gave it)", got, wantClaim)
					}
					if !reflect.DeepEqual(instruction2774PlainVars(hit.Vars), plainVars) {
						t.Fatalf("variables moved apart from instruction: %v vs %v", instruction2774PlainVars(hit.Vars), plainVars)
					}
					if out.String() != plainOut {
						t.Fatalf("output changed apart from the claim:\n%s\n%s", out.String(), plainOut)
					}
				} else {
					if _, ok := hit.Vars["instruction"]; ok {
						t.Fatalf("instruction sent without the flag: %v", hit.Vars)
					}
					if strings.Contains(hit.Query, "instruction") {
						t.Fatal("plain document mentions instruction")
					}
					plainOut = out.String()
					plainVars = hit.Vars
				}
			}
		})
	}
}

// plain2774Query and instructed2774Query name the generated documents the
// senders use. Equating the sent query with the gen constant does NOT catch
// selection drift by itself (the senders send those same constants); drift is
// caught by the struct casts in instruction_2774.go and the field-name
// comparison in TestTeamInstruction2774LiftFieldParity — everything but the
// lift response's outer struct, which sendTeamStopSignalLift builds by hand.
func plain2774Query(op string) string {
	switch op {
	case "SetTeamPause":
		return gen.SetTeamPause_Operation
	case "TeamStopSignalSet":
		return gen.TeamStopSignalSet_Operation
	case "TeamStopSignalLift":
		return gen.TeamStopSignalLift_Operation
	case "SetWorkerWakeSettings":
		return gen.SetWorkerWakeSettings_Operation
	case "IssueHostOperation":
		return gen.IssueHostOperation_Operation
	case "TeamDueWakes":
		return gen.TeamDueWakes_Operation
	case "TeamDueWakesPreview":
		return gen.TeamDueWakesPreview_Operation
	}
	panic("unknown op " + op)
}

func instructed2774Query(op string) string {
	switch op {
	case "SetTeamPause":
		return gen.SetTeamPauseInstructed_Operation
	case "TeamStopSignalSet":
		return gen.TeamStopSignalSetInstructed_Operation
	case "TeamStopSignalLift":
		return gen.TeamStopSignalLiftInstructed_Operation
	case "SetWorkerWakeSettings":
		return gen.SetWorkerWakeSettingsInstructed_Operation
	case "IssueHostOperation":
		return gen.IssueHostOperationInstructed_Operation
	case "TeamDueWakes":
		return gen.TeamDueWakesInstructed_Operation
	case "TeamDueWakesPreview":
		return gen.TeamDueWakesPreviewInstructed_Operation
	}
	panic("unknown op " + op)
}

// A malformed claim is a usage error before any request, on every command.
// Each case gets a bound worktree so the App scope resolves and the ONLY
// exit-2 left is the claim's own refusal: without a binding or --app these
// doors refuse with "no team App" first, which would pass this test even for
// a mutant that silently drops a malformed claim (#2774 review, Jonas F1).
func TestTeamInstruction2774UsageBeforeRequests(t *testing.T) {
	for _, tc := range instruction2774Cases() {
		for _, bad := range []string{"", "task:", "unknown:ref"} {
			t.Run(tc.name+"/"+bad, func(t *testing.T) {
				dir := teamGitDir(t)
				// No canned answer at all: any request fails the test.
				srv, calls := attnServer(t, map[string]string{})
				writeTeamBindingIn(t, dir, srv.URL)
				f, _ := testFactory(t)
				root := NewRootCmd(f)
				args := append(append([]string{"--server", srv.URL}, tc.args...), "--instruction", bad)
				root.SetArgs(args)
				err := root.Execute()
				if err == nil || exitCodeFor(err) != 2 || !strings.Contains(err.Error(), "--instruction") || len(*calls) != 0 {
					t.Fatalf("err=%v (exit %d) calls=%d: the refusal must be the claim's own", err, exitCodeFor(err), len(*calls))
				}
			})
		}
	}
}

// A binding too broken to name the App is a refusal before any request on
// the App-scoped doors — the claim never rides an App it cannot verify. The
// refusal comes from scope resolution reading the binding, which is
// fail-closed by construction. wake-settings set is the exception, not an
// omission: its write names no App (the server resolves the target), so a
// corrupt binding there is the #1594 person fallback, pinned below.
func TestTeamInstruction2774RefusesUnreadableBinding(t *testing.T) {
	for _, tc := range instruction2774Cases() {
		if tc.name == "wake-settings-set" {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			dir := teamGitDir(t)
			if err := os.WriteFile(dir+"/hadron-team-session.json", []byte("{not json"), 0o600); err != nil {
				t.Fatal(err)
			}
			srv, calls := attnServer(t, map[string]string{
				tc.op:                tc.body,
				tc.op + "Instructed": tc.body,
			})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			args := append(append([]string{"--server", srv.URL}, tc.args...), "--instruction", instruction2774Claim)
			root.SetArgs(args)
			err := root.Execute()
			if err == nil || len(*calls) != 0 {
				t.Fatalf("err=%v calls=%d (the claim must refuse before any request)", err, len(*calls))
			}
		})
	}
}

// A binding that cannot carry the claim sends the write out as the signed-in
// person — headerless, on the Instructed twin, with the claim still attached
// and a note on stderr. wrong-server covers a representative App-scoped write,
// the wake-settings write, host issue and the read; corrupt covers
// wake-settings, whose write names no App, so an unreadable binding falls
// back (#1594) where the App-scoped doors refuse above. The wake-settings and
// host-issue rows also pin the as-person guard (cli#2491 F1): the fallback
// must stay headerless, never borrowing the unverified binding's session.
func TestTeamInstruction2774MismatchedBindingPersonFallback(t *testing.T) {
	for _, row := range []struct{ name, mode string }{
		{"pause", "wrong-server"},
		{"wake-settings-set", "wrong-server"},
		{"wake-settings-set", "corrupt"},
		{"host-issue", "wrong-server"},
		{"wakes-due", "wrong-server"},
	} {
		t.Run(row.name+"/"+row.mode, func(t *testing.T) {
			var tc struct {
				name string
				args []string
				op   string
				body string
			}
			for _, c := range instruction2774Cases() {
				if c.name == row.name {
					tc.name, tc.args, tc.op, tc.body = c.name, c.args, c.op, c.body
				}
			}
			if tc.name == "" {
				t.Fatalf("unknown case %q", row.name)
			}
			dir := teamGitDir(t)
			srv, calls := attnServer(t, map[string]string{
				tc.op:                tc.body,
				tc.op + "Instructed": tc.body,
			})
			writeTeamBindingIn(t, dir, srv.URL)
			if row.mode == "wrong-server" {
				rewriteBinding1592(t, func(b map[string]any) { b["server"] = "https://other.invalid" })
			} else {
				if err := os.WriteFile(dir+"/hadron-team-session.json", []byte("{not json"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			args := append(append([]string{"--server", srv.URL}, tc.args...), "--instruction", instruction2774Claim)
			root.SetArgs(args)
			err := root.Execute()
			if err != nil {
				t.Fatal(err)
			}
			if len(*calls) != 1 {
				t.Fatalf("calls=%d, want exactly the one claimed call: %+v", len(*calls), *calls)
			}
			c := (*calls)[0]
			if c.Op != tc.op+"Instructed" || c.Session != "" || c.Vars["instruction"] == nil {
				t.Fatalf("person fallback must be headerless with the claim on the Instructed twin: %+v", c)
			}
			if got := f.IOStreams.ErrOut.(*strings.Builder).String(); !strings.Contains(got, "worker session is not used") {
				t.Fatalf("stderr lacks the fallback note: %s", got)
			}
		})
	}
}

// --as-person promises no worker session on this write; --instruction claims
// the verified session's attribution. The pair is refused rather than
// resolved silently (#2774 review, Joran P2-2).
func TestTeamInstruction2774HostIssueAsPersonExclusive(t *testing.T) {
	dir := teamGitDir(t)
	srv, calls := attnServer(t, map[string]string{})
	writeTeamBindingIn(t, dir, srv.URL)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"--server", srv.URL, "team", "host", "issue", "check", "--host", "orca1", "--worker", "Jonas",
		"--as-person", "--instruction", instruction2774Claim, "--json"})
	err := root.Execute()
	if err == nil || exitCodeFor(err) != 2 || len(*calls) != 0 {
		t.Fatalf("err=%v (exit %d) calls=%d: --as-person and --instruction must refuse together", err, exitCodeFor(err), len(*calls))
	}
}

// A null liftTeamStopSignal result with no error must keep the plain twin's
// shape — a non-nil response with a nil field — so the caller's nil guard,
// not a nil-pointer panic, answers it (#2774 review, Joran P2-1).
func TestTeamInstruction2774LiftNullResult(t *testing.T) {
	dir := teamGitDir(t)
	srv, calls := attnServer(t, map[string]string{
		"TeamStopSignalLift":           `{"data":{"liftTeamStopSignal":null}}`,
		"TeamStopSignalLiftInstructed": `{"data":{"liftTeamStopSignal":null}}`,
	})
	writeTeamBindingIn(t, dir, srv.URL)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"--server", srv.URL, "team", "stop", "lift", "sig1", "--instruction", instruction2774Claim, "--json"})
	err := root.Execute()
	if err == nil || exitCodeFor(err) != exitcode.Error || !strings.Contains(err.Error(), "no lift result") || len(*calls) != 1 {
		t.Fatalf("err=%v (exit %d) calls=%d: a null result is the guard's clean refusal, never a panic", err, exitCodeFor(err), len(*calls))
	}
}

// TestTeamInstruction2774LiftFieldParity pins what the struct casts cannot:
// the lift response is built by hand in sendTeamStopSignalLift, so a field
// selected by only one twin would silently zero. The generated response
// types must name the same fields at both levels.
func TestTeamInstruction2774LiftFieldParity(t *testing.T) {
	plain := reflect.TypeOf(gen.TeamStopSignalLiftResponse{})
	claimed := reflect.TypeOf(gen.TeamStopSignalLiftInstructedResponse{})
	topPlain, topClaimed := fieldNames2774(plain), fieldNames2774(claimed)
	if !reflect.DeepEqual(topPlain, topClaimed) {
		t.Fatalf("lift response top level drifted: %v vs %v", topPlain, topClaimed)
	}
	nestedPlain := reflect.TypeOf(gen.TeamStopSignalLiftLiftTeamStopSignalLiftStopSignalResult{})
	nestedClaimed := reflect.TypeOf(gen.TeamStopSignalLiftInstructedLiftTeamStopSignalLiftStopSignalResult{})
	if !reflect.DeepEqual(fieldNames2774(nestedPlain), fieldNames2774(nestedClaimed)) {
		t.Fatalf("lift result fields drifted: %v vs %v", fieldNames2774(nestedPlain), fieldNames2774(nestedClaimed))
	}
}

func fieldNames2774(t reflect.Type) []string {
	names := make([]string, t.NumField())
	for i := range names {
		names[i] = t.Field(i).Name
	}
	return names
}
