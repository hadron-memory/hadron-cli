package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2510 (gap #58): agent import publish|rm, agent ai-config clear, agent
// repair-system-memory and agent session start|end. Each verb sends exactly one
// matching mutation, refs as typed, with no retry or follow-up; destructive
// verbs refuse non-interactively without --yes before any request.

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

const agentImportJSON = `{"parentAgentId":"agtP","importedAgentId":"agtI","position":2,"required":true,"createdAt":"2026-11-19T00:00:00Z"}`

const agentSessionJSON = `{"id":"ses1","agentId":"agt1","appId":"app1","memoryId":"mem1","userId":null,` +
	`"type":"CHATBOT","state":"LIVE","isLive":true,"startedAt":"2026-11-19T00:00:00Z","expiresAt":null}`

func wantOneOp(t *testing.T, calls *lifecycleCall, op string) map[string]any {
	t.Helper()
	if len(calls.ops) != 1 || calls.ops[0] != op {
		t.Fatalf("want exactly one %s, got %v", op, calls.ops)
	}
	return calls.vars[op]
}

func TestAgentImportPublishSendsRefsAsTyped(t *testing.T) {
	calls, out, err := runAgentLifecycle(t, map[string]string{
		"PublishAgentImport": `{"data":{"publishAgentImport":` + agentImportJSON + `}}`,
	}, "import", "publish", "hrn:agent:acme.com:parent", "hrn:agent:acme.com:dep", "--position", "2")
	if err != nil {
		t.Fatal(err)
	}
	v := wantOneOp(t, calls, "PublishAgentImport")
	if v["parentAgentRef"] != "hrn:agent:acme.com:parent" || v["importedAgentRef"] != "hrn:agent:acme.com:dep" || v["position"] != float64(2) {
		t.Fatalf("vars: %v", v)
	}
	if _, present := v["required"]; present {
		t.Fatalf("an unset --required must be omitted (server default), got %v", v)
	}
	if !strings.Contains(out, "hrn:agent:acme.com:dep") {
		t.Fatalf("output should name the imported agent: %q", out)
	}
}

func TestAgentImportPublishRequiredFalseIsSent(t *testing.T) {
	calls, _, err := runAgentLifecycle(t, map[string]string{
		"PublishAgentImport": `{"data":{"publishAgentImport":` + agentImportJSON + `}}`,
	}, "import", "publish", "agtP", "agtI", "--position", "0", "--required=false")
	if err != nil {
		t.Fatal(err)
	}
	v := wantOneOp(t, calls, "PublishAgentImport")
	if v["required"] != false || v["position"] != float64(0) {
		t.Fatalf("vars: %v", v)
	}
}

func TestAgentImportPublishWithoutPositionIsUsage(t *testing.T) {
	calls, _, err := runAgentLifecycle(t, nil, "import", "publish", "agtP", "agtI")
	if err == nil || exitCodeFor(err) != exitcode.Usage || len(calls.ops) != 0 {
		t.Fatalf("missing --position must be a usage error with no call: err=%v ops=%v", err, calls.ops)
	}
}

func TestAgentImportPublishJSON(t *testing.T) {
	_, out, err := runAgentLifecycle(t, map[string]string{
		"PublishAgentImport": `{"data":{"publishAgentImport":` + agentImportJSON + `}}`,
	}, "import", "publish", "agtP", "agtI", "--position", "2", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if got["parentAgentId"] != "agtP" || got["importedAgentId"] != "agtI" || got["position"] != float64(2) || got["required"] != true {
		t.Fatalf("json: %v", got)
	}
}

func TestAgentImportRmConfirmsThenSendsOneCall(t *testing.T) {
	calls, _, err := runAgentLifecycle(t, nil, "import", "rm", "agtP", "agtI")
	if err == nil || !strings.Contains(err.Error(), "--yes") || len(calls.ops) != 0 {
		t.Fatalf("non-TTY rm without --yes must refuse before any call: err=%v ops=%v", err, calls.ops)
	}
	calls, out, err := runAgentLifecycle(t, map[string]string{
		"RemoveAgentImport": `{"data":{"removeAgentImport":true}}`,
	}, "import", "rm", "hrn:agent:acme.com:parent", "hrn:agent:acme.com:dep", "--yes", "--json")
	if err != nil {
		t.Fatal(err)
	}
	v := wantOneOp(t, calls, "RemoveAgentImport")
	if v["parentAgentRef"] != "hrn:agent:acme.com:parent" || v["importedAgentRef"] != "hrn:agent:acme.com:dep" {
		t.Fatalf("vars: %v", v)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil || got["removed"] != true {
		t.Fatalf("json: %v %s", err, out)
	}
}

func TestAgentAIConfigClearConfirmsThenSendsOneCall(t *testing.T) {
	calls, _, err := runAgentLifecycle(t, nil, "ai-config", "clear", "agt1")
	if err == nil || !strings.Contains(err.Error(), "--yes") || len(calls.ops) != 0 {
		t.Fatalf("non-TTY clear without --yes must refuse before any call: err=%v ops=%v", err, calls.ops)
	}
	calls, out, err := runAgentLifecycle(t, map[string]string{
		"ClearAgentAIConfig": `{"data":{"clearAgentAIConfig":` + agentJSON + `}}`,
	}, "ai-config", "clear", "hrn:agent:acme.com:support-bot", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	v := wantOneOp(t, calls, "ClearAgentAIConfig")
	if v["agentId"] != "hrn:agent:acme.com:support-bot" {
		t.Fatalf("the ref must be sent as typed: %v", v)
	}
	if !strings.Contains(out, "Support Bot") {
		t.Fatalf("output: %q", out)
	}
}

func TestAgentRepairSystemMemoryNeedsNoConfirm(t *testing.T) {
	calls, out, err := runAgentLifecycle(t, map[string]string{
		"RepairAgentSystemMemory": `{"data":{"repairAgentSystemMemory":` + agentJSON + `}}`,
	}, "repair-system-memory", "hrn:agent:acme.com:support-bot", "--json")
	if err != nil {
		t.Fatal(err)
	}
	v := wantOneOp(t, calls, "RepairAgentSystemMemory")
	if v["agentRef"] != "hrn:agent:acme.com:support-bot" {
		t.Fatalf("vars: %v", v)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil || got["id"] != "agt1" {
		t.Fatalf("json: %v %s", err, out)
	}
}

func TestAgentLifecycleHelpStatesServerFacts(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"repair-system-memory"}, []string{"platform admin"}},
		{[]string{"ai-config", "clear"}, []string{"every named", "only the configuration named", "never read or set"}},
		{[]string{"import", "publish"}, []string{"itself imports", "license"}},
		{[]string{"session"}, []string{"hadron team session", "App key"}},
	} {
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append(append([]string{"agent"}, tc.args...), "--help"))
		if err := root.Execute(); err != nil {
			t.Fatalf("%v: %v", tc.args, err)
		}
		help := strings.ToLower(out.String())
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
		{"PublishAgentImport", []string{"import", "publish", "agtP", "agtI", "--position", "1"}},
		{"RemoveAgentImport", []string{"import", "rm", "agtP", "agtI", "--yes"}},
		{"ClearAgentAIConfig", []string{"ai-config", "clear", "agt1", "--yes"}},
		{"RepairAgentSystemMemory", []string{"repair-system-memory", "agt1"}},
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
		{"import", "publish", " ", "agtI", "--position", "1"},
		{"import", "rm", "agtP", "", "--yes"},
		{"ai-config", "clear", " ", "--yes"},
		{"repair-system-memory", ""},
		{"session", "start", " "},
		{"session", "end", ""},
	} {
		calls, _, err := runAgentLifecycle(t, nil, args...)
		if err == nil || exitCodeFor(err) != exitcode.Usage || len(calls.ops) != 0 {
			t.Fatalf("%v: blank ref must be usage with no call: err=%v ops=%v", args, err, calls.ops)
		}
	}
}
