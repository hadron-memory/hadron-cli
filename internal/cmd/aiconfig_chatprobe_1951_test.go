package cmd

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// The result document with a chat probe: aiTest1347 plus the chat selection.
func aiTestWithChat1951(status string) string {
	return strings.TrimSuffix(aiTestPass1951, "}") + `,"chat":{"status":"` + status + `"}}`
}

const aiTestPass1951 = `{"ok":true,"provider":"openai","model":"m","endpoint":null,"apiStyle":"RESPONSES","latencyMs":12,"reply":"ok","error":null,"used":{"provider":"SAVED","model":"SAVED","endpoint":"SAVED","apiStyle":"SAVED","key":"SAVED","params":"SAVED"},"knobs":[]}`

func runAiTest1951(t *testing.T, responses map[string]string, args ...string) (string, map[string]json.RawMessage, []string, error) {
	t.Helper()
	var mu sync.Mutex
	var calls []string
	gql, captured := captureGraphQLFunc(t, func(op string) string {
		mu.Lock()
		calls = append(calls, op)
		mu.Unlock()
		return responses[op]
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"ai-config", "test", "cfg1", "--server", gql.URL}, args...))
	err := root.Execute()
	mu.Lock()
	defer mu.Unlock()
	return out.String(), captured, append([]string{}, calls...), err
}

func TestAiConfig1951ChatProbeIsSentAndRenderedForEveryStatus(t *testing.T) {
	for _, status := range []string{"PASS", "FAIL_TEXT_ONLY", "FAIL_OTHER_TOOL", "FAIL_MULTIPLE_CALLS", "FAIL_MALFORMED", "FAIL_PROVIDER_UNAVAILABLE", "UNSUPPORTED_PROVIDER", "FAIL_SOME_FUTURE_VALUE"} {
		t.Run(status, func(t *testing.T) {
			resp := map[string]string{"TestAiServiceConfigWithChatProbe": `{"data":{"testAiServiceConfig":` + aiTestWithChat1951(status) + `}}`}
			out, captured, calls, err := runAiTest1951(t, resp, "--chat-probe", "--json")
			if err != nil {
				t.Fatal(err)
			}
			if len(calls) != 1 || calls[0] != "TestAiServiceConfigWithChatProbe" {
				t.Fatalf("calls %v", calls)
			}
			if v := unmarshalVars(t, captured["TestAiServiceConfigWithChatProbe"]); v["chatProbe"] != true || v["id"] != "cfg1" {
				t.Fatalf("variables %v", v)
			}
			var dto map[string]any
			if err := json.Unmarshal([]byte(out), &dto); err != nil {
				t.Fatal(err)
			}
			if dto["chat"] != status || dto["ok"] != true {
				t.Fatalf("json chat/ok: %s", out)
			}
			text, _, _, err := runAiTest1951(t, resp, "--chat-probe")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(text, "\nchat: "+status+"\n") || !strings.HasPrefix(text, "ok  ") {
				t.Fatalf("chat status must be its own line apart from the run state:\n%s", text)
			}
		})
	}
}

func TestAiConfig1951RunPassWithChatFailureKeepsRunExitAndTwoOutcomes(t *testing.T) {
	resp := map[string]string{"TestAiServiceConfigWithChatProbe": `{"data":{"testAiServiceConfig":` + aiTestWithChat1951("FAIL_TEXT_ONLY") + `}}`}
	text, _, _, err := runAiTest1951(t, resp, "--chat-probe")
	if err != nil || exitCodeFor(err) != exitcode.OK {
		t.Fatalf("a chat status must not change the exit: %v", err)
	}
	if !strings.HasPrefix(text, "ok  ") || !strings.Contains(text, "\nchat: FAIL_TEXT_ONLY\n") || strings.Contains(strings.ToUpper(text), "OVERALL") {
		t.Fatalf("want the run result and a separate chat line:\n%s", text)
	}
}

func TestAiConfig1951DefaultIsUnchanged(t *testing.T) {
	resp := map[string]string{"TestAiServiceConfig": `{"data":{"testAiServiceConfig":` + aiTestPass1951 + `}}`}
	out, captured, calls, err := runAiTest1951(t, resp, "--json")
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0] != "TestAiServiceConfig" {
		t.Fatalf("default must use today's document only, got %v", calls)
	}
	if v := unmarshalVars(t, captured["TestAiServiceConfig"]); func() bool { _, ok := v["chatProbe"]; return ok }() {
		t.Fatalf("chatProbe must be omitted entirely, got %v", v)
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatal(err)
	}
	if v, ok := dto["chat"]; !ok || v != nil {
		t.Fatalf("chat must be an explicit null key when no probe was requested: %s", out)
	}
	text, _, _, err := runAiTest1951(t, resp)
	if err != nil || strings.Contains(text, "chat") {
		t.Fatalf("default text must not mention chat:\n%s %v", text, err)
	}
}

func TestAiConfig1951ProbeRefusalsAreLoudAndNeverRetried(t *testing.T) {
	cases := []struct {
		name, errJSON string
		wantExit      int
	}{
		{"older-server", `{"errors":[{"message":"Unknown argument \"chatProbe\" on field \"Mutation.testAiServiceConfig\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`, exitcode.Usage},
		{"forbidden", `{"errors":[{"message":"Forbidden","extensions":{"code":"FORBIDDEN"}}],"data":null}`, exitcode.Forbidden},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, calls, err := runAiTest1951(t, map[string]string{"TestAiServiceConfigWithChatProbe": c.errJSON}, "--chat-probe", "--json")
			if err == nil || exitCodeFor(err) != c.wantExit {
				t.Fatalf("err %v exit %d, want %d", err, exitCodeFor(err), c.wantExit)
			}
			if len(calls) != 1 {
				t.Fatalf("no retry and no fallback document: %v", calls)
			}
		})
	}
}

func TestAiConfig1951ProbeWithoutChatInResultIsAnError(t *testing.T) {
	resp := map[string]string{"TestAiServiceConfigWithChatProbe": `{"data":{"testAiServiceConfig":` + aiTestPass1951 + `}}`}
	_, _, _, err := runAiTest1951(t, resp, "--chat-probe")
	if err == nil || exitCodeFor(err) != exitcode.Error {
		t.Fatalf("a requested probe with no chat status must not read as success: %v", err)
	}
}
