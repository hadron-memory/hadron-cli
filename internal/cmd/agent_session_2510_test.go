package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2510 (gap #58): agent session start|end. Each verb sends exactly one
// matching mutation, refs as typed, with no retry or follow-up. The other four
// #58 commands (ai-config clear, import publish|rm, repair-system-memory) came
// with cli#2729 and are pinned by its own tests.

type lifecycleCall struct {
	ops  []string
	vars map[string]map[string]any
}

func runAgentLifecycle(t *testing.T, responses map[string]string, args ...string) (*lifecycleCall, string, error) {
	t.Helper()
	calls := &lifecycleCall{vars: map[string]map[string]any{}}
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

const agentSessionJSON = `{"id":"ses1","agentId":"agt1","appId":"app1","memoryId":"mem1","userId":null,` +
	`"type":"CHATBOT","state":"LIVE","isLive":true,"startedAt":"2026-11-19T00:00:00Z","expiresAt":null}`

func wantOneOp(t *testing.T, calls *lifecycleCall, op string) map[string]any {
	t.Helper()
	if len(calls.ops) != 1 || calls.ops[0] != op {
		t.Fatalf("want exactly one %s, got %v", op, calls.ops)
	}
	return calls.vars[op]
}

func TestAgentLifecycleHelpStatesServerFacts(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"session"}, []string{"hadron team session", "App key"}},
		// cli#2895 F5: the verb pages' own facts, not just the group page.
		{[]string{"session", "start"}, []string{"must be positive", "implicit user creation", "usage error, not an anonymous session"}},
		{[]string{"session", "end"}, []string{"already ended succeeds"}},
	} {
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append(append([]string{"agent"}, tc.args...), "--help"))
		if err := root.Execute(); err != nil {
			t.Fatalf("%v: %v", tc.args, err)
		}
		help := strings.ToLower(strings.Join(strings.Fields(out.String()), " "))
		for _, w := range tc.want {
			if !strings.Contains(help, strings.ToLower(w)) {
				t.Errorf("%v help should say %q:\n%s", tc.args, w, out.String())
			}
		}
	}
}

func TestAgentSessionStartByIDSendsOneMutation(t *testing.T) {
	calls, out, err := runAgentLifecycle(t, map[string]string{
		"StartAgentSession": `{"data":{"startAgentSession":` + agentSessionJSON + `}}`,
	}, "session", "start", "01a0091679447ebb87ffec2301cd12c1", "--json")
	if err != nil {
		t.Fatal(err)
	}
	v := wantOneOp(t, calls, "StartAgentSession")
	if v["agentId"] != "01a0091679447ebb87ffec2301cd12c1" {
		t.Fatalf("vars: %v", v)
	}
	for _, k := range []string{"externalUserId", "sessionTimeoutSeconds"} {
		if _, present := v[k]; present {
			t.Fatalf("unset %s must be omitted: %v", k, v)
		}
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if got["id"] != "ses1" || got["agentId"] != "agt1" || got["state"] != "LIVE" || got["isLive"] != true {
		t.Fatalf("json: %v", got)
	}
	if strings.Contains(strings.ToLower(out), "key") {
		t.Fatalf("no key material or key field in the session output: %s", out)
	}
}

func TestAgentSessionStartSendsOptionalFlags(t *testing.T) {
	calls, _, err := runAgentLifecycle(t, map[string]string{
		"StartAgentSession": `{"data":{"startAgentSession":` + agentSessionJSON + `}}`,
	}, "session", "start", "agt1", "--external-user", "u-42", "--timeout-seconds", "600")
	if err != nil {
		t.Fatal(err)
	}
	v := wantOneOp(t, calls, "StartAgentSession")
	if v["externalUserId"] != "u-42" || v["sessionTimeoutSeconds"] != float64(600) {
		t.Fatalf("vars: %v", v)
	}
}

// startAgentSession looks the agent up by ID only, so a URN is resolved first
// with one read; the mutation then carries the ID.
func TestAgentSessionStartResolvesURN(t *testing.T) {
	calls, _, err := runAgentLifecycle(t, map[string]string{
		"AgentSessionAgentID": `{"data":{"agent":{"id":"agt1"}}}`,
		"StartAgentSession":   `{"data":{"startAgentSession":` + agentSessionJSON + `}}`,
	}, "session", "start", "hrn:agent:acme.com:support-bot")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(calls.ops, ",") != "AgentSessionAgentID,StartAgentSession" {
		t.Fatalf("want one read then one mutation, got %v", calls.ops)
	}
	if calls.vars["AgentSessionAgentID"]["ref"] != "hrn:agent:acme.com:support-bot" || calls.vars["StartAgentSession"]["agentId"] != "agt1" {
		t.Fatalf("vars: %v", calls.vars)
	}
}

func TestAgentSessionStartUnreadableURNIsNotFoundWithoutMutation(t *testing.T) {
	calls, _, err := runAgentLifecycle(t, map[string]string{
		"AgentSessionAgentID": `{"data":{"agent":null}}`,
	}, "session", "start", "hrn:agent:acme.com:nope")
	if err == nil || exitCodeFor(err) != exitcode.NotFound {
		t.Fatalf("want not-found, got %v", err)
	}
	if len(calls.ops) != 1 || calls.ops[0] != "AgentSessionAgentID" {
		t.Fatalf("no mutation after a failed resolve: %v", calls.ops)
	}
	if !strings.Contains(err.Error(), "agent ID") {
		t.Fatalf("the refusal should name the ID remedy: %v", err)
	}
}

// The App-agent session's SECRET identify mode is out of scope: the CLI never
// sends the x-hadron-operation-key header and offers no flag for it.
func TestAgentSessionStartSendsNoOperationKey(t *testing.T) {
	var sawKeyHeader, sawStart bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-hadron-operation-key") != "" {
			sawKeyHeader = true
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"capabilities":[]}`))
			return
		}
		var body struct {
			OperationName string `json:"operationName"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		sawStart = sawStart || body.OperationName == "StartAgentSession"
		_, _ = w.Write([]byte(`{"data":{"startAgentSession":` + agentSessionJSON + `}}`))
	}))
	defer srv.Close()
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "session", "start", "agt1", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !sawStart || sawKeyHeader {
		t.Fatalf("want the StartAgentSession mutation without x-hadron-operation-key: start=%v key=%v", sawStart, sawKeyHeader)
	}
	f, _ = testFactory(t)
	root = NewRootCmd(f)
	root.SetArgs([]string{"agent", "session", "start", "agt1", "--operation-key", "x", "--server", srv.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("there must be no --operation-key flag")
	}
}

func TestAgentSessionEndSendsOneCall(t *testing.T) {
	calls, out, err := runAgentLifecycle(t, map[string]string{
		"EndAgentSession": `{"data":{"endAgentSession":true}}`,
	}, "session", "end", "ses1", "--json")
	if err != nil {
		t.Fatal(err)
	}
	v := wantOneOp(t, calls, "EndAgentSession")
	if v["sessionId"] != "ses1" {
		t.Fatalf("vars: %v", v)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil || got["sessionId"] != "ses1" || got["ended"] != true {
		t.Fatalf("json: %v %s", err, out)
	}
}

func TestAgentLifecycleRefusalsMapWithoutFollowUp(t *testing.T) {
	verbs := []struct {
		op   string
		args []string
	}{
		{"StartAgentSession", []string{"session", "start", "agt1"}},
		{"EndAgentSession", []string{"session", "end", "ses1"}},
	}
	for _, vb := range verbs {
		for _, tc := range []struct {
			code string
			want int
		}{{"FORBIDDEN", exitcode.Forbidden}, {"AGENT_NOT_FOUND", exitcode.NotFound}, {"BAD_USER_INPUT", exitcode.Usage}} {
			calls, out, err := runAgentLifecycle(t, map[string]string{
				vb.op: `{"errors":[{"message":"refused","extensions":{"code":"` + tc.code + `"}}]}`,
			}, vb.args...)
			if err == nil || exitCodeFor(err) != tc.want {
				t.Fatalf("%s %s: want exit %d, got %v (%d)", vb.op, tc.code, tc.want, err, exitCodeFor(err))
			}
			if len(calls.ops) != 1 || out != "" {
				t.Fatalf("%s %s: no retry/follow-up, no output: ops=%v out=%q", vb.op, tc.code, calls.ops, out)
			}
		}
	}
}

func TestAgentLifecycleBlankRefsAreUsage(t *testing.T) {
	for _, args := range [][]string{
		{"session", "start", " "},
		{"session", "end", ""},
	} {
		calls, _, err := runAgentLifecycle(t, nil, args...)
		if err == nil || exitCodeFor(err) != exitcode.Usage || len(calls.ops) != 0 {
			t.Fatalf("%v: blank ref must be usage with no call: err=%v ops=%v", args, err, calls.ops)
		}
	}
}

// cli#2895 F1: --timeout-seconds 0 or negative is refused locally (exit 2)
// with nothing sent — the URN read must not even run, hence the URN ref here.
func TestAgentSessionStartTimeoutSecondsRefusedLocally(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
	}{
		{"zero", "0"},
		{"negative", "-5"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls, _, err := runAgentLifecycle(t, nil,
				"session", "start", "hrn:agent:acme.com:support-bot", "--timeout-seconds", tc.value)
			if err == nil || exitCodeFor(err) != exitcode.Usage {
				t.Fatalf("want a usage refusal, got %v (%d)", err, exitCodeFor(err))
			}
			if len(calls.ops) != 0 {
				t.Fatalf("no read and no mutation may be sent: %v", calls.ops)
			}
			if !strings.Contains(err.Error(), "--timeout-seconds") {
				t.Fatalf("the refusal must name the flag: %v", err)
			}
		})
	}
}

// cli#2895 F2: an empty or whitespace-only --external-user is refused locally
// (exit 2, nothing sent); omitting the flag stays the anonymous session.
func TestAgentSessionStartExternalUserBlankRefused(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
	}{
		{"empty", ""},
		{"whitespace", "  "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls, _, err := runAgentLifecycle(t, nil,
				"session", "start", "hrn:agent:acme.com:support-bot", "--external-user", tc.value)
			if err == nil || exitCodeFor(err) != exitcode.Usage {
				t.Fatalf("want a usage refusal, got %v (%d)", err, exitCodeFor(err))
			}
			if len(calls.ops) != 0 {
				t.Fatalf("no read and no mutation may be sent: %v", calls.ops)
			}
			if !strings.Contains(err.Error(), "--external-user") {
				t.Fatalf("the refusal must name the flag: %v", err)
			}
		})
	}
}

// cli#2895 F2: a non-blank value is sent exactly as typed, no trimming.
func TestAgentSessionStartExternalUserSentAsTyped(t *testing.T) {
	calls, _, err := runAgentLifecycle(t, map[string]string{
		"StartAgentSession": `{"data":{"startAgentSession":` + agentSessionJSON + `}}`,
	}, "session", "start", "agt1", "--external-user", " u-42 ")
	if err != nil {
		t.Fatal(err)
	}
	if v := wantOneOp(t, calls, "StartAgentSession"); v["externalUserId"] != " u-42 " {
		t.Fatalf("vars: %v", v)
	}
}

// cli#2895 F6: the URN detection stays the `:`-contains check — a malformed
// colon-bearing non-URN goes to the agent(ref) read and is refused as
// not-found, never sent as a bare-ID mutation. Shape pinned per the attach.
func TestAgentSessionStartColonRefGoesToTheRead(t *testing.T) {
	calls, _, err := runAgentLifecycle(t, map[string]string{
		"AgentSessionAgentID": `{"data":{"agent":null}}`,
	}, "session", "start", "definitely:not-a-urn")
	if err == nil || exitCodeFor(err) != exitcode.NotFound {
		t.Fatalf("want not-found, got %v (%d)", err, exitCodeFor(err))
	}
	if len(calls.ops) != 1 || calls.ops[0] != "AgentSessionAgentID" {
		t.Fatalf("exactly the read, no mutation: %v", calls.ops)
	}
	if calls.vars["AgentSessionAgentID"]["ref"] != "definitely:not-a-urn" {
		t.Fatalf("vars: %v", calls.vars)
	}
}
