package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#61 first cut: `agent revision create` and `agent lock acquire|release`.
// Thin calls to createRevision / acquireEditorLock / releaseEditorLock; refs go
// out verbatim, results are preserved exactly, and nothing follows a result.

type agentCall struct {
	ops  []string
	vars map[string]map[string]any
}

func runAgentSub(t *testing.T, responses map[string]string, args ...string) (*agentCall, string, error) {
	t.Helper()
	calls := &agentCall{vars: map[string]map[string]any{}}
	gql, captured := captureGraphQLFunc(t, func(op string) string {
		calls.ops = append(calls.ops, op)
		return responses[op]
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{"agent"}, args...), "--server", gql.URL))
	err := root.Execute()
	for op, raw := range captured {
		v := map[string]any{}
		_ = json.Unmarshal(raw, &v)
		calls.vars[op] = v
	}
	return calls, out.String(), err
}

const revJSON = `{"createdAt":"2026-11-02T00:00:00Z","label":"draft-7","published":false,"revisionLoc":"revisions:draft-7"}`

func TestAgentRevisionCreateForwardsRefAndPrintsExactResult(t *testing.T) {
	calls, out, err := runAgentSub(t, map[string]string{
		"CreateRevision": `{"data":{"createRevision":` + revJSON + `}}`,
	}, "revision", "create", " hrn:agent:acme.com:bot ", "--description", "before launch", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if len(calls.ops) != 1 || calls.ops[0] != "CreateRevision" {
		t.Fatalf("ops=%v (no publish/restore/list follow-up)", calls.ops)
	}
	v := calls.vars["CreateRevision"]
	if v["agentId"] != " hrn:agent:acme.com:bot " || v["description"] != "before launch" {
		t.Fatalf("vars=%v", v)
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if dto["revisionLoc"] != "revisions:draft-7" || dto["label"] != "draft-7" || dto["published"] != false || dto["createdAt"] != "2026-11-02T00:00:00Z" {
		t.Fatalf("dto=%v", dto)
	}
}

func TestAgentRevisionCreateOmitsUnsetDescription(t *testing.T) {
	calls, out, err := runAgentSub(t, map[string]string{
		"CreateRevision": `{"data":{"createRevision":` + revJSON + `}}`,
	}, "revision", "create", "agt1")
	if err != nil {
		t.Fatal(err)
	}
	if _, present := calls.vars["CreateRevision"]["description"]; present {
		t.Fatalf("unset --description must be omitted: %v", calls.vars)
	}
	if !strings.Contains(out, "revisions:draft-7") {
		t.Fatalf("text must show the returned revisionLoc: %q", out)
	}
}

func TestAgentLockAcquireStates(t *testing.T) {
	t.Run("acquired", func(t *testing.T) {
		calls, out, err := runAgentSub(t, map[string]string{
			"AcquireEditorLock": `{"data":{"acquireEditorLock":{"locked":true,"lockedBy":"me","expiresAt":"2026-11-02T01:00:00Z"}}}`,
		}, "lock", "acquire", "hrn:agent:acme.com:bot", "--json")
		if err != nil || len(calls.ops) != 1 || calls.vars["AcquireEditorLock"]["agentId"] != "hrn:agent:acme.com:bot" {
			t.Fatalf("err=%v ops=%v vars=%v", err, calls.ops, calls.vars)
		}
		if !strings.Contains(out, `"locked": true`) || !strings.Contains(out, "2026-11-02T01:00:00Z") {
			t.Fatalf("out=%s", out)
		}
	})
	t.Run("contended is conflict, not an error from the server, no retry", func(t *testing.T) {
		calls, out, err := runAgentSub(t, map[string]string{
			"AcquireEditorLock": `{"data":{"acquireEditorLock":{"locked":false,"lockedBy":"someone","expiresAt":null}}}`,
		}, "lock", "acquire", "agt1", "--json")
		if err == nil || exitCodeFor(err) != exitcode.Conflict {
			t.Fatalf("err=%v", err)
		}
		if len(calls.ops) != 1 {
			t.Fatalf("no retry/force/renew: %v", calls.ops)
		}
		var dto map[string]any
		if e := json.Unmarshal([]byte(out), &dto); e != nil || dto["locked"] != false || dto["lockedBy"] != "someone" {
			t.Fatalf("--json must carry the provider result: %v %s", e, out)
		}
		if v, ok := dto["expiresAt"]; !ok || v != nil {
			t.Fatalf("null expiresAt must stay null: %s", out)
		}
	})
	t.Run("null result is a controlled error", func(t *testing.T) {
		_, out, err := runAgentSub(t, map[string]string{"AcquireEditorLock": `{"data":{"acquireEditorLock":null}}`}, "lock", "acquire", "agt1")
		if err == nil || exitCodeFor(err) != exitcode.Error || out != "" {
			t.Fatalf("err=%v out=%q", err, out)
		}
	})
}

func TestAgentLockRelease(t *testing.T) {
	calls, out, err := runAgentSub(t, map[string]string{"ReleaseEditorLock": `{"data":{"releaseEditorLock":true}}`}, "lock", "release", "agt1", "--json")
	if err != nil || len(calls.ops) != 1 || calls.vars["ReleaseEditorLock"]["agentId"] != "agt1" || !strings.Contains(out, `"released": true`) {
		t.Fatalf("err=%v ops=%v out=%s", err, calls.ops, out)
	}
	calls, out, err = runAgentSub(t, map[string]string{"ReleaseEditorLock": `{"data":{"releaseEditorLock":false}}`}, "lock", "release", "agt1", "--json")
	if err == nil || exitCodeFor(err) != exitcode.Conflict || len(calls.ops) != 1 || !strings.Contains(out, `"released": false`) {
		t.Fatalf("false must be reported, not success: err=%v out=%s", err, out)
	}
}

func TestAgentRevisionLockRefusalsMapOnEveryVerb(t *testing.T) {
	for _, tc := range []struct {
		args []string
		op   string
	}{
		{[]string{"revision", "create", "agt1"}, "CreateRevision"},
		{[]string{"lock", "acquire", "agt1"}, "AcquireEditorLock"},
		{[]string{"lock", "release", "agt1"}, "ReleaseEditorLock"},
	} {
		for code, want := range map[string]int{"FORBIDDEN": exitcode.Forbidden, "AGENT_NOT_FOUND": exitcode.NotFound} {
			t.Run(tc.op+"/"+code, func(t *testing.T) {
				calls, out, err := runAgentSub(t, map[string]string{
					tc.op: `{"errors":[{"message":"refused","extensions":{"code":"` + code + `"}}]}`,
				}, tc.args...)
				if err == nil || exitCodeFor(err) != want || len(calls.ops) != 1 || out != "" {
					t.Fatalf("err=%v ops=%v out=%q", err, calls.ops, out)
				}
			})
		}
	}
}

func TestAgentRevisionLockBlankRefIsUsageWithNoCall(t *testing.T) {
	for _, args := range [][]string{{"revision", "create", "  "}, {"lock", "acquire", ""}, {"lock", "release", " "}} {
		calls, _, err := runAgentSub(t, nil, args...)
		if err == nil || exitCodeFor(err) != exitcode.Usage || len(calls.ops) != 0 {
			t.Fatalf("%v: err=%v ops=%v", args, err, calls.ops)
		}
	}
}
