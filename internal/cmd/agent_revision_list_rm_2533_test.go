package cmd

import (
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2533 (server hadron-server#4488, merged as 699dccd1): `agent revision list`
// and `agent revision rm`. Fake HTTP only. The server's contract these pin: a
// list is newest first with slotLimit / highestSlotUsed / slotsRemaining; slot
// numbers are never reused, so a delete does NOT raise slotsRemaining; delete
// refuses the published revision (REVISION_PUBLISHED) and the only revision
// (REVISION_ONLY) before any write.

const revList2533 = `{"data":{"agentRevisions":{"revisions":[` +
	`{"revisionLoc":"revisions:rev-4","label":"launch","published":true,"createdAt":"2026-11-02T04:00:00Z"},` +
	`{"revisionLoc":"revisions:rev-2","label":"draft","published":false,"createdAt":"2026-11-02T02:00:00Z"}],` +
	`"slotLimit":5,"highestSlotUsed":4,"slotsRemaining":1}}}`

func TestAgentRevisionListSendsOneRequestAndPrintsTheContract(t *testing.T) {
	calls, out, err := runAgentSub(t, map[string]string{"AgentRevisions": revList2533}, "revision", "list", " hrn:agent:acme.com:bot ", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if len(calls.ops) != 1 || calls.ops[0] != "AgentRevisions" || calls.vars["AgentRevisions"]["agentRef"] != " hrn:agent:acme.com:bot " {
		t.Fatalf("one request, ref verbatim: ops=%v vars=%v", calls.ops, calls.vars)
	}
	var dto struct {
		Revisions                                  []map[string]any `json:"revisions"`
		SlotLimit, HighestSlotUsed, SlotsRemaining int
	}
	if e := json.Unmarshal([]byte(out), &dto); e != nil {
		t.Fatalf("%v\n%s", e, out)
	}
	if len(dto.Revisions) != 2 || dto.Revisions[0]["revisionLoc"] != "revisions:rev-4" || dto.Revisions[0]["published"] != true || len(dto.Revisions[0]) != 4 {
		t.Fatalf("revisions (server order, four keys): %v", dto.Revisions)
	}
	for _, k := range []string{`"slotLimit": 5`, `"highestSlotUsed": 4`, `"slotsRemaining": 1`} {
		if !strings.Contains(out, k) {
			t.Errorf("--json lacks %s:\n%s", k, out)
		}
	}
}

func TestAgentRevisionListTextShowsTheTableAndTheSlotsLine(t *testing.T) {
	_, out, err := runAgentSub(t, map[string]string{"AgentRevisions": revList2533}, "revision", "list", "agt1")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"REVISION", "LABEL", "PUBLISHED", "CREATED", "revisions:rev-4", "launch", "true",
		"slots: highest used 4 of 5; 1 saves left (a delete does not bring one back)"} {
		if !strings.Contains(out, want) {
			t.Errorf("text output lacks %q:\n%s", want, out)
		}
	}
}

func TestAgentRevisionListEmptyIsAnEmptyArrayNotNull(t *testing.T) {
	empty := `{"data":{"agentRevisions":{"revisions":[],"slotLimit":5,"highestSlotUsed":0,"slotsRemaining":5}}}`
	_, out, err := runAgentSub(t, map[string]string{"AgentRevisions": empty}, "revision", "list", "agt1", "--json")
	if err != nil || !strings.Contains(out, `"revisions": []`) || strings.Contains(out, "null") {
		t.Fatalf("err=%v\n%s", err, out)
	}
	_, out, err = runAgentSub(t, map[string]string{"AgentRevisions": empty}, "revision", "list", "agt1")
	if err != nil || !strings.Contains(out, "No revisions.") || !strings.Contains(out, "highest used 0 of 5; 5 saves left") {
		t.Fatalf("err=%v\n%s", err, out)
	}
}

func TestAgentRevisionListRefusesABrokenAnswerInsteadOfPrintingIt(t *testing.T) {
	for name, wire := range map[string]string{
		"null list":          `{"data":{"agentRevisions":null}}`,
		"null entry":         `{"data":{"agentRevisions":{"revisions":[null],"slotLimit":5,"highestSlotUsed":1,"slotsRemaining":4}}}`,
		"fractional slots":   `{"data":{"agentRevisions":{"revisions":[],"slotLimit":5.5,"highestSlotUsed":1,"slotsRemaining":4}}}`,
		"negative remaining": `{"data":{"agentRevisions":{"revisions":[],"slotLimit":5,"highestSlotUsed":1,"slotsRemaining":-1}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, out, err := runAgentSub(t, map[string]string{"AgentRevisions": wire}, "revision", "list", "agt1", "--json")
			if err == nil || exitCodeFor(err) != exitcode.Unavailable || out != "" {
				t.Fatalf("a broken answer is Unavailable and prints nothing: err=%v out=%q", err, out)
			}
		})
	}
}

func TestAgentRevisionRmNeedsYesWithoutATerminalAndSendsNothing(t *testing.T) {
	calls, _, err := runAgentSub(t, map[string]string{"DeleteAgentRevision": `{}`}, "revision", "rm", "agt1", "revisions:rev-2")
	if err == nil || exitCodeFor(err) != exitcode.Usage || len(calls.ops) != 0 {
		t.Fatalf("zero requests without --yes: err=%v ops=%v", err, calls.ops)
	}
}

const delOK2533 = `{"data":{"deleteAgentRevision":{"revisionLoc":"revisions:rev-2","label":"draft","published":false,"createdAt":"2026-11-02T02:00:00Z"}}}`

func TestAgentRevisionRmSendsOneRequestAndPrintsTheDeletedLoc(t *testing.T) {
	calls, out, err := runAgentSub(t, map[string]string{"DeleteAgentRevision": delOK2533}, "revision", "rm", " hrn:agent:acme.com:bot ", " revisions:rev-2 ", "--yes", "--json")
	if err != nil {
		t.Fatal(err)
	}
	v := calls.vars["DeleteAgentRevision"]
	if len(calls.ops) != 1 || v["agentRef"] != " hrn:agent:acme.com:bot " || v["revisionLoc"] != " revisions:rev-2 " {
		t.Fatalf("one request, both refs verbatim: ops=%v vars=%v", calls.ops, v)
	}
	var dto map[string]any
	if e := json.Unmarshal([]byte(out), &dto); e != nil || len(dto) != 2 || dto["revisionLoc"] != "revisions:rev-2" || dto["status"] != "deleted" {
		t.Fatalf("json=%s (%v)", out, e)
	}
	_, out, err = runAgentSub(t, map[string]string{"DeleteAgentRevision": delOK2533}, "revision", "rm", "agt1", "revisions:rev-2", "--yes")
	if err != nil || out != "✓ deleted revision revisions:rev-2\n" {
		t.Fatalf("err=%v out=%q", err, out)
	}
}

func TestAgentRevisionRmAsksOnATerminal(t *testing.T) {
	for answer, want := range map[string]int{"n\n": 0, "y\n": 1} {
		var n int32
		srv, _ := captureGraphQLFunc(t, func(string) string { atomic.AddInt32(&n, 1); return delOK2533 })
		f, _, errOut := testFactoryTTY(t, answer)
		root := NewRootCmd(f)
		root.SetArgs([]string{"agent", "revision", "rm", "agt1", "revisions:rev-2", "--server", srv.URL})
		e := root.Execute()
		if int(atomic.LoadInt32(&n)) != want {
			t.Errorf("answer %q: %d requests, want %d", answer, n, want)
		}
		if answer == "n\n" && (e == nil || exitCodeFor(e) != exitcode.Cancelled) {
			t.Errorf("declining is a cancellation: %v", e)
		}
		if answer == "y\n" && e != nil {
			t.Errorf("accepting must succeed: %v", e)
		}
		if !strings.Contains(errOut.String(), "revisions:rev-2") {
			t.Errorf("the prompt names the revision: %q", errOut.String())
		}
	}
}

func TestAgentRevisionRmRefusalsKeepTheirClassAndSendOnce(t *testing.T) {
	for _, tc := range []struct {
		code string
		want int
	}{
		{"REVISION_PUBLISHED", exitcode.Conflict}, // publish another first
		{"REVISION_ONLY", exitcode.Conflict},      // the Agent's only revision
		{"BAD_USER_INPUT", exitcode.Usage},        // "Revision not found: <loc>"
		{"FORBIDDEN", exitcode.Forbidden},         // not the Agent's manager
	} {
		t.Run(tc.code, func(t *testing.T) {
			calls, out, err := runAgentSub(t, map[string]string{
				"DeleteAgentRevision": `{"errors":[{"message":"refused","extensions":{"code":"` + tc.code + `"}}],"data":null}`,
			}, "revision", "rm", "agt1", "revisions:rev-2", "--yes")
			if err == nil || exitCodeFor(err) != tc.want || len(calls.ops) != 1 || out != "" {
				t.Fatalf("err=%v exit=%d ops=%v out=%q", err, exitCodeFor(err), calls.ops, out)
			}
		})
	}
}

func TestAgentRevisionRmAnAmbiguousFailureIsSentOnceAndNamesTheCheckOnItsLastLine(t *testing.T) {
	calls, _, err := runAgentSub(t, map[string]string{
		"DeleteAgentRevision": `{"errors":[{"message":"boom","extensions":{"code":"SOMETHING_UNMAPPED"}}],"data":null}`,
	}, "revision", "rm", "hrn:agent:acme.com:bot", "revisions:rev-2", "--yes")
	if err == nil || len(calls.ops) != 1 || !strings.Contains(err.Error(), "outcome unknown (sent once, not retried)") {
		t.Fatalf("err=%v ops=%v", err, calls.ops)
	}
	lines := strings.Split(err.Error(), "\n")
	if last := strings.TrimSpace(lines[len(lines)-1]); last != "hadron agent revision list hrn:agent:acme.com:bot" {
		t.Fatalf("the check command is alone on the last line, got %q (full: %q)", last, err.Error())
	}
}

func TestAgentRevisionRmAnEmptyOrNullAnswerIsNotSuccess(t *testing.T) {
	for name, wire := range map[string]string{
		"null result": `{"data":{"deleteAgentRevision":null}}`,
		"empty loc":   `{"data":{"deleteAgentRevision":{"revisionLoc":"","label":"x","published":false,"createdAt":"c"}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, out, err := runAgentSub(t, map[string]string{"DeleteAgentRevision": wire}, "revision", "rm", "agt1", "revisions:rev-2", "--yes")
			if err == nil || exitCodeFor(err) != exitcode.Unavailable || out != "" || !strings.Contains(err.Error(), "did not confirm the delete") {
				t.Fatalf("err=%v out=%q", err, out)
			}
		})
	}
}

func TestAgentRevisionListAndRmBlankArgumentsAreUsageWithNoRequest(t *testing.T) {
	for _, args := range [][]string{{"revision", "list", "  "}, {"revision", "rm", " ", "revisions:rev-2", "--yes"}, {"revision", "rm", "agt1", " ", "--yes"}} {
		calls, _, err := runAgentSub(t, nil, args...)
		if err == nil || exitCodeFor(err) != exitcode.Usage || len(calls.ops) != 0 {
			t.Fatalf("%v: err=%v ops=%v", args, err, calls.ops)
		}
	}
}

// The help must never promise what the server does not do: a delete does not free
// a slot (slot numbers are never reused).
func TestAgentRevisionHelpNeverPromisesThatADeleteFreesASlot(t *testing.T) {
	for _, page := range [][]string{{"agent", "revision"}, {"agent", "revision", "list"}, {"agent", "revision", "rm"}, {"agent", "revision", "restore"}} {
		h := flatHelp1053S2(t, page...)
		low := strings.ToLower(h)
		if strings.Contains(low, "frees a slot") || strings.Contains(low, "free a slot to") || strings.Contains(low, "recover a slot") {
			t.Errorf("%v help promises a freed slot:\n%s", page, h)
		}
	}
	if h := flatHelp1053S2(t, "agent", "revision", "rm"); !strings.Contains(h, "It does NOT free a slot") || !strings.Contains(h, "REVISION_PUBLISHED") || !strings.Contains(h, "REVISION_ONLY") {
		t.Errorf("rm help must say it does not free a slot and name both refusals:\n%s", h)
	}
	if h := flatHelp1053S2(t, "agent", "revision", "list"); !strings.Contains(h, "does not bring a save back") {
		t.Errorf("list help must say a delete does not bring a save back:\n%s", h)
	}
}

// Xan's re-gate acceptance 3 (cli#2533): with permanent slot numbers, a user at
// REVISION_SLOTS_EXHAUSTED who runs rm is STILL at the limit and restore is
// STILL refused. A future claim that rm recovers a slot (in help, output or the
// CLI's own behaviour) must fail this test. The fake answers as the server
// contract says: the delete succeeds, the list afterwards still reports no saves
// left, restore is refused with the server's own code.
func TestAgentRevisionAtSlotsExhaustedRmDoesNotRecoverAndRestoreIsStillRefused(t *testing.T) {
	full := `{"data":{"agentRevisions":{"revisions":[` +
		`{"revisionLoc":"revisions:rev-5","label":"launch","published":true,"createdAt":"2026-11-02T05:00:00Z"},` +
		`{"revisionLoc":"revisions:rev-2","label":"draft","published":false,"createdAt":"2026-11-02T02:00:00Z"}],` +
		`"slotLimit":5,"highestSlotUsed":5,"slotsRemaining":0}}}`
	afterDelete := `{"data":{"agentRevisions":{"revisions":[` +
		`{"revisionLoc":"revisions:rev-5","label":"launch","published":true,"createdAt":"2026-11-02T05:00:00Z"}],` +
		`"slotLimit":5,"highestSlotUsed":5,"slotsRemaining":0}}}`
	exhausted := `{"errors":[{"message":"No revision slots left for this Agent.","extensions":{"code":"REVISION_SLOTS_EXHAUSTED"}}],"data":null}`

	_, out, err := runAgentSub(t, map[string]string{"AgentRevisions": full}, "revision", "list", "agt1", "--json")
	if err != nil || !strings.Contains(out, `"slotsRemaining": 0`) {
		t.Fatalf("setup: the Agent is at the limit: err=%v\n%s", err, out)
	}
	_, out, err = runAgentSub(t, map[string]string{"DeleteAgentRevision": delOK2533}, "revision", "rm", "agt1", "revisions:rev-2", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(out), "slot") || strings.Contains(strings.ToLower(out), "free") {
		t.Fatalf("rm must not say anything about slots, it frees none: %q", out)
	}
	_, out, err = runAgentSub(t, map[string]string{"AgentRevisions": afterDelete}, "revision", "list", "agt1", "--json")
	if err != nil || !strings.Contains(out, `"slotsRemaining": 0`) || !strings.Contains(out, `"highestSlotUsed": 5`) {
		t.Fatalf("after the delete the list still reports no saves left: err=%v\n%s", err, out)
	}
	calls, out, err := runAgentSub(t, map[string]string{"RestoreRevision": exhausted}, "revision", "restore", "agt1", "revisions:rev-5")
	if err == nil || len(calls.ops) != 1 || out != "" || !strings.Contains(err.Error(), "No revision slots left") {
		t.Fatalf("restore is still refused with the server's message: err=%v ops=%v out=%q", err, calls.ops, out)
	}
}

// Acceptance 3, last clause: a non-manager is refused on list too (an unreadable
// Agent answers as absent), with the exit class the server's code carries.
func TestAgentRevisionListRefusalsKeepTheirClass(t *testing.T) {
	for code, want := range map[string]int{"FORBIDDEN": exitcode.Forbidden, "AGENT_NOT_FOUND": exitcode.NotFound} {
		t.Run(code, func(t *testing.T) {
			calls, out, err := runAgentSub(t, map[string]string{
				"AgentRevisions": `{"errors":[{"message":"refused","extensions":{"code":"` + code + `"}}],"data":null}`,
			}, "revision", "list", "agt1")
			if err == nil || exitCodeFor(err) != want || len(calls.ops) != 1 || out != "" {
				t.Fatalf("err=%v exit=%d ops=%v out=%q", err, exitCodeFor(err), calls.ops, out)
			}
		})
	}
}
