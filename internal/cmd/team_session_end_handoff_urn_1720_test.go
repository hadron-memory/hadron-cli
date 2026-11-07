package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// cli#1720: `session end --handoff` shows where the handoff landed. The end is
// committed (and, with a handoff, the server wrote it first, cor:agt:020:10), so
// a read-back that fails or does not match must change one line and nothing
// else: not the exit code, not the success line, not the JSON keys.

const handoffURN1720 = "hrn:node:acme.com:team:handoffs:s-new"

func readBackJSON1720(status, previousID, urn string) string {
	handoff := "null"
	if urn != "" {
		handoff = `{"urn":"` + urn + `"}`
	}
	prev := "null"
	if previousID != "" {
		prev = `{"id":"` + previousID + `"}`
	}
	return `{"data":{"worker":{"continuity":{"status":"` + status + `","handoff":` + handoff + `,"previousSession":` + prev + `}}}}`
}

type end1720 struct {
	out, errOut string
	err         error
	captured    map[string]json.RawMessage
}

func runEnd1720(t *testing.T, readBack string, extra ...string) end1720 {
	t.Helper()
	bindWorktree(t)
	stubs := endStubs()
	if readBack != "" {
		stubs["GetWorkerHandoffReadBack"] = readBack
	}
	gql, captured := captureGraphQL(t, stubs)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	args := append([]string{"team", "session", "end", "--server", gql.URL}, extra...)
	root.SetArgs(args)
	err := root.Execute()
	return end1720{out: out.String(), errOut: f.IOStreams.ErrOut.(*strings.Builder).String(), err: err, captured: captured}
}

func TestSessionEndShowsTheSavedHandoffURN1720(t *testing.T) {
	r := runEnd1720(t, readBackJSON1720("CURRENT", "s-new", handoffURN1720), "--handoff", "text")
	if r.err != nil {
		t.Fatal(r.err)
	}
	if !strings.Contains(r.out, "✓ ended session") || !strings.Contains(r.out, "handoff saved: "+handoffURN1720) {
		t.Errorf("text output must carry the URN under the ✓ line:\n%s", r.out)
	}
	var vars map[string]any
	_ = json.Unmarshal(r.captured["GetWorkerHandoffReadBack"], &vars)
	if vars["ref"] != "wkr1" {
		t.Errorf("the read-back must ask the ended session's worker: %v", vars)
	}
	j := runEnd1720(t, readBackJSON1720("CURRENT", "s-new", handoffURN1720), "--handoff", "text", "--json")
	var dto map[string]any
	if err := json.Unmarshal([]byte(j.out), &dto); err != nil {
		t.Fatalf("json: %v\n%s", err, j.out)
	}
	if dto["handoff"] != handoffURN1720 || dto["sessionId"] != "s-new" {
		t.Errorf("--json must add handoff and keep its keys: %v", dto)
	}
}

func TestSessionEndWithoutAHandoffPrintsNothingNew1720(t *testing.T) {
	r := runEnd1720(t, readBackJSON1720("CURRENT", "s-new", handoffURN1720), "--summary", "x")
	if r.err != nil {
		t.Fatal(r.err)
	}
	if _, asked := r.captured["GetWorkerHandoffReadBack"]; asked {
		t.Errorf("no handoff was sent: there is nothing to read back")
	}
	if strings.Contains(r.out, "handoff") {
		t.Errorf("output must be unchanged:\n%s", r.out)
	}
	j := runEnd1720(t, "", "--summary", "x", "--json")
	var dto map[string]json.RawMessage
	_ = json.Unmarshal([]byte(j.out), &dto)
	if _, has := dto["handoff"]; has {
		t.Errorf("no handoff key without a handoff: %s", j.out)
	}
}

func TestSessionEndReadBackFailureChangesOneLine1720(t *testing.T) {
	cases := map[string]string{
		"graphql error":      `{"errors":[{"message":"boom"}]}`,
		"null worker":        `{"data":{"worker":null}}`,
		"null continuity":    `{"data":{"worker":{"continuity":null}}}`,
		"status MISSING":     readBackJSON1720("MISSING", "s-new", handoffURN1720),
		"different session":  readBackJSON1720("CURRENT", "s-other", handoffURN1720),
		"no previousSession": readBackJSON1720("CURRENT", "", handoffURN1720),
		"no handoff":         readBackJSON1720("CURRENT", "s-new", ""),
	}
	for name, resp := range cases {
		t.Run(name, func(t *testing.T) {
			r := runEnd1720(t, resp, "--handoff", "text")
			if r.err != nil {
				t.Fatalf("a failed read-back must not change the exit code: %v", r.err)
			}
			if !strings.Contains(r.out, "✓ ended session") || !strings.Contains(r.out, "handoff saved; URN not read back (") {
				t.Errorf("success line plus the not-read-back line expected:\n%s", r.out)
			}
			if strings.Contains(r.out+r.errOut, handoffURN1720) {
				t.Errorf("a non-matching read-back must never print a URN:\n%s", r.out)
			}
			j := runEnd1720(t, resp, "--handoff", "text", "--json")
			if j.err != nil {
				t.Fatal(j.err)
			}
			var dto map[string]any
			if err := json.Unmarshal([]byte(j.out), &dto); err != nil {
				t.Fatalf("json must stay clean: %v\n%s", err, j.out)
			}
			if _, has := dto["handoff"]; has || dto["sessionId"] != "s-new" {
				t.Errorf("--json omits handoff and keeps the rest: %v", dto)
			}
		})
	}
}

func TestSessionEndOnAServerWithoutContinuityIsToday1720(t *testing.T) {
	r := runEnd1720(t, "", "--handoff", "text")
	if r.err != nil {
		t.Fatal(r.err)
	}
	if r.out != "✓ ended session s-new (worker Iris)\n" {
		t.Errorf("unexpected output: %q", r.out)
	}
	if strings.Contains(r.out, "handoff") {
		t.Errorf("an old server prints today's output, nothing more:\n%s", r.out)
	}
}
