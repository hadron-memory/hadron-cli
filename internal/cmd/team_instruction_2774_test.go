package cmd

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
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
} {
	return []struct {
		name string
		args []string
		op   string
		body string
	}{
		{"pause", []string{"team", "pause", "--json"}, "SetTeamPause",
			`{"data":{"setTeamPause":{"teamPausedAt":"2026-10-31T05:00:00Z"}}}`},
		{"resume", []string{"team", "resume", "--json"}, "SetTeamPause",
			`{"data":{"setTeamPause":{"teamPausedAt":null}}}`},
		{"stop-set", []string{"team", "stop", "set", "--scope", "worker", "--level", "pause", "--worker", "wkr1", "--json"}, "TeamStopSignalSet",
			`{"data":{"setTeamStopSignal":` + stopSignalJSON + `}}`},
		{"stop-lift", []string{"team", "stop", "lift", "sig1", "--json"}, "TeamStopSignalLift",
			`{"data":{"liftTeamStopSignal":{"changed":true,"signal":` + endedStopSignalJSON + `}}}`},
		{"wake-settings-set", []string{"team", "wake-settings", "set", "--worker", c1292Worker, "--mentions", "off", "--json"}, "SetWorkerWakeSettings",
			`{"data":{"setWorkerWakeSettings":` + c1292Stored + `}}`},
		// --as-person on both passes: with a claim the write is as-person by
		// construction (cli#2491 F1 — an explicit instruction owns
		// attribution), so this holds every difference to the one flag.
		{"host-issue", []string{"team", "host", "issue", "check", "--host", "orca1", "--worker", "Jonas", "--as-person", "--json"}, "IssueHostOperation",
			`{"data":{"issueHostOperation":{"id":"hop1","appId":"capp100000000000000000000","hostId":"orca1","type":"check","args":{"x":"1"},"workerId":"wkr1","worker":{"id":"wkr1","name":"Iris"},"status":"ISSUED","issuedBy":null,"issuedByUserId":"usr1","issuedByWorkerId":null,"issuedAt":"2026-11-21T09:00:00Z","collectedAt":null,"finishedAt":null,"result":null,"failureReason":null}}}`},
		{"wakes-due", []string{"team", "wakes", "due", "--json"}, "TeamDueWakes",
			c1279Page("OK", 0, c1279TickWake)},
		{"wakes-due-preview", []string{"team", "wakes", "due", "--preview", "--json"}, "TeamDueWakesPreview",
			c1279Page("OK", 0, c1279TickWake)},
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
				wantSession := ""
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

// plain2774Query and instructed2774Query name the generated documents, so a
// twin's selection drifting from its plain twin's is caught here too.
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
func TestTeamInstruction2774UsageBeforeRequests(t *testing.T) {
	for _, tc := range instruction2774Cases() {
		for _, bad := range []string{"", "task:", "unknown:ref"} {
			t.Run(tc.name+"/"+bad, func(t *testing.T) {
				teamGitDir(t)
				// No canned answer at all: any request fails the test.
				srv, calls := attnServer(t, map[string]string{})
				f, _ := testFactory(t)
				root := NewRootCmd(f)
				args := append(append([]string{"--server", srv.URL}, tc.args...), "--instruction", bad)
				root.SetArgs(args)
				err := root.Execute()
				if exitCodeFor(err) != 2 || strings.Contains(err.Error(), "unknown flag") || len(*calls) != 0 {
					t.Fatalf("err=%v (exit %d) calls=%d", err, exitCodeFor(err), len(*calls))
				}
			})
		}
	}
}

// The claim must be refused, not silently dropped, when the binding cannot be
// verified — one representative write and the read (fail-closed, cli#1592's
// contract carried to the 2774 doors).
func TestTeamInstruction2774RefusesUnreadableBinding(t *testing.T) {
	for _, tc := range instruction2774Cases()[:1] { // the pause write
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
