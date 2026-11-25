package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	if last := strings.TrimSpace(lines[len(lines)-1]); last != "hadron agent revision list <agent>" {
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

// agentRendered2533 runs `agent <args>` through the real error renderer and
// returns stdout, stderr and the command's error, so a field the renderer adds
// from the error's typed metadata is measured, not just err.Error().
func agentRendered2533(t *testing.T, responses map[string]string, jsonMode bool, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	srv, _ := captureGraphQLFunc(t, func(op string) string { return responses[op] })
	f, out := testFactory(t)
	root := NewRootCmd(f)
	a := append(append([]string{"agent"}, args...), "--server", srv.URL)
	if jsonMode {
		a = append(a, "--json")
	}
	root.SetArgs(a)
	err = root.Execute()
	if err != nil {
		renderError(f, err)
	}
	se, ok := f.IOStreams.ErrOut.(interface{ String() string })
	if !ok {
		t.Fatal("stderr cannot be inspected")
	}
	return out.String(), se.String(), err
}

// Jibril's local P2-1 (#2800): a missing or null REQUIRED field decodes as zero in
// a plain float64 and would print an all-zero report as a fact. Presence is kept at
// the generated boundary (pointers) and a missing/null required field is refused
// (exit 7, nothing printed); a genuine all-zero answer is still a success.
func TestAgentRevisionListRefusesAMissingOrNullRequiredField(t *testing.T) {
	for name, wire := range map[string]string{
		"empty object":           `{"data":{"agentRevisions":{}}}`,
		"slotLimit null":         `{"data":{"agentRevisions":{"revisions":[],"slotLimit":null,"highestSlotUsed":0,"slotsRemaining":0}}}`,
		"highestSlotUsed absent": `{"data":{"agentRevisions":{"revisions":[],"slotLimit":5,"slotsRemaining":5}}}`,
		"slotsRemaining null":    `{"data":{"agentRevisions":{"revisions":[],"slotLimit":5,"highestSlotUsed":0,"slotsRemaining":null}}}`,
		"revisions null":         `{"data":{"agentRevisions":{"revisions":null,"slotLimit":5,"highestSlotUsed":0,"slotsRemaining":5}}}`,
		"revisions absent":       `{"data":{"agentRevisions":{"slotLimit":5,"highestSlotUsed":0,"slotsRemaining":5}}}`,
	} {
		for _, jsonMode := range []bool{true, false} {
			t.Run(name, func(t *testing.T) {
				calls := 0
				srv, _ := captureGraphQLFunc(t, func(string) string { calls++; return wire })
				f, out := testFactory(t)
				root := NewRootCmd(f)
				args := []string{"agent", "revision", "list", "agt1", "--server", srv.URL}
				if jsonMode {
					args = append(args, "--json")
				}
				root.SetArgs(args)
				err := root.Execute()
				if err == nil || exitCodeFor(err) != exitcode.Unavailable || out.Len() != 0 || calls != 1 {
					t.Fatalf("a missing/null required field is refused, one request, nothing printed: err=%v exit=%d out=%q calls=%d", err, exitCodeFor(err), out.String(), calls)
				}
			})
		}
	}
}

func TestAgentRevisionListAGenuineAllZeroAnswerIsStillASuccess(t *testing.T) {
	zero := `{"data":{"agentRevisions":{"revisions":[],"slotLimit":0,"highestSlotUsed":0,"slotsRemaining":0}}}`
	_, out, err := runAgentSub(t, map[string]string{"AgentRevisions": zero}, "revision", "list", "agt1", "--json")
	if err != nil || !strings.Contains(out, `"slotLimit": 0`) || !strings.Contains(out, `"slotsRemaining": 0`) || !strings.Contains(out, `"revisions": []`) {
		t.Fatalf("zero is a real value: err=%v\n%s", err, out)
	}
	_, out, err = runAgentSub(t, map[string]string{"AgentRevisions": zero}, "revision", "list", "agt1")
	if err != nil || !strings.Contains(out, "highest used 0 of 0; 0 saves left") {
		t.Fatalf("err=%v\n%s", err, out)
	}
}

// Jibril's local P2-2 (#2800): an internal fault's request id travels as typed
// metadata that the --json envelope adds (requestId, serverCode). Reformatting the
// error into a fresh message dropped it; the delete's recovery guidance now WRAPS
// the mapped error, so the field survives, from the body and from the header.
func TestAgentRevisionRmKeepsAnInternalFaultsTypedRequestID(t *testing.T) {
	fault := `{"errors":[{"message":"secret stack","extensions":{"code":"INTERNAL_SERVER_ERROR","requestId":"r-123"}}],"data":null}`
	stdout, stderr, err := agentRendered2533(t, map[string]string{"DeleteAgentRevision": fault}, true, "revision", "rm", "hrn:agent:acme.com:bot", "revisions:rev-2", "--yes")
	if err == nil || exitCodeFor(err) != exitcode.Error {
		t.Fatalf("err=%v exit=%d", err, exitCodeFor(err))
	}
	var env struct {
		Error map[string]any `json:"error"`
	}
	if e := json.Unmarshal([]byte(stdout), &env); e != nil {
		t.Fatalf("envelope: %v\nstdout=%q stderr=%q", e, stdout, stderr)
	}
	if env.Error["requestId"] != "r-123" || env.Error["serverCode"] != "INTERNAL_SERVER_ERROR" {
		t.Fatalf("the typed request id and server code must survive the guidance: %v", env.Error)
	}
	msg, _ := env.Error["message"].(string)
	if !strings.Contains(msg, "delete outcome unknown (sent once, not retried)") || strings.Contains(msg, "secret stack") {
		t.Fatalf("the guidance is added and the server's raw text stays out: %q", msg)
	}
	lines := strings.Split(msg, "\n")
	if last := strings.TrimSpace(lines[len(lines)-1]); last != "hadron agent revision list <agent>" {
		t.Fatalf("the check command is still alone on the last line: %q", last)
	}
	// Text mode keeps the id in prose too.
	_, stderr, err = agentRendered2533(t, map[string]string{"DeleteAgentRevision": fault}, false, "revision", "rm", "agt1", "revisions:rev-2", "--yes")
	if err == nil || !strings.Contains(stderr, "r-123") {
		t.Fatalf("text mode names the request id: err=%v stderr=%q", err, stderr)
	}
}

func TestAgentRevisionRmKeepsAHeaderRequestIDToo(t *testing.T) {
	// A fault whose body carries no id: the transport falls back to the response's
	// X-Request-Id header and the typed field must survive the recovery guidance.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-Id", "h-1")
		_, _ = w.Write([]byte(`{"errors":[{"message":"x","extensions":{"code":"INTERNAL_SERVER_ERROR"}}],"data":null}`))
	}))
	defer srv.Close()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "revision", "rm", "agt1", "revisions:rev-2", "--yes", "--json", "--server", srv.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("must fail")
	}
	renderError(f, err)
	var env struct {
		Error map[string]any `json:"error"`
	}
	if e := json.Unmarshal([]byte(out.String()), &env); e != nil || env.Error["requestId"] != "h-1" || env.Error["serverCode"] != "INTERNAL_SERVER_ERROR" {
		t.Fatalf("the header's request id must reach the envelope: %v %v\n%q", e, env.Error, out.String())
	}
}

// With no id anywhere the field is omitted, never invented.
func TestAgentRevisionRmAnInternalFaultWithNoIDOmitsTheField(t *testing.T) {
	fault := `{"errors":[{"message":"x","extensions":{"code":"INTERNAL_SERVER_ERROR"}}],"data":null}`
	stdout, _, err := agentRendered2533(t, map[string]string{"DeleteAgentRevision": fault}, true, "revision", "rm", "agt1", "revisions:rev-2", "--yes")
	if err == nil {
		t.Fatal("must fail")
	}
	var env struct {
		Error map[string]any `json:"error"`
	}
	if e := json.Unmarshal([]byte(stdout), &env); e != nil || env.Error["serverCode"] != "INTERNAL_SERVER_ERROR" {
		t.Fatalf("the typed internal-fault fields survive without an id too: %v %v", e, env.Error)
	}
	if _, has := env.Error["requestId"]; has {
		t.Fatalf("no id was returned, so none is invented: %v", env.Error)
	}
}

// Joshua's BLIND P3s (#2800), the cheap ones.
func TestAgentRevisionListNamesTheMissingField(t *testing.T) {
	for wire, want := range map[string]string{
		`{"data":{"agentRevisions":{}}}`: "(revisions, slotLimit, highestSlotUsed, slotsRemaining)",
		`{"data":{"agentRevisions":{"revisions":[],"slotLimit":5,"highestSlotUsed":0,"slotsRemaining":null}}}`:    "(slotsRemaining)",
		`{"data":{"agentRevisions":{"revisions":[],"slotLimit":null,"highestSlotUsed":null,"slotsRemaining":1}}}`: "(slotLimit, highestSlotUsed)",
	} {
		_, _, err := runAgentSub(t, map[string]string{"AgentRevisions": wire}, "revision", "list", "agt1")
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must name the missing field %s: %v", want, err)
		}
	}
}

func TestAgentRevisionListOverflowAndFractionHaveTheirOwnMessage(t *testing.T) {
	big := `{"data":{"agentRevisions":{"revisions":[],"slotLimit":1e16,"highestSlotUsed":1,"slotsRemaining":1}}}`
	_, _, err := runAgentSub(t, map[string]string{"AgentRevisions": big}, "revision", "list", "agt1")
	if err == nil || !strings.Contains(err.Error(), "larger than this CLI can show") || strings.Contains(err.Error(), "whole non-negative") {
		t.Errorf("overflow message: %v", err)
	}
	frac := `{"data":{"agentRevisions":{"revisions":[],"slotLimit":5.5,"highestSlotUsed":1,"slotsRemaining":1}}}`
	_, _, err = runAgentSub(t, map[string]string{"AgentRevisions": frac}, "revision", "list", "agt1")
	if err == nil || !strings.Contains(err.Error(), "not a whole non-negative count") {
		t.Errorf("fraction message: %v", err)
	}
}

func TestAgentRevisionListLsAliasWorks(t *testing.T) {
	calls, out, err := runAgentSub(t, map[string]string{"AgentRevisions": revList2533}, "revision", "ls", "agt1", "--json")
	if err != nil || len(calls.ops) != 1 || calls.ops[0] != "AgentRevisions" || !strings.Contains(out, `"revisions"`) {
		t.Fatalf("ls is the same command: err=%v ops=%v", err, calls.ops)
	}
}

// The wrap applies to an AMBIGUOUS failure only: a transport-class Unavailable (a
// gateway 503, whose delete may have committed) is wrapped with the recovery
// guidance and keeps exit 7; a definitive refusal (FORBIDDEN) is NOT wrapped.
func TestAgentRevisionRmWrapsAnUnavailableAndNeverAnythingDefinitive(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream down", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "revision", "rm", "agt1", "revisions:rev-2", "--yes", "--server", srv.URL})
	err := root.Execute()
	if err == nil || exitCodeFor(err) != exitcode.Unavailable || !strings.Contains(err.Error(), "delete outcome unknown (sent once, not retried)") {
		t.Fatalf("an unavailable delete is wrapped with the recovery guidance, exit 7: err=%v exit=%d", err, exitCodeFor(err))
	}
	_, _, err = runAgentSub(t, map[string]string{
		"DeleteAgentRevision": `{"errors":[{"message":"refused","extensions":{"code":"FORBIDDEN"}}],"data":null}`,
	}, "revision", "rm", "agt1", "revisions:rev-2", "--yes")
	if err == nil || exitCodeFor(err) != exitcode.Forbidden || strings.Contains(err.Error(), "outcome unknown") {
		t.Fatalf("a definitive refusal must not claim an unknown outcome: %v", err)
	}
}

// QA (Charles) on #2800: a real server's slotLimit is the constant
// MAX_REVISION_SLOT, 999999999999999 (15 digits, below Number.MAX_SAFE_INTEGER;
// hadron-server src/lib/revisionSlot.ts), so `list` must show it, in text and
// --json, instead of refusing it as too large for an int32.
func TestAgentRevisionListShowsTheRealServersSlotLimit(t *testing.T) {
	wire := `{"data":{"agentRevisions":{"revisions":[],"slotLimit":999999999999999,"highestSlotUsed":2,"slotsRemaining":999999999999997}}}`
	_, out, err := runAgentSub(t, map[string]string{"AgentRevisions": wire}, "revision", "list", "agt1", "--json")
	if err != nil {
		t.Fatalf("the server's own constant must be accepted: %v", err)
	}
	var d struct {
		SlotLimit       int64 `json:"slotLimit"`
		HighestSlotUsed int64 `json:"highestSlotUsed"`
		SlotsRemaining  int64 `json:"slotsRemaining"`
	}
	if e := json.Unmarshal([]byte(out), &d); e != nil || d.SlotLimit != 999999999999999 || d.HighestSlotUsed != 2 || d.SlotsRemaining != 999999999999997 {
		t.Errorf("the numbers are shown exactly: %v %+v\n%s", e, d, out)
	}
	_, text, err := runAgentSub(t, map[string]string{"AgentRevisions": wire}, "revision", "list", "agt1")
	if err != nil || !strings.Contains(text, "highest used 2 of 999999999999999; 999999999999997 saves left") {
		t.Errorf("text: %v %q", err, text)
	}
}
