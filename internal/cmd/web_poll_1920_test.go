package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

const webPollJSON = `{"jobId":"job_1","appId":"app_1","sourceRunId":"run_1","url":"https://example.com/status",
	"status":"active","consecutiveFailures":0,"triggerCount":2,"createdAt":"2026-11-01T00:00:00Z",
	"expiresAt":"2026-12-01T00:00:00Z","lastCheckedAt":"2026-11-11T10:00:00Z","lastTriggeredAt":null,"nextRunAt":"2026-11-11T11:00:00Z"}`

func runWebPoll(t *testing.T, responses map[string]string, args ...string) (map[string]json.RawMessage, string, error) {
	t.Helper()
	gql, captured := captureGraphQL(t, responses)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{"web-poll"}, args...), "--server", gql.URL))
	err := root.Execute()
	return captured, out.String(), err
}

// cli#1920: list sends the org ref and renders the server's rows as returned.
func TestWebPollListForwardsOrgAndRendersRows(t *testing.T) {
	captured, out, err := runWebPoll(t, map[string]string{
		"WebPolls": `{"data":{"webPolls":[` + webPollJSON + `]}}`,
	}, "list", "--org", "acme.com", "--json")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		OrgRef string `json:"orgRef"`
	}
	_ = json.Unmarshal(captured["WebPolls"], &vars)
	if vars.OrgRef != "acme.com" {
		t.Errorf("--org should forward as orgRef, got %q", vars.OrgRef)
	}
	var got []map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("--json invalid: %v\n%s", err, out)
	}
	if len(got) != 1 || got[0]["jobId"] != "job_1" || got[0]["status"] != "active" || got[0]["url"] != "https://example.com/status" {
		t.Errorf("unexpected rows: %s", out)
	}
	if got[0]["lastTriggeredAt"] != nil {
		t.Errorf("an unset timestamp must render null, got %v", got[0]["lastTriggeredAt"])
	}
}

// cli#1920 condition 2 (cor:api:120:04 gap): the list is unpaged server-side, so
// the output is the rows and nothing else: no total, no "all" claim, in JSON or text.
func TestWebPollListClaimsNoCompleteness(t *testing.T) {
	resp := map[string]string{"WebPolls": `{"data":{"webPolls":[` + webPollJSON + `]}}`}
	_, jout, err := runWebPoll(t, resp, "list", "--org", "acme.com", "--json")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.HasPrefix(strings.TrimSpace(jout), "{") {
		t.Errorf("--json must be the bare array of rows, not an envelope with a total: %s", jout)
	}
	_, tout, err := runWebPoll(t, resp, "list", "--org", "acme.com")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	low := strings.ToLower(tout)
	for _, bad := range []string{"total", "all watches", "of 1", "complete"} {
		if strings.Contains(low, bad) {
			t.Errorf("text output claims %q:\n%s", bad, tout)
		}
	}
	if !strings.Contains(tout, "job_1") {
		t.Errorf("text output missing the row:\n%s", tout)
	}
}

func TestWebPollListEmptyIsAnEmptyArray(t *testing.T) {
	_, out, err := runWebPoll(t, map[string]string{"WebPolls": `{"data":{"webPolls":[]}}`}, "list", "--org", "acme.com", "--json")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.TrimSpace(out) != "[]" {
		t.Errorf("empty list must render [], got %q", out)
	}
}

func TestWebPollListRequiresOrg(t *testing.T) {
	_, _, err := runWebPoll(t, map[string]string{}, "list")
	if got := exitCodeFor(err); got != exitcode.Usage {
		t.Errorf("a missing --org: want exit %d, got %d (%v)", exitcode.Usage, got, err)
	}
}

// cli#1920 condition 1 (cor:web:030:00): the CLI adds no authorization of its
// own. A refusal the server returns is rendered as returned, a cross-org job id
// and an unknown one answer the identical typed NOT_FOUND, and no other
// operation is sent ahead of the one the user asked for.
func TestWebPollCancelRendersServerRefusalsAsReturned(t *testing.T) {
	// The exit code is whatever the CLI's shared mapping gives the server's code;
	// the pin is that the refusal comes through, naming the server's code.
	for code, want := range map[string]int{"NOT_FOUND": exitcode.NotFound, "FORBIDDEN": exitcode.Forbidden, "POLICY_DENIED": exitcode.Error} {
		t.Run(code, func(t *testing.T) {
			captured, _, err := runWebPoll(t, map[string]string{"CancelWebPoll": batchGQLErr(code)}, "cancel", "job_x", "--org", "acme.com", "--yes")
			if got := exitCodeFor(err); got != want {
				t.Fatalf("%s: want exit %d, got %d (%v)", code, want, got, err)
			}
			if err == nil || !strings.Contains(err.Error(), code) {
				t.Errorf("%s: the server's refusal must be rendered as returned, got %v", code, err)
			}
			if len(captured) != 1 {
				t.Errorf("the CLI must send only cancelWebPoll, no preflight; sent %d operations", len(captured))
			}
		})
	}
}

func TestWebPollCancelNotFoundIsIdenticalForCrossOrgAndUnknown(t *testing.T) {
	_, _, e1 := runWebPoll(t, map[string]string{"CancelWebPoll": batchGQLErr("NOT_FOUND")}, "cancel", "job_other_org", "--org", "acme.com", "--yes")
	_, _, e2 := runWebPoll(t, map[string]string{"CancelWebPoll": batchGQLErr("NOT_FOUND")}, "cancel", "job_unknown", "--org", "acme.com", "--yes")
	if e1 == nil || e2 == nil {
		t.Fatalf("both must refuse (%v, %v)", e1, e2)
	}
	if strings.ReplaceAll(e1.Error(), "job_other_org", "X") != strings.ReplaceAll(e2.Error(), "job_unknown", "X") {
		t.Errorf("the CLI must not distinguish the two: %q vs %q", e1, e2)
	}
}

// A fresh stop and a repeat cancel are both successes, rendered distinctly.
func TestWebPollCancelFreshAndAlreadyCancelledAreDistinct(t *testing.T) {
	_, fresh, err := runWebPoll(t, map[string]string{
		"CancelWebPoll": `{"data":{"cancelWebPoll":{"jobId":"job_1","cancelled":true,"alreadyCancelled":false}}}`,
	}, "cancel", "job_1", "--org", "acme.com", "--yes", "--json")
	if err != nil {
		t.Fatalf("fresh stop: %v", err)
	}
	_, again, err := runWebPoll(t, map[string]string{
		"CancelWebPoll": `{"data":{"cancelWebPoll":{"jobId":"job_1","cancelled":true,"alreadyCancelled":true}}}`,
	}, "cancel", "job_1", "--org", "acme.com", "--yes", "--json")
	if err != nil {
		t.Fatalf("repeat cancel must succeed: %v", err)
	}
	var f, a map[string]any
	if err := json.Unmarshal([]byte(fresh), &f); err != nil {
		t.Fatalf("fresh --json: %v\n%s", err, fresh)
	}
	if err := json.Unmarshal([]byte(again), &a); err != nil {
		t.Fatalf("again --json: %v\n%s", err, again)
	}
	if f["alreadyCancelled"] != false || a["alreadyCancelled"] != true || f["jobId"] != "job_1" {
		t.Errorf("fresh = %v, repeat = %v", f, a)
	}
	_, ftxt, _ := runWebPoll(t, map[string]string{
		"CancelWebPoll": `{"data":{"cancelWebPoll":{"jobId":"job_1","cancelled":true,"alreadyCancelled":false}}}`,
	}, "cancel", "job_1", "--org", "acme.com", "--yes")
	_, atxt, _ := runWebPoll(t, map[string]string{
		"CancelWebPoll": `{"data":{"cancelWebPoll":{"jobId":"job_1","cancelled":true,"alreadyCancelled":true}}}`,
	}, "cancel", "job_1", "--org", "acme.com", "--yes")
	if ftxt == atxt || !strings.Contains(strings.ToLower(atxt), "already") || strings.Contains(strings.ToLower(ftxt), "already") {
		t.Errorf("text must tell them apart:\nfresh: %q\nrepeat: %q", ftxt, atxt)
	}
}

func TestWebPollCancelForwardsOrgAndJobAndNeedsYes(t *testing.T) {
	captured, _, err := runWebPoll(t, map[string]string{
		"CancelWebPoll": `{"data":{"cancelWebPoll":{"jobId":"job_1","cancelled":true,"alreadyCancelled":false}}}`,
	}, "cancel", "job_1", "--org", "acme.com", "--yes")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		OrgRef string `json:"orgRef"`
		JobID  string `json:"jobId"`
	}
	_ = json.Unmarshal(captured["CancelWebPoll"], &vars)
	if vars.OrgRef != "acme.com" || vars.JobID != "job_1" {
		t.Errorf("variables = %+v", vars)
	}
	captured, _, err = runWebPoll(t, map[string]string{}, "cancel", "job_1", "--org", "acme.com")
	if got := exitCodeFor(err); got != exitcode.Usage {
		t.Errorf("without --yes non-interactively: want exit %d, got %d (%v)", exitcode.Usage, got, err)
	}
	if len(captured) != 0 {
		t.Errorf("nothing may be sent before the confirmation: %v", captured)
	}
}

// A payload that is neither a stop nor an already-stopped watch is not a success.
func TestWebPollCancelNotCancelledIsAnError(t *testing.T) {
	_, _, err := runWebPoll(t, map[string]string{
		"CancelWebPoll": `{"data":{"cancelWebPoll":{"jobId":"job_1","cancelled":false,"alreadyCancelled":false}}}`,
	}, "cancel", "job_1", "--org", "acme.com", "--yes")
	if got := exitCodeFor(err); got != exitcode.Error {
		t.Errorf("want exit %d, got %d (%v)", exitcode.Error, got, err)
	}
}
